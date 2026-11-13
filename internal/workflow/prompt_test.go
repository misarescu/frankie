package workflow

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
	p := Prompt(in, "agent/gc-1/add-search")
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
