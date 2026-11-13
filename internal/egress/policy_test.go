package egress

import "testing"

func TestParsePolicy(t *testing.T) {
	p, err := ParsePolicy([]string{" proxy.golang.org ", "*.githubusercontent.com", "Registry.Example:8443", "", "203.0.113.7", "trailing.dot."})
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}
	if p.Len() != 5 {
		t.Fatalf("Len = %d, want 5", p.Len())
	}
	for _, bad := range []string{"https://github.com", "github.com/path", "*", "*.com", "a*.example.com", "host:0", "host:99999", "host:abc", "user@host"} {
		if _, err := ParsePolicy([]string{bad}); err == nil {
			t.Errorf("ParsePolicy accepted %q", bad)
		}
	}
}

func TestPolicyAllows(t *testing.T) {
	p, err := ParsePolicy([]string{"proxy.golang.org", "*.githubusercontent.com", "registry.example:8443", "203.0.113.7", "trailing.dot."})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		host string
		port int
		want bool
	}{
		{"proxy.golang.org", 443, true},
		{"PROXY.golang.org.", 443, true},             // case and trailing dot
		{"proxy.golang.org", 80, false},              // default port is 443 only
		{"sum.golang.org", 443, false},               // not listed
		{"evilproxy.golang.org", 443, false},         // exact entries do not match subdomains
		{"objects.githubusercontent.com", 443, true}, // wildcard subdomain
		{"a.b.githubusercontent.com", 443, true},     // deeper subdomain
		{"githubusercontent.com", 443, false},        // wildcard excludes the apex
		{"evilgithubusercontent.com", 443, false},    // suffix must be a label boundary
		{"registry.example", 8443, true},             // explicit port
		{"registry.example", 443, false},             // only that port
		{"203.0.113.7", 443, true},                   // listed IP literal
		{"203.0.113.8", 443, false},                  // unlisted IP literal
		{"trailing.dot", 443, true},
	}
	for _, tt := range tests {
		if got := p.Allows(tt.host, tt.port); got != tt.want {
			t.Errorf("Allows(%q, %d) = %v, want %v", tt.host, tt.port, got, tt.want)
		}
	}
}
