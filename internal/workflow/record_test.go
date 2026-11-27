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
	"sync"
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
	// mu guards pushes: a group of writers runs its sessions at once (D122).
	mu         sync.Mutex
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
	// unchanged, when set, says which agent sessions push nothing (D128).
	unchanged func(run activities.StartRunResult) bool
}

func (s *stubActivities) AwaitProviderChecks(_ context.Context, req activities.ProviderChecksRequest) (activities.GateOutcome, error) {
	if s.ciFails {
		return activities.GateOutcome{StepRunID: req.StepRunID, Summary: "the repo's CI failed: test", Feedback: "- test: 2 tests failed\n"}, nil
	}
	return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true, Summary: "the repo's CI passed (2 checks)"}, nil
}

func (s *stubActivities) LeaveQueue(context.Context, string) error { return nil }

// The preview gate's Activities (D126): the first pushed head fails its
// suite in the preview, later heads pass.
func (s *stubActivities) PublishPreview(context.Context, activities.PreviewRequest) (activities.PreviewPublished, error) {
	return activities.PreviewPublished{ChangeRequest: 42, State: []byte(`{"commit":"c1"}`)}, nil
}

func (s *stubActivities) AwaitPreview(context.Context, activities.PreviewRequest) (activities.StageStatus, error) {
	return activities.StageStatus{Ready: true}, nil
}

func (s *stubActivities) VerifyPreview(_ context.Context, req activities.PreviewRequest) (activities.Verification, error) {
	if req.HeadSHA == fmt.Sprintf("%040d", 1) {
		return activities.Verification{Summary: "1 of 219 tests failed", Failures: []activities.TestFailure{{Test: "gc-1 search", Message: "expected 200, got 500"}}}, nil
	}
	return activities.Verification{Passed: true, Summary: "219 tests passed"}, nil
}

func (s *stubActivities) RemovePreview(context.Context, activities.PreviewRequest) error { return nil }

func (s *stubActivities) FinishPreview(_ context.Context, res activities.PreviewResult) (activities.GateOutcome, error) {
	return activities.PreviewOutcome(res.StepRunID, res.Details), nil
}

func (s *stubActivities) CloseChangeRequest(context.Context, activities.IssueRunInput, string, string) error {
	return nil
}
func (s *stubActivities) CheckPreserved(_ context.Context, _ activities.IssueRunInput, req activities.PreserveRequest) (activities.GateOutcome, error) {
	return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true, Summary: "the 1 files reproduce changed are as it left them"}, nil
}

func (s *stubActivities) RecordFindings(_ context.Context, req activities.FindingsRequest) (activities.FindingsResult, error) {
	f, err := domain.ParseBugFindings([]byte(req.Result))
	if err != nil {
		return activities.FindingsResult{Error: err.Error()}, nil
	}
	return activities.FindingsResult{Findings: f}, nil
}

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

func (s *stubActivities) EnsureStepImage(context.Context, activities.IssueRunInput, string, string) (string, error) {
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
	if run.Step.Contract == config.ContractBugFindings { // an investigator reads and pushes nothing
		return activities.SessionResult{Status: "succeeded", Branch: run.Branch, Result: fmt.Sprintf(testFindings, true)}, nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unchanged != nil && s.unchanged(run) {
		return activities.SessionResult{Status: "succeeded", Branch: run.Branch, CommitSHA: fmt.Sprintf("%040d", s.pushes),
			Result: "Nothing to change: the e2e test's setup never tags book B1."}, nil
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
		changes    bool // the first human gate gets "request changes"
	}{
		// The first command gate fails, the loop back to implement fixes it.
		{"interpret_loop_then_success.json", loopSnapshot(t, loopDefinition), func(req activities.GateRequest) bool {
			return req.StepID == "checks" && req.CommitSHA == fmt.Sprintf("%040d", 1)
		}, nil, false, false, false, false, false, false, false},
		// The command gate keeps failing until maxLoops is used up (recorded before
		// escalation existed, so the run failed there).
		{"interpret_loops_exhausted.json", loopSnapshot(t, loopDefinition), func(req activities.GateRequest) bool { return req.StepID == "checks" }, nil, false, false, false, false, false, false, false},
		// default-build: the reviewer fails the first commit, the loop back to
		// implement fixes it.
		{"interpret_default_build_review_loop.json", defaultBuildSnapshot(t), func(activities.GateRequest) bool { return false },
			func(req activities.VerdictGateRequest) bool { return req.CommitSHA == fmt.Sprintf("%040d", 1) }, false, false, false, false, false, false, false},
		// A definition's human gate after the change request opened, approved.
		{"interpret_human_gate_approved.json", loopSnapshot(t, approvalDefinition), func(activities.GateRequest) bool { return false }, nil, true, false, false, false, false, false, false},
		// maxLoops used up: the gate escalates to a human, who approves.
		{"interpret_loops_exhausted_escalated.json", loopSnapshot(t, loopDefinition),
			func(req activities.GateRequest) bool { return req.StepID == "checks" }, nil, true, false, false, false, false, false, false},
		// default-build on a change to a protected path: the forced human gate, approved.
		{"interpret_protected_changes_approved.json", defaultBuildSnapshot(t), func(activities.GateRequest) bool { return false }, nil, true, true, false, false, false, false, false},
		// step-interventions version: a person steers the implement session,
		// then aborts it, which fails the run.
		{"interpret_steer_then_abort.json", loopSnapshot(t, loopDefinition), func(activities.GateRequest) bool { return false }, nil, false, false, true, false, false, false, false},
		// step-answers version: the implement session asks a question, a
		// person answers it, and the run succeeds.
		{"interpret_question_answered.json", loopSnapshot(t, loopDefinition), func(activities.GateRequest) bool { return false }, nil, false, false, false, true, false, false, false},
		// merge-queue version: default-build enqueues its change request on a
		// protected change: the forced gate first, approved, then the queue
		// merges it.
		{"interpret_default_build_merged.json", defaultBuildSnapshot(t), func(activities.GateRequest) bool { return false }, nil, true, true, false, false, true, false, false},
		// merge-queue-loop version: the queue sends the change request back
		// with a conflict, the coder resolves it, and the second pass merges.
		{"interpret_queue_conflict_then_merged.json", defaultBuildSnapshot(t), func(activities.GateRequest) bool { return false }, nil, true, false, false, false, true, true, false},
		// merge-approval version: the project asks for merge approval; a
		// person requests changes, the coder makes them, the second ask is
		// approved and the queue merges.
		{"interpret_merge_approval_changes_requested.json", mergeApprovalSnapshot(t), func(activities.GateRequest) bool { return false }, nil, true, false, false, false, true, false, true},
		// image-build-events version: agent steps resolve their image as
		// themselves, so a build they wait for is recorded on them.
		{"interpret_image_build_events.json", loopSnapshot(t, loopDefinition), func(activities.GateRequest) bool { return false }, nil, false, false, false, false, false, false, false},
		// bug-findings version: an investigation finds a testable bug, the
		// reproduce step runs, then the fix.
		{"interpret_investigate_testable.json", investigateSnapshot(t), func(activities.GateRequest) bool { return false }, nil, false, false, false, false, false, false, false},
		// bug-gates version: a testable bug's test must fail before the fix,
		// and the fix keeps it.
		{"interpret_bug_testable.json", bugSnapshot(t), func(activities.GateRequest) bool { return false }, nil, false, false, false, false, false, false, false},
		// concurrent-writers version (D122): the coder and a second writer
		// run at once, the second on its own branch, merged into the issue's;
		// the first checks fail and only the coder runs again.
		{"interpret_concurrent_writers.json", loopSnapshot(t, writersDefinition), failFirst("checks"), nil, false, false, false, false, false, false, false},
		// preview-gate version (D126): the change request's first head fails
		// its suite in a preview, the coder fixes it, the second head passes
		// there, and the queue merges it.
		{"interpret_preview_gate.json", previewSnapshot(t), func(activities.GateRequest) bool { return false }, nil, false, false, false, false, true, false, false},
		// disputed-changes version (D128): the e2e gate fails, the coder sent
		// back changes nothing, the test writer fixes its tests with the
		// coder's report, and the merged fix passes.
		{"interpret_disputed_tests.json", loopSnapshot(t, disputeDefinition), failFirst("e2e"), nil, false, false, false, false, false, false, false},
	}
	// setup adjusts the stub Activities of the scenarios that need more.
	setup := map[string]func(*stubActivities){
		"interpret_disputed_tests.json": func(s *stubActivities) { s.unchanged = nthSession("implement", 2) },
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
			if f := setup[sc.file]; f != nil {
				f(stub)
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
				first := true
				for id := range stub.opened {
					if sc.changes && first {
						first = false
						_ = c.SignalWorkflow(t.Context(), run.GetID(), run.GetRunID(), HumanDecisionSignal,
							HumanDecision{StepRunID: id, By: "web", Comment: "keep the old route too", Action: DecisionRequestChanges})
						continue
					}
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

// failFirst fails a gate step's first execution only.
func failFirst(stepID string) func(req activities.GateRequest) bool {
	var mu sync.Mutex
	failed := false
	return func(req activities.GateRequest) bool {
		mu.Lock()
		defer mu.Unlock()
		if req.StepID != stepID || failed {
			return false
		}
		failed = true
		return true
	}
}

// nthSession matches the n-th session of an agent step; the stub calls it
// holding its lock.
func nthSession(stepID string, n int) func(run activities.StartRunResult) bool {
	seen := 0
	return func(run activities.StartRunResult) bool {
		if run.Step.StepID != stepID {
			return false
		}
		seen++
		return seen == n
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
	s.mu.Lock()
	defer s.mu.Unlock()
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

// mergeApprovalSnapshot is default-build for a project that approves merges.
func mergeApprovalSnapshot(t *testing.T) snapshot.Snapshot {
	s := defaultBuildSnapshot(t)
	s.MergeApproval = true
	return s
}

// releaseStub answers for the release Activities in a recording.
type releaseStub struct {
	failVerify bool
	// agent turns the release agent on (D129).
	agent bool
	// awaitFor makes waiting for a stage take this long, so signals arrive
	// while a release runs.
	awaitFor time.Duration
	// gated puts a human gate before production (D139); gates receives the
	// step run ID of each gate opened.
	gated bool
	gates chan string
}

func (s *releaseStub) PrepareRelease(_ context.Context, req activities.PrepareReleaseRequest) (activities.ReleasePlan, error) {
	return activities.ReleasePlan{ReleaseID: "0198f2a4-5555-7000-8000-" + req.SHA[:12], Status: domain.ReleaseInProgress, Issues: []string{"gc-1"}, Agent: s.agent,
		Stages: []activities.ReleaseStage{
			{Name: "staging", Driver: "flux", Config: []byte(`{"path":"apps/goodreads/staging"}`), URL: "https://goodreads-stg.example.ts.net", Verify: "e2e-suite", Rollback: true},
			{Name: "production", Driver: "flux", Config: []byte(`{"path":"apps/goodreads/production"}`), Rollback: true, Gate: map[bool]string{true: config.GateHuman}[s.gated]},
		}}, nil
}

func (s *releaseStub) OpenReleaseGate(context.Context, activities.ReleaseGateRequest) (string, error) {
	const id = "0198f2a4-6666-7000-8000-000000000001"
	s.gates <- id
	return id, nil
}

func (s *releaseStub) CloseHumanGate(context.Context, activities.HumanGateClose) error { return nil }

func (s *releaseStub) StartDeployment(_ context.Context, req activities.StageRequest) (string, error) {
	return "0198f2a4-6666-7000-8000-00000000000" + map[string]string{"staging": "1", "production": "2"}[req.Stage.Name], nil
}

func (s *releaseStub) PublishStage(context.Context, activities.StageRequest) error { return nil }

func (s *releaseStub) AwaitStage(context.Context, activities.StageRequest) (activities.StageStatus, error) {
	time.Sleep(s.awaitFor)
	return activities.StageStatus{Ready: true}, nil
}

func (s *releaseStub) VerifyStage(_ context.Context, req activities.StageRequest) (activities.Verification, error) {
	if s.failVerify && req.Stage.Name == "staging" {
		return activities.Verification{Summary: "1 of 12 tests failed: Books/search",
			Failures: []activities.TestFailure{{Test: "Books/search", IssueKeys: []string{"gc-1"}, Message: "expected 200, got 500"}}}, nil
	}
	return activities.Verification{Passed: true, Summary: "12 tests passed"}, nil
}

func (s *releaseStub) EndDeployment(context.Context, activities.EndDeploymentRequest) error {
	return nil
}
func (s *releaseStub) RollbackStage(context.Context, activities.StageRequest) error { return nil }
func (s *releaseStub) FinishRelease(context.Context, activities.FinishReleaseRequest) error {
	return nil
}

func (s *releaseStub) ReportReleaseFailure(context.Context, activities.ReleaseFailureReport) (string, error) {
	return "https://github.com/misarescu/Goodreads-Clone/issues/131", nil
}

// The release agent's Activities (D129).

func (s *releaseStub) PrepareReleaseSession(_ context.Context, req activities.ReleaseAgentRequest) (activities.ReleaseSession, error) {
	step := map[string]string{activities.ReleaseStepNotes: "1", activities.ReleaseStepAnalysis: "2"}[req.Step]
	return activities.ReleaseSession{
		Issue: activities.IssueRunInput{IssueKey: "release-0000000", Title: "Release 0000000", Spec: "Write the release notes.", Repo: "misarescu/Goodreads-Clone", BaseBranch: "main"},
		Run: activities.StartRunResult{RunID: "0198f2a4-7777-7000-8000-000000000001", StepRunID: "0198f2a4-8888-7000-8000-00000000000" + step,
			Branch: "agent/release-0000000/" + req.Step, ReleaseID: req.ReleaseID,
			Step: &activities.AgentStep{StepID: req.Step, Role: "release-agent", RoleVersion: 1, Timeout: 20 * time.Minute, Inputs: []string{"spec"}, Contract: "release-notes"}},
	}, nil
}

func (s *releaseStub) EnsureStepImage(context.Context, activities.IssueRunInput, string, string) (string, error) {
	return "localhost:30500/toolchains/goodreads@sha256:1111111111111111111111111111111111111111111111111111111111111111", nil
}

func (s *releaseStub) LaunchAgentPod(_ context.Context, _ activities.IssueRunInput, run activities.StartRunResult) (activities.AgentPod, error) {
	return activities.AgentPod{Name: "release-agent-" + run.Step.StepID, IP: "10.48.0.9"}, nil
}

func (s *releaseStub) RunAgentSession(_ context.Context, _ activities.IssueRunInput, run activities.StartRunResult, _ activities.AgentPod) (activities.SessionResult, error) {
	if run.Step.StepID == activities.ReleaseStepAnalysis {
		return activities.SessionResult{Status: "succeeded", Result: `{"suspects":[{"issue":"gc-1","likelihood":"high","why":"search pages"}],"summary":"gc-1 changed search"}`}, nil
	}
	return activities.SessionResult{Status: "succeeded", Result: `{"bump":"minor","reason":"a new endpoint","notes":"- gc-1: search"}`}, nil
}

func (s *releaseStub) DeleteAgentPod(context.Context, string) error         { return nil }
func (s *releaseStub) FinishStep(context.Context, activities.StepEnd) error { return nil }

func (s *releaseStub) RecordReleaseNotes(context.Context, activities.ReleaseNotesRequest) (domain.NotesSource, error) {
	return domain.NotesByAgent, nil
}

func (s *releaseStub) PublishVersion(context.Context, string) (activities.PublishedVersion, error) {
	return activities.PublishedVersion{Version: "v0.1.0", URL: "https://github.com/misarescu/Goodreads-Clone/releases/tag/v0.1.0"}, nil
}

func (s *releaseStub) FinishReleaseRun(context.Context, string) error { return nil }

// TestRecordReleaseHistories records Release (D114): a release through two
// stages; one whose staging verification fails and is rolled back; two
// requests arriving during a release, batched into one more; and (D115) a
// failed verification that opens a bug issue. FRANKIE_RECORD_ONLY records one
// file, so the others stay as recorded.
//
//	FRANKIE_RECORD_TEMPORAL=localhost:7266 FRANKIE_RECORD_ONLY=release_bug_issue.json mise exec -- go test -tags record ./internal/workflow -run TestRecordReleaseHistories
func TestRecordReleaseHistories(t *testing.T) {
	addr := os.Getenv("FRANKIE_RECORD_TEMPORAL")
	if addr == "" {
		t.Skip("FRANKIE_RECORD_TEMPORAL is not set")
	}
	c, err := client.Dial(client.Options{HostPort: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sha := func(n int) string { return fmt.Sprintf("%040d", n) }
	for _, rec := range []struct {
		file    string
		stub    *releaseStub
		signals []string
	}{
		{"release_succeeded.json", &releaseStub{}, nil},
		{"release_verify_failed_rolled_back.json", &releaseStub{failVerify: true}, nil},
		{"release_batched.json", &releaseStub{awaitFor: 2 * time.Second}, []string{sha(2), sha(3)}},
		{"release_bug_issue.json", &releaseStub{failVerify: true}, nil},
		// release-agent version (D129): notes while the stages deploy, then
		// the version; and the analysis of a failed verification.
		{"release_agent_versioned.json", &releaseStub{agent: true}, nil},
		{"release_agent_analysis.json", &releaseStub{agent: true, failVerify: true}, nil},
		// release-stage-gate version (D139): production waits for a person.
		{"release_gate_approved.json", &releaseStub{gated: true, gates: make(chan string, 1)}, nil},
	} {
		if only := os.Getenv("FRANKIE_RECORD_ONLY"); only != "" && only != rec.file {
			continue
		}
		queue := fmt.Sprintf("record-rel-%d", time.Now().UnixNano())
		w := worker.New(c, queue, worker.Options{})
		w.RegisterWorkflow(Release)
		w.RegisterActivity(rec.stub)
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		run, err := c.ExecuteWorkflow(t.Context(), client.StartWorkflowOptions{ID: ReleaseWorkflowID("misarescu/Goodreads-Clone") + "-" + queue, TaskQueue: queue},
			Release, ReleaseInput{Repo: "misarescu/Goodreads-Clone", Pending: &ReleaseRequest{SHA: sha(1)}})
		if err != nil {
			t.Fatal(err)
		}
		if len(rec.signals) > 0 {
			time.Sleep(time.Second) // the first release is waiting for staging
		}
		for _, s := range rec.signals {
			if err := c.SignalWorkflow(t.Context(), run.GetID(), run.GetRunID(), ReleaseRequestSignal, ReleaseRequest{SHA: s}); err != nil {
				t.Fatal(err)
			}
		}
		if rec.stub.gated {
			d := HumanDecision{StepRunID: <-rec.stub.gates, Approved: true, By: "mihai"}
			if err := c.SignalWorkflow(t.Context(), run.GetID(), run.GetRunID(), HumanDecisionSignal, d); err != nil {
				t.Fatal(err)
			}
		}
		if err := run.Get(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
		writeHistory(t, c, run, rec.file)
		w.Stop()
	}
}
