package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

// ProviderConfig configures one external SSO provider (D145).
type ProviderConfig struct {
	ID           domain.SSOProvider
	Name         string
	ClientID     string
	ClientSecret string
	// IssuerURL is the OIDC issuer for Google (default https://accounts.google.com)
	// or GitLab (default https://gitlab.com). Ignored for GitHub.
	IssuerURL string
	// Optional overrides for GitHub (mainly for tests or GitHub Enterprise).
	AuthURL     string
	TokenURL    string
	UserInfoURL string
	EmailsURL   string
	OrgsURL     string
}

// ProviderSummary is the public metadata returned by GET /api/auth/providers.
type ProviderSummary struct {
	ID   domain.SSOProvider `json:"id"`
	Name string             `json:"name"`
}

// Allowlist defines who may auto-provision an account on first SSO sign-in
// without an explicit admin invite (D145).
type Allowlist struct {
	EmailDomains []string
	GitHubOrgs   []string
	DefaultRole  domain.Role
}

// NormalizedRole returns the allowlist's default role (never admin; defaults to viewer).
func (a Allowlist) NormalizedRole() domain.Role {
	if a.DefaultRole == domain.RoleMember {
		return domain.RoleMember
	}
	return domain.RoleViewer
}

// Matches reports whether ident satisfies the email domain or GitHub org allowlist.
func (a Allowlist) Matches(ident SSOIdentity) bool {
	if ident.EmailVerified && ident.Email != "" && len(a.EmailDomains) > 0 {
		if _, dom, ok := strings.Cut(strings.ToLower(strings.TrimSpace(ident.Email)), "@"); ok {
			for _, allowed := range a.EmailDomains {
				if strings.EqualFold(strings.TrimSpace(allowed), dom) {
					return true
				}
			}
		}
	}
	if ident.Provider == domain.ProviderGitHub && len(a.GitHubOrgs) > 0 {
		for _, org := range ident.GitHubOrgs {
			for _, allowed := range a.GitHubOrgs {
				if strings.EqualFold(strings.TrimSpace(allowed), strings.TrimSpace(org)) {
					return true
				}
			}
		}
	}
	return false
}

// SSOIdentity is the verified external identity returned by Exchange.
type SSOIdentity struct {
	Provider      domain.SSOProvider
	Subject       string
	Email         string
	EmailVerified bool
	Login         string
	Name          string
	GitHubOrgs    []string
}

// SSOService handles authorization redirects and code exchanges for enabled
// Google (OIDC), GitLab (OIDC) and GitHub (OAuth2) providers (D145).
type SSOService struct {
	http      *http.Client
	allowlist Allowlist
	providers map[domain.SSOProvider]ProviderConfig
	now       func() time.Time

	mu        sync.Mutex
	discovery map[domain.SSOProvider]cachedDiscovery
	jwks      map[string]cachedJWKS
}

type cachedDiscovery struct {
	doc       oidcDiscovery
	fetchedAt time.Time
}

type oidcDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
}

type cachedJWKS struct {
	keys      map[string]crypto.PublicKey
	fetchedAt time.Time
}

// NewSSOService validates and constructs an SSOService. Providers with empty
// ClientID or ClientSecret are skipped.
func NewSSOService(configs []ProviderConfig, allowlist Allowlist, client *http.Client) (*SSOService, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if allowlist.DefaultRole != "" && allowlist.DefaultRole != domain.RoleViewer && allowlist.DefaultRole != domain.RoleMember {
		return nil, fmt.Errorf("auth: allowlist defaultRole %q must be viewer or member", allowlist.DefaultRole)
	}
	provs := make(map[domain.SSOProvider]ProviderConfig)
	for _, cfg := range configs {
		if !domain.ValidSSOProvider(cfg.ID) {
			return nil, fmt.Errorf("auth: unsupported SSO provider %q", cfg.ID)
		}
		if cfg.ClientID == "" || cfg.ClientSecret == "" {
			continue
		}
		switch cfg.ID {
		case domain.ProviderGoogle:
			if cfg.Name == "" {
				cfg.Name = "Google"
			}
			if cfg.IssuerURL == "" {
				cfg.IssuerURL = "https://accounts.google.com"
			}
		case domain.ProviderGitLab:
			if cfg.Name == "" {
				cfg.Name = "GitLab"
			}
			if cfg.IssuerURL == "" {
				cfg.IssuerURL = "https://gitlab.com"
			}
		case domain.ProviderGitHub:
			if cfg.Name == "" {
				cfg.Name = "GitHub"
			}
			if cfg.AuthURL == "" {
				cfg.AuthURL = "https://github.com/login/oauth/authorize"
			}
			if cfg.TokenURL == "" {
				cfg.TokenURL = "https://github.com/login/oauth/access_token"
			}
			if cfg.UserInfoURL == "" {
				cfg.UserInfoURL = "https://api.github.com/user"
			}
			if cfg.EmailsURL == "" {
				cfg.EmailsURL = "https://api.github.com/user/emails"
			}
			if cfg.OrgsURL == "" {
				cfg.OrgsURL = "https://api.github.com/user/orgs?per_page=100"
			}
		}
		provs[cfg.ID] = cfg
	}
	return &SSOService{
		http:      client,
		allowlist: allowlist,
		providers: provs,
		now:       time.Now,
		discovery: make(map[domain.SSOProvider]cachedDiscovery),
		jwks:      make(map[string]cachedJWKS),
	}, nil
}

// Allowlist returns the configured allowlist.
func (s *SSOService) Allowlist() Allowlist { return s.allowlist }

// EnabledProviders returns the enabled providers in stable display order.
func (s *SSOService) EnabledProviders() []ProviderSummary {
	order := []domain.SSOProvider{domain.ProviderGoogle, domain.ProviderGitLab, domain.ProviderGitHub}
	out := make([]ProviderSummary, 0, len(s.providers))
	for _, id := range order {
		if cfg, ok := s.providers[id]; ok {
			out = append(out, ProviderSummary{ID: cfg.ID, Name: cfg.Name})
		}
	}
	return out
}

// Enabled reports whether provider is configured.
func (s *SSOService) Enabled(provider domain.SSOProvider) bool {
	_, ok := s.providers[provider]
	return ok
}

// AuthURL builds the external authorization redirect URL with PKCE S256.
func (s *SSOService) AuthURL(ctx context.Context, provider domain.SSOProvider, redirectURI, state, nonce, codeVerifier string) (string, error) {
	cfg, ok := s.providers[provider]
	if !ok {
		return "", fmt.Errorf("auth: SSO provider %q is not configured", provider)
	}
	q := url.Values{
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"state":                 {state},
		"code_challenge":        {PKCEChallenge(codeVerifier)},
		"code_challenge_method": {"S256"},
	}
	if provider == domain.ProviderGitHub {
		scope := "read:user user:email"
		if len(s.allowlist.GitHubOrgs) > 0 {
			scope += " read:org"
		}
		q.Set("scope", scope)
		return cfg.AuthURL + "?" + q.Encode(), nil
	}
	doc, err := s.discover(ctx, cfg)
	if err != nil {
		return "", err
	}
	q.Set("scope", "openid email profile")
	q.Set("nonce", nonce)
	return doc.AuthorizationEndpoint + "?" + q.Encode(), nil
}

// Exchange exchanges an authorization code for a verified external identity.
func (s *SSOService) Exchange(ctx context.Context, provider domain.SSOProvider, redirectURI, code, codeVerifier, expectedNonce string) (SSOIdentity, error) {
	cfg, ok := s.providers[provider]
	if !ok {
		return SSOIdentity{}, fmt.Errorf("auth: SSO provider %q is not configured", provider)
	}
	if provider == domain.ProviderGitHub {
		return s.exchangeGitHub(ctx, cfg, redirectURI, code, codeVerifier)
	}
	return s.exchangeOIDC(ctx, cfg, redirectURI, code, codeVerifier, expectedNonce)
}

func (s *SSOService) exchangeOIDC(ctx context.Context, cfg ProviderConfig, redirectURI, code, codeVerifier, expectedNonce string) (SSOIdentity, error) {
	doc, err := s.discover(ctx, cfg)
	if err != nil {
		return SSOIdentity{}, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code_verifier": {codeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, doc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return SSOIdentity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return SSOIdentity{}, fmt.Errorf("auth: oidc token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return SSOIdentity{}, fmt.Errorf("auth: read oidc token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return SSOIdentity{}, fmt.Errorf("auth: oidc token exchange returned %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.IDToken == "" {
		return SSOIdentity{}, errors.New("auth: oidc response missing id_token")
	}

	claims, err := s.verifyIDToken(ctx, doc, cfg.ClientID, tok.IDToken, expectedNonce)
	if err != nil {
		return SSOIdentity{}, err
	}
	ident := SSOIdentity{
		Provider:      cfg.ID,
		Subject:       claims.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Login:         claims.PreferredUsername,
		Name:          claims.Name,
	}
	// Fall back to UserInfo if email or name was omitted from the ID token.
	if (ident.Email == "" || ident.Name == "") && doc.UserInfoEndpoint != "" && tok.AccessToken != "" {
		if ui, err := s.fetchOIDCUserInfo(ctx, doc.UserInfoEndpoint, tok.AccessToken); err == nil && ui.Subject == ident.Subject {
			if ident.Email == "" {
				ident.Email = ui.Email
				ident.EmailVerified = ui.EmailVerified
			}
			if ident.Name == "" {
				ident.Name = ui.Name
			}
			if ident.Login == "" {
				ident.Login = ui.PreferredUsername
			}
		}
	}
	return ident, nil
}

type idTokenClaims struct {
	Issuer            string      `json:"iss"`
	Subject           string      `json:"sub"`
	Audience          audienceVal `json:"aud"`
	AuthorizedParty   string      `json:"azp"`
	ExpiresAt         int64       `json:"exp"`
	NotBefore         int64       `json:"nbf"`
	Nonce             string      `json:"nonce"`
	Email             string      `json:"email"`
	EmailVerified     bool        `json:"email_verified"`
	Name              string      `json:"name"`
	PreferredUsername string      `json:"preferred_username"`
}

type audienceVal []string

func (a *audienceVal) UnmarshalJSON(b []byte) error {
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		*a = []string{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	*a = list
	return nil
}

func (s *SSOService) verifyIDToken(ctx context.Context, doc oidcDiscovery, clientID, rawJWT, expectedNonce string) (idTokenClaims, error) {
	parts := strings.Split(rawJWT, ".")
	if len(parts) != 3 {
		return idTokenClaims{}, errors.New("auth: malformed id_token")
	}
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return idTokenClaims{}, errors.New("auth: decode id_token header")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return idTokenClaims{}, errors.New("auth: parse id_token header")
	}
	if hdr.Alg != "RS256" && hdr.Alg != "ES256" && hdr.Alg != "ES384" {
		return idTokenClaims{}, fmt.Errorf("auth: unsupported id_token alg %q", hdr.Alg)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return idTokenClaims{}, errors.New("auth: decode id_token signature")
	}
	pub, err := s.lookupJWK(ctx, doc.JWKSURI, hdr.Kid)
	if err != nil {
		return idTokenClaims{}, err
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	if err := verifyJWTSig(hdr.Alg, pub, signingInput, sig); err != nil {
		return idTokenClaims{}, err
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return idTokenClaims{}, errors.New("auth: decode id_token payload")
	}
	var claims idTokenClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return idTokenClaims{}, errors.New("auth: parse id_token claims")
	}
	now := s.now().Unix()
	const skew = 30
	if claims.Issuer != doc.Issuer {
		return idTokenClaims{}, fmt.Errorf("auth: id_token issuer %q != %q", claims.Issuer, doc.Issuer)
	}
	if !slices.Contains(claims.Audience, clientID) {
		return idTokenClaims{}, errors.New("auth: id_token audience mismatch")
	}
	if len(claims.Audience) > 1 && claims.AuthorizedParty != clientID {
		return idTokenClaims{}, errors.New("auth: id_token azp mismatch")
	}
	if claims.Subject == "" {
		return idTokenClaims{}, errors.New("auth: id_token missing sub")
	}
	if claims.ExpiresAt <= now-skew {
		return idTokenClaims{}, errors.New("auth: id_token expired")
	}
	if claims.NotBefore > 0 && claims.NotBefore > now+skew {
		return idTokenClaims{}, errors.New("auth: id_token not yet valid")
	}
	if expectedNonce != "" && subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(expectedNonce)) != 1 {
		return idTokenClaims{}, errors.New("auth: id_token nonce mismatch")
	}
	return claims, nil
}

func verifyJWTSig(alg string, pub crypto.PublicKey, input, sig []byte) error {
	switch alg {
	case "RS256":
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return errors.New("auth: jwk key type mismatch for RS256")
		}
		h := sha256.Sum256(input)
		if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, h[:], sig); err != nil {
			return errors.New("auth: invalid RS256 signature")
		}
		return nil
	case "ES256":
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok || ecPub.Curve != elliptic.P256() || len(sig) != 64 {
			return errors.New("auth: invalid ES256 key or signature")
		}
		h := sha256.Sum256(input)
		r := new(big.Int).SetBytes(sig[:32])
		sv := new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(ecPub, h[:], r, sv) {
			return errors.New("auth: invalid ES256 signature")
		}
		return nil
	case "ES384":
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok || ecPub.Curve != elliptic.P384() || len(sig) != 96 {
			return errors.New("auth: invalid ES384 key or signature")
		}
		h := sha512.Sum384(input)
		r := new(big.Int).SetBytes(sig[:48])
		sv := new(big.Int).SetBytes(sig[48:])
		if !ecdsa.Verify(ecPub, h[:], r, sv) {
			return errors.New("auth: invalid ES384 signature")
		}
		return nil
	default:
		return fmt.Errorf("auth: unsupported alg %q", alg)
	}
}

func (s *SSOService) discover(ctx context.Context, cfg ProviderConfig) (oidcDiscovery, error) {
	s.mu.Lock()
	if c, ok := s.discovery[cfg.ID]; ok && s.now().Sub(c.fetchedAt) < time.Hour {
		s.mu.Unlock()
		return c.doc, nil
	}
	s.mu.Unlock()

	wellKnown := strings.TrimRight(cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return oidcDiscovery{}, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return oidcDiscovery{}, fmt.Errorf("auth: oidc discovery %s: %w", cfg.ID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return oidcDiscovery{}, fmt.Errorf("auth: oidc discovery %s returned %d", cfg.ID, resp.StatusCode)
	}
	var doc oidcDiscovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return oidcDiscovery{}, fmt.Errorf("auth: decode oidc discovery: %w", err)
	}
	if doc.Issuer == "" || doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return oidcDiscovery{}, errors.New("auth: incomplete oidc discovery document")
	}
	if strings.TrimRight(doc.Issuer, "/") != strings.TrimRight(cfg.IssuerURL, "/") {
		return oidcDiscovery{}, fmt.Errorf("auth: oidc discovery issuer %q != configured %q", doc.Issuer, cfg.IssuerURL)
	}
	s.mu.Lock()
	s.discovery[cfg.ID] = cachedDiscovery{doc: doc, fetchedAt: s.now()}
	s.mu.Unlock()
	return doc, nil
}

func (s *SSOService) lookupJWK(ctx context.Context, jwksURI, kid string) (crypto.PublicKey, error) {
	s.mu.Lock()
	c, ok := s.jwks[jwksURI]
	s.mu.Unlock()
	if ok && s.now().Sub(c.fetchedAt) < 15*time.Minute {
		if k, found := pickJWK(c.keys, kid); found {
			return k, nil
		}
	}
	keys, err := s.fetchJWKS(ctx, jwksURI)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.jwks[jwksURI] = cachedJWKS{keys: keys, fetchedAt: s.now()}
	s.mu.Unlock()
	k, found := pickJWK(keys, kid)
	if !found {
		return nil, fmt.Errorf("auth: jwk kid %q not found", kid)
	}
	return k, nil
}

func pickJWK(keys map[string]crypto.PublicKey, kid string) (crypto.PublicKey, bool) {
	if kid != "" {
		k, ok := keys[kid]
		return k, ok
	}
	if len(keys) == 1 {
		for _, k := range keys {
			return k, true
		}
	}
	return nil, false
}

func (s *SSOService) fetchJWKS(ctx context.Context, jwksURI string) (map[string]crypto.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: fetch jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth: jwks returned %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, fmt.Errorf("auth: decode jwks: %w", err)
	}
	out := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch k.Kty {
		case "RSA":
			nb, err1 := base64.RawURLEncoding.DecodeString(k.N)
			eb, err2 := base64.RawURLEncoding.DecodeString(k.E)
			if err1 != nil || err2 != nil || len(nb) == 0 || len(eb) == 0 || len(eb) > 8 {
				continue
			}
			eVal := int(new(big.Int).SetBytes(eb).Int64())
			if eVal < 3 || eVal%2 == 0 {
				continue
			}
			out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: eVal}
		case "EC":
			var curve elliptic.Curve
			var byteLen int
			switch k.Crv {
			case "P-256":
				curve, byteLen = elliptic.P256(), 32
			case "P-384":
				curve, byteLen = elliptic.P384(), 48
			default:
				continue
			}
			xb, err1 := base64.RawURLEncoding.DecodeString(k.X)
			yb, err2 := base64.RawURLEncoding.DecodeString(k.Y)
			if err1 != nil || err2 != nil || len(xb) > byteLen || len(yb) > byteLen {
				continue
			}
			uncompressed := make([]byte, 1+2*byteLen)
			uncompressed[0] = 0x04
			copy(uncompressed[1+byteLen-len(xb):1+byteLen], xb)
			copy(uncompressed[1+2*byteLen-len(yb):], yb)
			pub, err := ecdsa.ParseUncompressedPublicKey(curve, uncompressed)
			if err != nil {
				continue
			}
			out[k.Kid] = pub
		}
	}
	return out, nil
}

func (s *SSOService) fetchOIDCUserInfo(ctx context.Context, endpoint, accessToken string) (idTokenClaims, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return idTokenClaims{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := s.http.Do(req)
	if err != nil {
		return idTokenClaims{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return idTokenClaims{}, fmt.Errorf("userinfo returned %d", resp.StatusCode)
	}
	var c idTokenClaims
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&c); err != nil {
		return idTokenClaims{}, err
	}
	return c, nil
}

func (s *SSOService) exchangeGitHub(ctx context.Context, cfg ProviderConfig, redirectURI, code, codeVerifier string) (SSOIdentity, error) {
	form := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return SSOIdentity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return SSOIdentity{}, fmt.Errorf("auth: github token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
		return SSOIdentity{}, fmt.Errorf("auth: decode github token: %w", err)
	}
	if resp.StatusCode != http.StatusOK || tok.AccessToken == "" {
		return SSOIdentity{}, fmt.Errorf("auth: github token exchange failed (%d %s)", resp.StatusCode, tok.Error)
	}

	var ghUser struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := s.githubGet(ctx, cfg.UserInfoURL, tok.AccessToken, &ghUser); err != nil {
		return SSOIdentity{}, fmt.Errorf("auth: github user: %w", err)
	}
	if ghUser.ID == 0 {
		return SSOIdentity{}, errors.New("auth: github user missing id")
	}

	ident := SSOIdentity{
		Provider: domain.ProviderGitHub,
		Subject:  strconv.FormatInt(ghUser.ID, 10),
		Login:    ghUser.Login,
		Name:     ghUser.Name,
	}

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := s.githubGet(ctx, cfg.EmailsURL, tok.AccessToken, &emails); err == nil {
		for _, e := range emails {
			if e.Primary && e.Verified {
				ident.Email = e.Email
				ident.EmailVerified = true
				break
			}
		}
		if ident.Email == "" {
			for _, e := range emails {
				if e.Verified {
					ident.Email = e.Email
					ident.EmailVerified = true
					break
				}
			}
		}
	}

	if len(s.allowlist.GitHubOrgs) > 0 {
		var orgs []struct {
			Login string `json:"login"`
		}
		if err := s.githubGet(ctx, cfg.OrgsURL, tok.AccessToken, &orgs); err == nil {
			for _, o := range orgs {
				if o.Login != "" {
					ident.GitHubOrgs = append(ident.GitHubOrgs, o.Login)
				}
			}
		}
	}

	return ident, nil
}

func (s *SSOService) githubGet(ctx context.Context, endpoint, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "frankie-controlplane")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
