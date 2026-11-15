package egress

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

const tokenPrefix = "feg1"

var (
	// ErrInvalidToken means the token is malformed or its signature does not match.
	ErrInvalidToken = errors.New("egress: invalid token")
	// ErrExpiredToken means the token was valid but has expired.
	ErrExpiredToken = errors.New("egress: token expired")
)

// Grant is what a pod's egress token allows beyond the platform's list (D80):
// the hosts its project added in `.factory/config.yaml`, read from the base
// branch when the run started. The proxy needs nothing else to check them.
type Grant struct {
	Repo  string   `json:"repo"` // for audit logs
	Hosts []string `json:"hosts"`
	Exp   int64    `json:"exp"` // Unix seconds
}

// Signer issues and verifies egress tokens with HMAC-SHA256. Tokens are
// stateless, so the proxy keeps no state and survives restarts; they cannot be
// revoked before expiry.
type Signer struct {
	key []byte
	now func() time.Time
}

// NewSigner returns a signer for key, which must be at least 32 bytes.
func NewSigner(key []byte) (*Signer, error) {
	if len(key) < 32 {
		return nil, errors.New("egress: signing key must be at least 32 bytes")
	}
	return &Signer{key: key, now: time.Now}, nil
}

// Issue signs a token for g, valid for ttl. Its hosts must be ones a project
// may add (ValidateProjectHosts).
func (s *Signer) Issue(g Grant, ttl time.Duration) (string, error) {
	if g.Repo == "" || len(g.Hosts) == 0 {
		return "", errors.New("egress: grant needs a repo and hosts")
	}
	if err := ValidateProjectHosts(g.Hosts); err != nil {
		return "", err
	}
	if ttl <= 0 {
		return "", errors.New("egress: ttl must be positive")
	}
	g.Exp = s.now().Add(ttl).Unix()
	payload, err := json.Marshal(g)
	if err != nil {
		return "", fmt.Errorf("egress: encode grant: %w", err)
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
	if err := json.Unmarshal(payload, &g); err != nil || g.Repo == "" || g.Exp == 0 {
		return Grant{}, ErrInvalidToken
	}
	if !s.now().Before(time.Unix(g.Exp, 0)) {
		return Grant{}, ErrExpiredToken
	}
	return g, nil
}

func (s *Signer) mac(body string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(body))
	return m.Sum(nil)
}
