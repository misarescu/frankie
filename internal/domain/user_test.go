package domain

import (
	"strings"
	"testing"
	"time"
)

func TestRoleAtLeast(t *testing.T) {
	cases := []struct {
		have, min Role
		want      bool
	}{
		{RoleAdmin, RoleAdmin, true},
		{RoleAdmin, RoleMember, true},
		{RoleAdmin, RoleViewer, true},
		{RoleMember, RoleAdmin, false},
		{RoleMember, RoleMember, true},
		{RoleMember, RoleViewer, true},
		{RoleViewer, RoleAdmin, false},
		{RoleViewer, RoleMember, false},
		{RoleViewer, RoleViewer, true},
		{Role("other"), RoleViewer, false},
		{RoleAdmin, Role("other"), false},
	}
	for _, tc := range cases {
		if got := tc.have.AtLeast(tc.min); got != tc.want {
			t.Errorf("%q.AtLeast(%q) = %v, want %v", tc.have, tc.min, got, tc.want)
		}
	}
}

func TestNormalizeUsername(t *testing.T) {
	good := map[string]string{
		"Mihai":        "mihai",
		"  alice.b-1 ": "alice.b-1",
		"u_42":         "u_42",
	}
	for in, want := range good {
		got, err := NormalizeUsername(in)
		if err != nil || got != want {
			t.Errorf("NormalizeUsername(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", " ", "-lead", ".dot", "has space", "bad@email", strings.Repeat("a", 65)} {
		if _, err := NormalizeUsername(bad); err == nil {
			t.Errorf("NormalizeUsername(%q) accepted, want error", bad)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got, err := NormalizeEmail("  Alice@Example.COM "); err != nil || got != "alice@example.com" {
		t.Errorf("NormalizeEmail = %q, %v", got, err)
	}
	if got, err := NormalizeEmail(""); err != nil || got != "" {
		t.Errorf("NormalizeEmail empty = %q, %v", got, err)
	}
	for _, bad := range []string{"not-an-email", "@example.com", "a@localhost", "a@b@c.com", "a b@c.com"} {
		if _, err := NormalizeEmail(bad); err == nil {
			t.Errorf("NormalizeEmail(%q) accepted, want error", bad)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short123456"); err == nil {
		t.Error("11-char password accepted")
	}
	if err := ValidatePassword("long-enough1"); err != nil {
		t.Errorf("12-char password rejected: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("x", 129)); err == nil {
		t.Error("129-char password accepted")
	}
}

func TestUserActorName(t *testing.T) {
	if got := (User{Username: "alice", DisplayName: " Alice Smith "}).ActorName(); got != "Alice Smith" {
		t.Errorf("ActorName = %q", got)
	}
	if got := (User{Username: "alice"}).ActorName(); got != "alice" {
		t.Errorf("ActorName fallback = %q", got)
	}
}

func TestSuggestUsername(t *testing.T) {
	cases := [][3]string{
		{"MihaiS", "m@example.com", "mihais"},
		{"", "Alice.Smith+work@example.com", "alice.smith-work"},
		{"---", "", "user"},
	}
	for _, tc := range cases {
		if got := SuggestUsername(tc[0], tc[1]); got != tc[2] {
			t.Errorf("SuggestUsername(%q, %q) = %q, want %q", tc[0], tc[1], got, tc[2])
		}
	}
}

func TestInviteUsable(t *testing.T) {
	now := time.Now()
	in := Invite{ExpiresAt: now.Add(time.Hour)}
	if !in.Usable(now) {
		t.Error("future unaccepted invite should be usable")
	}
	if in.Usable(now.Add(2 * time.Hour)) {
		t.Error("expired invite should not be usable")
	}
	accepted := now
	in.AcceptedAt = &accepted
	if in.Usable(now) {
		t.Error("accepted invite should not be usable")
	}
}
