package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runnerv1 "github.com/misarescu/frankie/gen/frankie/runner/v1"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// Intervention is a person steering or aborting a running agent step, as the
// workflow received it (docs/spec/04, "Human intervention").
type Intervention struct {
	RunID     string
	StepRunID string
	Action    string // domain.InterventionSteer, InterventionAbort or InterventionAnswer
	By        string
	// Text is the steering message, the reason for an abort, or the answer.
	Text string
	// QuestionID is the runner's ID of the question an answer answers.
	QuestionID string `json:",omitempty"`
}

// InterveneSession delivers a steer, an abort or an answer to the step's
// runner and records it in the run's transcript. A session that already
// finished cannot take it any more, nor a question it stopped waiting for;
// that is recorded rather than failed, since the person acted on a step that
// ended meanwhile. An answer the runner took is recorded on its question
// (D55), and a question the runner no longer waits for expires.
func (a *Activities) InterveneSession(ctx context.Context, pod AgentPod, in Intervention) (bool, error) {
	conn, runner, err := a.runnerConn(pod)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()

	switch in.Action {
	case domain.InterventionSteer:
		_, err = runner.Steer(ctx, &runnerv1.SteerRequest{Text: in.Text})
	case domain.InterventionAbort:
		_, err = runner.Abort(ctx, &runnerv1.AbortRequest{Reason: fmt.Sprintf("aborted by %s: %s", in.By, in.Text)})
	case domain.InterventionAnswer:
		_, err = runner.Answer(ctx, &runnerv1.AnswerRequest{QuestionId: in.QuestionID, Text: in.Text})
	default:
		return false, fmt.Errorf("unknown intervention %q", in.Action)
	}
	delivered := err == nil
	gone := status.Code(err) == codes.FailedPrecondition ||
		(in.Action == domain.InterventionAnswer && status.Code(err) == codes.NotFound)
	if err != nil && !gone {
		return false, fmt.Errorf("runner %s: %w", in.Action, err)
	}

	runID, err1 := uuid.Parse(in.RunID)
	stepID, err2 := uuid.Parse(in.StepRunID)
	if err1 != nil || err2 != nil {
		return false, fmt.Errorf("intervention for run %q step %q: bad id", in.RunID, in.StepRunID)
	}
	if in.Action == domain.InterventionAnswer {
		if err := a.recordAnswer(ctx, stepID, in, delivered); err != nil {
			return false, err
		}
	}
	payload, err := json.Marshal(domain.Intervention{Action: in.Action, By: in.By, Text: in.Text, QuestionID: in.QuestionID, Delivered: delivered})
	if err != nil {
		return false, err
	}
	if _, err := a.Store.AppendEvent(ctx, store.NewEvent{RunID: &runID, StepRunID: &stepID, Ts: time.Now(),
		Type: domain.EventIntervention, Payload: payload}); err != nil {
		return false, err
	}
	return delivered, nil
}

// recordAnswer records on the question what became of an answer: the answer
// the runner took, or that the runner no longer waits for the question. A
// question someone else answered first keeps that answer.
func (a *Activities) recordAnswer(ctx context.Context, stepRunID uuid.UUID, in Intervention, delivered bool) error {
	if !delivered {
		_, err := a.Store.ExpireQuestion(ctx, stepRunID, in.QuestionID)
		return err
	}
	_, err := a.Store.AnswerQuestion(ctx, stepRunID, in.QuestionID, in.Text, in.By)
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
		a.Log.Warn("answer the runner took is not the question's", "step", stepRunID, "question", in.QuestionID, "error", err)
		return nil
	}
	return err
}
