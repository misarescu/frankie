package domain

import (
	"testing"
)

func TestValidKeyPrefix(t *testing.T) {
	for p, want := range map[string]bool{
		"frk": true, "ab": true, "a1": true, "abcdefghij": true,
		"a": false, "abcdefghijk": false, "1ab": false, "Frk": false, "fr-k": false, "fr/k": false, "": false,
	} {
		if got := ValidKeyPrefix(p); got != want {
			t.Errorf("ValidKeyPrefix(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestAgentBranches(t *testing.T) {
	key := IssueKey("frk", 42)
	if key != "frk-42" || AgentBranchPrefix(key) != "agent/frk-42/" {
		t.Fatalf("key %q, prefix %q", key, AgentBranchPrefix(key))
	}
	tests := []struct {
		branch string
		want   bool
	}{
		{"agent/frk-42/fix-login", true},
		{"agent/frk-42/a/b", true},
		{"agent/frk-42/", false},     // empty slug
		{"agent/frk-42", false},      // the namespace itself
		{"agent/frk-421/fix", false}, // another issue sharing a prefix
		{"agent/frk-4/fix", false},   // another issue
		{"main", false},
		{"refs/heads/agent/frk-42/x", false}, // caller strips refs/heads/
	}
	for _, tt := range tests {
		if got := IsAgentBranchOf(tt.branch, key); got != tt.want {
			t.Errorf("IsAgentBranchOf(%q, %q) = %v, want %v", tt.branch, key, got, tt.want)
		}
	}
}
