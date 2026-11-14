package activities

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/store/storetest"
)

// draftIssue is a draft issue to refine, and the run input for it.
func draftIssue(t *testing.T) (*store.Store, IssueRunInput) {
	t.Helper()
	st := storetest.New(t)
	p, err := st.CreateProject(t.Context(), "misarescu/Goodreads-Clone", "gc")
	if err != nil {
		t.Fatal(err)
	}
	is, err := st.CreateIssue(t.Context(), store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "Add search",
		Status: domain.IssueDraft, CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return st, IssueRunInput{IssueID: is.ID.String(), IssueKey: is.Key, Title: is.Title, Repo: p.Name, BaseBranch: "main"}
}

func startRefinement(t *testing.T, a *Activities, in IssueRunInput) StartRunResult {
	t.Helper()
	snap := snapshot.Snapshot{Source: "built-in", BaseSHA: "abc"}
	snap.Workflow.Name, snap.Workflow.Version = snapshot.RefinementName, 1
	v, err := activityEnv(a).ExecuteActivity(a.StartRefinement, in, snap)
	if err != nil {
		t.Fatal(err)
	}
	var run StartRunResult
	_ = v.Get(&run)
	return run
}

func finishRefinement(t *testing.T, a *Activities, out RefinementOutcome) string {
	t.Helper()
	v, err := activityEnv(a).ExecuteActivity(a.FinishRefinement, out)
	if err != nil {
		t.Fatal(err)
	}
	var reason string
	_ = v.Get(&reason)
	return reason
}

// D66: a refinement runs as a run of the issue, draft -> refining, and a valid
// refined spec waits on the issue for approval.
func TestRefinementSucceeds(t *testing.T) {
	st, in := draftIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	run := startRefinement(t, a, in)
	r, err := st.GetRun(t.Context(), uuid.MustParse(run.RunID))
	if err != nil || r.WorkflowVersion != "refine@1" || r.Status != domain.ExecRunning {
		t.Fatalf("run = %+v, %v", r, err)
	}
	is, _ := st.GetIssueByKey(t.Context(), in.IssueKey)
	if is.Status != domain.IssueRefining {
		t.Fatalf("issue = %s, want refining", is.Status)
	}

	result := `{"spec":"Add a search box.","acceptance_criteria":["typing filters the list"]}`
	if reason := finishRefinement(t, a, RefinementOutcome{IssueID: in.IssueID, RunID: run.RunID, Succeeded: true, Result: result}); reason != "" {
		t.Fatalf("reason = %q", reason)
	}
	is, _ = st.GetIssueByKey(t.Context(), in.IssueKey)
	if is.Status != domain.IssueRefining || is.SpecProposal == nil || is.SpecProposal.Spec != "Add a search box." {
		t.Fatalf("issue = %s, proposal %+v", is.Status, is.SpecProposal)
	}
}

// A result outside the contract, or a failed session, fails the run and holds the draft.
func TestRefinementFails(t *testing.T) {
	for name, out := range map[string]RefinementOutcome{
		"not a refined spec": {Succeeded: true, Result: "I could not decide."},
		"session failed":     {Reason: "agent session failed: budget used up"},
	} {
		t.Run(name, func(t *testing.T) {
			st, in := draftIssue(t)
			a := &Activities{Store: st, Log: quietLog()}
			run := startRefinement(t, a, in)
			out.IssueID, out.RunID = in.IssueID, run.RunID
			reason := finishRefinement(t, a, out)
			if reason == "" {
				t.Fatal("no reason")
			}
			is, _ := st.GetIssueByKey(t.Context(), in.IssueKey)
			if is.Status != domain.IssueDraft || is.SpecRejectedAt == nil || !strings.Contains(is.SpecFeedback, reason) {
				t.Errorf("issue = %s, held %v, feedback %q", is.Status, is.SpecRejectedAt, is.SpecFeedback)
			}
			events, _ := st.ListRunEvents(t.Context(), uuid.MustParse(run.RunID), 0, 100)
			var last map[string]string
			for _, e := range events {
				if e.Type == domain.EventError {
					_ = json.Unmarshal(e.Payload, &last)
				}
			}
			if last["detail"] != reason {
				t.Errorf("transcript error = %v, want %q", last, reason)
			}
		})
	}
}

// The refiner sees why its last spec was sent back.
func TestStepPromptCarriesSpecFeedback(t *testing.T) {
	in := testInput
	in.SpecFeedback = "search on the server (ana)"
	p := StepPrompt(in, "agent/gc-1/x", false, AgentStep{StepID: "refine", Contract: "refined-spec"}, nil)
	if !strings.Contains(p, "sent back") || !strings.Contains(p, "search on the server (ana)") {
		t.Errorf("prompt = %s", p)
	}
	if strings.Contains(StepPrompt(testInput, "agent/gc-1/x", false, AgentStep{StepID: "implement"}, nil), "sent back. Address this") {
		t.Error("a prompt without feedback mentions it")
	}
}
