package activities

import (
	"context"
	"encoding/json"
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
	Action    string // domain.InterventionSteer or domain.InterventionAbort
	By        string
	// Text is the steering message, or the reason for an abort.
	Text string
}

// InterveneSession delivers a steer or an abort to the step's runner and
// records it in the run's transcript. A session that already finished
// cannot take it any more; that is recorded rather than failed, since the
// person acted on a step that ended meanwhile.
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
	default:
		return false, fmt.Errorf("unknown intervention %q", in.Action)
	}
	delivered := err == nil
	if err != nil && status.Code(err) != codes.FailedPrecondition {
		return false, fmt.Errorf("runner %s: %w", in.Action, err)
	}

	payload, err := json.Marshal(domain.Intervention{Action: in.Action, By: in.By, Text: in.Text, Delivered: delivered})
	if err != nil {
		return false, err
	}
	runID, err1 := uuid.Parse(in.RunID)
	stepID, err2 := uuid.Parse(in.StepRunID)
	if err1 != nil || err2 != nil {
		return false, fmt.Errorf("intervention for run %q step %q: bad id", in.RunID, in.StepRunID)
	}
	if _, err := a.Store.AppendEvent(ctx, store.NewEvent{RunID: &runID, StepRunID: &stepID, Ts: time.Now(),
		Type: domain.EventIntervention, Payload: payload}); err != nil {
		return false, err
	}
	return delivered, nil
}
