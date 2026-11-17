package activities

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/misarescu/frankie/internal/drivers"
)

// StageStatus is whether a stage came to run a release.
type StageStatus struct {
	Ready  bool
	Reason string `json:",omitempty"` // why not
}

// AwaitStage asks the stage's driver, every Release.PollInterval, whether the
// stage runs the release, until it does, the driver reports it failed, or
// Release.StageTimeout passes (D114). A driver error is retried at the next
// poll until the timeout, where the last one is the reason.
func (a *Activities) AwaitStage(ctx context.Context, req StageRequest) (StageStatus, error) {
	c, err := a.resolve(ctx, req)
	if err != nil {
		return StageStatus{}, err
	}
	return a.awaitDriver(ctx, c.driver, c.request, req.Stage.Name, req.SHA, a.Release.stageTimeout())
}

// awaitDriver asks d, every Release.PollInterval, whether stage runs r's
// subject, until it does, the driver reports it failed, or timeout passes. A
// driver error is retried at the next poll until the timeout, where the last
// one is the reason.
func (a *Activities) awaitDriver(ctx context.Context, d drivers.Driver, r drivers.Request, stage, sha string, timeout time.Duration) (StageStatus, error) {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		st, err := d.Status(ctx, r)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return StageStatus{}, ctx.Err()
			}
			if errors.Is(err, drivers.ErrUnsupported) || errors.Is(err, drivers.ErrInvalidRequest) {
				return StageStatus{}, driverError("status of "+stage, err)
			}
			last = "the driver failed: " + err.Error()
		case st.Phase == drivers.PhaseReady:
			return StageStatus{Ready: true}, nil
		case st.Phase == drivers.PhaseFailed:
			return StageStatus{Reason: fmt.Sprintf("%s failed: %s", stage, orDefault(st.Message, "the driver reported a failure"))}, nil
		default:
			last = orDefault(st.Message, "still progressing")
		}
		if time.Now().After(deadline) {
			return StageStatus{Reason: fmt.Sprintf("%s did not run %s within %s: %s", stage, short(sha), timeout, last)}, nil
		}
		activity.RecordHeartbeat(ctx, last)
		select {
		case <-ctx.Done():
			return StageStatus{}, ctx.Err()
		case <-time.After(a.Release.pollInterval()):
		}
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// short is a commit's first seven characters.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
