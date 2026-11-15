package activities

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

const testFindings = `{"root_cause":"stats sum shelf rows","evidence":[{"file":"db/queries/shelf_books.sql","line":40,"note":"SUM"}],
"reproduce":"two shelves","testable":true,"test_plan":"storage test","confidence":"high"}`

// M6 (D94): an investigating step's message is parsed as bug findings and
// recorded on its stream; one that holds none says why.
func TestRecordFindings(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	run := startedRun(t, a, in)
	step := runningAgentStep(t, a, run)
	tests := []struct {
		name, result string
		ok           bool
	}{
		{"fenced JSON", "Here is what I found:\n```json\n" + testFindings + "\n```", true},
		{"no JSON", "I could not find it.", false},
		{"invalid findings", `{"root_cause":"x"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := activityEnv(a).ExecuteActivity(a.RecordFindings, FindingsRequest{RunID: run.RunID, StepRunID: step, Result: tt.result})
			if err != nil {
				t.Fatal(err)
			}
			var got FindingsResult
			_ = v.Get(&got)
			if (got.Error == "") != tt.ok || (tt.ok && (!got.Findings.Testable || got.Findings.RootCause != "stats sum shelf rows")) {
				t.Errorf("result = %+v", got)
			}
		})
	}
	events, _ := st.ListRunEvents(t.Context(), uuid.MustParse(run.RunID), 0, 100)
	var recorded []map[string]any
	for _, e := range events {
		if e.Type == domain.EventFindings {
			var p map[string]any
			_ = json.Unmarshal(e.Payload, &p)
			recorded = append(recorded, p)
		}
	}
	if len(recorded) != 3 || recorded[0]["findings"] == nil || recorded[1]["error"] == nil {
		t.Errorf("events = %v", recorded)
	}
}
