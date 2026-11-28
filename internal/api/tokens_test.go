package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

// D144: per-person API tokens can be created (shown once), used for auth, and
// revoked, and users cannot revoke another user's token.
func TestAPITokensLifecycle(t *testing.T) {
	st, srv, _, _ := testServer(t)
	_, memberTok := createUserWithToken(t, st, "tok-member", "member secret password", domain.RoleMember)

	do := func(method, path, tok, body string, out any) int {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if out != nil && resp.StatusCode < 300 {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatalf("decode: %v", err)
			}
		}
		return resp.StatusCode
	}

	var created struct {
		Token  apiTokenJSON `json:"token"`
		Secret string       `json:"secret"`
	}
	if code := do(http.MethodPost, "/api/tokens", memberTok, `{"name":"laptop","expires_in_days":30}`, &created); code != http.StatusCreated || !strings.HasPrefix(created.Secret, "frk_") {
		t.Fatalf("create token = %d %+v", code, created)
	}

	var me struct {
		User userJSON `json:"user"`
	}
	if code := do(http.MethodGet, "/api/auth/me", created.Secret, "", &me); code != http.StatusOK || me.User.Username != "tok-member" {
		t.Fatalf("me with new token = %d %+v", code, me)
	}

	// Admin cannot revoke member's token through /api/tokens/{id} (scoped to caller).
	if code := do(http.MethodDelete, "/api/tokens/"+created.Token.ID.String(), testToken, "", nil); code != http.StatusNotFound {
		t.Fatalf("cross-user revoke = %d, want 404", code)
	}

	// Member revokes own token -> immediately returns 401.
	if code := do(http.MethodDelete, "/api/tokens/"+created.Token.ID.String(), memberTok, "", nil); code != http.StatusNoContent {
		t.Fatalf("revoke = %d, want 204", code)
	}
	if code := do(http.MethodGet, "/api/auth/me", created.Secret, "", nil); code != http.StatusUnauthorized {
		t.Fatalf("revoked token = %d, want 401", code)
	}
}
