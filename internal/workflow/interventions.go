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
	// AnswerSignal answers a question the agent asked with the ask-human
	// tool (D55).
	AnswerSignal = "step-answer"
)

// StepIntervention is a person's steer, abort or answer to one agent step.
type StepIntervention struct {
	// StepRunID is the step's StepRun; one for any other step (one that
	// already ended) is ignored.
	StepRunID string
	By        string
	// Text is the steering message, the reason for an abort, or the answer.
	Text string
	// QuestionID is the runner's ID of the question an answer answers.
	QuestionID string `json:",omitempty"`
}

// sessionInbox holds the signals routed to one of several sessions running
// at once (D122).
type sessionInbox struct {
	steers, aborts, answers workflow.Channel
}

// signalRouter hands each steer, abort and answer to the inbox of the session
// it addresses, so sessions running at once do not take each other's signals
// (D122). Its map is only indexed, never ranged over.
type signalRouter struct {
	inboxes map[string]*sessionInbox // by StepRun ID
}

func newSignalRouter() *signalRouter { return &signalRouter{inboxes: map[string]*sessionInbox{}} }

// inbox registers a session's inbox under its StepRun ID.
func (r *signalRouter) inbox(ctx workflow.Context, stepRunID string) *sessionInbox {
	box := &sessionInbox{steers: workflow.NewBufferedChannel(ctx, 16), aborts: workflow.NewBufferedChannel(ctx, 16),
		answers: workflow.NewBufferedChannel(ctx, 16)}
	r.inboxes[stepRunID] = box
	return box
}

// route receives the signals until ctx is canceled. A signal for a session
// that is not running is dropped, as awaitSession drops it.
func (r *signalRouter) route(ctx workflow.Context) {
	sel := workflow.NewSelector(ctx)
	forward := func(name string, to func(*sessionInbox) workflow.Channel) {
		sel.AddReceive(workflow.GetSignalChannel(ctx, name), func(c workflow.ReceiveChannel, _ bool) {
			var in StepIntervention
			c.Receive(ctx, &in)
			if box := r.inboxes[in.StepRunID]; box != nil {
				to(box).SendAsync(in)
			}
		})
	}
	forward(SteerSignal, func(b *sessionInbox) workflow.Channel { return b.steers })
	forward(AbortSignal, func(b *sessionInbox) workflow.Channel { return b.aborts })
	forward(AnswerSignal, func(b *sessionInbox) workflow.Channel { return b.answers })
	done := false
	sel.AddReceive(ctx.Done(), func(workflow.ReceiveChannel, bool) { done = true })
	for !done {
		sel.Select(ctx)
	}
}

// awaitSession waits for an agent step's session and meanwhile relays the
// steers and aborts addressed to it to the step's runner, and its answers
// when the session offers the ask-human tool. It returns the abort a person
// sent, if any: the session then ends aborted and the run fails with who
// aborted it and why. With an inbox, the session reads its signals there,
// routed to it, instead of from the workflow's signal channels.
func (it *interpreter) awaitSession(ctx workflow.Context, stepRunID string, pod activities.AgentPod, session workflow.Future, answers bool, out *activities.SessionResult, box *sessionInbox) (*StepIntervention, error) {
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
			RunID: it.run.RunID, StepRunID: stepRunID, Action: action, By: in.By, Text: in.Text, QuestionID: in.QuestionID,
		}).Get(ctx, nil)
	}
	var steers, aborts, answered workflow.ReceiveChannel
	if box != nil {
		steers, aborts, answered = box.steers, box.aborts, box.answers
	} else {
		steers, aborts = workflow.GetSignalChannel(ctx, SteerSignal), workflow.GetSignalChannel(ctx, AbortSignal)
		answered = workflow.GetSignalChannel(ctx, AnswerSignal)
	}
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
	if answers {
		sel.AddReceive(answered, func(c workflow.ReceiveChannel, _ bool) {
			var in StepIntervention
			c.Receive(ctx, &in)
			if in.QuestionID != "" {
				relay(domain.InterventionAnswer, in)
			}
		})
	}
	for !ended {
		sel.Select(ctx)
	}
	return aborted, err
}

// abortReason is how an aborted step fails its run.
func abortReason(in *StepIntervention) string {
	return fmt.Sprintf("aborted by %s: %s", in.By, in.Text)
}
