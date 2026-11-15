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

// nextRefinement starts another refinement of the issue as StartRefinement
// does; the test activity environment has one workflow execution, on which
// StartRefinement would find the first run again.
func nextRefinement(t *testing.T, st *store.Store, in IssueRunInput) StartRunResult {
	t.Helper()
	ctx, issueID := t.Context(), uuid.MustParse(in.IssueID)
	id := uuid.NewString()
	r, err := st.CreateRun(ctx, store.NewRun{IssueID: issueID, WorkflowSnapshot: []byte("{}"), WorkflowVersion: "refine@1",
		TemporalWorkflowID: "refine-" + in.IssueKey, TemporalRunID: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionIssue(ctx, issueID, domain.IssueDraft, domain.IssueRefining); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionRun(ctx, r.ID, domain.ExecPending, domain.ExecRunning); err != nil {
		t.Fatal(err)
	}
	return StartRunResult{RunID: r.ID.String()}
}

// refinerStarted records that the refinement's agent got a running pod.
func refinerStarted(t *testing.T, st *store.Store, run StartRunResult) {
	t.Helper()
	step, err := st.CreateStepRun(t.Context(), store.NewStepRun{RunID: uuid.MustParse(run.RunID), StepID: "refine", Attempt: 1, Kind: domain.StepAgent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetStepRunPod(t.Context(), step.ID, "gc-1-refine-00000001"); err != nil {
		t.Fatal(err)
	}
}

// A result outside the contract, or a session that failed once the refiner
// started, fails the run and holds the draft.
func TestRefinementFails(t *testing.T) {
	for name, out := range map[string]RefinementOutcome{
		"not a refined spec": {Succeeded: true, Result: "I could not decide."},
		"session failed":     {Reason: "agent session failed: budget used up"},
	} {
		t.Run(name, func(t *testing.T) {
			st, in := draftIssue(t)
			a := &Activities{Store: st, Log: quietLog()}
			run := startRefinement(t, a, in)
			refinerStarted(t, st, run)
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

// M5.2 (D79): a refinement that fails before its refiner starts (image build,
// pod) leaves the draft unheld, so the scheduler starts it again, until three
// in a row have failed that way.
func TestRefinementFailingBeforeTheRefinerStarts(t *testing.T) {
	st, in := draftIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	if _, err := st.RequestIssueStart(t.Context(), []uuid.UUID{uuid.MustParse(in.IssueID)}, "ana"); err != nil {
		t.Fatal(err)
	}
	const why = "agent step failed: toolchain image: build job tc-1 failed"
	for i := 1; i <= maxEarlyRefinementFailures; i++ {
		run := nextRefinement(t, st, in)
		reason := finishRefinement(t, a, RefinementOutcome{IssueID: in.IssueID, RunID: run.RunID, Reason: why})
		is, _ := st.GetIssueByKey(t.Context(), in.IssueKey)
		if is.Status != domain.IssueDraft || is.StartRequestedAt == nil {
			t.Fatalf("refinement %d: issue %s, requested %v; want a requested draft", i, is.Status, is.StartRequestedAt)
		}
		held := is.SpecRejectedAt != nil
		if last := i == maxEarlyRefinementFailures; held != last {
			t.Fatalf("refinement %d: held = %v, want %v (feedback %q)", i, held, last, is.SpecFeedback)
		}
		if !held {
			if is.SpecFeedback != "" {
				t.Errorf("refinement %d: feedback %q, want none for the next refiner", i, is.SpecFeedback)
			}
			continue
		}
		if !strings.Contains(reason, "3 refinements in a row") || !strings.Contains(is.SpecFeedback, why) {
			t.Errorf("held with reason %q, feedback %q", reason, is.SpecFeedback)
		}
	}
}

// A refinement whose refiner started breaks a run of early failures.
func TestRefinementEarlyFailuresCountOnlyInARow(t *testing.T) {
	st, in := draftIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	early := RefinementOutcome{IssueID: in.IssueID, Reason: "agent step failed: launch agent pod: timeout"}

	run := nextRefinement(t, st, in)
	early.RunID = run.RunID
	finishRefinement(t, a, early)
	run = nextRefinement(t, st, in)
	refinerStarted(t, st, run)
	finishRefinement(t, a, RefinementOutcome{IssueID: in.IssueID, RunID: run.RunID, Reason: "agent session failed: budget used up"})
	// A person asks again, lifting the hold.
	if _, err := st.RequestIssueStart(t.Context(), []uuid.UUID{uuid.MustParse(in.IssueID)}, "ana"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		run = nextRefinement(t, st, in)
		early.RunID = run.RunID
		finishRefinement(t, a, early)
		if is, _ := st.GetIssueByKey(t.Context(), in.IssueKey); is.SpecRejectedAt != nil {
			t.Fatalf("held after %q; the refiner ran in between", is.SpecFeedback)
		}
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
