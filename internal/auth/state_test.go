package auth

import (
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

func TestStateSigner(t *testing.T) {
	if _, err := NewStateSigner([]byte("too-short")); err == nil {
		t.Fatal("expected error for short key")
	}
	signer, err := NewStateSigner([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	signer.now = func() time.Time { return now }

	st := OAuthState{
		State:        "state-123",
		Provider:     domain.ProviderGoogle,
		Mode:         "login",
		Next:         "/approvals",
		Nonce:        "nonce-1",
		CodeVerifier: "verifier-1",
		ExpiresAt:    now.Add(10 * time.Minute).Unix(),
	}
	cookie, err := signer.Sign(st)
	if err != nil {
		t.Fatal(err)
	}
	got, err := signer.Verify(cookie, "state-123", domain.ProviderGoogle)
	if err != nil || got.Next != "/approvals" || got.Nonce != "nonce-1" {
		t.Fatalf("Verify valid = %+v, %v", got, err)
	}
	if _, err := signer.Verify(cookie, "wrong", domain.ProviderGoogle); err == nil {
		t.Fatal("expected state mismatch error")
	}
	if _, err := signer.Verify(cookie, "state-123", domain.ProviderGitHub); err == nil {
		t.Fatal("expected provider mismatch error")
	}
	if _, err := signer.Verify(cookie+"x", "state-123", domain.ProviderGoogle); err == nil {
		t.Fatal("expected tampered signature error")
	}
	now = now.Add(11 * time.Minute)
	if _, err := signer.Verify(cookie, "state-123", domain.ProviderGoogle); err == nil {
		t.Fatal("expected expired state error")
	}
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"/projects/frk/board":    "/projects/frk/board",
		"/issues/frk-1?tab=runs": "/issues/frk-1?tab=runs",
		"//evil.com":             "/",
		"https://evil.com":       "/",
		"/\\evil.com":            "/",
		"":                       "/",
	}
	for in, want := range cases {
		if got := SafeNext(in, "/"); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}
