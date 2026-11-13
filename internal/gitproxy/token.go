// Package gitproxy is the control plane's git smart-HTTP proxy for agent pods
// (D21). Pods authenticate with a short-lived per-step token; the proxy allows
// reading the bound repo and updating refs only under refs/heads/agent/<issue-key>/,
// and talks to the real git host with a credential the pod never sees.
package gitproxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const tokenPrefix = "fgp1"

var (
	// ErrInvalidToken means the token is malformed or its signature does not match.
	ErrInvalidToken = errors.New("gitproxy: invalid token")
	// ErrExpiredToken means the token was valid but has expired.
	ErrExpiredToken = errors.New("gitproxy: token expired")
)

// Grant is what a token allows: read the repo, push branches of one issue.
type Grant struct {
	Repo      string `json:"repo"`           // factory repo name, e.g. "misarescu/toy"
	IssueKey  string `json:"issue"`          // e.g. "frk-42"
	StepRunID string `json:"step,omitempty"` // for audit logs
	// ReadOnly forbids pushing: for pods that run code but must not change the
	// repo, such as command gates (D33).
	ReadOnly bool      `json:"ro,omitempty"`
	Expires  time.Time `json:"-"`
	Exp      int64     `json:"exp"` // Unix seconds, the signed form of Expires
}

// Signer issues and verifies tokens with HMAC-SHA256. Tokens are stateless, so
// they survive control plane restarts; they cannot be revoked before expiry.
type Signer struct {
	key []byte
	now func() time.Time
}

// NewSigner returns a signer for key, which must be at least 32 bytes.
func NewSigner(key []byte) (*Signer, error) {
	if len(key) < 32 {
		return nil, errors.New("gitproxy: signing key must be at least 32 bytes")
	}
	return &Signer{key: key, now: time.Now}, nil
}

// Issue signs a token for g, valid for ttl.
func (s *Signer) Issue(g Grant, ttl time.Duration) (string, error) {
	if g.Repo == "" || g.IssueKey == "" {
		return "", errors.New("gitproxy: grant needs repo and issue key")
	}
	if ttl <= 0 {
		return "", errors.New("gitproxy: ttl must be positive")
	}
	g.Exp = s.now().Add(ttl).Unix()
	payload, err := json.Marshal(g)
	if err != nil {
		return "", fmt.Errorf("gitproxy: encode grant: %w", err)
	}
	body := tokenPrefix + "." + base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.mac(body)), nil
}

// Verify checks the signature and expiry and returns the grant.
func (s *Signer) Verify(token string) (Grant, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix {
		return Grant{}, ErrInvalidToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, s.mac(parts[0]+"."+parts[1])) {
		return Grant{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Grant{}, ErrInvalidToken
	}
	var g Grant
	if err := json.Unmarshal(payload, &g); err != nil || g.Repo == "" || g.IssueKey == "" || g.Exp == 0 {
		return Grant{}, ErrInvalidToken
	}
	g.Expires = time.Unix(g.Exp, 0)
	if !s.now().Before(g.Expires) {
		return Grant{}, ErrExpiredToken
	}
	return g, nil
}

func (s *Signer) mac(body string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(body))
	return m.Sum(nil)
}
