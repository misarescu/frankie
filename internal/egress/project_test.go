package egress

import (
	"strings"
	"testing"
)

// M5.2 (D80): a project adds plain hostnames; anything wider is the deployment's.
func TestValidateProjectHosts(t *testing.T) {
	if err := ValidateProjectHosts([]string{"nodejs.org", "registry.npmjs.org", "a-b.example.co.uk"}); err != nil {
		t.Errorf("valid hosts refused: %v", err)
	}
	for name, hosts := range map[string][]string{
		"wildcard":     {"*.nodejs.org"},
		"port":         {"nodejs.org:8443"},
		"url":          {"https://nodejs.org"},
		"path":         {"nodejs.org/dist"},
		"ip":           {"140.82.112.3"},
		"ipv6":         {"::1"},
		"single label": {"localhost"},
		"uppercase":    {"NodeJS.org"},
		"empty":        {""},
		"bad label":    {"-nodejs.org"},
		"numeric tld":  {"host.123"},
		"duplicate":    {"nodejs.org", "nodejs.org"},
		"credentials":  {"user@nodejs.org"},
		"too long":     {strings.Repeat(strings.Repeat("a", 60)+".", 5) + "org"},
		"too many hosts": func() []string {
			var hs []string
			for i := range MaxProjectHosts + 1 {
				hs = append(hs, "h"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+".example.org")
			}
			return hs
		}(),
	} {
		if err := ValidateProjectHosts(hosts); err == nil {
			t.Errorf("%s: %v accepted", name, hosts)
		}
	}
}
