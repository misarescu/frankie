package activities

import (
	"fmt"
	"net/url"
	"time"

	"github.com/misarescu/frankie/internal/egress"
)

// egressTokenUser is the user name in a pod's proxy URL; the proxy reads only
// the password, the egress token.
const egressTokenUser = "frankie"

// egressProxyURL is the proxy URL a pod of the run uses for ttl: the
// platform's, with an egress token for the hosts the project adds (D80) when
// it adds any. Clients send the URL's credentials as Proxy-Authorization.
func (a *Activities) egressProxyURL(in IssueRunInput, ttl time.Duration) (string, error) {
	base := a.Agent.EgressProxyURL
	if len(in.EgressAllow) == 0 {
		return base, nil
	}
	if a.Egress == nil {
		// Runs start only where project hosts are allowed; one recorded before
		// a deployment stopped allowing them keeps the platform's list.
		a.Log.Warn("project egress hosts ignored: this deployment issues no egress tokens", "issue", in.IssueKey)
		return base, nil
	}
	token, err := a.Egress.Issue(egress.Grant{Repo: in.Repo, Hosts: in.EgressAllow}, ttl)
	if err != nil {
		return "", fmt.Errorf("egress token: %w", err)
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("egress proxy url: %w", err)
	}
	u.User = url.UserPassword(egressTokenUser, token)
	return u.String(), nil
}
