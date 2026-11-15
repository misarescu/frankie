package activities

import (
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/egress"
)

func egressSigner(t *testing.T) *egress.Signer {
	t.Helper()
	s, err := egress.NewSigner([]byte(strings.Repeat("e", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// egressGrant reads the grant a pod's proxy URL carries; ok is false without one.
func egressGrant(t *testing.T, s *egress.Signer, proxy string) (egress.Grant, bool) {
	t.Helper()
	u, err := url.Parse(proxy)
	if err != nil {
		t.Fatal(err)
	}
	pass, ok := u.User.Password()
	if !ok {
		return egress.Grant{}, false
	}
	g, err := s.Verify(pass)
	if err != nil {
		t.Fatalf("token in %s: %v", proxy, err)
	}
	return g, true
}

// M5.2 (D80): a run's pods carry a token for the hosts its project adds;
// without any, or where the deployment signs none, the platform's list.
func TestEgressProxyURL(t *testing.T) {
	s := egressSigner(t)
	const base = "http://frankie-egress-proxy.frankie.svc:3128"
	with := testInput
	with.EgressAllow = []string{"nodejs.org"}

	a := &Activities{Agent: AgentConfig{EgressProxyURL: base}, Egress: s, Log: quietLog()}
	got, err := a.egressProxyURL(with, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := egressGrant(t, s, got)
	if !ok || g.Repo != with.Repo || !slices.Equal(g.Hosts, with.EgressAllow) || !strings.HasSuffix(got, "@frankie-egress-proxy.frankie.svc:3128") {
		t.Errorf("proxy = %s, grant %+v", got, g)
	}

	if got, _ := a.egressProxyURL(testInput, time.Hour); got != base {
		t.Errorf("no project hosts: proxy = %s, want %s", got, base)
	}
	a.Egress = nil
	if got, _ := a.egressProxyURL(with, time.Hour); got != base {
		t.Errorf("no signer: proxy = %s, want %s", got, base)
	}
}
