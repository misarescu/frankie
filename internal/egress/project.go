package egress

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// labelRE is one DNS label (RFC 1123).
var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// MaxProjectHosts bounds how many hosts a project adds to the egress list.
const MaxProjectHosts = 50

// ValidateProjectHosts checks hosts a project adds to the platform's egress
// list (D80): plain hostnames, reached on port 443. Wildcards, ports, IP
// addresses and single-label names are the deployment's to allow, not a
// project's.
func ValidateProjectHosts(hosts []string) error {
	if len(hosts) > MaxProjectHosts {
		return fmt.Errorf("egress: %d hosts, at most %d", len(hosts), MaxProjectHosts)
	}
	var errs []error
	seen := map[string]bool{}
	for i, h := range hosts {
		if err := validProjectHost(h); err != nil {
			errs = append(errs, fmt.Errorf("egress.allow[%d] %q: %w", i, h, err))
			continue
		}
		if seen[h] {
			errs = append(errs, fmt.Errorf("egress.allow[%d] %q: listed twice", i, h))
		}
		seen[h] = true
	}
	return errors.Join(errs...)
}

func validProjectHost(h string) error {
	switch {
	case h == "":
		return errors.New("empty")
	case h != strings.ToLower(h):
		return errors.New("must be lowercase")
	case strings.ContainsAny(h, "*:/@ "):
		return errors.New("must be a plain hostname: no wildcard, port, scheme or path")
	case !strings.Contains(h, "."):
		return errors.New("must have a domain")
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return errors.New("must be a hostname, not an IP address")
	}
	if len(h) > 253 {
		return errors.New("longer than 253 characters")
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		if !labelRE.MatchString(l) {
			return fmt.Errorf("label %q is not a DNS label", l)
		}
	}
	if tld := labels[len(labels)-1]; strings.Trim(tld, "0123456789") == "" {
		return errors.New("must be a hostname, not an IP address")
	}
	return nil
}
