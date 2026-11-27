package workflow

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// releaseFixture records what the release workflow asked its Activities to do.
type releaseFixture struct {
	env      *testsuite.TestWorkflowEnvironment
	calls    []string // "<activity> <stage or sha>"
	finished []activities.FinishReleaseRequest
	ended    []activities.EndDeploymentRequest
	reported []activities.ReleaseFailureReport

	plan     func(sha string) activities.ReleasePlan
	publish  func(req activities.StageRequest) error
	await    func(req activities.StageRequest) activities.StageStatus
	verify   func(req activities.StageRequest) activities.Verification
	rollback func(req activities.StageRequest) error
	// awaitDelay makes waiting for a stage take this long (test clock).
	awaitDelay time.Duration

	// The release agent (D129): agentLog records its Activities and the
	// release's end in order (mu guards it, as the notes session runs next
	// to the stages); agent decides each session's end, sessionDelay how
	// long it takes; sessions and notes record the requests.
	mu           sync.Mutex
	agentLog     []string
	agent        func(req activities.ReleaseAgentRequest) activities.SessionResult
	sessionDelay time.Duration
	sessions     []activities.ReleaseAgentRequest
	notes        []activities.ReleaseNotesRequest

	// Human gates before stages (D139): gates opened, and how they closed.
	gates  []activities.ReleaseGateRequest
	closed []activities.HumanGateClose
}

func (f *releaseFixture) logAgent(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentLog = append(f.agentLog, s)
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
		f.logAgent("finish " + string(req.Status))
		return nil
	})
	f.env.OnActivity(a.PrepareReleaseSession, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.ReleaseAgentRequest) (activities.ReleaseSession, error) {
		f.mu.Lock()
		f.sessions = append(f.sessions, req)
		f.mu.Unlock()
		f.logAgent("session " + req.Step)
		id := "sr-" + req.Step
		return activities.ReleaseSession{Issue: activities.IssueRunInput{IssueKey: "release-x", Repo: "misarescu/Goodreads-Clone"},
			Run: activities.StartRunResult{RunID: "run-" + req.ReleaseID, StepRunID: id, ReleaseID: req.ReleaseID,
				Step: &activities.AgentStep{StepID: req.Step, Timeout: 20 * time.Minute}}}, nil
	})
	f.env.OnActivity(a.EnsureStepImage, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("toolchain@sha256:1", nil)
	f.env.OnActivity(a.LaunchAgentPod, mock.Anything, mock.Anything, mock.Anything).Return(func(_ context.Context, _ activities.IssueRunInput, run activities.StartRunResult) (activities.AgentPod, error) {
		return activities.AgentPod{Name: "pod-" + run.StepRunID, IP: "10.0.0.1"}, nil
	})
	f.env.OnActivity(a.RunAgentSession, mock.Anything, mock.Anything, mock.Anything, mock.Anything).After(f.sessionDelay).Return(
		func(_ context.Context, _ activities.IssueRunInput, run activities.StartRunResult, _ activities.AgentPod) (activities.SessionResult, error) {
			f.mu.Lock()
			req := f.sessions[len(f.sessions)-1]
			for _, s := range f.sessions {
				if s.Step == run.Step.StepID {
					req = s
				}
			}
			f.mu.Unlock()
			if f.agent != nil {
				return f.agent(req), nil
			}
			return activities.SessionResult{Status: "succeeded", Result: `{"bump":"minor","reason":"r","notes":"- gc-1"}`}, nil
		})
	f.env.OnActivity(a.DeleteAgentPod, mock.Anything, mock.Anything).Return(nil)
	f.env.OnActivity(a.FinishStep, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.StepEnd) error {
		f.logAgent(fmt.Sprintf("end %s %v", in.StepRunID, in.Succeeded))
		return nil
	})
	f.env.OnActivity(a.RecordReleaseNotes, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.ReleaseNotesRequest) (domain.NotesSource, error) {
		f.mu.Lock()
		f.notes = append(f.notes, req)
		f.mu.Unlock()
		f.logAgent("notes")
		return domain.NotesByAgent, nil
	})
	f.env.OnActivity(a.PublishVersion, mock.Anything, mock.Anything).Return(func(_ context.Context, id string) (activities.PublishedVersion, error) {
		f.logAgent("publish " + id)
		return activities.PublishedVersion{Version: "v0.1.0"}, nil
	})
	f.env.OnActivity(a.FinishReleaseRun, mock.Anything, mock.Anything).Return(func(_ context.Context, id string) error {
		f.logAgent("finish-run " + id)
		return nil
	})
	f.env.OnActivity(a.OpenReleaseGate, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.ReleaseGateRequest) (string, error) {
		f.gates = append(f.gates, req)
		f.calls = append(f.calls, "gate "+req.Stage)
		return "gate-" + req.Stage, nil
	})
	f.env.OnActivity(a.CloseHumanGate, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.HumanGateClose) error {
		f.closed = append(f.closed, in)
		f.calls = append(f.calls, "close "+string(in.Status))
		return nil
	})
	f.env.OnActivity(a.ReportReleaseFailure, mock.Anything, mock.Anything).Return(func(_ context.Context, req activities.ReleaseFailureReport) (string, error) {
		f.reported = append(f.reported, req)
		return "https://github.com/misarescu/Goodreads-Clone/issues/131", nil
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
	// D115: the failed verification opens a bug issue.
	if len(f.reported) != 1 {
		t.Fatalf("reported = %+v, want one bug issue", f.reported)
	}
	r := f.reported[0]
	if r.ReleaseID != "rel-"+shaA || r.Repo != "misarescu/Goodreads-Clone" || r.SHA != shaA || r.Stage != "staging" || r.URL != "https://stg" ||
		!r.RolledBack || r.Verification.Summary != "1 of 12 tests failed: Books/search" {
		t.Errorf("report = %+v", r)
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
			name: "verification inconclusive",
			opt: func(f *releaseFixture) {
				f.verify = func(activities.StageRequest) activities.Verification {
					return activities.Verification{Inconclusive: true, Summary: "1 of 1 tests could not run (e.g. Users > request: ECONNREFUSED)"}
				}
			},
			wantCalls:  []string{"start staging", "publish staging", "await staging", "verify staging", "rollback staging"},
			wantStatus: domain.ReleaseRolledBack, wantReason: "staging: verification could not judge the release: 1 of 1 tests could not run",
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
			// D115: only a failed verification is a bug in the release.
			if len(f.reported) != 0 {
				t.Errorf("reported %+v, want no bug issue", f.reported)
			}
		})
	}
}

// A failed verification on a stage that is not rolled back says so in its bug issue.
func TestReleaseReportsVerificationFailureWithoutRollback(t *testing.T) {
	f := newReleaseFixture(t, func(f *releaseFixture) {
		f.plan = func(sha string) activities.ReleasePlan {
			st := stagingStage
			st.Rollback = false
			return activities.ReleasePlan{ReleaseID: "rel", Stages: []activities.ReleaseStage{st}}
		}
		f.verify = func(activities.StageRequest) activities.Verification {
			return activities.Verification{Summary: "2 failed"}
		}
	})
	f.run(t, shaA)
	if len(f.reported) != 1 || f.reported[0].RolledBack || f.finished[0].Status != domain.ReleaseFailed {
		t.Errorf("reported = %+v, finished %+v; want one report, not rolled back", f.reported, f.finished)
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
	for _, f := range []string{"release_succeeded.json", "release_verify_failed_rolled_back.json", "release_batched.json", "release_bug_issue.json",
		"release_agent_versioned.json", "release_agent_analysis.json", "release_gate_approved.json"} {
		if err := r.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/"+f); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
}

var gatedProdStage = activities.ReleaseStage{Name: "production", Driver: "flux", Rollback: true, Gate: config.GateHuman}

// gatedFixture releases through staging and a production stage with a human
// gate; each decision is signalled at its time on the test clock.
func gatedFixture(t *testing.T, decisions map[time.Duration]HumanDecision) *releaseFixture {
	t.Helper()
	f := newReleaseFixture(t, func(f *releaseFixture) {
		f.plan = func(sha string) activities.ReleasePlan {
			return activities.ReleasePlan{ReleaseID: "rel-" + sha, Stages: []activities.ReleaseStage{stagingStage, gatedProdStage}, Status: domain.ReleaseInProgress}
		}
	})
	for at, d := range decisions {
		f.env.RegisterDelayedCallback(func() { f.env.SignalWorkflow(HumanDecisionSignal, d) }, at)
	}
	return f
}

var stagingCalls = []string{"start staging", "publish staging", "await staging", "verify staging"}

// M8, D139: a release waits at a gated stage until a person approves, then
// deploys to it.
func TestReleaseGateApproved(t *testing.T) {
	f := gatedFixture(t, map[time.Duration]HumanDecision{
		time.Hour:     {StepRunID: "gate-staging", Approved: true, By: "someone"}, // another gate's: ignored
		2 * time.Hour: {StepRunID: "gate-production", Approved: true, By: "mihai", Comment: "ship it"},
	})
	start := f.env.Now()
	var publishedAt time.Duration
	f.publish = func(req activities.StageRequest) error {
		if req.Stage.Name == "production" {
			publishedAt = f.env.Now().Sub(start)
		}
		return nil
	}
	f.run(t, shaA)
	want := slices.Concat([]string{"prepare " + shaA}, stagingCalls, []string{"gate production", "close approved",
		"start production", "publish production", "await production", "verify production"})
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	if publishedAt < 2*time.Hour {
		t.Errorf("production published after %s, before the approval at 2h", publishedAt)
	}
	if g := f.gates[0]; g.ReleaseID != "rel-"+shaA || g.SHA != shaA || g.Stage != "production" ||
		g.ExpiresAt.Sub(start) != time.Duration(config.DefaultHumanTimeout) {
		t.Errorf("gate = %+v", g)
	}
	if c := f.closed[0]; c != (activities.HumanGateClose{StepRunID: "gate-production", Status: domain.ApprovalApproved, By: "mihai", Comment: "ship it"}) {
		t.Errorf("closed = %+v", c)
	}
	if len(f.finished) != 1 || f.finished[0].Status != domain.ReleaseSucceeded {
		t.Errorf("finished = %+v", f.finished)
	}
	if !slices.Contains(f.agentLog, "finish-run rel-"+shaA) {
		t.Errorf("the release's run was not finished: %v", f.agentLog)
	}
}

// M8, D139: rejected or undecided, the release stops before the gated stage,
// which keeps what it ran; the stages before it keep the release.
func TestReleaseGateStops(t *testing.T) {
	tests := []struct {
		name      string
		decisions map[time.Duration]HumanDecision
		status    domain.ApprovalStatus
		reason    string
	}{
		{"rejected", map[time.Duration]HumanDecision{time.Hour: {StepRunID: "gate-production", By: "mihai", Comment: "not today"}},
			domain.ApprovalRejected, "production: rejected by mihai: not today"},
		{"no decision", nil, domain.ApprovalTimedOut, "production: no human decision within 72h0m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := gatedFixture(t, tt.decisions)
			f.run(t, shaA)
			want := slices.Concat([]string{"prepare " + shaA}, stagingCalls, []string{"gate production", "close " + string(tt.status)})
			if !slices.Equal(f.calls, want) {
				t.Errorf("calls = %v, want %v", f.calls, want)
			}
			if len(f.finished) != 1 || f.finished[0] != (activities.FinishReleaseRequest{ReleaseID: "rel-" + shaA, Status: domain.ReleaseFailed, Reason: tt.reason}) {
				t.Errorf("finished = %+v", f.finished)
			}
			if len(f.reported) != 0 {
				t.Errorf("a stopped release opened a bug issue: %+v", f.reported)
			}
		})
	}
}

// A release that fails before the gated stage never asks a person.
func TestReleaseGateNotOpenedAfterAFailedStage(t *testing.T) {
	f := gatedFixture(t, nil)
	f.verify = func(activities.StageRequest) activities.Verification {
		return activities.Verification{Summary: "1 test failed"}
	}
	f.run(t, shaA)
	if len(f.gates) != 0 || slices.Contains(f.calls, "start production") {
		t.Errorf("calls = %v, want no gate and no production", f.calls)
	}
	if len(f.finished) != 1 || f.finished[0].Status != domain.ReleaseRolledBack {
		t.Errorf("finished = %+v", f.finished)
	}
}
