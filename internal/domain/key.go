package domain

import (
	"fmt"
	"regexp"
	"strings"
)

var keyPrefixRE = regexp.MustCompile(`^[a-z][a-z0-9]{1,9}$`)

// ValidKeyPrefix reports whether p can prefix issue keys (D22): 2-10 lowercase
// letters or digits, starting with a letter. Prefixes appear in branch names.
func ValidKeyPrefix(p string) bool {
	return keyPrefixRE.MatchString(p)
}

// IssueKey formats an issue's key, e.g. IssueKey("frk", 42) == "frk-42".
func IssueKey(prefix string, number int) string {
	return fmt.Sprintf("%s-%d", prefix, number)
}

// AgentBranchPrefix is the branch namespace an issue's agents may push to:
// "agent/<issue-key>/". The git proxy enforces it (D21).
func AgentBranchPrefix(issueKey string) string {
	return "agent/" + issueKey + "/"
}

// IsAgentBranchOf reports whether branch (without refs/heads/) is a non-empty
// branch under the issue's agent namespace.
func IsAgentBranchOf(branch, issueKey string) bool {
	rest, ok := strings.CutPrefix(branch, AgentBranchPrefix(issueKey))
	return ok && rest != ""
}
