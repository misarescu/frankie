package activities

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/snapshot"
)

// The run records the snapshot it executes (invariant 4) and is idempotent per
// Temporal execution.
func TestStartWorkflowRun(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	env := activityEnv(a)
	snap := snapshot.Snapshot{Source: "built-in", BaseSHA: "abc"}
	snap.Workflow.Name, snap.Workflow.Version = "fast", 3

	var first, again StartRunResult
	v, err := env.ExecuteActivity(a.StartWorkflowRun, in, snap)
	if err != nil {
		t.Fatal(err)
	}
	_ = v.Get(&first)
	if first.StepRunID != "" || first.Branch != "agent/gc-1/add-search" {
		t.Errorf("result = %+v", first)
	}
	run, err := st.GetRun(t.Context(), uuid.MustParse(first.RunID))
	if err != nil {
		t.Fatal(err)
	}
	var recorded snapshot.Snapshot
	if err := json.Unmarshal(run.WorkflowSnapshot, &recorded); err != nil {
		t.Fatal(err)
	}
	if run.WorkflowVersion != "fast@3" || recorded.BaseSHA != "abc" || run.Status != domain.ExecRunning {
		t.Errorf("run = %+v, snapshot %+v", run, recorded)
	}
	if steps, _ := st.ListStepRuns(t.Context(), run.ID); len(steps) != 0 {
		t.Errorf("steps are created as they run, found %d", len(steps))
	}
	issue, _ := st.GetIssueByKey(t.Context(), in.IssueKey)
	if issue.Status != domain.IssueRunning {
		t.Errorf("issue = %s", issue.Status)
	}

	v, err = env.ExecuteActivity(a.StartWorkflowRun, in, snap)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	_ = v.Get(&again)
	if again != first {
		t.Errorf("retry = %+v, want %+v", again, first)
	}
}

func TestStartWorkflowRunRecordsAnInvalidConfiguration(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	var out StartRunResult
	v, err := activityEnv(a).ExecuteActivity(a.StartWorkflowRun, in, snapshot.Snapshot{Error: "bad"})
	if err != nil {
		t.Fatal(err)
	}
	_ = v.Get(&out)
	run, _ := st.GetRun(t.Context(), uuid.MustParse(out.RunID))
	if run.WorkflowVersion != "invalid" {
		t.Errorf("version = %q", run.WorkflowVersion)
	}
}
