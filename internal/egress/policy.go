// Package egress implements the agent egress proxy (D20): a CONNECT-only
// forward proxy that allows listed hostnames and never dials non-public addresses.
package egress

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

const defaultPort = 443

type rule struct {
	host     string // lowercased; for wildcards, the suffix without "*."
	wildcard bool   // "*.example.com" matches subdomains, not example.com itself
	port     int
}

// Policy is an egress allowlist.
type Policy struct {
	rules []rule
}

// ParsePolicy parses allowlist entries. Each entry is a hostname or IP, optionally
// with a ":port" (default 443), or "*.domain" for any subdomain of domain.
func ParsePolicy(entries []string) (Policy, error) {
	var p Policy
	for _, raw := range entries {
		e := strings.ToLower(strings.TrimSpace(raw))
		if e == "" {
			continue
		}
		if strings.Contains(e, "://") || strings.ContainsAny(e, "/?#@ ") {
			return Policy{}, fmt.Errorf("egress: entry %q: want host[:port], not a URL", raw)
		}
		r := rule{port: defaultPort}
		host := e
		if h, portStr, err := net.SplitHostPort(e); err == nil {
			port, err := strconv.Atoi(portStr)
			if err != nil || port < 1 || port > 65535 {
				return Policy{}, fmt.Errorf("egress: entry %q: invalid port", raw)
			}
			host, r.port = h, port
		}
		if rest, ok := strings.CutPrefix(host, "*."); ok {
			host, r.wildcard = rest, true
		}
		host = strings.TrimSuffix(host, ".")
		if host == "" || strings.Contains(host, "*") || (r.wildcard && !strings.Contains(host, ".")) {
			return Policy{}, fmt.Errorf("egress: entry %q: invalid host", raw)
		}
		r.host = host
		p.rules = append(p.rules, r)
	}
	return p, nil
}

// Len is the number of rules.
func (p Policy) Len() int { return len(p.rules) }

// Allows reports whether a CONNECT to host:port is permitted.
func (p Policy) Allows(host string, port int) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, r := range p.rules {
		if r.port != port {
			continue
		}
		if r.wildcard {
			if strings.HasSuffix(host, "."+r.host) {
				return true
			}
		} else if host == r.host {
			return true
		}
	}
	return false
}
