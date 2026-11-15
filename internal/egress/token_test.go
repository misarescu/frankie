package egress

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// M5.2 (D80): an egress token carries its project's hosts, signed, for a while.
func TestSignerRoundTrip(t *testing.T) {
	s := testSigner(t, "k")
	tok, err := s.Issue(Grant{Repo: "misarescu/Goodreads-Clone", Hosts: []string{"nodejs.org", "registry.npmjs.org"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if g.Repo != "misarescu/Goodreads-Clone" || !slices.Equal(g.Hosts, []string{"nodejs.org", "registry.npmjs.org"}) {
		t.Errorf("grant = %+v", g)
	}
}

func TestSignerRefuses(t *testing.T) {
	s := testSigner(t, "k")
	tok, err := s.Issue(Grant{Repo: "o/r", Hosts: []string{"nodejs.org"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	later := testSigner(t, "k")
	later.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	for name, tt := range map[string]struct {
		signer *Signer
		token  string
		want   error
	}{
		"other key":   {testSigner(t, "x"), tok, ErrInvalidToken},
		"tampered":    {s, parts[0] + "." + parts[1] + "x." + parts[2], ErrInvalidToken},
		"git token":   {s, "fgp1." + parts[1] + "." + parts[2], ErrInvalidToken},
		"not a token": {s, "secret", ErrInvalidToken},
		"expired":     {later, tok, ErrExpiredToken},
	} {
		if _, err := tt.signer.Verify(tt.token); !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tt.want)
		}
	}
	if _, err := NewSigner([]byte("short")); err == nil {
		t.Error("a short key was accepted")
	}
	for name, g := range map[string]Grant{
		"no hosts": {Repo: "o/r"},
		"no repo":  {Hosts: []string{"nodejs.org"}},
		"bad host": {Repo: "o/r", Hosts: []string{"*.nodejs.org"}},
	} {
		if _, err := s.Issue(g, time.Hour); err == nil {
			t.Errorf("%s: issued", name)
		}
	}
}
