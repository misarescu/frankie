package activities

import (
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

// StartRun is idempotent per Temporal execution: a retry gets the same run.
func TestStartRun(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	env := activityEnv(a)

	var first, again StartRunResult
	v, err := env.ExecuteActivity(a.StartRun, in)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := v.Get(&first); err != nil {
		t.Fatal(err)
	}
	if first.Branch != "agent/gc-1/add-search" {
		t.Errorf("branch = %q", first.Branch)
	}
	issue, _ := st.GetIssueByKey(t.Context(), "gc-1")
	run, _ := st.GetRun(t.Context(), uuid.MustParse(first.RunID))
	if issue.Status != domain.IssueRunning || run.Status != domain.ExecRunning || run.TemporalRunID == "" {
		t.Errorf("issue %s, run %s (temporal run id %q)", issue.Status, run.Status, run.TemporalRunID)
	}

	v, err = env.ExecuteActivity(a.StartRun, in)
	if err != nil {
		t.Fatalf("retried StartRun: %v", err)
	}
	if err := v.Get(&again); err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("retry = %+v, want %+v", again, first)
	}
}
