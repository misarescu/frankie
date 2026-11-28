package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/auth"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/store/storetest"
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

// D145: POST /api/auth/invites/accept must enforce per-username rate limits in
// addition to per-IP limits so rotating client IPs cannot brute-force one user.
func TestAcceptInviteRateLimitedByUsername(t *testing.T) {
	_, srv, _, _ := testServerAuth(t, nil, AuthConfig{
		LoginIPLimiter:   auth.NewRateLimiter(100, time.Minute),
		LoginUserLimiter: auth.NewRateLimiter(2, time.Minute),
	})

	post := func(ip, username string) *http.Response {
		t.Helper()
		body := `{"token":"not-a-real-invite","username":"` + username + `","password":"a sufficiently long password"}`
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/auth/invites/accept", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	for i, ip := range []string{"203.0.113.1", "203.0.113.2"} {
		resp := post(ip, "victim")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusGone {
			t.Fatalf("attempt %d status = %d, want 410", i+1, resp.StatusCode)
		}
	}

	resp := post("203.0.113.3", "victim")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("3rd attempt for same username from new IP = %d, want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header on 429 response")
	}

	// Different username from a fresh IP must remain unaffected.
	other := post("203.0.113.4", "other-user")
	_ = other.Body.Close()
	if other.StatusCode != http.StatusGone {
		t.Fatalf("different username status = %d, want 410", other.StatusCode)
	}
}

// D145: acceptInvite must not run expensive argon2id hashing until the invite
// token is verified to exist, be unused/unexpired, and match any restricted email.
func TestAcceptInviteDefersPasswordHashingUntilInviteValid(t *testing.T) {
	dbURL := storetest.DatabaseURL(t)
	st, err := store.Open(t.Context(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(t.Context(), st.Pool()); err != nil {
		t.Fatal(err)
	}

	var hashCalls atomic.Int32
	mux := http.NewServeMux()
	(&Server{
		Store: st,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth: AuthConfig{
			LoginIPLimiter:   auth.NewRateLimiter(100, time.Minute),
			LoginUserLimiter: auth.NewRateLimiter(100, time.Minute),
		},
		hashPasswordFn: func(pw string) (string, error) {
			hashCalls.Add(1)
			return auth.HashPassword(pw)
		},
	}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	post := func(body string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/auth/invites/accept", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	// Unknown invite token with a valid password must return 410 without hashing.
	if code := post(`{"token":"bogus-token","username":"u1","password":"valid long password"}`); code != http.StatusGone {
		t.Fatalf("bogus token status = %d, want 410", code)
	}
	if n := hashCalls.Load(); n != 0 {
		t.Fatalf("hashPassword called %d times for bogus token, want 0", n)
	}

	// Unknown invite token with a short password must still reject the invite first (410, not 400).
	if code := post(`{"token":"bogus-token","username":"u2","password":"short"}`); code != http.StatusGone {
		t.Fatalf("bogus token + short password status = %d, want 410", code)
	}
	if n := hashCalls.Load(); n != 0 {
		t.Fatalf("hashPassword called %d times for bogus token + short password, want 0", n)
	}

	// Expired invite must return 410 without hashing.
	expRaw, expHash, err := auth.GenerateInviteToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateInvite(t.Context(), store.NewInvite{
		TokenHash: expHash,
		Role:      domain.RoleMember,
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if code := post(`{"token":"` + expRaw + `","username":"u3","password":"valid long password"}`); code != http.StatusGone {
		t.Fatalf("expired invite status = %d, want 410", code)
	}
	if n := hashCalls.Load(); n != 0 {
		t.Fatalf("hashPassword called %d times for expired invite, want 0", n)
	}

	// Email-restricted invite with wrong email must return 410 without hashing.
	validRaw, validHash, err := auth.GenerateInviteToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateInvite(t.Context(), store.NewInvite{
		TokenHash: validHash,
		Email:     "alice@example.com",
		Role:      domain.RoleMember,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if code := post(`{"token":"` + validRaw + `","username":"alice","email":"wrong@example.com","password":"valid long password"}`); code != http.StatusGone {
		t.Fatalf("mismatched email status = %d, want 410", code)
	}
	if n := hashCalls.Load(); n != 0 {
		t.Fatalf("hashPassword called %d times for mismatched email, want 0", n)
	}

	// Valid invite with matching email hashes once and creates the account.
	if code := post(`{"token":"` + validRaw + `","username":"alice","email":"alice@example.com","password":"valid long password"}`); code != http.StatusCreated {
		t.Fatalf("valid invite accept status = %d, want 201", code)
	}
	if n := hashCalls.Load(); n != 1 {
		t.Fatalf("hashPassword called %d times after valid accept, want 1", n)
	}

	// Reusing the now-accepted invite must return 410 without hashing again.
	if code := post(`{"token":"` + validRaw + `","username":"alice2","email":"alice@example.com","password":"valid long password"}`); code != http.StatusGone {
		t.Fatalf("reused invite status = %d, want 410", code)
	}
	if n := hashCalls.Load(); n != 1 {
		t.Fatalf("hashPassword called %d times after reused invite, want 1", n)
	}
}
