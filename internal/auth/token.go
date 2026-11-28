package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// APITokenPrefix identifies Frankie per-person bearer tokens (D144).
const APITokenPrefix = "frk_"

// HashToken returns the SHA-256 digest of a raw secret token.
func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// GenerateSessionToken creates a 256-bit random session secret and its hash.
func GenerateSessionToken() (raw string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("auth: session token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw), nil
}

// GenerateAPIToken creates a per-person API token (`frk_<48 hex>`), its display
// prefix (`frk_<8 hex>`), and its SHA-256 hash.
func GenerateAPIToken() (raw, prefix string, hash []byte, err error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", "", nil, fmt.Errorf("auth: api token: %w", err)
	}
	raw = APITokenPrefix + hex.EncodeToString(b)
	prefix = raw[:len(APITokenPrefix)+8]
	return raw, prefix, HashToken(raw), nil
}

// GenerateInviteToken creates a 192-bit random invite token and its hash.
func GenerateInviteToken() (raw string, hash []byte, err error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("auth: invite token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw), nil
}
