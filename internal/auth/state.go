package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

// MinStateKeyLen is the minimum HMAC key length for OAuth state cookies.
const MinStateKeyLen = 32

// OAuthState is the signed, short-lived payload carried in the HttpOnly
// frankie_oauth_state cookie across an external SSO redirect (D145).
type OAuthState struct {
	State        string             `json:"s"`
	Provider     domain.SSOProvider `json:"p"`
	Mode         string             `json:"m"` // "login" or "link"
	UserID       uuid.UUID          `json:"u,omitempty"`
	InviteToken  string             `json:"i,omitempty"`
	Next         string             `json:"n,omitempty"`
	Nonce        string             `json:"no,omitempty"`
	CodeVerifier string             `json:"cv"`
	ExpiresAt    int64              `json:"exp"`
}

// StateSigner signs and verifies OAuthState cookies with HMAC-SHA256.
type StateSigner struct {
	key []byte
	now func() time.Time
}

// NewStateSigner creates a signer with key (>= 32 bytes).
func NewStateSigner(key []byte) (*StateSigner, error) {
	if len(key) < MinStateKeyLen {
		return nil, fmt.Errorf("auth: state signing key must be at least %d bytes", MinStateKeyLen)
	}
	cp := append([]byte(nil), key...)
	return &StateSigner{key: cp, now: time.Now}, nil
}

// Sign encodes and MACs st into a cookie-safe string.
func (s *StateSigner) Sign(st OAuthState) (string, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return "", fmt.Errorf("auth: marshal state: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := s.mac(payload)
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

// Verify checks the MAC, expiry, provider and state query parameter.
func (s *StateSigner) Verify(cookieVal, queryState string, provider domain.SSOProvider) (OAuthState, error) {
	payload, sigB64, ok := strings.Cut(cookieVal, ".")
	if !ok || payload == "" || sigB64 == "" {
		return OAuthState{}, errors.New("auth: malformed state cookie")
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return OAuthState{}, errors.New("auth: malformed state signature")
	}
	want := s.mac(payload)
	if subtle.ConstantTimeCompare(sig, want) != 1 {
		return OAuthState{}, errors.New("auth: invalid state signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return OAuthState{}, errors.New("auth: malformed state payload")
	}
	var st OAuthState
	if err := json.Unmarshal(raw, &st); err != nil {
		return OAuthState{}, errors.New("auth: malformed state json")
	}
	if s.now().Unix() > st.ExpiresAt {
		return OAuthState{}, errors.New("auth: state expired")
	}
	if st.Provider != provider {
		return OAuthState{}, errors.New("auth: state provider mismatch")
	}
	if st.State == "" || subtle.ConstantTimeCompare([]byte(st.State), []byte(queryState)) != 1 {
		return OAuthState{}, errors.New("auth: state mismatch")
	}
	return st, nil
}

func (s *StateSigner) mac(payload string) []byte {
	h := hmac.New(sha256.New, s.key)
	_, _ = h.Write([]byte(payload))
	return h.Sum(nil)
}

// RandomURLToken returns n random bytes base64url-encoded (no padding).
func RandomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// PKCEChallenge returns the S256 code_challenge for codeVerifier (RFC 7636).
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// SafeNext returns next if it is a safe same-origin relative path, else fallback.
func SafeNext(next, fallback string) string {
	next = strings.TrimSpace(next)
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\r\n\\") {
		return fallback
	}
	return next
}
