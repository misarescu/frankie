package workflow

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// releaseFixture records what the release workflow asked its Activities to do.
type releaseFixture struct {
	env      *testsuite.TestWorkflowEnvironment
	calls    []string // "<activity> <stage or sha>"
	finished []activities.FinishReleaseRequest
	ended    []activities.EndDeploymentRequest

	plan     func(sha string) activities.ReleasePlan
	publish  func(req activities.StageRequest) error
	await    func(req activities.StageRequest) activities.StageStatus
	verify   func(req activities.StageRequest) activities.Verification
	rollback func(req activities.StageRequest) error
	// awaitDelay makes waiting for a stage take this long (test clock).
	awaitDelay time.Duration
}

var (
	stagingStage = activities.ReleaseStage{Name: "staging", Driver: "flux", Verify: "e2e-suite", URL: "https://stg", Rollback: true}
	prodStage    = activities.ReleaseStage{Name: "production", Driver: "flux", Rollback: true}
)

func newReleaseFixture(t *testing.T, opts ...func(*releaseFixture)) *releaseFixture {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	f := &releaseFixture{env: s.NewTestWorkflowEnvironment()}
	f.plan = func(sha string) activities.ReleasePlan {
		return activities.ReleasePlan{ReleaseID: "rel-" + sha, Stages: []activities.ReleaseStage{stagingStage, prodStage}, Status: domain.ReleaseInProgress}
	}
	for _, o := range opts {
		o(f)
	}
	a := &activities.Activities{}
	f.env.RegisterActivity(a)
	f.env.OnActivity(a.PrepareRelease, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.PrepareReleaseRequest) (activities.ReleasePlan, error) {
		f.calls = append(f.calls, "prepare "+req.SHA)
		return f.plan(req.SHA), nil
	})
	f.env.OnActivity(a.StartDeployment, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.StageRequest) (string, error) {
		f.calls = append(f.calls, "start "+req.Stage.Name)
		return "dep-" + req.Stage.Name, nil
	})
	f.env.OnActivity(a.PublishStage, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.StageRequest) error {
		f.calls = append(f.calls, "publish "+req.Stage.Name)
		if req.DeploymentID != "dep-"+req.Stage.Name {
			return temporal.NewNonRetryableApplicationError("no deployment id", "test", nil)
		}
		if f.publish != nil {
			return f.publish(req)
		}
		return nil
	})
	f.env.OnActivity(a.AwaitStage, mock.Anything, mock.Anything).After(f.awaitDelay).Return(func(_ context.Context, req activities.StageRequest) (activities.StageStatus, error) {
		f.calls = append(f.calls, "await "+req.Stage.Name)
		if f.await != nil {
			return f.await(req), nil
		}
		return activities.StageStatus{Ready: true}, nil
	})
	f.env.OnActivity(a.VerifyStage, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.StageRequest) (activities.Verification, error) {
		f.calls = append(f.calls, "verify "+req.Stage.Name)
		if f.verify != nil {
			return f.verify(req), nil
		}
		return activities.Verification{Passed: true}, nil
	})
	f.env.OnActivity(a.EndDeployment, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.EndDeploymentRequest) error {
		f.ended = append(f.ended, req)
		return nil
	})
	f.env.OnActivity(a.RollbackStage, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.StageRequest) error {
		f.calls = append(f.calls, "rollback "+req.Stage.Name)
		if f.rollback != nil {
			return f.rollback(req)
		}
		return nil
	})
	f.env.OnActivity(a.FinishRelease, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.FinishReleaseRequest) error {
		f.finished = append(f.finished, req)
		return nil
	})
	return f
}

func (f *releaseFixture) run(t *testing.T, shas ...string) {
	t.Helper()
	in := ReleaseInput{Repo: "misarescu/Goodreads-Clone", Pending: &ReleaseRequest{SHA: shas[0]}}
	for i, sha := range shas[1:] {
		f.env.RegisterDelayedCallback(func() { f.env.SignalWorkflow(ReleaseRequestSignal, ReleaseRequest{SHA: sha}) }, time.Duration(i+1)*time.Second)
	}
	f.env.ExecuteWorkflow(Release, in)
	if !f.env.IsWorkflowCompleted() || f.env.GetWorkflowError() != nil {
		t.Fatalf("workflow: completed %v, %v", f.env.IsWorkflowCompleted(), f.env.GetWorkflowError())
	}
}

// M7, D114: a release that passes every stage succeeds.
func TestReleaseSucceeds(t *testing.T) {
	f := newReleaseFixture(t)
	f.run(t, shaA)
	want := []string{"prepare " + shaA,
		"start staging", "publish staging", "await staging", "verify staging",
		"start production", "publish production", "await production", "verify production"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	if len(f.ended) != 2 || !f.ended[0].Succeeded || !f.ended[1].Succeeded {
		t.Errorf("deployments ended %+v, want both succeeded", f.ended)
	}
	if len(f.finished) != 1 || f.finished[0] != (activities.FinishReleaseRequest{ReleaseID: "rel-" + shaA, Status: domain.ReleaseSucceeded}) {
		t.Errorf("finished = %+v", f.finished)
	}
}

// FR-RL-2: a stage that fails stops the release and is rolled back when it allows.
func TestReleaseRollsBackAFailedStage(t *testing.T) {
	f := newReleaseFixture(t, func(f *releaseFixture) {
		f.verify = func(req activities.StageRequest) activities.Verification {
			return activities.Verification{Summary: "1 of 12 tests failed: Books/search"}
		}
	})
	f.run(t, shaA)
	want := []string{"prepare " + shaA, "start staging", "publish staging", "await staging", "verify staging", "rollback staging"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	reason := "staging: verification failed: 1 of 12 tests failed: Books/search"
	if len(f.ended) != 1 || f.ended[0] != (activities.EndDeploymentRequest{DeploymentID: "dep-staging", Reason: reason}) {
		t.Errorf("ended = %+v", f.ended)
	}
	if len(f.finished) != 1 || f.finished[0] != (activities.FinishReleaseRequest{ReleaseID: "rel-" + shaA, Status: domain.ReleaseRolledBack, Reason: reason}) {
		t.Errorf("finished = %+v", f.finished)
	}
}

func TestReleaseFailures(t *testing.T) {
	tests := []struct {
		name       string
		opt        func(*releaseFixture)
		wantCalls  []string
		wantStatus domain.ReleaseStatus
		wantReason string
	}{
		{
			name: "not ready, no rollback",
			opt: func(f *releaseFixture) {
				f.plan = func(sha string) activities.ReleasePlan {
					st := stagingStage
					st.Rollback = false
					return activities.ReleasePlan{ReleaseID: "rel", Stages: []activities.ReleaseStage{st, prodStage}}
				}
				f.await = func(activities.StageRequest) activities.StageStatus {
					return activities.StageStatus{Reason: "staging did not run aaaaaaa within 15m0s"}
				}
			},
			wantCalls:  []string{"start staging", "publish staging", "await staging"},
			wantStatus: domain.ReleaseFailed, wantReason: "staging: staging did not run aaaaaaa within 15m0s",
		},
		{
			name: "publishing fails",
			opt: func(f *releaseFixture) {
				f.publish = func(activities.StageRequest) error {
					return temporal.NewNonRetryableApplicationError("git push refused", "driver", nil)
				}
			},
			wantCalls:  []string{"start staging", "publish staging", "rollback staging"},
			wantStatus: domain.ReleaseRolledBack, wantReason: "staging: publishing failed",
		},
		{
			name: "rolling back fails",
			opt: func(f *releaseFixture) {
				f.await = func(activities.StageRequest) activities.StageStatus {
					return activities.StageStatus{Reason: "health check failed"}
				}
				f.rollback = func(activities.StageRequest) error {
					return temporal.NewNonRetryableApplicationError("revert conflicts", "driver", nil)
				}
			},
			wantCalls:  []string{"start staging", "publish staging", "await staging", "rollback staging"},
			wantStatus: domain.ReleaseFailed, wantReason: "staging: health check failed; rolling back failed",
		},
		{
			name: "the second stage fails",
			opt: func(f *releaseFixture) {
				f.await = func(req activities.StageRequest) activities.StageStatus {
					if req.Stage.Name == "production" {
						return activities.StageStatus{Reason: "health check failed"}
					}
					return activities.StageStatus{Ready: true}
				}
			},
			wantCalls: []string{"start staging", "publish staging", "await staging", "verify staging",
				"start production", "publish production", "await production", "rollback production"},
			wantStatus: domain.ReleaseRolledBack, wantReason: "production: health check failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newReleaseFixture(t, tt.opt)
			f.run(t, shaA)
			if got := f.calls[1:]; !slices.Equal(got, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", got, tt.wantCalls)
			}
			if len(f.finished) != 1 || f.finished[0].Status != tt.wantStatus || !strings.HasPrefix(f.finished[0].Reason, tt.wantReason) {
				t.Errorf("finished = %+v, want %s: %s", f.finished, tt.wantStatus, tt.wantReason)
			}
		})
	}
}

// A release that is skipped, or failed before its stages, runs nothing more.
func TestReleaseNotRun(t *testing.T) {
	for name, plan := range map[string]activities.ReleasePlan{
		"skipped": {Skip: "the project has no release stages"},
		"invalid": {ReleaseID: "rel", Failed: "stages.prod: gate human is not supported yet (M8)", Status: domain.ReleaseFailed},
	} {
		t.Run(name, func(t *testing.T) {
			f := newReleaseFixture(t, func(f *releaseFixture) {
				f.plan = func(string) activities.ReleasePlan { return plan }
			})
			f.run(t, shaA)
			if !slices.Equal(f.calls, []string{"prepare " + shaA}) || len(f.finished) != 0 {
				t.Errorf("calls = %v, finished %+v; want only the preparation", f.calls, f.finished)
			}
		})
	}
}

// Requests that arrive while a release runs are batched into one release of
// the newest commit.
func TestReleaseBatchesRequests(t *testing.T) {
	shaC := strings.Repeat("c", 40)
	f := newReleaseFixture(t, func(f *releaseFixture) {
		f.awaitDelay = time.Minute
		f.plan = func(sha string) activities.ReleasePlan {
			return activities.ReleasePlan{ReleaseID: "rel-" + sha, Stages: []activities.ReleaseStage{stagingStage}}
		}
	})
	f.run(t, shaA, shaB, shaC)
	var prepared []string
	for _, c := range f.calls {
		if sha, ok := strings.CutPrefix(c, "prepare "); ok {
			prepared = append(prepared, sha)
		}
	}
	if !slices.Equal(prepared, []string{shaA, shaC}) {
		t.Errorf("released %v, want %s then only the newest %s", prepared, shaA, shaC)
	}
	if len(f.finished) != 2 || f.finished[1].ReleaseID != "rel-"+shaC {
		t.Errorf("finished = %+v", f.finished)
	}
}

// Recorded histories (record_test.go) keep replaying as the workflow changes.
func TestReleaseReplaysRecordedHistories(t *testing.T) {
	r := worker.NewWorkflowReplayer()
	r.RegisterWorkflow(Release)
	for _, f := range []string{"release_succeeded.json", "release_verify_failed_rolled_back.json", "release_batched.json"} {
		if err := r.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/"+f); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
}
