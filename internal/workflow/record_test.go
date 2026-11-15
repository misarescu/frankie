//go:build record

// Records Interpret histories for the replay tests. Not part of the normal
// suite: it needs a Temporal dev server. Run it with
//
//	temporal server start-dev --headless --port 7266
//	FRANKIE_RECORD_TEMPORAL=localhost:7266 mise exec -- go test -tags record ./internal/workflow -run TestRecordInterpretHistories
//
// It runs Interpret against stub Activities registered under the real
// Activities' names and writes each history to testdata/ as the
// `temporal workflow show --output json` format the replayer reads. Re-record
// only for a new scenario; existing fixtures must keep replaying unchanged.
package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/guard"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// stubActivities answer for the Activities in a recording; failGate decides
// whether a gate execution fails.
type stubActivities struct {
	failGate   func(req activities.GateRequest) bool
	failReview func(req activities.VerdictGateRequest) bool
	pushes     int
	opened     chan string // step run IDs of opened human gates
	protected  bool        // the change touches a protected path
	// started receives the step run ID of each coder session when set; the
	// session then runs until an abort reaches it.
	started chan string
	abort   chan struct{}
	// answered, when set with started, makes the first coder session wait
	// for an answer to its question instead of an abort, then succeed.
	answered chan struct{}
	// enqueued receives each enqueue request, for the recording to answer
	// with the queue's outcome.
	enqueued chan activities.EnqueueRequest
	// conflict makes the queue's branch update conflict.
	conflict bool
	// ciFails makes the repo's CI fail on the commit the queue would merge.
	ciFails bool
}

func (s *stubActivities) AwaitProviderChecks(_ context.Context, req activities.ProviderChecksRequest) (activities.GateOutcome, error) {
	if s.ciFails {
		return activities.GateOutcome{StepRunID: req.StepRunID, Summary: "the repo's CI failed: test", Feedback: "- test: 2 tests failed\n"}, nil
	}
	return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true, Summary: "the repo's CI passed (2 checks)"}, nil
}

func (s *stubActivities) LeaveQueue(context.Context, string) error { return nil }

func (s *stubActivities) ConflictContext(_ context.Context, req activities.ConflictRequest) (string, error) {
	return "### " + req.Files[0] + "\n\nChanged on main since your branch forked:\n```diff\n+import Shelves\n```\n", nil
}

func (s *stubActivities) EnqueueChangeRequest(_ context.Context, req activities.EnqueueRequest) error {
	s.enqueued <- req
	return nil
}

func (s *stubActivities) InterveneSession(_ context.Context, _ activities.AgentPod, in activities.Intervention) (bool, error) {
	switch in.Action {
	case "abort":
		close(s.abort)
	case "answer":
		close(s.answered)
	}
	return true, nil
}

func (s *stubActivities) DetectProtectedChanges(context.Context, activities.IssueRunInput, activities.GuardRequest) (guard.Report, error) {
	if !s.protected {
		return guard.Report{}, nil
	}
	return guard.Report{Protected: []guard.ProtectedChange{{Path: "mise.toml", Pattern: "mise.toml"}}}, nil
}

func (s *stubActivities) OpenHumanGate(_ context.Context, req activities.HumanGateRequest) error {
	s.opened <- req.StepRunID
	return nil
}

func (s *stubActivities) CloseHumanGate(context.Context, activities.HumanGateClose) error { return nil }

func (s *stubActivities) ExtendHumanGate(context.Context, string, time.Time) error { return nil }

func (s *stubActivities) StartWorkflowRun(context.Context, activities.IssueRunInput, snapshot.Snapshot) (activities.StartRunResult, error) {
	return activities.StartRunResult{RunID: testRun.RunID, Branch: testRun.Branch}, nil
}

func (s *stubActivities) StartStep(_ context.Context, in activities.StepStart) (string, error) {
	return fmt.Sprintf("0198f2a4-4444-7000-8000-%04d%08d", in.Attempt, len(in.StepID)), nil
}

func (s *stubActivities) FinishStep(context.Context, activities.StepEnd) error { return nil }

func (s *stubActivities) EnsureToolchainImage(context.Context, activities.IssueRunInput) (string, error) {
	return toolchainRef, nil
}

func (s *stubActivities) LaunchAgentPod(_ context.Context, _ activities.IssueRunInput, run activities.StartRunResult) (activities.AgentPod, error) {
	return activities.AgentPod{Name: "gc-1-" + run.Step.StepID, IP: "10.42.0.9"}, nil
}

func (s *stubActivities) RunAgentSession(_ context.Context, _ activities.IssueRunInput, run activities.StartRunResult, _ activities.AgentPod) (activities.SessionResult, error) {
	if run.Step.Contract == config.ContractRefinedSpec { // the refiner reads the base branch
		return activities.SessionResult{Status: "succeeded", Branch: run.Branch,
			Result: `{"spec":"Add a search box above the book list.","acceptance_criteria":["typing filters the list by title"]}`}, nil
	}
	if run.Step.Contract != "" { // a reviewer reads the head and pushes nothing
		return activities.SessionResult{Status: "succeeded", Branch: run.Branch, CommitSHA: fmt.Sprintf("%040d", s.pushes),
			Result: `{"verdict":"fail","summary":"Missing test.","findings":[{"severity":"major","file":"internal/server/version.go","line":3,"message":"add a handler test"}]}`}, nil
	}
	if s.started != nil && s.answered != nil {
		s.started <- run.StepRunID
		<-s.answered
		s.started = nil
	}
	if s.started != nil {
		s.started <- run.StepRunID
		<-s.abort
		return activities.SessionResult{Status: "aborted", Reason: "aborted by ana: wrong approach"}, nil
	}
	s.pushes++
	return activities.SessionResult{Status: "succeeded", Result: "Done.", Branch: run.Branch,
		CommitSHA: fmt.Sprintf("%040d", s.pushes), Pushed: true}, nil
}

func (s *stubActivities) RunVerdictGate(_ context.Context, req activities.VerdictGateRequest) (activities.GateOutcome, error) {
	if s.failReview != nil && s.failReview(req) {
		return activities.GateOutcome{StepRunID: req.StepRunID, Summary: "code-reviewer: fail (1 major)",
			Feedback: "Review by code-reviewer: Missing test.\n- [major] internal/server/version.go:3: add a handler test\n"}, nil
	}
	return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true, Summary: "code-reviewer: pass"}, nil
}

func (s *stubActivities) DeleteAgentPod(context.Context, string) error { return nil }

func (s *stubActivities) RunGate(_ context.Context, _ activities.IssueRunInput, req activities.GateRequest) (activities.GateOutcome, error) {
	if s.failGate(req) {
		return activities.GateOutcome{StepRunID: req.StepRunID, Summary: "mise run test exited 1",
			Feedback: "`mise run test` exited 1. The end of its output:\n```\n--- FAIL: TestSearch\n```\n"}, nil
	}
	return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true, Summary: "all checks passed"}, nil
}

func (s *stubActivities) RecordLoop(context.Context, string, string, int) error { return nil }

func (s *stubActivities) OpenChangeRequest(context.Context, activities.IssueRunInput, activities.StartRunResult, activities.SessionResult) (string, error) {
	return "https://github.com/misarescu/Goodreads-Clone/pull/42", nil
}

func (s *stubActivities) FinishRun(context.Context, activities.FinishInput) error { return nil }

func (s *stubActivities) StartRefinement(context.Context, activities.IssueRunInput, snapshot.Snapshot) (activities.StartRunResult, error) {
	return activities.StartRunResult{RunID: testRun.RunID, Branch: testRun.Branch}, nil
}

func (s *stubActivities) FinishRefinement(context.Context, activities.RefinementOutcome) (string, error) {
	return "", nil
}

// TestRecordRefineHistories records Refine (D66), like TestRecordInterpretHistories:
//
//	FRANKIE_RECORD_TEMPORAL=localhost:7266 mise exec -- go test -tags record ./internal/workflow -run TestRecordRefineHistories
func TestRecordRefineHistories(t *testing.T) {
	addr := os.Getenv("FRANKIE_RECORD_TEMPORAL")
	if addr == "" {
		t.Skip("FRANKIE_RECORD_TEMPORAL is not set")
	}
	c, err := client.Dial(client.Options{HostPort: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// The refiner returns a refined spec, which waits for approval.
	const file = "refine_proposal_recorded.json"
	if only := os.Getenv("FRANKIE_RECORD_ONLY"); only != "" && only != file {
		t.Skip("not selected")
	}
	queue := fmt.Sprintf("record-refine-%d", time.Now().UnixNano())
	w := worker.New(c, queue, worker.Options{})
	w.RegisterWorkflow(Refine)
	w.RegisterActivity(&stubActivities{opened: make(chan string, 1)})
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	run, err := c.ExecuteWorkflow(t.Context(), client.StartWorkflowOptions{ID: "refine-gc-1-" + queue, TaskQueue: queue},
		Refine, RefineInput{Issue: testInput, Snapshot: refineSnapshot(t)})
	if err != nil {
		t.Fatal(err)
	}
	var out RefineResult
	if err := run.Get(t.Context(), &out); err != nil {
		t.Fatal(err)
	}
	t.Logf("result: %+v", out)
	writeHistory(t, c, run, file)
}

func TestRecordInterpretHistories(t *testing.T) {
	addr := os.Getenv("FRANKIE_RECORD_TEMPORAL")
	if addr == "" {
		t.Skip("FRANKIE_RECORD_TEMPORAL is not set")
	}
	c, err := client.Dial(client.Options{HostPort: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	scenarios := []struct {
		file       string
		snap       snapshot.Snapshot
		failGate   func(req activities.GateRequest) bool
		failReview func(req activities.VerdictGateRequest) bool
		approve    bool // the decision sent to each human gate the run opens
		protected  bool
		intervene  bool // steer, then abort, the first agent session
		answer     bool // answer a question of the first agent session
		merge      bool // the merge queue merges the change request
		sendBack   bool // ... after sending it back once with a conflict
	}{
		// The first command gate fails, the loop back to implement fixes it.
		{"interpret_loop_then_success.json", loopSnapshot(t, loopDefinition), func(req activities.GateRequest) bool {
			return req.StepID == "checks" && req.CommitSHA == fmt.Sprintf("%040d", 1)
		}, nil, false, false, false, false, false, false},
		// The command gate keeps failing until maxLoops is used up (recorded before
		// escalation existed, so the run failed there).
		{"interpret_loops_exhausted.json", loopSnapshot(t, loopDefinition), func(req activities.GateRequest) bool { return req.StepID == "checks" }, nil, false, false, false, false, false, false},
		// default-build: the reviewer fails the first commit, the loop back to
		// implement fixes it.
		{"interpret_default_build_review_loop.json", defaultBuildSnapshot(t), func(activities.GateRequest) bool { return false },
			func(req activities.VerdictGateRequest) bool { return req.CommitSHA == fmt.Sprintf("%040d", 1) }, false, false, false, false, false, false},
		// A definition's human gate after the change request opened, approved.
		{"interpret_human_gate_approved.json", loopSnapshot(t, approvalDefinition), func(activities.GateRequest) bool { return false }, nil, true, false, false, false, false, false},
		// maxLoops used up: the gate escalates to a human, who approves.
		{"interpret_loops_exhausted_escalated.json", loopSnapshot(t, loopDefinition),
			func(req activities.GateRequest) bool { return req.StepID == "checks" }, nil, true, false, false, false, false, false},
		// default-build on a change to a protected path: the forced human gate, approved.
		{"interpret_protected_changes_approved.json", defaultBuildSnapshot(t), func(activities.GateRequest) bool { return false }, nil, true, true, false, false, false, false},
		// step-interventions version: a person steers the implement session,
		// then aborts it, which fails the run.
		{"interpret_steer_then_abort.json", loopSnapshot(t, loopDefinition), func(activities.GateRequest) bool { return false }, nil, false, false, true, false, false, false},
		// step-answers version: the implement session asks a question, a
		// person answers it, and the run succeeds.
		{"interpret_question_answered.json", loopSnapshot(t, loopDefinition), func(activities.GateRequest) bool { return false }, nil, false, false, false, true, false, false},
		// merge-queue version: default-build enqueues its change request on a
		// protected change: the forced gate first, approved, then the queue
		// merges it.
		{"interpret_default_build_merged.json", defaultBuildSnapshot(t), func(activities.GateRequest) bool { return false }, nil, true, true, false, false, true, false},
		// merge-queue-loop version: the queue sends the change request back
		// with a conflict, the coder resolves it, and the second pass merges.
		{"interpret_queue_conflict_then_merged.json", defaultBuildSnapshot(t), func(activities.GateRequest) bool { return false }, nil, true, false, false, false, true, true},
	}
	for i, sc := range scenarios {
		if only := os.Getenv("FRANKIE_RECORD_ONLY"); only != "" && only != sc.file {
			continue // never rewrite a fixture that already exists by accident
		}
		t.Run(sc.file, func(t *testing.T) {
			queue := fmt.Sprintf("record-%d-%d", time.Now().UnixNano(), i)
			w := worker.New(c, queue, worker.Options{})
			w.RegisterWorkflow(Interpret)
			stub := &stubActivities{failGate: sc.failGate, failReview: sc.failReview, opened: make(chan string, 4), protected: sc.protected,
				enqueued: make(chan activities.EnqueueRequest, 2)}
			if sc.intervene {
				stub.started, stub.abort = make(chan string, 1), make(chan struct{})
			}
			if sc.answer {
				stub.started, stub.answered = make(chan string, 1), make(chan struct{})
			}
			w.RegisterActivity(stub)
			if err := w.Start(); err != nil {
				t.Fatal(err)
			}
			defer w.Stop()

			run, err := c.ExecuteWorkflow(t.Context(), client.StartWorkflowOptions{ID: "issue-gc-1-" + queue, TaskQueue: queue},
				Interpret, InterpretInput{Issue: testInput, Snapshot: sc.snap})
			if err != nil {
				t.Fatal(err)
			}
			if sc.intervene {
				go func() { // a person steers the running session, then aborts it
					id := <-stub.started
					_ = c.SignalWorkflow(t.Context(), run.GetID(), run.GetRunID(), SteerSignal, StepIntervention{StepRunID: id, By: "ana", Text: "use the helper"})
					_ = c.SignalWorkflow(t.Context(), run.GetID(), run.GetRunID(), AbortSignal, StepIntervention{StepRunID: id, By: "ana", Text: "wrong approach"})
				}()
			}
			if sc.answer {
				go func() { // a person answers the running session's question
					id := <-stub.started
					_ = c.SignalWorkflow(t.Context(), run.GetID(), run.GetRunID(), AnswerSignal, StepIntervention{StepRunID: id, By: "ana", Text: "8080", QuestionID: "q1"})
				}()
			}
			if sc.merge {
				go func() { // the merge queue answers each enqueue
					for req := range stub.enqueued {
						out := QueueOutcome{StepID: req.StepID, Attempt: req.Attempt, Kind: QueueOutcomeMerged, MergeSHA: "m3rge", HeadSHA: req.HeadSHA}
						if sc.sendBack && req.Attempt == 1 {
							out = QueueOutcome{StepID: req.StepID, Attempt: req.Attempt, Kind: QueueOutcomeConflict, Reason: "conflicts with main",
								Conflicts: []string{"web/App.jsx"}, Feedback: "### web/App.jsx\n+import Shelves", HeadSHA: req.HeadSHA}
						}
						_ = c.SignalWorkflow(t.Context(), run.GetID(), run.GetRunID(), MergeQueueOutcomeSignal, out)
					}
				}()
			}
			go func() { // a person decides each human gate the run opens
				for id := range stub.opened {
					_ = c.SignalWorkflow(t.Context(), run.GetID(), run.GetRunID(), HumanDecisionSignal,
						HumanDecision{StepRunID: id, Approved: sc.approve, By: "web", Comment: "recorded"})
				}
			}()
			var out IssueRunResult
			if err := run.Get(t.Context(), &out); err != nil {
				t.Fatal(err)
			}
			close(stub.opened)
			t.Logf("result: %+v", out)
			writeHistory(t, c, run, sc.file)
		})
	}
}

// writeHistory saves a finished workflow's history to testdata/ for the replayer.
func writeHistory(t *testing.T, c client.Client, run client.WorkflowRun, file string) {
	t.Helper()
	var events []*historypb.HistoryEvent
	it := c.GetWorkflowHistory(t.Context(), run.GetID(), run.GetRunID(), false, 0)
	for it.HasNext() {
		e, err := it.Next()
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	raw, err := protojson.MarshalOptions{Indent: "  "}.Marshal(&historypb.History{Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", file), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (s *stubActivities) CheckQueueEntry(context.Context, activities.IssueRunInput, int) (activities.QueueEntryState, error) {
	return activities.QueueEntryState{Queued: true, ChangeRequest: domain.ChangeRequestOpen}, nil
}

func (s *stubActivities) UpdateBranch(_ context.Context, req activities.BranchUpdateRequest) (activities.BranchUpdateResult, error) {
	if s.conflict {
		return activities.BranchUpdateResult{Outcome: activities.BranchConflict, BaseSHA: fmt.Sprintf("%040d", 200), Conflicts: []string{"web/App.jsx"}}, nil
	}
	s.pushes++
	return activities.BranchUpdateResult{Outcome: activities.BranchUpdated, HeadSHA: fmt.Sprintf("%040d", 100+s.pushes),
		BaseSHA: fmt.Sprintf("%040d", 200+s.pushes)}, nil
}

func (s *stubActivities) MergeQueued(_ context.Context, req activities.QueueMergeRequest) (activities.QueueMergeResult, error) {
	if s.pushes == 1 { // the base moved after the first update
		return activities.QueueMergeResult{Outcome: activities.QueueBaseMoved, Reason: "main moved"}, nil
	}
	return activities.QueueMergeResult{Outcome: activities.QueueMerged, MergeSHA: "m3rge"}, nil
}

// TestRecordMergeQueueHistories records MergeQueue (D87): an entry whose
// update is checked, the base moves before the merge, and the second try
// merges; the issue workflow it signals is gone.
//
//	FRANKIE_RECORD_TEMPORAL=localhost:7266 mise exec -- go test -tags record ./internal/workflow -run TestRecordMergeQueueHistories
func TestRecordMergeQueueHistories(t *testing.T) {
	addr := os.Getenv("FRANKIE_RECORD_TEMPORAL")
	if addr == "" {
		t.Skip("FRANKIE_RECORD_TEMPORAL is not set")
	}
	c, err := client.Dial(client.Options{HostPort: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	queue := fmt.Sprintf("record-mq-%d", time.Now().UnixNano())
	w := worker.New(c, queue, worker.Options{})
	w.RegisterWorkflow(MergeQueue)
	stub := &stubActivities{failGate: func(activities.GateRequest) bool { return false }}
	w.RegisterActivity(stub)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	entry := QueueEntry{Issue: testInput, RunID: testRun.RunID, WorkflowID: "issue-gc-1-gone", StepID: "enqueue", Attempt: 1,
		ChangeRequestID: "0198f2a4-4444-7000-8000-000000000004", Number: 42, Branch: testRun.Branch, HeadSHA: fmt.Sprintf("%040d", 1),
		Gates: []QueueGate{{StepID: "checks", Commands: []string{"mise run lint", "mise run test"}}}, Method: domain.MergeSquash,
		Title: "gc-1: Add search (#42)"}
	run, err := c.ExecuteWorkflow(t.Context(), client.StartWorkflowOptions{ID: MergeQueueWorkflowID("misarescu/Goodreads-Clone") + "-" + queue, TaskQueue: queue},
		MergeQueue, MergeQueueInput{Repo: "misarescu/Goodreads-Clone", Pending: []QueueEntry{entry}})
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Get(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	file := "merge_queue_base_moved_then_merged.json"
	if only := os.Getenv("FRANKIE_RECORD_ONLY"); only == "merge_queue_ci_failed.json" {
		// provider-checks version: Frankie's checks pass on the update, the
		// repo's own CI fails.
		stub.ciFails, file = true, only
		entry.WorkflowID = "issue-gc-1-gone-ci"
		run, err = c.ExecuteWorkflow(t.Context(), client.StartWorkflowOptions{ID: MergeQueueWorkflowID("misarescu/Goodreads-Clone") + "-ci-" + queue, TaskQueue: queue},
			MergeQueue, MergeQueueInput{Repo: "misarescu/Goodreads-Clone", Pending: []QueueEntry{entry}})
		if err != nil {
			t.Fatal(err)
		}
		if err := run.Get(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("FRANKIE_RECORD_ONLY") == "merge_queue_conflict.json" {
		// conflict-context version: the update conflicts, and both sides go back.
		stub.conflict, file = true, "merge_queue_conflict.json"
		entry.WorkflowID = "issue-gc-1-gone-conflict"
		run, err = c.ExecuteWorkflow(t.Context(), client.StartWorkflowOptions{ID: MergeQueueWorkflowID("misarescu/Goodreads-Clone") + "-c-" + queue, TaskQueue: queue},
			MergeQueue, MergeQueueInput{Repo: "misarescu/Goodreads-Clone", Pending: []QueueEntry{entry}})
		if err != nil {
			t.Fatal(err)
		}
		if err := run.Get(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
	}
	writeHistory(t, c, run, file)
}
