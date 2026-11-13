package activities

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/misarescu/frankie/internal/domain"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// BranchName is the issue's agent branch: agent/<issue-key>/<slug of the title> (D22).
func BranchName(issueKey, title string) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		slug = "change"
	}
	return domain.AgentBranchPrefix(issueKey) + slug
}

// SystemPrompt is appended to the harness's own system prompt.
func SystemPrompt() string {
	return `You are a coding agent in the Frankie software factory, working unattended in a sandbox.
The repository is checked out in the current directory on your own branch.
Make the change the issue asks for, keep it focused, and run the project's tests if you can.
Do not push and do not open pull requests: the factory commits, pushes and opens the pull request when you finish.
If something blocks you, explain it in your final message instead of guessing.`
}

// Prompt is the task given to the agent. continuing means the branch already
// holds an earlier attempt at the issue, which the agent should build on.
func Prompt(in IssueRunInput, branch string, continuing bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Issue %s: %s\n", in.IssueKey, in.Title)
	if in.SourceURL != "" {
		fmt.Fprintf(&b, "Source: %s\n", in.SourceURL)
	}
	fmt.Fprintf(&b, "Your branch: %s (based on %s)\n", branch, in.BaseBranch)
	if continuing {
		b.WriteString("The branch already has an earlier attempt at this issue (see git log). " +
			"Review it, keep what is right and fix what is incomplete or failing; the lint and test checks must pass.\n")
	}
	b.WriteString("\n")
	if s := strings.TrimSpace(in.Spec); s != "" {
		b.WriteString(s)
		b.WriteString("\n")
	}
	if len(in.AcceptanceCriteria) > 0 {
		b.WriteString("\nAcceptance criteria:\n")
		for _, c := range in.AcceptanceCriteria {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	b.WriteString("\nWhen you are done, summarize what you changed and how you verified it.")
	return b.String()
}

// PRBody describes the change for reviewers.
func PRBody(in IssueRunInput, s SessionResult) string {
	var b strings.Builder
	if in.SourceNumber > 0 {
		fmt.Fprintf(&b, "Closes #%d\n\n", in.SourceNumber)
	}
	fmt.Fprintf(&b, "Implemented by a Frankie agent for issue **%s**.\n\n", in.IssueKey)
	if r := strings.TrimSpace(s.Result); r != "" {
		b.WriteString("### Agent summary\n\n")
		b.WriteString(r)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Commit: `%s`\n", s.CommitSHA)
	return b.String()
}
