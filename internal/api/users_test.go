package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

// D145, D146: admin user management, last-admin protection (409), and invite
// creation + public acceptance.
func TestAdminUsersAndInvites(t *testing.T) {
	_, srv, _, _ := testServer(t)

	do := func(method, path, authHdr, body string, out any) int {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if authHdr != "" {
			req.Header.Set("Authorization", authHdr)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if out != nil && resp.StatusCode < 300 {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatalf("decode %s %s: %v", method, path, err)
			}
		}
		return resp.StatusCode
	}

	var users []userJSON
	if code := do(http.MethodGet, "/api/users", "Bearer "+testToken, "", &users); code != http.StatusOK || len(users) != 1 {
		t.Fatalf("list users = %d %+v", code, users)
	}
	adminID := users[0].ID

	// Last-admin protection: cannot demote or disable the only active admin.
	if code := do(http.MethodPatch, "/api/users/"+adminID.String(), "Bearer "+testToken, `{"role":"member"}`, nil); code != http.StatusConflict {
		t.Fatalf("demote last admin = %d, want 409", code)
	}
	if code := do(http.MethodPatch, "/api/users/"+adminID.String(), "Bearer "+testToken, `{"disabled":true}`, nil); code != http.StatusConflict {
		t.Fatalf("disable last admin = %d, want 409", code)
	}

	var alice userJSON
	if code := do(http.MethodPost, "/api/users", "Bearer "+testToken,
		`{"username":"alice","display_name":"Alice","email":"alice@example.com","role":"member","password":"alice secret password"}`, &alice); code != http.StatusCreated || alice.Username != "alice" || alice.Role != domain.RoleMember {
		t.Fatalf("create user = %d %+v", code, alice)
	}

	if code := do(http.MethodPatch, "/api/users/"+alice.ID.String(), "Bearer "+testToken, `{"role":"admin"}`, &alice); code != http.StatusOK || alice.Role != domain.RoleAdmin {
		t.Fatalf("promote alice = %d %+v", code, alice)
	}

	if code := do(http.MethodDelete, "/api/users/"+alice.ID.String()+"/sessions", "Bearer "+testToken, "", nil); code != http.StatusNoContent {
		t.Fatalf("revoke sessions = %d, want 204", code)
	}

	var inv struct {
		Invite inviteJSON `json:"invite"`
		Token  string     `json:"token"`
		Path   string     `json:"path"`
	}
	if code := do(http.MethodPost, "/api/invites", "Bearer "+testToken,
		`{"email":"bob@example.com","role":"member","expires_in_hours":24}`, &inv); code != http.StatusCreated || inv.Token == "" || !strings.HasPrefix(inv.Path, "/invite/") {
		t.Fatalf("create invite = %d %+v", code, inv)
	}

	var invites []inviteJSON
	if code := do(http.MethodGet, "/api/invites", "Bearer "+testToken, "", &invites); code != http.StatusOK || len(invites) != 1 {
		t.Fatalf("list invites = %d %+v", code, invites)
	}

	var preview struct {
		Email string      `json:"email"`
		Role  domain.Role `json:"role"`
	}
	if code := do(http.MethodGet, "/api/auth/invites/"+inv.Token, "", "", &preview); code != http.StatusOK || preview.Email != "bob@example.com" || preview.Role != domain.RoleMember {
		t.Fatalf("preview invite = %d %+v", code, preview)
	}

	var bob struct {
		User userJSON `json:"user"`
	}
	if code := do(http.MethodPost, "/api/auth/invites/accept", "",
		`{"token":"`+inv.Token+`","username":"bob","display_name":"Bob","password":"bob secret password"}`, &bob); code != http.StatusCreated || bob.User.Username != "bob" || bob.User.Role != domain.RoleMember {
		t.Fatalf("accept invite = %d %+v", code, bob)
	}

	if code := do(http.MethodPost, "/api/auth/invites/accept", "",
		`{"token":"`+inv.Token+`","username":"bob2","display_name":"Bob2","password":"bob2 secret password"}`, nil); code != http.StatusGone {
		t.Fatalf("reuse invite = %d, want 410", code)
	}
}
