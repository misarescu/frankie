package activities

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
)

// Merge queue signals (D87). An issue's workflow enqueues its change request
// on the repo's queue workflow, started with the signal if it is not running,
// and waits for the outcome signal.
const (
	MergeQueueEnqueueSignal = "merge-queue-enqueue"
	MergeQueueOutcomeSignal = "merge-queue-outcome"
)

// MergeQueueWorkflowID is the ID of repo's queue workflow: one per repo, so
// merges into it are serialized without locks (FR-MQ-1).
func MergeQueueWorkflowID(repo string) string { return "merge-queue/" + repo }

// MergeQueueInput is the queue's repo and the entries carried over from the
// queue's previous run.
type MergeQueueInput struct {
	Repo    string
	Pending []QueueEntry `json:",omitempty"`
}

// QueueEntry is one change request waiting to merge, with what the queue
// needs to check it again: everything comes from the issue's run, which
// snapshotted it at start (invariant 4).
type QueueEntry struct {
	Issue IssueRunInput
	RunID string
	// WorkflowID is the issue's workflow, which waits for the outcome.
	WorkflowID string
	// StepID and Attempt are the enqueue step's execution; the queue's own
	// steps are recorded on the issue's run under the step's ID.
	StepID          string
	Attempt         int
	ChangeRequestID string
	Number          int
	Branch          string
	HeadSHA         string // the commit the issue's gates passed
	// Gates are the definition's command gates, re-run on the updated commit.
	Gates  []QueueGate
	Method domain.MergeMethod
	Title  string // the merge commit's
	// CILogs bound what is read of the repo's failed CI checks' logs (D120).
	CILogs domain.CILogLimits `json:",omitzero"`
}

// QueueGate is a command gate the queue re-runs.
type QueueGate struct {
	StepID   string
	Commands []string
	Timeout  time.Duration
}

// How a change request left the queue.
const (
	QueueOutcomeMerged   = "merged"   // merged by the queue, or on the provider by a person
	QueueOutcomeConflict = "conflict" // its branch conflicts with the base branch
	QueueOutcomeRed      = "red"      // a gate failed on the updated commit
	QueueOutcomeMoved    = "moved"    // its branch moved while queued
	QueueOutcomeRefused  = "refused"  // the provider refused the merge
	QueueOutcomeClosed   = "closed"   // a person closed it while queued
	QueueOutcomeDequeued = "dequeued" // its issue left the queue, e.g. canceled
	QueueOutcomeBusy     = "busy"     // the base branch kept moving
	QueueOutcomeError    = "error"    // the queue could not check or merge it
)

// QueueOutcome tells the issue's workflow how its change request left the queue.
type QueueOutcome struct {
	StepID   string
	Attempt  int
	Kind     string
	MergeSHA string `json:",omitempty"`
	Reason   string `json:",omitempty"`
	// Feedback is what a failed gate found, for the coder.
	Feedback  string   `json:",omitempty"`
	Conflicts []string `json:",omitempty"`
	// HeadSHA is the commit the queue last checked.
	HeadSHA string `json:",omitempty"`
}

// Merged reports whether the change request reached the base branch.
func (o QueueOutcome) Merged() bool { return o.Kind == QueueOutcomeMerged }

// QueueStarter is what enqueueing needs of the Temporal client:
// client.Client in production.
type QueueStarter interface {
	SignalWithStartWorkflow(ctx context.Context, workflowID, signalName string, signalArg any,
		options client.StartWorkflowOptions, workflow any, workflowArgs ...any) (client.WorkflowRun, error)
}

// mergeQueueWorkflow is the queue workflow's registered type name.
const mergeQueueWorkflow = "MergeQueue"

// EnqueueRequest puts a run's change request on its repo's merge queue: one
// execution of the definition's merge-queue.enqueue step (D88).
type EnqueueRequest struct {
	Issue      IssueRunInput
	RunID      string
	StepRunID  string
	WorkflowID string // the issue's workflow, which waits for the outcome
	StepID     string
	Attempt    int
	Branch     string
	HeadSHA    string // the commit the run's gates passed
	Gates      []QueueGate
	Method     domain.MergeMethod
	CILogs     domain.CILogLimits `json:",omitzero"`
}

// EnqueueChangeRequest moves the issue to queued and signals its repo's merge
// queue, starting the queue workflow if none is running (signal-with-start,
// FR-MQ-1). The change request is the issue's open one for the run's branch.
// Idempotent: the queue replaces an entry from the same issue workflow.
func (a *Activities) EnqueueChangeRequest(ctx context.Context, req EnqueueRequest) error {
	issueID, err1 := uuid.Parse(req.Issue.IssueID)
	stepID, err2 := uuid.Parse(req.StepRunID)
	if err := errors.Join(err1, err2); err != nil {
		return temporal.NewNonRetryableApplicationError("bad id", "input", err)
	}
	if a.Queue == nil {
		return temporal.NewNonRetryableApplicationError("the merge queue is not configured", "config", nil)
	}
	crs, err := a.Store.ListChangeRequestsForIssue(ctx, issueID)
	if err != nil {
		return err
	}
	var cr *domain.ChangeRequest
	for i := range crs {
		if crs[i].Branch == req.Branch && crs[i].Status == string(domain.ChangeRequestOpen) {
			cr = &crs[i]
			break
		}
	}
	if cr == nil {
		return temporal.NewNonRetryableApplicationError(fmt.Sprintf("no open change request for %s", req.Branch), "state", nil)
	}
	step, err := a.Store.GetStepRun(ctx, stepID)
	if err != nil {
		return err
	}
	if step.Status == domain.ExecPending {
		if _, err := a.Store.TransitionStepRun(ctx, stepID, domain.ExecPending, domain.ExecRunning); err != nil {
			return err
		}
	}
	if _, err := a.Store.SetStepRunCommit(ctx, stepID, req.HeadSHA); err != nil {
		return err
	}
	if _, err := a.Store.TransitionIssue(ctx, issueID, domain.IssueRunning, domain.IssueQueued); err != nil {
		return err
	}
	entry := QueueEntry{
		Issue: req.Issue, RunID: req.RunID, WorkflowID: req.WorkflowID, StepID: req.StepID, Attempt: req.Attempt,
		ChangeRequestID: cr.ID.String(), Number: cr.Number, Branch: req.Branch, HeadSHA: req.HeadSHA,
		Gates: req.Gates, Method: req.Method, CILogs: req.CILogs,
		Title: fmt.Sprintf("%s: %s (#%d)", req.Issue.IssueKey, req.Issue.Title, cr.Number),
	}
	id := MergeQueueWorkflowID(req.Issue.Repo)
	_, err = a.Queue.SignalWithStartWorkflow(ctx, id, MergeQueueEnqueueSignal, entry,
		client.StartWorkflowOptions{ID: id, TaskQueue: a.TaskQueue}, mergeQueueWorkflow, MergeQueueInput{Repo: req.Issue.Repo})
	if err != nil {
		return fmt.Errorf("enqueue on %s: %w", id, err)
	}
	return nil
}
