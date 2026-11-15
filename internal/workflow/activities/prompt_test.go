package activities

import (
	"fmt"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
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
	body := PRBody(in, SessionResult{Result: "Added a search box.", CommitSHA: "abc"}, "")
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
	p := StepPrompt(in, "agent/gc-3/add-search", true, all, nil)
	for _, want := range []string{"Issue gc-3: Add search", "Users can search.", "- search box", "git diff origin/main...HEAD",
		"earlier work on this issue", "sent back. Address every point", "mise run test exited 1"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}

	// An e2e test writer gets the spec, deliberately not the coder's diff (docs/spec/03).
	specOnly := AgentStep{StepID: "e2e-tests", Inputs: []string{"spec"}, Feedback: "ignored"}
	p = StepPrompt(in, "agent/gc-3/add-search", false, specOnly, nil)
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
		Inputs: []string{"spec", "diff"}}, nil)
	if !strings.Contains(p, "git diff origin/main...HEAD") || !strings.HasSuffix(p, "must be only the JSON result your instructions describe.") ||
		strings.Contains(p, "summarize what you did") {
		t.Errorf("reviewer prompt:\n%s", p)
	}
}

// D56: every step, a reviewer and a step that takes only the spec included,
// gets what people answered about the issue as part of it, latest last.
func TestStepPromptCarriesAnswers(t *testing.T) {
	in := IssueRunInput{IssueKey: "gc-6", Title: "Add a count", Spec: "Count the books.", BaseBranch: "main"}
	answers := []domain.Question{
		{Text: "Which JSON field name?", Answer: " book_count "},
		{Text: "Which status code?", Answer: "200"},
	}
	for _, s := range []AgentStep{
		{StepID: "implement", Inputs: []string{"spec"}},
		{StepID: "code-reviewer", Contract: "review-verdict", Inputs: []string{"diff"}},
	} {
		p := StepPrompt(in, "agent/gc-6/add-a-count", true, s, answers)
		for _, want := range []string{"decided by a person", "do not ask them again", "Q: Which JSON field name?\nA: book_count\n", "Q: Which status code?\nA: 200"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s prompt missing %q:\n%s", s.StepID, want, p)
			}
		}
		if strings.Index(p, "book_count") > strings.Index(p, "Which status code?") {
			t.Errorf("%s prompt does not keep the answers in order:\n%s", s.StepID, p)
		}
	}
	if p := StepPrompt(in, "agent/gc-6/add-a-count", false, AgentStep{StepID: "implement"}, nil); strings.Contains(p, "People answered") {
		t.Errorf("prompt without answers mentions them:\n%s", p)
	}

	many := make([]domain.Question, maxAnswers+5)
	for i := range many {
		many[i] = domain.Question{Text: fmt.Sprintf("question %d?", i), Answer: "yes"}
	}
	p := StepPrompt(in, "agent/gc-6/add-a-count", false, AgentStep{StepID: "implement"}, many)
	if strings.Contains(p, "Q: question 4?") || !strings.Contains(p, "Q: question 5?") || !strings.Contains(p, fmt.Sprintf("Q: question %d?", maxAnswers+4)) {
		t.Errorf("prompt does not keep only the latest %d answers:\n%s", maxAnswers, p)
	}
}

// M5.1: refinement gets its dependencies: status, change requests and spec, and
// is told to read an unmerged one's branch instead of calling the issue blocked.
func TestStepPromptCarriesDependencies(t *testing.T) {
	in := IssueRunInput{IssueKey: "gc-10", Title: "Document the count", Spec: "Write docs.", BaseBranch: "main",
		Dependencies: []IssueDependency{
			{Key: "gc-8", Title: "Add the count endpoint", Status: "awaiting_human", Spec: "GET /books/count returns {count}.",
				ChangeRequests: []DependencyChange{{URL: "https://github.com/o/r/pull/26", Branch: "agent/gc-8/add-count", Status: "open"}}},
			{Key: "gc-5", Title: "Base layout", Status: "merged"},
		}}
	p := StepPrompt(in, "agent/gc-10/document", false, AgentStep{StepID: "refine", Contract: "refined-spec", Inputs: []string{"spec"}}, nil)
	for _, want := range []string{
		"depends on the issues below", "git fetch origin <branch>", "rather than calling this issue blocked",
		"gc-8: Add the count endpoint (awaiting human)", "Change request https://github.com/o/r/pull/26 (open), branch agent/gc-8/add-count",
		"GET /books/count returns {count}.", "gc-5: Base layout (merged)",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Index(p, "Write docs.") > strings.Index(p, "depends on the issues below") {
		t.Errorf("dependencies come before the issue's own spec:\n%s", p)
	}
	if p := StepPrompt(IssueRunInput{IssueKey: "gc-3", BaseBranch: "main"}, "b", false, AgentStep{StepID: "implement", Inputs: []string{"spec"}}, nil); strings.Contains(p, "depends on") {
		t.Errorf("a run without dependencies mentions them:\n%s", p)
	}
}

// M6 (D94): steps that take the findings input get what the investigation
// found, and the change request describes it.
func TestFindingsInPromptAndPRBody(t *testing.T) {
	in := IssueRunInput{IssueKey: "gc-79", Title: "User stats count a finished book twice", BaseBranch: "main", SourceNumber: 79}
	findings := "**Root cause** (high confidence): stats sum shelf rows\n"
	step := AgentStep{StepID: "reproduce", Inputs: []string{"spec", "findings"}, Findings: findings}
	if p := StepPrompt(in, "agent/gc-79/x", false, step, nil); !strings.Contains(p, "What the investigation of this bug found:\n\n**Root cause**") {
		t.Errorf("prompt lacks the findings:\n%s", p)
	}
	step.Inputs = []string{"spec"}
	if p := StepPrompt(in, "agent/gc-79/x", false, step, nil); strings.Contains(p, "Root cause") {
		t.Errorf("a step without the findings input got them:\n%s", p)
	}
	body := PRBody(in, SessionResult{Result: "Fixed.", CommitSHA: "abc"}, findings)
	if !strings.Contains(body, "### What the investigation found\n\n**Root cause**") || !strings.HasPrefix(body, "Closes #79") {
		t.Errorf("body = %s", body)
	}
}
