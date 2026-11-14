package opencode

import (
	"slices"
	"strings"
	"testing"
)

func TestPermissionsWithoutAListAllowAllButAskingAndSearching(t *testing.T) {
	rules, err := permissions(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []rule{{"*", "*", "allow"}, {"question", "*", "deny"}, {"websearch", "*", "deny"}}
	if !slices.Equal(rules, want) {
		t.Errorf("rules = %v, want %v", rules, want)
	}
}

func TestPermissionsWithAListDenyEverythingElse(t *testing.T) {
	rules, err := permissions([]string{"read", "edit", "write", "bash", "web-fetch"})
	if err != nil {
		t.Fatal(err)
	}
	// The catch-all comes first: the last matching rule wins, so the allows override it.
	want := []rule{{"*", "*", "deny"}, {"read", "*", "allow"}, {"edit", "*", "allow"}, {"shell", "*", "allow"}, {"webfetch", "*", "allow"}}
	if !slices.Equal(rules, want) {
		t.Errorf("rules = %v, want %v", rules, want)
	}
}

func TestPermissionsReadOnlyShell(t *testing.T) {
	rules, err := permissions([]string{"read", "bash:readonly"})
	if err != nil {
		t.Fatal(err)
	}
	if rules[0] != (rule{"*", "*", "deny"}) {
		t.Fatalf("first rule = %v", rules[0])
	}
	for _, r := range rules[1:] {
		if r.Action == "shell" && (r.Resource == "*" || !strings.HasSuffix(r.Resource, " *")) {
			t.Errorf("read-only shell rule %v allows more than a command pattern", r)
		}
	}
	for _, want := range []rule{{"shell", "git diff *", "allow"}, {"shell", "cat *", "allow"}, {"read", "*", "allow"}} {
		if !slices.Contains(rules, want) {
			t.Errorf("missing %v in %v", want, rules)
		}
	}
}

func TestPermissionsErrors(t *testing.T) {
	for name, allowed := range map[string][]string{
		"unknown tool": {"kubectl"},
		"both shells":  {"bash", "bash:readonly"},
	} {
		if _, err := permissions(allowed); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
