package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/auth"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

const (
	// MinTokenLen is the shortest preview service token the control plane accepts.
	MinTokenLen = 32

	// SessionCookieName is the HttpOnly session cookie (D144).
	SessionCookieName = "frankie_session"
	// OAuthStateCookieName carries the signed OAuth/OIDC state payload (D145).
	OAuthStateCookieName = "frankie_oauth_state"

	oauthStateTTL = 10 * time.Minute
)

type actorContextKey struct{}

// Actor is the authenticated caller attached to a request context.
type Actor struct {
	User           domain.User
	ViaSession     bool
	SessionHash    []byte
	TokenID        *uuid.UUID
	PreviewService bool
}

func actorFromContext(ctx context.Context) (*Actor, bool) {
	a, ok := ctx.Value(actorContextKey{}).(*Actor)
	return a, ok && a != nil
}

// actorName returns the authenticated caller's display/username for audit fields (D146).
func actorName(r *http.Request) string {
	if a, ok := actorFromContext(r.Context()); ok {
		if n := a.User.ActorName(); n != "" {
			return n
		}
	}
	return "unknown"
}

// requireRead serves a read endpoint: open only when PreviewMode is enabled and
// previewOpen is true (D146 (4)); otherwise requires viewer authentication.
func (s *Server) requireRead(previewOpen bool, next http.HandlerFunc) http.HandlerFunc {
	if previewOpen && s.Auth.PreviewMode {
		return next
	}
	return s.requireAuth(domain.RoleViewer, next)
}

// requireAuth authenticates the request (Bearer token or session cookie),
// enforces CSRF Origin checks on cookie-authenticated writes, and checks that
// the caller holds at least minRole (D144, D146).
func (s *Server) requireAuth(minRole domain.Role, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := s.authenticate(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="frankie"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if actor.ViaSession && isMutatingMethod(r.Method) && !s.validOrigin(r) {
			http.Error(w, "cross-origin write rejected", http.StatusForbidden)
			return
		}
		if !actor.User.Role.AtLeast(minRole) {
			http.Error(w, fmt.Sprintf("role %s cannot perform this action", actor.User.Role), http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), actorContextKey{}, actor)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) authenticate(r *http.Request) (*Actor, bool) {
	if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		tok := strings.TrimSpace(raw)
		if tok == "" {
			return nil, false
		}
		if s.Auth.PreviewMode && s.Auth.PreviewToken != "" && tokenEqual(tok, s.Auth.PreviewToken) {
			return &Actor{
				User: domain.User{
					Username:    "preview-setup",
					DisplayName: "preview-setup",
					Role:        domain.RoleMember,
				},
				PreviewService: true,
			}, true
		}
		if !strings.HasPrefix(tok, auth.APITokenPrefix) {
			return nil, false
		}
		t, u, err := s.Store.GetAPITokenByHash(r.Context(), auth.HashToken(tok))
		if err != nil {
			return nil, false
		}
		return &Actor{User: u, TokenID: &t.ID}, true
	}

	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil, false
	}
	hash := auth.HashToken(cookie.Value)
	_, u, err := s.Store.GetSessionByHash(r.Context(), hash)
	if err != nil {
		return nil, false
	}
	return &Actor{User: u, ViaSession: true, SessionHash: hash}, true
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// validOrigin verifies Origin (or Referer fallback) against the request's own
// origin, PublicURL and AllowedOrigins (D144).
func (s *Server) validOrigin(r *http.Request) bool {
	raw := strings.TrimSpace(r.Header.Get("Origin"))
	if raw == "" {
		if ref := strings.TrimSpace(r.Header.Get("Referer")); ref != "" {
			if u, err := url.Parse(ref); err == nil && u.Scheme != "" && u.Host != "" {
				raw = u.Scheme + "://" + u.Host
			}
		}
	}
	if raw == "" || raw == "null" {
		return false
	}
	got, err := normalizeOrigin(raw)
	if err != nil {
		return false
	}
	for _, allowed := range s.allowedOrigins(r) {
		if got == allowed {
			return true
		}
	}
	return false
}

func (s *Server) allowedOrigins(r *http.Request) []string {
	var out []string
	if s.Auth.PublicURL != "" {
		if o, err := normalizeOrigin(s.Auth.PublicURL); err == nil {
			out = append(out, o)
		}
	}
	for _, a := range s.Auth.AllowedOrigins {
		if o, err := normalizeOrigin(a); err == nil {
			out = append(out, o)
		}
	}
	host := firstHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if host != "" {
		scheme := "http"
		if s.isHTTPS(r) {
			scheme = "https"
		}
		if o, err := normalizeOrigin(scheme + "://" + host); err == nil {
			out = append(out, o)
		}
	}
	return out
}

func normalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("invalid origin")
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	if (scheme == "http" && strings.HasSuffix(host, ":80")) || (scheme == "https" && strings.HasSuffix(host, ":443")) {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
	}
	return scheme + "://" + host, nil
}

func (s *Server) isHTTPS(r *http.Request) bool {
	if s.Auth.SecureCookie != nil {
		return *s.Auth.SecureCookie
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s.Auth.PublicURL)), "https://") {
		return true
	}
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(firstHeaderValue(r.Header.Get("X-Forwarded-Proto")), "https")
}

func (s *Server) publicBaseURL(r *http.Request) string {
	if s.Auth.PublicURL != "" {
		return strings.TrimRight(strings.TrimSpace(s.Auth.PublicURL), "/")
	}
	scheme := "http"
	if s.isHTTPS(r) {
		scheme = "https"
	}
	host := firstHeaderValue(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

func firstHeaderValue(h string) string {
	if h == "" {
		return ""
	}
	v, _, _ := strings.Cut(h, ",")
	return strings.TrimSpace(v)
}

func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if ip := firstHeaderValue(r.Header.Get("X-Forwarded-For")); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, rawToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    rawToken,
		Path:     "/",
		MaxAge:   int(s.Auth.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) setOAuthStateCookie(w http.ResponseWriter, r *http.Request, val string) {
	http.SetCookie(w, &http.Cookie{
		Name:     OAuthStateCookieName,
		Value:    val,
		Path:     "/api/auth/sso/",
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearOAuthStateCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     OAuthStateCookieName,
		Value:    "",
		Path:     "/api/auth/sso/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// tokenEqual compares in constant time; hashing first hides the token's length too.
func tokenEqual(got, want string) bool {
	g, w := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}

type userJSON struct {
	ID          uuid.UUID      `json:"id"`
	Username    string         `json:"username"`
	DisplayName string         `json:"display_name"`
	Email       string         `json:"email,omitempty"`
	Role        domain.Role    `json:"role"`
	HasPassword bool           `json:"has_password"`
	DisabledAt  *time.Time     `json:"disabled_at,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Identities  []identityJSON `json:"identities,omitempty"`
}

type identityJSON struct {
	ID        uuid.UUID          `json:"id"`
	Provider  domain.SSOProvider `json:"provider"`
	Email     string             `json:"email,omitempty"`
	Login     string             `json:"login,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
}

func toUserJSON(u domain.User, ids []domain.UserIdentity) userJSON {
	out := userJSON{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Email:       u.Email,
		Role:        u.Role,
		HasPassword: u.HasPassword(),
		DisabledAt:  u.DisabledAt,
		CreatedAt:   u.CreatedAt,
	}
	if len(ids) > 0 {
		out.Identities = make([]identityJSON, len(ids))
		for i, id := range ids {
			out.Identities[i] = identityJSON{
				ID: id.ID, Provider: id.Provider, Email: id.Email, Login: id.Login, CreatedAt: id.CreatedAt,
			}
		}
	}
	return out
}

// authProviders returns the enabled sign-in methods for the login screen.
func (s *Server) authProviders(w http.ResponseWriter, _ *http.Request) {
	sso := []auth.ProviderSummary{}
	if s.Auth.SSO != nil {
		sso = s.Auth.SSO.EnabledProviders()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"password": true,
		"sso":      sso,
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// login verifies username/email + password (argon2id, rate-limited) and sets
// the session cookie (D144, D145).
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" && !s.validOrigin(r) {
		http.Error(w, "cross-origin login rejected", http.StatusForbidden)
		return
	}
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	loginKey := strings.ToLower(strings.TrimSpace(req.Username))
	if loginKey == "" || req.Password == "" {
		http.Error(w, "username and password are required", http.StatusBadRequest)
		return
	}
	ip := clientIP(r)
	if ok, retry := s.Auth.LoginIPLimiter.Check(ip); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(max(int(retry.Seconds()), 1)))
		http.Error(w, "too many sign-in attempts; try again later", http.StatusTooManyRequests)
		return
	}
	if ok, retry := s.Auth.LoginUserLimiter.Check(loginKey); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(max(int(retry.Seconds()), 1)))
		http.Error(w, "too many sign-in attempts; try again later", http.StatusTooManyRequests)
		return
	}

	u, err := s.Store.GetUserByLogin(r.Context(), loginKey)
	if err != nil || !u.Active() || !u.HasPassword() {
		auth.DummyVerify()
		s.Auth.LoginIPLimiter.Allow(ip)
		s.Auth.LoginUserLimiter.Allow(loginKey)
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}
	ok, err := auth.VerifyPassword(req.Password, u.PasswordHash)
	if err != nil || !ok {
		s.Auth.LoginIPLimiter.Allow(ip)
		s.Auth.LoginUserLimiter.Allow(loginKey)
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}

	raw, hash, err := auth.GenerateSessionToken()
	if err != nil {
		http.Error(w, "could not create session", http.StatusInternalServerError)
		return
	}
	if _, err := s.Store.CreateSession(r.Context(), u.ID, hash, time.Now().UTC().Add(s.Auth.SessionTTL)); err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	s.Auth.LoginUserLimiter.Reset(loginKey)
	s.setSessionCookie(w, r, raw)
	s.Log.Info("user signed in", "user", u.Username, "method", "password")

	ids, _ := s.Store.ListUserIdentities(r.Context(), u.ID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"user": toUserJSON(u, ids)})
}

// logout deletes the browser session and clears the session cookie.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(SessionCookieName)
	if err == nil && cookie.Value != "" {
		if !s.validOrigin(r) {
			http.Error(w, "cross-origin logout rejected", http.StatusForbidden)
			return
		}
		_ = s.Store.DeleteSessionByHash(r.Context(), auth.HashToken(cookie.Value))
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// authMe returns the signed-in user, their linked SSO identities and available providers.
func (s *Server) authMe(w http.ResponseWriter, r *http.Request) {
	actor, _ := actorFromContext(r.Context())
	ids, err := s.Store.ListUserIdentities(r.Context(), actor.User.ID)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	sso := []auth.ProviderSummary{}
	if s.Auth.SSO != nil {
		sso = s.Auth.SSO.EnabledProviders()
	}
	outIDs := make([]identityJSON, len(ids))
	for i, id := range ids {
		outIDs[i] = identityJSON{ID: id.ID, Provider: id.Provider, Email: id.Email, Login: id.Login, CreatedAt: id.CreatedAt}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"user":          toUserJSON(actor.User, ids),
		"identities":    outIDs,
		"sso_providers": sso,
	})
}

type updateMeRequest struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	actor, _ := actorFromContext(r.Context())
	var req updateMeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	display := strings.TrimSpace(req.DisplayName)
	if display == "" {
		display = actor.User.Username
	}
	u, err := s.Store.UpdateUserProfile(r.Context(), actor.User.ID, display, req.Email)
	switch {
	case errors.Is(err, store.ErrConflict):
		http.Error(w, "that email is already used by another account", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids, _ := s.Store.ListUserIdentities(r.Context(), u.ID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"user": toUserJSON(u, ids)})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	actor, _ := actorFromContext(r.Context())
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if actor.User.HasPassword() {
		ok, err := auth.VerifyPassword(req.CurrentPassword, actor.User.PasswordHash)
		if err != nil || !ok {
			http.Error(w, "current password is incorrect", http.StatusUnauthorized)
			return
		}
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Store.UpdateUserPassword(r.Context(), actor.User.ID, hash, actor.SessionHash); err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	s.Log.Info("password changed", "user", actor.User.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unlinkIdentity(w http.ResponseWriter, r *http.Request) {
	actor, _ := actorFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid identity id", http.StatusBadRequest)
		return
	}
	err = s.Store.DeleteIdentity(r.Context(), actor.User.ID, id)
	switch {
	case errors.Is(err, store.ErrLastAuthMethod):
		http.Error(w, "cannot unlink your only sign-in method; set a password or link another provider first", http.StatusConflict)
		return
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ssoStart begins an external OIDC/OAuth sign-in or invite acceptance flow (D145).
func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) {
	s.beginSSO(w, r, "login", uuid.Nil)
}

// ssoLink begins linking an additional SSO identity to the signed-in user (D145).
func (s *Server) ssoLink(w http.ResponseWriter, r *http.Request) {
	actor, _ := actorFromContext(r.Context())
	if !actor.ViaSession {
		http.Error(w, "linking an identity requires a browser session", http.StatusForbidden)
		return
	}
	s.beginSSO(w, r, "link", actor.User.ID)
}

func (s *Server) beginSSO(w http.ResponseWriter, r *http.Request, mode string, userID uuid.UUID) {
	provider := domain.SSOProvider(r.PathValue("provider"))
	if !domain.ValidSSOProvider(provider) || s.Auth.SSO == nil || !s.Auth.SSO.Enabled(provider) || s.Auth.StateSigner == nil {
		http.Error(w, "SSO provider is not available", http.StatusNotFound)
		return
	}
	stateTok, err1 := auth.RandomURLToken(24)
	nonce, err2 := auth.RandomURLToken(24)
	verifier, err3 := auth.RandomURLToken(32)
	if err := errors.Join(err1, err2, err3); err != nil {
		http.Error(w, "could not start sign-in", http.StatusInternalServerError)
		return
	}
	fallback := "/"
	if mode == "link" {
		fallback = "/settings/account"
	}
	st := auth.OAuthState{
		State:        stateTok,
		Provider:     provider,
		Mode:         mode,
		UserID:       userID,
		InviteToken:  strings.TrimSpace(r.URL.Query().Get("invite")),
		Next:         auth.SafeNext(r.URL.Query().Get("next"), fallback),
		Nonce:        nonce,
		CodeVerifier: verifier,
		ExpiresAt:    time.Now().UTC().Add(oauthStateTTL).Unix(),
	}
	signed, err := s.Auth.StateSigner.Sign(st)
	if err != nil {
		http.Error(w, "could not sign state", http.StatusInternalServerError)
		return
	}
	redirectURI := s.publicBaseURL(r) + "/api/auth/sso/" + string(provider) + "/callback"
	target, err := s.Auth.SSO.AuthURL(r.Context(), provider, redirectURI, stateTok, nonce, verifier)
	if err != nil {
		s.Log.Warn("sso start failed", "provider", provider, "error", err)
		http.Error(w, "could not reach SSO provider", http.StatusBadGateway)
		return
	}
	s.setOAuthStateCookie(w, r, signed)
	http.Redirect(w, r, target, http.StatusFound)
}

// ssoCallback completes an external OIDC/OAuth sign-in, invite acceptance, or link (D145).
func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	s.clearOAuthStateCookie(w, r)
	provider := domain.SSOProvider(r.PathValue("provider"))
	if !domain.ValidSSOProvider(provider) || s.Auth.SSO == nil || !s.Auth.SSO.Enabled(provider) || s.Auth.StateSigner == nil {
		http.Redirect(w, r, "/login?error=provider_unavailable", http.StatusFound)
		return
	}
	if r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, "/login?error=sso_denied", http.StatusFound)
		return
	}
	cookie, err := r.Cookie(OAuthStateCookieName)
	if err != nil {
		http.Redirect(w, r, "/login?error=invalid_state", http.StatusFound)
		return
	}
	st, err := s.Auth.StateSigner.Verify(cookie.Value, r.URL.Query().Get("state"), provider)
	if err != nil {
		http.Redirect(w, r, "/login?error=invalid_state", http.StatusFound)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		http.Redirect(w, r, "/login?error=missing_code", http.StatusFound)
		return
	}

	redirectURI := s.publicBaseURL(r) + "/api/auth/sso/" + string(provider) + "/callback"
	ident, err := s.Auth.SSO.Exchange(r.Context(), provider, redirectURI, code, st.CodeVerifier, st.Nonce)
	if err != nil {
		s.Log.Warn("sso exchange failed", "provider", provider, "error", err)
		dest := "/login?error=sso_failed"
		if st.Mode == "link" {
			dest = "/settings/account?error=sso_failed"
		}
		http.Redirect(w, r, dest, http.StatusFound)
		return
	}

	if st.Mode == "link" {
		actor, ok := s.authenticate(r)
		if !ok || !actor.ViaSession || actor.User.ID != st.UserID {
			http.Redirect(w, r, "/login?error=session_required", http.StatusFound)
			return
		}
		_, err := s.Store.LinkIdentity(r.Context(), store.NewIdentity{
			UserID:   actor.User.ID,
			Provider: ident.Provider,
			Subject:  ident.Subject,
			Email:    ident.Email,
			Login:    ident.Login,
		})
		if errors.Is(err, store.ErrConflict) {
			http.Redirect(w, r, "/settings/account?error=identity_in_use", http.StatusFound)
			return
		}
		if err != nil {
			http.Redirect(w, r, "/settings/account?error=link_failed", http.StatusFound)
			return
		}
		s.Log.Info("sso identity linked", "user", actor.User.Username, "provider", provider)
		http.Redirect(w, r, auth.SafeNext(st.Next, "/settings/account"), http.StatusFound)
		return
	}

	// Mode == "login": resolve existing linked identity, else invite, else allowlist.
	var user domain.User
	existing, err := s.Store.GetIdentity(r.Context(), ident.Provider, ident.Subject)
	switch {
	case err == nil:
		u, uerr := s.Store.GetUser(r.Context(), existing.UserID)
		if uerr != nil || !u.Active() {
			http.Redirect(w, r, "/login?error=account_disabled", http.StatusFound)
			return
		}
		user = u
	case errors.Is(err, store.ErrNotFound):
		newIdent := store.NewIdentity{
			Provider: ident.Provider,
			Subject:  ident.Subject,
			Email:    ident.Email,
			Login:    ident.Login,
		}
		display := strings.TrimSpace(ident.Name)
		if display == "" {
			display = ident.Login
		}
		email := ""
		if ident.EmailVerified {
			email = ident.Email
		}
		if st.InviteToken != "" {
			u, aerr := s.Store.AcceptInvite(r.Context(), auth.HashToken(st.InviteToken), store.NewUser{
				DisplayName: display,
				Email:       email,
			}, &newIdent)
			if aerr != nil {
				s.Log.Warn("sso invite accept failed", "provider", provider, "error", aerr)
				http.Redirect(w, r, "/login?error=invite_invalid", http.StatusFound)
				return
			}
			user = u
		} else if s.Auth.SSO.Allowlist().Matches(ident) {
			u, _, cerr := s.Store.CreateUserWithIdentity(r.Context(), store.NewUser{
				Username:    ident.Login,
				DisplayName: display,
				Email:       email,
				Role:        s.Auth.SSO.Allowlist().NormalizedRole(),
			}, newIdent)
			if cerr != nil {
				s.Log.Warn("sso allowlist provision failed", "provider", provider, "error", cerr)
				http.Redirect(w, r, "/login?error=provision_failed", http.StatusFound)
				return
			}
			user = u
		} else {
			http.Redirect(w, r, "/login?error=not_allowed", http.StatusFound)
			return
		}
	default:
		http.Redirect(w, r, "/login?error=sso_failed", http.StatusFound)
		return
	}

	raw, hash, err := auth.GenerateSessionToken()
	if err != nil {
		http.Redirect(w, r, "/login?error=session_failed", http.StatusFound)
		return
	}
	if _, err := s.Store.CreateSession(r.Context(), user.ID, hash, time.Now().UTC().Add(s.Auth.SessionTTL)); err != nil {
		http.Redirect(w, r, "/login?error=session_failed", http.StatusFound)
		return
	}
	s.setSessionCookie(w, r, raw)
	s.Log.Info("user signed in", "user", user.Username, "method", string(provider))
	http.Redirect(w, r, auth.SafeNext(st.Next, "/"), http.StatusFound)
}
