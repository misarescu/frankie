package activities

import (
	"testing"

	"github.com/google/uuid"
)

func TestRecordLoop(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	run := startedRun(t, a, in)
	env := activityEnv(a)
	for _, loops := range []int{1, 2, 2} {
		if _, err := env.ExecuteActivity(a.RecordLoop, run.RunID, "checks", loops); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := st.GetRun(t.Context(), uuid.MustParse(run.RunID))
	if got.LoopCounts["checks"] != 2 {
		t.Errorf("loop counts = %v", got.LoopCounts)
	}
}
