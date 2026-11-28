package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/auth"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

func createUserWithToken(t *testing.T, st *store.Store, username, password string, role domain.Role) (domain.User, string) {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(t.Context(), store.NewUser{
		Username:     username,
		DisplayName:  username,
		Email:        username + "@example.com",
		Role:         role,
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, prefix, tokenHash, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAPIToken(t.Context(), store.NewAPIToken{
		UserID:      u.ID,
		Name:        "cli",
		TokenHash:   tokenHash,
		TokenPrefix: prefix,
	}); err != nil {
		t.Fatal(err)
	}
	return u, raw
}

// D146: every /api/* endpoint except the public sign-in surface returns 401
// without a valid session cookie or personal API token, including reads and SSE.
func TestAllProtectedRoutesRequireAuth(t *testing.T) {
	_, srv, run, _ := testServer(t)

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/version"},
		{http.MethodGet, "/api/projects"},
		{http.MethodGet, "/api/issues?project=misarescu/Goodreads-Clone"},
		{http.MethodGet, "/api/issues/gc-1"},
		{http.MethodGet, "/api/runs"},
		{http.MethodGet, "/api/runs/" + run.ID.String()},
		{http.MethodGet, "/api/runs/" + run.ID.String() + "/steps"},
		{http.MethodGet, "/api/runs/" + run.ID.String() + "/events"},
		{http.MethodGet, "/api/runs/" + run.ID.String() + "/stream"},
		{http.MethodGet, "/api/approvals"},
		{http.MethodGet, "/api/questions"},
		{http.MethodGet, "/api/specs"},
		{http.MethodGet, "/api/releases?project=misarescu/Goodreads-Clone"},
		{http.MethodGet, "/api/auth/me"},
		{http.MethodPatch, "/api/auth/me"},
		{http.MethodPost, "/api/auth/password"},
		{http.MethodGet, "/api/tokens"},
		{http.MethodPost, "/api/tokens"},
		{http.MethodGet, "/api/users"},
		{http.MethodPost, "/api/users"},
		{http.MethodGet, "/api/invites"},
		{http.MethodPost, "/api/invites"},
		{http.MethodPost, "/api/gates/00000000-0000-0000-0000-000000000000/decision"},
		{http.MethodPost, "/api/steps/00000000-0000-0000-0000-000000000000/steer"},
		{http.MethodPost, "/api/steps/00000000-0000-0000-0000-000000000000/abort"},
		{http.MethodPost, "/api/questions/00000000-0000-0000-0000-000000000000/answer"},
		{http.MethodPost, "/api/issues/start"},
		{http.MethodPost, "/api/issues/gc-1/retry"},
		{http.MethodPost, "/api/issues/gc-1/spec"},
		{http.MethodPost, "/api/issues/gc-1/complete"},
		{http.MethodPost, "/api/issues/gc-1/cancel"},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), rt.method, srv.URL+rt.path, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
		})
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/auth/providers", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/auth/providers = %d, want 200", resp.StatusCode)
	}
}

// D144, D145, D146: password login sets HttpOnly SameSite=Lax cookie, works on
// reads/writes with CSRF origin check, rejects cross-origin writes, logs out.
func TestSessionLoginCSRFAndLogout(t *testing.T) {
	_, srv, _, _ := testServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Bad password -> 401.
	badReq, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/auth/login",
		strings.NewReader(`{"username":"test-admin","password":"wrong password"}`))
	badReq.Header.Set("Content-Type", "application/json")
	badResp, err := client.Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", badResp.StatusCode)
	}

	// Good login -> 200 + session cookie.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/auth/login",
		strings.NewReader(`{"username":"test-admin","password":"correct horse battery staple"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200", resp.StatusCode)
	}
	var foundCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == SessionCookieName {
			foundCookie = c
		}
	}
	if foundCookie == nil || !foundCookie.HttpOnly || foundCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %+v", foundCookie)
	}

	// GET /api/auth/me works with cookie.
	meReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/auth/me", nil)
	meResp, err := client.Do(meReq)
	if err != nil {
		t.Fatal(err)
	}
	var me struct {
		User userJSON `json:"user"`
	}
	_ = json.NewDecoder(meResp.Body).Decode(&me)
	_ = meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK || me.User.Username != "test-admin" || !me.User.HasPassword {
		t.Fatalf("me = %d %+v", meResp.StatusCode, me)
	}

	// Cross-origin cookie write -> 403 CSRF rejection.
	csrfReq, _ := http.NewRequestWithContext(t.Context(), http.MethodPatch, srv.URL+"/api/auth/me",
		strings.NewReader(`{"display_name":"Evil"}`))
	csrfReq.Header.Set("Content-Type", "application/json")
	csrfReq.Header.Set("Origin", "https://evil.example")
	csrfResp, err := client.Do(csrfReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = csrfResp.Body.Close()
	if csrfResp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin write = %d, want 403", csrfResp.StatusCode)
	}

	// Missing Origin/Referer on cookie write -> 403.
	noOrigReq, _ := http.NewRequestWithContext(t.Context(), http.MethodPatch, srv.URL+"/api/auth/me",
		strings.NewReader(`{"display_name":"NoOrigin"}`))
	noOrigReq.Header.Set("Content-Type", "application/json")
	noOrigResp, err := client.Do(noOrigReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = noOrigResp.Body.Close()
	if noOrigResp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing origin write = %d, want 403", noOrigResp.StatusCode)
	}

	// Same-origin cookie write -> 200.
	okReq, _ := http.NewRequestWithContext(t.Context(), http.MethodPatch, srv.URL+"/api/auth/me",
		strings.NewReader(`{"display_name":"Updated Name"}`))
	okReq.Header.Set("Content-Type", "application/json")
	okReq.Header.Set("Origin", srv.URL)
	okResp, err := client.Do(okReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("same-origin write = %d, want 200", okResp.StatusCode)
	}

	// Logout invalidates the session.
	outReq, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/auth/logout", nil)
	outReq.Header.Set("Origin", srv.URL)
	outResp, err := client.Do(outReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = outResp.Body.Close()
	if outResp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", outResp.StatusCode)
	}

	// Subsequent /api/auth/me returns 401.
	meReq2, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/auth/me", nil)
	meResp2, err := client.Do(meReq2)
	if err != nil {
		t.Fatal(err)
	}
	_ = meResp2.Body.Close()
	if meResp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d, want 401", meResp2.StatusCode)
	}
}

// D146: viewer cannot write or hit admin endpoints; member cannot hit admin
// endpoints; disabled users lose access immediately.
func TestRoleEnforcementAndDisabledUser(t *testing.T) {
	st, srv, _, _ := testServer(t)
	viewer, viewerTok := createUserWithToken(t, st, "viewer1", "viewer password 1", domain.RoleViewer)
	_, memberTok := createUserWithToken(t, st, "member1", "member password 1", domain.RoleMember)

	do := func(method, path, tok, body string) int {
		req, _ := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if code := do(http.MethodGet, "/api/runs", viewerTok, ""); code != http.StatusOK {
		t.Errorf("viewer GET /api/runs = %d, want 200", code)
	}
	if code := do(http.MethodPost, "/api/issues/start", viewerTok, `{"keys":["gc-1"]}`); code != http.StatusForbidden {
		t.Errorf("viewer POST /api/issues/start = %d, want 403", code)
	}
	if code := do(http.MethodGet, "/api/users", viewerTok, ""); code != http.StatusForbidden {
		t.Errorf("viewer GET /api/users = %d, want 403", code)
	}

	if code := do(http.MethodPost, "/api/issues/start", memberTok, `{"keys":[]}`); code != http.StatusBadRequest {
		t.Errorf("member POST /api/issues/start = %d, want 400", code)
	}
	if code := do(http.MethodGet, "/api/users", memberTok, ""); code != http.StatusForbidden {
		t.Errorf("member GET /api/users = %d, want 403", code)
	}

	if code := do(http.MethodGet, "/api/users", testToken, ""); code != http.StatusOK {
		t.Errorf("admin GET /api/users = %d, want 200", code)
	}

	if _, err := st.SetUserDisabled(t.Context(), viewer.ID, true); err != nil {
		t.Fatal(err)
	}
	if code := do(http.MethodGet, "/api/runs", viewerTok, ""); code != http.StatusUnauthorized {
		t.Errorf("disabled viewer GET /api/runs = %d, want 401", code)
	}
}

// D145: failed logins are rate-limited (429).
func TestLoginRateLimit(t *testing.T) {
	_, srv, _, _ := testServer(t)
	var got429 bool
	for range 12 {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/auth/login",
			strings.NewReader(`{"username":"test-admin","password":"wrong password"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("expected 429 Too Many Requests after repeated failed logins")
	}
}

// D145: a forged CF-Connecting-IP header must never bypass the per-IP login
// rate limiter when traffic comes through an Ingress or reverse proxy.
func TestLoginIgnoresSpoofedCFConnectingIP(t *testing.T) {
	_, srv, _, _ := testServerAuth(t, nil, AuthConfig{
		LoginIPLimiter:   auth.NewRateLimiter(2, time.Minute),
		LoginUserLimiter: auth.NewRateLimiter(100, time.Minute),
	})

	post := func(user, cfIP string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/auth/login",
			strings.NewReader(`{"username":"`+user+`","password":"wrong password 123"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "203.0.113.5")
		req.Header.Set("CF-Connecting-IP", cfIP)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if code := post("u1", "198.51.100.1"); code != http.StatusUnauthorized {
		t.Fatalf("attempt 1 = %d, want 401", code)
	}
	if code := post("u2", "198.51.100.2"); code != http.StatusUnauthorized {
		t.Fatalf("attempt 2 = %d, want 401", code)
	}
	if code := post("u3", "198.51.100.3"); code != http.StatusTooManyRequests {
		t.Fatalf("attempt 3 with rotated CF-Connecting-IP = %d, want 429", code)
	}
}

// D146: PreviewMode opens reads and accepts the static preview token on writes,
// while still keeping admin user management locked behind a real admin user.
func TestPreviewMode(t *testing.T) {
	const previewTok = "preview-static-token-0123456789abcdef"
	_, srv, _, _ := testServerAuth(t, nil, AuthConfig{
		PreviewMode:  true,
		PreviewToken: previewTok,
	})

	rReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/issues?project=misarescu/Goodreads-Clone", nil)
	rResp, err := http.DefaultClient.Do(rReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = rResp.Body.Close()
	if rResp.StatusCode != http.StatusOK {
		t.Fatalf("preview GET /api/issues = %d, want 200", rResp.StatusCode)
	}

	wReq, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/issues/start", strings.NewReader(`{"keys":[]}`))
	wReq.Header.Set("Content-Type", "application/json")
	wReq.Header.Set("Authorization", "Bearer "+previewTok)
	wResp, err := http.DefaultClient.Do(wReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = wResp.Body.Close()
	if wResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("preview write with static token = %d, want 400", wResp.StatusCode)
	}

	aReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/users", nil)
	aReq.Header.Set("Authorization", "Bearer "+previewTok)
	aResp, err := http.DefaultClient.Do(aReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = aResp.Body.Close()
	if aResp.StatusCode != http.StatusForbidden {
		t.Fatalf("preview token GET /api/users = %d, want 403", aResp.StatusCode)
	}
}

// D145: end-to-end OIDC SSO flow (start -> callback -> allowlist create ->
// unlink last method refused -> add password -> unlink succeeds).
func TestSSOLoginAllowlistAndLinking(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var idpURL, lastNonce string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 idpURL,
				"authorization_endpoint": idpURL + "/auth",
				"token_endpoint":         idpURL + "/token",
				"jwks_uri":               idpURL + "/jwks",
			})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{{
					"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
					"n": base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
					"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
				}},
			})
		case "/token":
			tok := signTestRS256(t, priv, "k1", map[string]any{
				"iss": idpURL, "aud": "cid", "sub": "g-sub-1", "nonce": lastNonce,
				"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
				"email": "sso-user@acme.io", "email_verified": true, "name": "SSO User",
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "id_token": tok})
		}
	}))
	t.Cleanup(idp.Close)
	idpURL = idp.URL

	signer, err := auth.NewStateSigner([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	ssoSvc, err := auth.NewSSOService(
		[]auth.ProviderConfig{{
			ID: domain.ProviderGoogle, Name: "Google", ClientID: "cid", ClientSecret: "csec", IssuerURL: idpURL,
		}},
		auth.Allowlist{EmailDomains: []string{"acme.io"}, DefaultRole: domain.RoleMember},
		idp.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, srv, _, _ := testServerAuth(t, nil, AuthConfig{
		StateSigner: signer,
		SSO:         ssoSvc,
	})

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	startReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/auth/sso/google/start?next=/issues", nil)
	startResp, err := client.Do(startReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = startResp.Body.Close()
	if startResp.StatusCode != http.StatusFound {
		t.Fatalf("start status = %d, want 302", startResp.StatusCode)
	}
	loc, err := url.Parse(startResp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := loc.Query().Get("state")
	lastNonce = loc.Query().Get("nonce")

	cbReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet,
		srv.URL+"/api/auth/sso/google/callback?code=abc&state="+url.QueryEscape(state), nil)
	cbResp, err := client.Do(cbReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusFound || cbResp.Header.Get("Location") != "/issues" {
		t.Fatalf("callback = %d %q, want 302 /issues", cbResp.StatusCode, cbResp.Header.Get("Location"))
	}

	meReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/auth/me", nil)
	meResp, err := client.Do(meReq)
	if err != nil {
		t.Fatal(err)
	}
	var me struct {
		User       userJSON       `json:"user"`
		Identities []identityJSON `json:"identities"`
	}
	_ = json.NewDecoder(meResp.Body).Decode(&me)
	_ = meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK || me.User.Role != domain.RoleMember || me.User.HasPassword || len(me.Identities) != 1 {
		t.Fatalf("me = %d %+v", meResp.StatusCode, me)
	}

	// Cannot unlink the last sign-in method (409).
	delReq, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srv.URL+"/api/auth/identities/"+me.Identities[0].ID.String(), nil)
	delReq.Header.Set("Origin", srv.URL)
	delResp, err := client.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusConflict {
		t.Fatalf("unlink last identity = %d, want 409", delResp.StatusCode)
	}

	// Set a password, then unlink succeeds.
	pwReq, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/auth/password",
		strings.NewReader(`{"new_password":"my brand new strong password"}`))
	pwReq.Header.Set("Content-Type", "application/json")
	pwReq.Header.Set("Origin", srv.URL)
	pwResp, err := client.Do(pwReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = pwResp.Body.Close()
	if pwResp.StatusCode != http.StatusNoContent {
		t.Fatalf("set password = %d, want 204", pwResp.StatusCode)
	}
	delReq2, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete, srv.URL+"/api/auth/identities/"+me.Identities[0].ID.String(), nil)
	delReq2.Header.Set("Origin", srv.URL)
	delResp2, err := client.Do(delReq2)
	if err != nil {
		t.Fatal(err)
	}
	_ = delResp2.Body.Close()
	if delResp2.StatusCode != http.StatusNoContent {
		t.Fatalf("unlink after password = %d, want 204", delResp2.StatusCode)
	}
}

func signTestRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"})
	pb, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, auth.HashToken(input))
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}
