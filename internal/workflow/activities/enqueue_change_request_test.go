package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"

	"github.com/misarescu/frankie/internal/domain"
)

// queueCall is one signal-with-start.
type queueCall struct {
	id, signal string
	entry      QueueEntry
	opts       client.StartWorkflowOptions
	workflow   any
	input      MergeQueueInput
}

// fakeQueue records signal-with-start calls.
type fakeQueue struct {
	calls []queueCall
	err   error
}

func (q *fakeQueue) SignalWithStartWorkflow(_ context.Context, id, signal string, arg any, opts client.StartWorkflowOptions, wf any, args ...any) (client.WorkflowRun, error) {
	q.calls = append(q.calls, queueCall{id, signal, arg.(QueueEntry), opts, wf, args[0].(MergeQueueInput)})
	return nil, q.err
}

func enqueueFixture(t *testing.T) (*queueFixture, *fakeQueue, EnqueueRequest) {
	t.Helper()
	f := newQueueActivityFixture(t)
	q := &fakeQueue{}
	f.a.Queue, f.a.TaskQueue = q, "frankie"
	id := f.step(t, "merge", domain.StepAction)
	return f, q, EnqueueRequest{Issue: f.in, RunID: f.run.RunID, StepRunID: id, WorkflowID: "issue-gc-1", StepID: "merge", Attempt: 1,
		Branch: f.run.Branch, HeadSHA: queuedHead, Gates: []QueueGate{{StepID: "checks", Commands: []string{"mise run test"}}}, Method: domain.MergeSquash}
}

// M6 (D88): enqueueing queues the issue and signals its repo's queue with the
// run's open change request, starting the queue if it is not running.
func TestEnqueueChangeRequest(t *testing.T) {
	f, q, req := enqueueFixture(t)
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.EnqueueChangeRequest, req); err != nil {
		t.Fatal(err)
	}
	if len(q.calls) != 1 {
		t.Fatalf("calls = %d", len(q.calls))
	}
	c := q.calls[0]
	if c.id != "merge-queue/misarescu/Goodreads-Clone" || c.signal != MergeQueueEnqueueSignal || c.opts.ID != c.id || c.opts.TaskQueue != "frankie" ||
		c.workflow != "MergeQueue" || c.input.Repo != "misarescu/Goodreads-Clone" {
		t.Errorf("call = %+v", c)
	}
	e := c.entry
	if e.ChangeRequestID != f.cr.ID.String() || e.Number != 9 || e.HeadSHA != queuedHead || e.WorkflowID != "issue-gc-1" ||
		e.Title != "gc-1: Add search (#9)" || len(e.Gates) != 1 || e.Method != domain.MergeSquash {
		t.Errorf("entry = %+v", e)
	}
	issue, _ := f.a.Store.GetIssue(t.Context(), uuid.MustParse(f.in.IssueID))
	step, _ := f.a.Store.GetStepRun(t.Context(), uuid.MustParse(req.StepRunID))
	if issue.Status != domain.IssueQueued || step.Status != domain.ExecRunning || step.CommitSHA != queuedHead {
		t.Errorf("issue %s, step %s at %q", issue.Status, step.Status, step.CommitSHA)
	}
	// A retry signals again; the queue replaces the entry.
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.EnqueueChangeRequest, req); err != nil || len(q.calls) != 2 {
		t.Errorf("retry: %v, calls %d", err, len(q.calls))
	}
}

func TestEnqueueChangeRequestFails(t *testing.T) {
	t.Run("no open change request for the branch", func(t *testing.T) {
		f, q, req := enqueueFixture(t)
		if err := f.a.Store.RecordChangeRequestClosed(t.Context(), f.cr.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := activityEnv(f.a).ExecuteActivity(f.a.EnqueueChangeRequest, req); err == nil || len(q.calls) != 0 {
			t.Fatalf("enqueued a closed change request: %v", err)
		}
	})
	t.Run("the queue cannot be signaled", func(t *testing.T) {
		f, q, req := enqueueFixture(t)
		q.err = errors.New("temporal down")
		if _, err := activityEnv(f.a).ExecuteActivity(f.a.EnqueueChangeRequest, req); err == nil {
			t.Fatal("a failed signal was not reported")
		}
	})
	t.Run("no queue configured", func(t *testing.T) {
		f, _, req := enqueueFixture(t)
		f.a.Queue = nil
		if _, err := activityEnv(f.a).ExecuteActivity(f.a.EnqueueChangeRequest, req); err == nil {
			t.Fatal("enqueued without a queue")
		}
	})
}
