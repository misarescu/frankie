package activities

import (
	"strings"
	"testing"
)

func TestBranchName(t *testing.T) {
	for title, want := range map[string]string{
		"Add search to the book list":   "agent/gc-1/add-search-to-the-book-list",
		"  Fix: login (500) error!!  ":  "agent/gc-1/fix-login-500-error",
		"???":                           "agent/gc-1/change",
		strings.Repeat("very long ", 9): "agent/gc-1/very-long-very-long-very-long-very-long",
	} {
		if got := BranchName("gc-1", title); got != want {
			t.Errorf("BranchName(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestPromptAndPRBody(t *testing.T) {
	in := IssueRunInput{IssueKey: "gc-1", Title: "Add search", Spec: "Users can search.", AcceptanceCriteria: []string{"search box", "results"},
		SourceURL: "https://github.com/o/r/issues/3", SourceNumber: 3, BaseBranch: "main"}
	p := Prompt(in, "agent/gc-1/add-search", false)
	for _, want := range []string{"Issue gc-1: Add search", "agent/gc-1/add-search (based on main)", "Users can search.", "- search box", "https://github.com/o/r/issues/3"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
	body := PRBody(in, SessionResult{Result: "Added a search box.", CommitSHA: "abc"})
	if !strings.HasPrefix(body, "Closes #3") || !strings.Contains(body, "Added a search box.") || !strings.Contains(body, "`abc`") {
		t.Errorf("PR body:\n%s", body)
	}
	if !strings.Contains(SystemPrompt(), "Do not push") {
		t.Error("system prompt must tell the agent not to push")
	}
}

func TestPromptContinuingAnEarlierAttempt(t *testing.T) {
	in := IssueRunInput{IssueKey: "gc-2", Title: "Add health", BaseBranch: "main"}
	if p := Prompt(in, "agent/gc-2/add-health", false); strings.Contains(p, "earlier attempt") {
		t.Errorf("fresh step mentions an earlier attempt:\n%s", p)
	}
	p := Prompt(in, "agent/gc-2/add-health", true)
	if !strings.Contains(p, "earlier attempt") || !strings.Contains(p, "lint and test checks must pass") {
		t.Errorf("continuing prompt does not say so:\n%s", p)
	}
}

func TestStepPromptHonorsInputs(t *testing.T) {
	in := IssueRunInput{IssueKey: "gc-3", Title: "Add search", Spec: "Users can search.", AcceptanceCriteria: []string{"search box"}, BaseBranch: "main"}
	all := AgentStep{StepID: "implement", Inputs: []string{"spec", "acceptance_criteria", "diff", "feedback"},
		Feedback: "The `checks` check failed on your last commit: mise run test exited 1"}
	p := StepPrompt(in, "agent/gc-3/add-search", true, all)
	for _, want := range []string{"Issue gc-3: Add search", "Users can search.", "- search box", "git diff origin/main...HEAD",
		"earlier work on this issue", "sent back. Address every point", "mise run test exited 1"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}

	// An e2e test writer gets the spec, deliberately not the coder's diff (docs/spec/03).
	specOnly := AgentStep{StepID: "e2e-tests", Inputs: []string{"spec"}, Feedback: "ignored"}
	p = StepPrompt(in, "agent/gc-3/add-search", false, specOnly)
	for _, unwanted := range []string{"search box", "git diff", "sent back", "earlier work"} {
		if strings.Contains(p, unwanted) {
			t.Errorf("prompt contains %q though the step does not take it:\n%s", unwanted, p)
		}
	}
	if !strings.Contains(p, "Users can search.") {
		t.Errorf("prompt lacks the spec:\n%s", p)
	}
}

// A reviewer is told to end with its verdict, not a summary of changes.
func TestStepPromptForAReviewer(t *testing.T) {
	in := IssueRunInput{IssueKey: "gc-3", Title: "Add search", Spec: "Users can search.", BaseBranch: "main"}
	p := StepPrompt(in, "agent/gc-3/add-search", true, AgentStep{StepID: "code-reviewer", Contract: "review-verdict",
		Inputs: []string{"spec", "diff"}})
	if !strings.Contains(p, "git diff origin/main...HEAD") || !strings.HasSuffix(p, "must be only the JSON result your instructions describe.") ||
		strings.Contains(p, "summarize what you did") {
		t.Errorf("reviewer prompt:\n%s", p)
	}
}
