// Package auth implements password hashing, token generation, login rate
// limiting, signed OAuth/OIDC state cookies and external SSO verification (M10,
// D144-D145).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/misarescu/frankie/internal/domain"
)

const (
	argonMemory  uint32 = 64 * 1024 // 64 MiB (OWASP recommended)
	argonTime    uint32 = 3
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

var dummySalt = []byte("frankie-dummy-16")

// HashPassword hashes password with argon2id and returns a PHC-formatted string.
func HashPassword(password string) (string, error) {
	if err := domain.ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: random salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks password against a PHC-formatted argon2id hash in
// constant time.
func VerifyPassword(password, encoded string) (bool, error) {
	if password == "" || len(password) > domain.MaxPasswordLen || encoded == "" {
		return false, nil
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errors.New("auth: unsupported password hash format")
	}
	if parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, errors.New("auth: unsupported argon2 version")
	}
	var mem, iters uint64
	var threads uint64
	for kv := range strings.SplitSeq(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return false, errors.New("auth: malformed argon2 parameters")
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return false, errors.New("auth: malformed argon2 parameter value")
		}
		switch k {
		case "m":
			mem = n
		case "t":
			iters = n
		case "p":
			threads = n
		default:
			return false, errors.New("auth: unknown argon2 parameter")
		}
	}
	if mem == 0 || iters == 0 || threads == 0 || threads > 255 {
		return false, errors.New("auth: invalid argon2 parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("auth: decode salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errors.New("auth: decode hash")
	}
	got := argon2.IDKey([]byte(password), salt, uint32(iters), uint32(mem), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// DummyVerify spends the same argon2id work as VerifyPassword so failed logins
// for unknown users do not return measurably faster than wrong passwords.
func DummyVerify() {
	_ = argon2.IDKey([]byte("dummy-password"), dummySalt, argonTime, argonMemory, argonThreads, argonKeyLen)
}
