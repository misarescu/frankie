package activities

import (
	"testing"
)

var (
	testInput = IssueRunInput{IssueID: "0198f2a4-1111-7000-8000-000000000001", IssueKey: "gc-1", Title: "Add search",
		Spec: "Users can search books.", Repo: "misarescu/Goodreads-Clone", BaseBranch: "main", SourceNumber: 3}
	testRun = StartRunResult{RunID: "0198f2a4-2222-7000-8000-000000000002", StepRunID: "0198f2a4-3333-7000-8000-000000000003", Branch: "agent/gc-1/add-search"}
)

func TestPodNameIsStablePerStep(t *testing.T) {
	a, b := podName(testInput, testRun), podName(testInput, testRun)
	if a != b || a != "gc-1-implement-00000003" {
		t.Errorf("podName = %q, %q", a, b)
	}
}
