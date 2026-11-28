package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

func TestAllowlistMatches(t *testing.T) {
	al := Allowlist{
		EmailDomains: []string{"Example.com"},
		GitHubOrgs:   []string{"frankie-team"},
		DefaultRole:  domain.RoleMember,
	}
	if !al.Matches(SSOIdentity{Provider: domain.ProviderGoogle, Email: "alice@example.com", EmailVerified: true}) {
		t.Error("verified allowed email domain should match")
	}
	if al.Matches(SSOIdentity{Provider: domain.ProviderGoogle, Email: "alice@example.com", EmailVerified: false}) {
		t.Error("unverified email must not match allowlist")
	}
	if al.Matches(SSOIdentity{Provider: domain.ProviderGoogle, Email: "alice@evil-example.com", EmailVerified: true}) {
		t.Error("partial suffix domain must not match")
	}
	if !al.Matches(SSOIdentity{Provider: domain.ProviderGitHub, GitHubOrgs: []string{"other", "Frankie-Team"}}) {
		t.Error("github org should match case-insensitively")
	}
	if al.Matches(SSOIdentity{Provider: domain.ProviderGitLab, GitHubOrgs: []string{"frankie-team"}}) {
		t.Error("github org allowlist must only apply to github provider")
	}
}

func TestOIDCExchangeAndVerification(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	nonceToSign := "nonce-abc"
	signJWT := func(claims map[string]any) string {
		hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1"})
		pay, _ := json.Marshal(claims)
		in := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(pay)
		h := sha256.Sum256([]byte(in))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
		if err != nil {
			t.Fatal(err)
		}
		return in + "." + base64.RawURLEncoding.EncodeToString(sig)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
			"jwks_uri":               srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA",
				"kid": "k1",
				"use": "sig",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		idTok := signJWT(map[string]any{
			"iss":                srv.URL,
			"sub":                "sub-42",
			"aud":                "cid",
			"exp":                time.Now().Add(time.Hour).Unix(),
			"nonce":              nonceToSign,
			"email":              "mihai@example.com",
			"email_verified":     true,
			"name":               "Mihai",
			"preferred_username": "mihai",
		})
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at", "id_token": idTok})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	svc, err := NewSSOService([]ProviderConfig{{
		ID: domain.ProviderGoogle, ClientID: "cid", ClientSecret: "csec", IssuerURL: srv.URL,
	}}, Allowlist{}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	u, err := svc.AuthURL(t.Context(), domain.ProviderGoogle, "https://app/cb", "st1", "nonce-abc", "ver1")
	if err != nil || !strings.HasPrefix(u, srv.URL+"/authorize?") || !strings.Contains(u, "nonce=nonce-abc") {
		t.Fatalf("AuthURL = %q, %v", u, err)
	}
	ident, err := svc.Exchange(t.Context(), domain.ProviderGoogle, "https://app/cb", "code1", "ver1", "nonce-abc")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if ident.Subject != "sub-42" || ident.Email != "mihai@example.com" || !ident.EmailVerified || ident.Name != "Mihai" {
		t.Fatalf("unexpected identity: %+v", ident)
	}
	// Wrong nonce must fail.
	if _, err := svc.Exchange(t.Context(), domain.ProviderGoogle, "https://app/cb", "code1", "ver1", "wrong-nonce"); err == nil {
		t.Fatal("expected nonce mismatch error")
	}
}

func TestGitHubExchange(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "gho_123"})
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 999, "login": "octo", "name": "Octo Cat"})
	})
	mux.HandleFunc("GET /user/emails", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"email": "unverified@example.com", "primary": false, "verified": false},
			{"email": "octo@example.com", "primary": true, "verified": true},
		})
	})
	mux.HandleFunc("GET /user/orgs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"login": "my-org"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	svc, err := NewSSOService([]ProviderConfig{{
		ID: domain.ProviderGitHub, ClientID: "gh-cid", ClientSecret: "gh-sec",
		AuthURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token",
		UserInfoURL: srv.URL + "/user", EmailsURL: srv.URL + "/user/emails", OrgsURL: srv.URL + "/user/orgs",
	}}, Allowlist{GitHubOrgs: []string{"my-org"}}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ident, err := svc.Exchange(t.Context(), domain.ProviderGitHub, "https://app/cb", "c1", "v1", "")
	if err != nil {
		t.Fatalf("GitHub Exchange: %v", err)
	}
	if ident.Subject != "999" || ident.Login != "octo" || ident.Email != "octo@example.com" || !ident.EmailVerified || len(ident.GitHubOrgs) != 1 || ident.GitHubOrgs[0] != "my-org" {
		t.Fatalf("unexpected github identity: %+v", ident)
	}
	if !svc.Allowlist().Matches(ident) {
		t.Fatal("expected github org allowlist to match")
	}
}
