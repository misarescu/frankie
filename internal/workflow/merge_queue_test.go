package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// queueFixture records what the merge queue asked its Activities to do.
type queueFixture struct {
	env      *testsuite.TestWorkflowEnvironment
	steps    []string // stepID#attempt
	updates  []activities.BranchUpdateRequest
	gates    []activities.GateRequest
	merges   []activities.QueueMergeRequest
	outcomes map[string]QueueOutcome // issue workflow ID -> outcome signaled
	order    []string                // issue workflow IDs in the order they were answered
	ended    map[string]bool

	state  func(issue activities.IssueRunInput) activities.QueueEntryState
	update func(n int, req activities.BranchUpdateRequest) activities.BranchUpdateResult
	// updateErr, when it returns an error, fails the update Activity.
	updateErr func(req activities.BranchUpdateRequest) error
	gate      func(req activities.GateRequest) activities.GateOutcome
	merge     func(n int, req activities.QueueMergeRequest) activities.QueueMergeResult
}

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shaM = "1111111111111111111111111111111111111111" // main
)

func queueEntry(key string, attempt int) QueueEntry {
	in := testInput
	in.IssueKey = key
	return QueueEntry{Issue: in, RunID: "run-" + key, WorkflowID: "issue-" + key, StepID: "enqueue", Attempt: attempt,
		ChangeRequestID: "cr-" + key, Number: 9, Branch: "agent/" + key + "/x", HeadSHA: shaA,
		Gates:  []QueueGate{{StepID: "checks", Commands: []string{"mise run lint", "mise run test"}}},
		Method: domain.MergeSquash, Title: key + ": Add search (#9)"}
}

func newQueueFixture(t *testing.T, opts ...func(*queueFixture)) *queueFixture {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	f := &queueFixture{env: s.NewTestWorkflowEnvironment(), outcomes: map[string]QueueOutcome{}, ended: map[string]bool{}}
	for _, o := range opts {
		o(f)
	}
	a := &activities.Activities{}
	f.env.RegisterActivity(a)
	f.env.OnActivity(a.CheckQueueEntry, mock.Anything, mock.Anything, mock.Anything).Return(func(_ context.Context, issue activities.IssueRunInput, _ int) (activities.QueueEntryState, error) {
		if f.state != nil {
			return f.state(issue), nil
		}
		return activities.QueueEntryState{Queued: true, ChangeRequest: domain.ChangeRequestOpen}, nil
	})
	f.env.OnActivity(a.StartStep, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.StepStart) (string, error) {
		f.steps = append(f.steps, fmt.Sprintf("%s#%d", in.StepID, in.Attempt))
		return fmt.Sprintf("sr-%s-%s-%d", in.RunID, in.StepID, in.Attempt), nil
	})
	f.env.OnActivity(a.FinishStep, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.StepEnd) error {
		f.ended[in.StepRunID] = in.Succeeded
		return nil
	})
	f.env.OnActivity(a.UpdateBranch, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.BranchUpdateRequest) (activities.BranchUpdateResult, error) {
		f.updates = append(f.updates, req)
		if f.updateErr != nil {
			if err := f.updateErr(req); err != nil {
				return activities.BranchUpdateResult{}, err
			}
		}
		if f.update != nil {
			return f.update(len(f.updates), req), nil
		}
		return activities.BranchUpdateResult{Outcome: activities.BranchUpToDate, HeadSHA: req.HeadSHA, BaseSHA: shaM}, nil
	})
	f.env.OnActivity(a.RunGate, mock.Anything, mock.Anything, mock.Anything).Return(func(_ context.Context, _ activities.IssueRunInput, req activities.GateRequest) (activities.GateOutcome, error) {
		f.gates = append(f.gates, req)
		if f.gate != nil {
			return f.gate(req), nil
		}
		return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true}, nil
	})
	f.env.OnActivity(a.MergeQueued, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.QueueMergeRequest) (activities.QueueMergeResult, error) {
		f.merges = append(f.merges, req)
		if f.merge != nil {
			return f.merge(len(f.merges), req), nil
		}
		return activities.QueueMergeResult{Outcome: activities.QueueMerged, MergeSHA: "merge-" + req.Issue.IssueKey}, nil
	})
	f.env.OnActivity(a.ConflictContext, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.ConflictRequest) (string, error) {
		return "### " + strings.Join(req.Files, ", ") + " at " + req.BaseSHA, nil
	})
	f.env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, "", MergeQueueOutcomeSignal, mock.Anything).Return(
		func(_, workflowID, _, _ string, arg any) error {
			out := arg.(QueueOutcome)
			f.outcomes[workflowID] = out
			f.order = append(f.order, workflowID)
			return nil
		})
	return f
}

func (f *queueFixture) run(t *testing.T, in MergeQueueInput) {
	t.Helper()
	f.env.ExecuteWorkflow(MergeQueue, in)
	if !f.env.IsWorkflowCompleted() {
		t.Fatal("queue did not complete")
	}
	if err := f.env.GetWorkflowError(); err != nil {
		t.Fatalf("queue failed: %v", err)
	}
}

// M6 (D87): change requests merge one at a time in arrival order. One already
// up to date merges at the commit its run checked; one whose branch the update
// changed passes the command gates again on the new commit first.
func TestMergeQueueMergesInOrder(t *testing.T) {
	f := newQueueFixture(t, func(f *queueFixture) {
		f.update = func(_ int, req activities.BranchUpdateRequest) activities.BranchUpdateResult {
			if req.Issue.IssueKey == "gc-2" {
				return activities.BranchUpdateResult{Outcome: activities.BranchUpdated, HeadSHA: shaB, BaseSHA: shaM}
			}
			return activities.BranchUpdateResult{Outcome: activities.BranchUpToDate, HeadSHA: req.HeadSHA, BaseSHA: shaM}
		}
	})
	// gc-1 is pending from the queue's last run; gc-2 arrives as a signal.
	f.env.RegisterDelayedCallback(func() { f.env.SignalWorkflow(MergeQueueEnqueueSignal, queueEntry("gc-2", 1)) }, 0)
	f.run(t, MergeQueueInput{Repo: "misarescu/Goodreads-Clone", Pending: []QueueEntry{queueEntry("gc-1", 1)}})

	if !slices.Equal(f.order, []string{"issue-gc-1", "issue-gc-2"}) {
		t.Fatalf("answered %v", f.order)
	}
	for _, id := range f.order {
		if o := f.outcomes[id]; !o.Merged() || o.MergeSHA != "merge-"+strings.TrimPrefix(id, "issue-") || o.StepID != "enqueue" {
			t.Errorf("%s: %+v", id, o)
		}
	}
	if len(f.gates) != 1 || f.gates[0].CommitSHA != shaB || f.gates[0].StepID != "enqueue-checks" ||
		strings.Join(f.gates[0].Commands, ";") != "mise run lint;mise run test" || f.gates[0].Branch != "agent/gc-2/x" {
		t.Errorf("gates = %+v; want only gc-2's updated commit checked", f.gates)
	}
	if len(f.merges) != 2 || f.merges[0].HeadSHA != shaA || f.merges[1].HeadSHA != shaB || f.merges[1].BaseSHA != shaM ||
		f.merges[1].Method != domain.MergeSquash || f.merges[1].Title != "gc-2: Add search (#9)" {
		t.Errorf("merges = %+v", f.merges)
	}
	if got := strings.Join(f.steps, ","); got != "enqueue-update#1,enqueue-merge#1,enqueue-update#1,enqueue-checks#1,enqueue-merge#1" {
		t.Errorf("steps = %s", got)
	}
}

// What leaves the queue without a merge, and why.
func TestMergeQueueOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		state      func(activities.IssueRunInput) activities.QueueEntryState
		update     func(int, activities.BranchUpdateRequest) activities.BranchUpdateResult
		gate       func(activities.GateRequest) activities.GateOutcome
		merge      func(int, activities.QueueMergeRequest) activities.QueueMergeResult
		want       QueueOutcome
		wantMerges int
	}{
		{
			name: "conflict goes back with the files",
			update: func(int, activities.BranchUpdateRequest) activities.BranchUpdateResult {
				return activities.BranchUpdateResult{Outcome: activities.BranchConflict, BaseSHA: shaM, Conflicts: []string{"web/App.jsx"}}
			},
			want: QueueOutcome{Kind: QueueOutcomeConflict, Conflicts: []string{"web/App.jsx"}, HeadSHA: shaA, Feedback: "### web/App.jsx at " + shaM},
		},
		{
			name: "red after the update goes back with the gate's feedback",
			update: func(int, activities.BranchUpdateRequest) activities.BranchUpdateResult {
				return activities.BranchUpdateResult{Outcome: activities.BranchUpdated, HeadSHA: shaB, BaseSHA: shaM}
			},
			gate: func(req activities.GateRequest) activities.GateOutcome {
				return activities.GateOutcome{StepRunID: req.StepRunID, Summary: "mise run test failed (exit 1)", Feedback: "FAIL TestShelves"}
			},
			want: QueueOutcome{Kind: QueueOutcomeRed, Feedback: "FAIL TestShelves", HeadSHA: shaB},
		},
		{
			name: "a branch pushed to while queued",
			update: func(int, activities.BranchUpdateRequest) activities.BranchUpdateResult {
				return activities.BranchUpdateResult{Outcome: activities.BranchMoved, HeadSHA: shaB}
			},
			want: QueueOutcome{Kind: QueueOutcomeMoved, HeadSHA: shaB},
		},
		{
			name: "a canceled issue is skipped",
			state: func(activities.IssueRunInput) activities.QueueEntryState {
				return activities.QueueEntryState{Queued: false}
			},
			want: QueueOutcome{Kind: QueueOutcomeDequeued, HeadSHA: shaA},
		},
		{
			name: "merged by hand while queued",
			state: func(activities.IssueRunInput) activities.QueueEntryState {
				return activities.QueueEntryState{Queued: true, ChangeRequest: domain.ChangeRequestMerged}
			},
			want: QueueOutcome{Kind: QueueOutcomeMerged, HeadSHA: shaA},
		},
		{
			name: "closed by hand while queued",
			state: func(activities.IssueRunInput) activities.QueueEntryState {
				return activities.QueueEntryState{Queued: true, ChangeRequest: domain.ChangeRequestClosed}
			},
			want: QueueOutcome{Kind: QueueOutcomeClosed, HeadSHA: shaA},
		},
		{
			name: "the provider refuses",
			merge: func(int, activities.QueueMergeRequest) activities.QueueMergeResult {
				return activities.QueueMergeResult{Outcome: activities.QueueRefused, Reason: "branch protection"}
			},
			want: QueueOutcome{Kind: QueueOutcomeRefused, HeadSHA: shaA}, wantMerges: 1,
		},
		{
			name: "the base keeps moving",
			merge: func(int, activities.QueueMergeRequest) activities.QueueMergeResult {
				return activities.QueueMergeResult{Outcome: activities.QueueBaseMoved}
			},
			want: QueueOutcome{Kind: QueueOutcomeBusy, HeadSHA: shaA}, wantMerges: maxMergeTries,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newQueueFixture(t, func(f *queueFixture) { f.state, f.update, f.gate, f.merge = tt.state, tt.update, tt.gate, tt.merge })
			f.run(t, MergeQueueInput{Repo: "r", Pending: []QueueEntry{queueEntry("gc-1", 1)}})
			got := f.outcomes["issue-gc-1"]
			if got.Kind != tt.want.Kind || got.Feedback != tt.want.Feedback || got.HeadSHA != tt.want.HeadSHA ||
				!slices.Equal(got.Conflicts, tt.want.Conflicts) || got.Merged() != (tt.want.Kind == QueueOutcomeMerged) {
				t.Errorf("outcome = %+v, want %+v", got, tt.want)
			}
			if got.Kind != QueueOutcomeMerged && got.Reason == "" {
				t.Error("an outcome without a merge says nothing about why")
			}
			if len(f.merges) != tt.wantMerges {
				t.Errorf("%d merges, want %d", len(f.merges), tt.wantMerges)
			}
		})
	}
}

// The base branch moved between the update and the merge: the queue takes
// it in and checks again, recording the second try as the next attempt.
func TestMergeQueueUpdatesAgainWhenTheBaseMoved(t *testing.T) {
	f := newQueueFixture(t, func(f *queueFixture) {
		f.update = func(n int, req activities.BranchUpdateRequest) activities.BranchUpdateResult {
			return activities.BranchUpdateResult{Outcome: activities.BranchUpdated, HeadSHA: fmt.Sprintf("%040d", n), BaseSHA: fmt.Sprintf("m%039d", n)}
		}
		f.merge = func(n int, _ activities.QueueMergeRequest) activities.QueueMergeResult {
			if n == 1 {
				return activities.QueueMergeResult{Outcome: activities.QueueBaseMoved}
			}
			return activities.QueueMergeResult{Outcome: activities.QueueMerged, MergeSHA: "m"}
		}
	})
	// The issue's second time in the queue: its steps' attempts follow its first.
	f.run(t, MergeQueueInput{Repo: "r", Pending: []QueueEntry{queueEntry("gc-1", 2)}})
	if o := f.outcomes["issue-gc-1"]; !o.Merged() || o.Attempt != 2 {
		t.Fatalf("outcome = %+v", o)
	}
	if len(f.updates) != 2 || f.updates[1].HeadSHA != fmt.Sprintf("%040d", 1) {
		t.Errorf("updates = %+v; the second starts from the first's push", f.updates)
	}
	if f.merges[1].HeadSHA != fmt.Sprintf("%040d", 2) || f.merges[1].BaseSHA != fmt.Sprintf("m%039d", 2) {
		t.Errorf("second merge = %+v", f.merges[1])
	}
	want := "enqueue-update#4,enqueue-checks#4,enqueue-merge#4,enqueue-update#5,enqueue-checks#5,enqueue-merge#5"
	if got := strings.Join(f.steps, ","); got != want {
		t.Errorf("steps = %s\nwant    %s", got, want)
	}
}

// An issue enqueued again replaces its waiting entry; a failing Activity ends
// the entry with an error and the queue goes on.
func TestMergeQueueReplacesAndSurvivesErrors(t *testing.T) {
	f := newQueueFixture(t, func(f *queueFixture) {
		f.updateErr = func(r activities.BranchUpdateRequest) error {
			if r.Issue.IssueKey == "gc-1" {
				return temporal.NewNonRetryableApplicationError("pod failed", "pod", errors.New("boom"))
			}
			return nil
		}
	})
	newer := queueEntry("gc-2", 2)
	newer.HeadSHA = shaB
	f.run(t, MergeQueueInput{Repo: "r", Pending: []QueueEntry{queueEntry("gc-1", 1), queueEntry("gc-2", 1)}})
	if o := f.outcomes["issue-gc-1"]; o.Kind != QueueOutcomeError || !strings.Contains(o.Reason, "pod failed") {
		t.Errorf("gc-1 = %+v", o)
	}
	if o := f.outcomes["issue-gc-2"]; !o.Merged() {
		t.Errorf("gc-2 = %+v; an error must not stop the queue", o)
	}
	if len(f.ended) != 1 {
		t.Errorf("ended %v; the failed update's step must be ended", f.ended)
	}

	g := newQueueFixture(t)
	g.env.RegisterDelayedCallback(func() { g.env.SignalWorkflow(MergeQueueEnqueueSignal, newer) }, 0)
	g.run(t, MergeQueueInput{Repo: "r", Pending: []QueueEntry{queueEntry("gc-1", 1), queueEntry("gc-2", 1)}})
	if o := g.outcomes["issue-gc-2"]; o.Attempt != 2 || len(g.order) != 2 {
		t.Errorf("gc-2 answered %d times, last %+v; the newer entry replaces the older", len(g.order), o)
	}
}

// A long queue continues as new, carrying what is still pending.
func TestMergeQueueContinuesAsNew(t *testing.T) {
	f := newQueueFixture(t)
	var pending []QueueEntry
	for i := range maxQueueEntriesPerRun + 2 {
		pending = append(pending, queueEntry(fmt.Sprintf("gc-%d", i+1), 1))
	}
	f.env.ExecuteWorkflow(MergeQueue, MergeQueueInput{Repo: "r", Pending: pending})
	var cont *workflow.ContinueAsNewError
	if err := f.env.GetWorkflowError(); !errors.As(err, &cont) {
		t.Fatalf("error = %v, want continue-as-new", err)
	}
	if len(f.order) != maxQueueEntriesPerRun {
		t.Errorf("answered %d before continuing", len(f.order))
	}
}

// Recorded histories (record_test.go) keep replaying as the queue changes.
func TestMergeQueueReplaysRecordedHistories(t *testing.T) {
	r := worker.NewWorkflowReplayer()
	r.RegisterWorkflow(MergeQueue)
	for _, f := range []string{"merge_queue_base_moved_then_merged.json", "merge_queue_conflict.json"} {
		if err := r.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/"+f); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
}
