package auth

import (
	"bytes"
	"strings"
	"testing"
)

func TestTokenGenerators(t *testing.T) {
	s1, h1, err := GenerateSessionToken()
	if err != nil || len(s1) < 32 || !bytes.Equal(h1, HashToken(s1)) {
		t.Fatalf("GenerateSessionToken = %q, len(h)=%d, err=%v", s1, len(h1), err)
	}
	s2, _, err := GenerateSessionToken()
	if err != nil || s1 == s2 {
		t.Fatalf("expected distinct session tokens")
	}

	raw, prefix, h, err := GenerateAPIToken()
	if err != nil || !strings.HasPrefix(raw, APITokenPrefix) || !strings.HasPrefix(raw, prefix) || len(prefix) != 12 || !bytes.Equal(h, HashToken(raw)) {
		t.Fatalf("GenerateAPIToken = %q prefix=%q err=%v", raw, prefix, err)
	}

	inv, ih, err := GenerateInviteToken()
	if err != nil || len(inv) < 24 || !bytes.Equal(ih, HashToken(inv)) {
		t.Fatalf("GenerateInviteToken = %q err=%v", inv, err)
	}
}
