package workflow

import (
	"fmt"

	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// Signals a running agent step takes from people (docs/spec/04, "Human
// intervention"); the API sends them. Pausing a step is not offered: no
// harness pauses natively, and a steer or an abort covers its uses (D52).
const (
	SteerSignal = "step-steer"
	AbortSignal = "step-abort"
)

// StepIntervention is a person's steer or abort of one agent step.
type StepIntervention struct {
	// StepRunID is the step's StepRun; one for any other step (one that
	// already ended) is ignored.
	StepRunID string
	By        string
	// Text is the steering message, or the reason for an abort.
	Text string
}

// awaitSession waits for an agent step's session and meanwhile relays the
// steers and aborts addressed to it to the step's runner. It returns the
// abort a person sent, if any: the session then ends aborted and the run
// fails with who aborted it and why.
func (it *interpreter) awaitSession(ctx workflow.Context, stepRunID string, pod activities.AgentPod, session workflow.Future, out *activities.SessionResult) (*StepIntervention, error) {
	var (
		err     error
		ended   bool
		aborted *StepIntervention
	)
	relay := func(action string, in StepIntervention) {
		if in.StepRunID != stepRunID {
			return
		}
		if action == domain.InterventionAbort && aborted == nil {
			aborted = &in
		}
		// Recording and delivery are best effort: a failure must not end a
		// session that is still working.
		_ = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, shortOptions), it.a.InterveneSession, pod, activities.Intervention{
			RunID: it.run.RunID, StepRunID: stepRunID, Action: action, By: in.By, Text: in.Text,
		}).Get(ctx, nil)
	}
	steers := workflow.GetSignalChannel(ctx, SteerSignal)
	aborts := workflow.GetSignalChannel(ctx, AbortSignal)
	sel := workflow.NewSelector(ctx)
	sel.AddFuture(session, func(f workflow.Future) {
		err = f.Get(ctx, out)
		ended = true
	})
	sel.AddReceive(steers, func(c workflow.ReceiveChannel, _ bool) {
		var in StepIntervention
		c.Receive(ctx, &in)
		relay(domain.InterventionSteer, in)
	})
	sel.AddReceive(aborts, func(c workflow.ReceiveChannel, _ bool) {
		var in StepIntervention
		c.Receive(ctx, &in)
		relay(domain.InterventionAbort, in)
	})
	for !ended {
		sel.Select(ctx)
	}
	return aborted, err
}

// abortReason is how an aborted step fails its run.
func abortReason(in *StepIntervention) string {
	return fmt.Sprintf("aborted by %s: %s", in.By, in.Text)
}
