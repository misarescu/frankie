package gitproxy

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func TestTokenRoundTrip(t *testing.T) {
	s, err := NewSigner(testKey)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.Issue(Grant{Repo: "misarescu/toy", IssueKey: "frk-42", StepRunID: "step-1"}, time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !strings.HasPrefix(tok, "fgp1.") {
		t.Errorf("token %q lacks version prefix", tok)
	}
	g, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if g.Repo != "misarescu/toy" || g.IssueKey != "frk-42" || g.StepRunID != "step-1" || time.Until(g.Expires) < 59*time.Minute {
		t.Errorf("grant = %+v", g)
	}
}

func TestTokenExpiry(t *testing.T) {
	s, _ := NewSigner(testKey)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	tok, err := s.Issue(Grant{Repo: "r", IssueKey: "frk-1"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Second)
	if _, err := s.Verify(tok); err != nil {
		t.Fatalf("before expiry: %v", err)
	}
	now = now.Add(time.Second)
	if _, err := s.Verify(tok); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("at expiry: err = %v, want ErrExpiredToken", err)
	}
}

func TestTokenRejectsForgery(t *testing.T) {
	s, _ := NewSigner(testKey)
	tok, _ := s.Issue(Grant{Repo: "misarescu/toy", IssueKey: "frk-42"}, time.Hour)
	other, _ := NewSigner([]byte("another key that is long enough!!"))
	forged, _ := other.Issue(Grant{Repo: "misarescu/toy", IssueKey: "frk-42"}, time.Hour)
	widened, _ := other.Issue(Grant{Repo: "misarescu/toy", IssueKey: "frk-43"}, time.Hour)
	parts := strings.Split(tok, ".")

	// An attacker swaps in a different grant but keeps the original signature.
	otherPayload := strings.Split(widened, ".")[1]
	if otherPayload == parts[1] {
		t.Fatal("test setup: swapped payload must differ from the original")
	}

	for name, bad := range map[string]string{
		"empty":            "",
		"garbage":          "not-a-token",
		"wrong version":    "fgp2." + parts[1] + "." + parts[2],
		"wrong key":        forged,
		"swapped payload":  parts[0] + "." + otherPayload + "." + parts[2],
		"truncated sig":    parts[0] + "." + parts[1] + "." + parts[2][:10],
		"extra part":       tok + ".x",
		"non-base64 sig":   parts[0] + "." + parts[1] + ".!!!",
		"missing sig part": parts[0] + "." + parts[1],
	} {
		if _, err := s.Verify(bad); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: err = %v, want ErrInvalidToken", name, err)
		}
	}
}

func TestSignerValidation(t *testing.T) {
	if _, err := NewSigner([]byte("short")); err == nil {
		t.Error("NewSigner accepted a short key")
	}
	s, _ := NewSigner(testKey)
	for _, g := range []Grant{{IssueKey: "frk-1"}, {Repo: "r"}} {
		if _, err := s.Issue(g, time.Hour); err == nil {
			t.Errorf("Issue accepted incomplete grant %+v", g)
		}
	}
	if _, err := s.Issue(Grant{Repo: "r", IssueKey: "frk-1"}, 0); err == nil {
		t.Error("Issue accepted zero ttl")
	}
}
