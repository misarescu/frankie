package factorye2e

import (
	"testing"

	"github.com/misarescu/frankie/internal/e2e"
)

// D132: the report reads back through the verification's own JUnit reader
// (D116): failures as failed, errors as errored, one suite per issue.
func TestJUnitReadsBack(t *testing.T) {
	r := Report{Cases: []Case{
		{Suite: "factory e2e > pr7dum-1", Name: "implement #1"},
		{Suite: "factory e2e > pr7dum-1", Name: "checks #1", Failure: "command gate checks failed on attempt 1"},
		{Suite: "factory e2e > pr7dum-1", Name: "the issue merged", Failure: "the issue ended failed: <gate> & loop"},
		{Suite: "factory e2e", Name: "the preview's API answers", Error: "connection refused"},
	}}
	b, err := r.JUnit()
	if err != nil {
		t.Fatal(err)
	}
	cases, err := e2e.ParseJUnit(b)
	if err != nil {
		t.Fatalf("ParseJUnit: %v\n%s", err, b)
	}
	want := []e2e.Case{
		{Suite: "factory e2e > pr7dum-1", Name: "implement #1", Outcome: e2e.Passed},
		{Suite: "factory e2e > pr7dum-1", Name: "checks #1", Outcome: e2e.Failed, Message: "command gate checks failed on attempt 1"},
		{Suite: "factory e2e > pr7dum-1", Name: "the issue merged", Outcome: e2e.Failed, Message: "the issue ended failed: <gate> & loop"},
		{Suite: "factory e2e", Name: "the preview's API answers", Outcome: e2e.Errored, Message: "connection refused"},
	}
	if len(cases) != len(want) {
		t.Fatalf("cases = %+v", cases)
	}
	for i := range want {
		if cases[i] != want[i] {
			t.Errorf("case %d = %+v, want %+v", i, cases[i], want[i])
		}
	}
}
