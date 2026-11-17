package e2e

import (
	"regexp"
	"strings"
)

// IssueKeys returns the issue keys with prefix (e.g. "gc") that appear in
// texts as whole words, lowercased and in order of first appearance: "gc-65"
// in "[GC-65] search by tag" or "@gc-65", but not in "gc-650" or "abc-65".
func IssueKeys(prefix string, texts ...string) []string {
	if prefix == "" {
		return nil
	}
	re := regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(` + regexp.QuoteMeta(prefix) + `-[0-9]+)(?:$|[^a-z0-9-])`)
	var out []string
	seen := map[string]bool{}
	for _, t := range texts {
		for _, m := range re.FindAllStringSubmatch(t, -1) {
			k := strings.ToLower(m[1])
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}
