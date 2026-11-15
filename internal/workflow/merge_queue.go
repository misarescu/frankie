package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// The merge queue's types live with the Activity that enqueues (activities
// cannot import this package); these names keep the workflow's code short.
const (
	MergeQueueEnqueueSignal = activities.MergeQueueEnqueueSignal
	MergeQueueOutcomeSignal = activities.MergeQueueOutcomeSignal
)

// MergeQueueWorkflowID is the ID of repo's queue workflow.
func MergeQueueWorkflowID(repo string) string { return activities.MergeQueueWorkflowID(repo) }

const (
	// maxQueueEntriesPerRun bounds a queue workflow's history: it continues
	// as new, carrying what is pending, after this many entries.
	maxQueueEntriesPerRun = 50
	// maxMergeTries bounds how often one entry is updated again because the
	// base branch moved between its update and its merge.
	maxMergeTries = 3
)

// The queue's step IDs are the enqueue step's, plus these suffixes.
const (
	queueUpdateStep = "-update"
	queueMergeStep  = "-merge"
)

// updateOptions cover a branch update pod: it runs only git, within 10 minutes.
var updateOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 20 * time.Minute,
	HeartbeatTimeout:    time.Minute,
	RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
}

type (
	MergeQueueInput = activities.MergeQueueInput
	QueueEntry      = activities.QueueEntry
	QueueGate       = activities.QueueGate
	QueueOutcome    = activities.QueueOutcome
)

// How a change request left the queue.
const (
	QueueOutcomeMerged   = activities.QueueOutcomeMerged
	QueueOutcomeConflict = activities.QueueOutcomeConflict
	QueueOutcomeRed      = activities.QueueOutcomeRed
	QueueOutcomeMoved    = activities.QueueOutcomeMoved
	QueueOutcomeRefused  = activities.QueueOutcomeRefused
	QueueOutcomeClosed   = activities.QueueOutcomeClosed
	QueueOutcomeDequeued = activities.QueueOutcomeDequeued
	QueueOutcomeBusy     = activities.QueueOutcomeBusy
	QueueOutcomeError    = activities.QueueOutcomeError
)

// MergeQueue is a repo's merge queue (FR-MQ-1, FR-MQ-2, D87): it takes change
// requests in the order they arrive and, one at a time, brings the branch up
// to date with the base branch, re-runs the command gates when that changed
// the commit, and merges at exactly that commit while the base branch has not
// moved, then signals the issue's workflow the outcome. Dependency order is
// already FIFO: the scheduler starts an issue only once its dependencies
// merged (D62). The workflow ends when nothing is pending; the next enqueue
// starts it again.
func MergeQueue(ctx workflow.Context, in MergeQueueInput) error {
	pending := in.Pending
	ch := workflow.GetSignalChannel(ctx, MergeQueueEnqueueSignal)
	add := func(e QueueEntry) {
		for i, p := range pending {
			if p.WorkflowID == e.WorkflowID { // the issue enqueued again: the newer attempt replaces it
				pending[i] = e
				return
			}
		}
		pending = append(pending, e)
	}
	for done := 0; ; done++ {
		for {
			var e QueueEntry
			if !ch.ReceiveAsync(&e) {
				break
			}
			add(e)
		}
		if len(pending) == 0 {
			return nil
		}
		if done == maxQueueEntriesPerRun {
			return workflow.NewContinueAsNewError(ctx, MergeQueue, MergeQueueInput{Repo: in.Repo, Pending: pending})
		}
		e := pending[0]
		pending = pending[1:]
		out := processQueueEntry(ctx, e)
		// An issue workflow that ended (canceled, failed) no longer waits.
		_ = workflow.SignalExternalWorkflow(ctx, e.WorkflowID, "", MergeQueueOutcomeSignal, out).Get(ctx, nil)
	}
}

// processQueueEntry checks and merges one change request.
func processQueueEntry(ctx workflow.Context, e QueueEntry) QueueOutcome {
	var a *activities.Activities
	out := QueueOutcome{StepID: e.StepID, Attempt: e.Attempt, HeadSHA: e.HeadSHA}
	short := workflow.WithActivityOptions(ctx, shortOptions)
	end := func(kind, reason string) QueueOutcome {
		out.Kind, out.Reason = kind, reason
		return out
	}

	var state activities.QueueEntryState
	if err := workflow.ExecuteActivity(short, a.CheckQueueEntry, e.Issue, e.Number).Get(ctx, &state); err != nil {
		return end(QueueOutcomeError, "checking the queue entry failed: "+err.Error())
	}
	switch {
	case !state.Queued:
		return end(QueueOutcomeDequeued, "the issue left the queue")
	case state.ChangeRequest == domain.ChangeRequestMerged:
		return end(QueueOutcomeMerged, "merged on the provider")
	case state.ChangeRequest == domain.ChangeRequestClosed:
		return end(QueueOutcomeClosed, "the change request was closed")
	}

	head := e.HeadSHA
	for try := 1; try <= maxMergeTries; try++ {
		attempt := (max(e.Attempt, 1)-1)*maxMergeTries + try
		start := func(suffix string, kind domain.StepKind) (string, error) {
			var id string
			err := workflow.ExecuteActivity(short, a.StartStep, activities.StepStart{RunID: e.RunID, StepID: e.StepID + suffix, Kind: kind, Attempt: attempt}).Get(ctx, &id)
			return id, err
		}

		updateID, err := start(queueUpdateStep, domain.StepAction)
		if err != nil {
			return end(QueueOutcomeError, err.Error())
		}
		var u activities.BranchUpdateResult
		err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, updateOptions), a.UpdateBranch, activities.BranchUpdateRequest{
			Issue: e.Issue, RunID: e.RunID, StepRunID: updateID, StepID: e.StepID + queueUpdateStep,
			Branch: e.Branch, HeadSHA: head, Method: e.Method,
		}).Get(ctx, &u)
		if err != nil {
			endStep(ctx, updateID, false)
			return end(QueueOutcomeError, "updating the branch failed: "+err.Error())
		}
		switch u.Outcome {
		case activities.BranchMoved:
			out.HeadSHA = u.HeadSHA
			return end(QueueOutcomeMoved, fmt.Sprintf("%s moved to %s while queued", e.Branch, u.HeadSHA))
		case activities.BranchConflict:
			out.Conflicts = u.Conflicts
			return end(QueueOutcomeConflict, fmt.Sprintf("%s conflicts with %s", e.Branch, e.Issue.BaseBranch))
		case activities.BranchUpdated:
			// The update is new code: the command gates check it again
			// (FR-MQ-2). An unchanged commit already passed them.
			head = u.HeadSHA
			out.HeadSHA = head
			if o, failed := runQueueGates(ctx, e, head, attempt); failed {
				o.StepID, o.Attempt = out.StepID, out.Attempt
				return o
			}
		}

		mergeID, err := start(queueMergeStep, domain.StepAction)
		if err != nil {
			return end(QueueOutcomeError, err.Error())
		}
		var r activities.QueueMergeResult
		err = workflow.ExecuteActivity(short, a.MergeQueued, activities.QueueMergeRequest{
			Issue: e.Issue, StepRunID: mergeID, ChangeRequestID: e.ChangeRequestID, Number: e.Number,
			HeadSHA: head, BaseSHA: u.BaseSHA, Method: e.Method, Title: e.Title,
		}).Get(ctx, &r)
		if err != nil {
			endStep(ctx, mergeID, false)
			return end(QueueOutcomeError, "merging failed: "+err.Error())
		}
		switch r.Outcome {
		case activities.QueueMerged:
			out.MergeSHA = r.MergeSHA
			return end(QueueOutcomeMerged, "")
		case activities.QueueHeadMoved:
			return end(QueueOutcomeMoved, r.Reason)
		case activities.QueueRefused:
			return end(QueueOutcomeRefused, r.Reason)
		}
		// The base branch moved after the update: take it in and check again.
	}
	return end(QueueOutcomeBusy, fmt.Sprintf("%s kept moving; tried %d times", e.Issue.BaseBranch, maxMergeTries))
}

// runQueueGates re-runs the entry's command gates on head; failed is true with
// the outcome when one did not pass.
func runQueueGates(ctx workflow.Context, e QueueEntry, head string, attempt int) (QueueOutcome, bool) {
	var a *activities.Activities
	for _, g := range e.Gates {
		stepID := e.StepID + "-" + g.StepID
		var id string
		if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), a.StartStep,
			activities.StepStart{RunID: e.RunID, StepID: stepID, Kind: domain.StepGate, Attempt: attempt}).Get(ctx, &id); err != nil {
			return QueueOutcome{Kind: QueueOutcomeError, Reason: err.Error(), HeadSHA: head}, true
		}
		opts := gateOptions
		if g.Timeout > 0 {
			opts.StartToCloseTimeout = g.Timeout + 120*time.Minute
		}
		var o activities.GateOutcome
		err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, opts), a.RunGate, e.Issue, activities.GateRequest{
			RunID: e.RunID, StepRunID: id, StepID: stepID, Commands: g.Commands, Timeout: g.Timeout,
			Branch: e.Branch, CommitSHA: head,
		}).Get(ctx, &o)
		switch {
		case err != nil:
			endStep(ctx, id, false)
			return QueueOutcome{Kind: QueueOutcomeError, Reason: "gate could not run: " + err.Error(), HeadSHA: head}, true
		case !o.Passed:
			return QueueOutcome{Kind: QueueOutcomeRed, Reason: fmt.Sprintf("the `%s` check failed after updating with %s: %s", g.StepID, e.Issue.BaseBranch, o.Summary),
				Feedback: o.Feedback, HeadSHA: head}, true
		}
	}
	return QueueOutcome{}, false
}

// endStep ends a StepRun whose Activity failed, even while canceling.
func endStep(ctx workflow.Context, stepRunID string, succeeded bool) {
	var a *activities.Activities
	cleanup, _ := workflow.NewDisconnectedContext(ctx)
	cleanup = workflow.WithActivityOptions(cleanup, shortOptions)
	_ = workflow.ExecuteActivity(cleanup, a.FinishStep, activities.StepEnd{StepRunID: stepRunID, Succeeded: succeeded}).Get(cleanup, nil)
}
