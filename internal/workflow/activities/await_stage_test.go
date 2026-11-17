package activities

import (
	"errors"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/drivers/driverstest"
)

func (f *releaseFixture) await(t *testing.T, req StageRequest) StageStatus {
	t.Helper()
	v, err := activityEnv(f.a).ExecuteActivity(f.a.AwaitStage, req)
	if err != nil {
		t.Fatalf("AwaitStage: %v", err)
	}
	var st StageStatus
	_ = v.Get(&st)
	return st
}

// M7, D114: the stage is awaited until its driver says it runs the release,
// fails, or the timeout passes.
func TestAwaitStage(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	req := f.stageRequest(t, 0)
	if st := f.await(t, req); !st.Ready {
		t.Errorf("ready driver = %+v", st)
	}

	f.flux.SetPhase(drivers.PhaseFailed, "Kustomization goodreads-staging: health check failed")
	if st := f.await(t, req); st.Ready || !strings.Contains(st.Reason, "health check failed") {
		t.Errorf("failed driver = %+v", st)
	}

	f.flux.SetPhase(drivers.PhaseProgressing, "version is a44235f")
	st := f.await(t, req)
	if st.Ready || !strings.Contains(st.Reason, "did not run cccccc") || !strings.Contains(st.Reason, "version is a44235f") {
		t.Errorf("progressing driver = %+v, want a timeout with the last message", st)
	}
	if n := len(deployCalls(f.flux)); n < 4 {
		t.Errorf("driver asked %d times, want it polled", n)
	}

	f.flux.Errs = map[string]error{driverstest.OpStatus: errors.New("connection refused")}
	if st := f.await(t, req); st.Ready || !strings.Contains(st.Reason, "connection refused") {
		t.Errorf("failing driver = %+v, want the last error as the reason", st)
	}
	f.flux.Errs = map[string]error{driverstest.OpStatus: drivers.ErrUnsupported}
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.AwaitStage, req); err == nil {
		t.Error("a driver refusal was polled until the timeout")
	}
}
