package workflow

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"

	"github.com/misarescu/frankie/internal/workflow/activities"
)

var testInput = activities.IssueRunInput{IssueID: "0198f2a4-1111-7000-8000-000000000001", IssueKey: "gc-1", Title: "Add search",
	Spec: "Users can search books.", Repo: "misarescu/Goodreads-Clone", BaseBranch: "main", SourceNumber: 3}

var testRun = activities.StartRunResult{RunID: "0198f2a4-2222-7000-8000-000000000002", StepRunID: "0198f2a4-3333-7000-8000-000000000003", Branch: "agent/gc-1/add-search"}

// toolchainRef is what the mocked EnsureToolchainImage resolves; the agent step
// must receive it in the run it is launched with.
const toolchainRef = "localhost:30500/toolchains/misarescu/goodreads-clone:tc-0123"

var testRunInImage = func() activities.StartRunResult { r := testRun; r.ToolchainImage = toolchainRef; return r }()

type fixture struct {
	env      *testsuite.TestWorkflowEnvironment
	finished *activities.FinishInput
	deleted  []string
}

func newEnv(t *testing.T) *fixture { return newEnvWithToolchain(t, toolchainRef, nil) }

func newEnvWithToolchain(t *testing.T, image string, err error) *fixture {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	f := &fixture{env: s.NewTestWorkflowEnvironment()}
	a := &activities.Activities{}
	f.env.RegisterActivity(a)
	f.env.OnActivity(a.StartRun, mock.Anything, testInput).Return(testRun, nil)
	f.env.OnActivity(a.EnsureToolchainImage, mock.Anything, testInput).Return(image, err)
	f.env.OnActivity(a.DeleteAgentPod, mock.Anything, mock.Anything).Return(func(_ context.Context, name string) error {
		f.deleted = append(f.deleted, name)
		return nil
	})
	f.env.OnActivity(a.FinishRun, mock.Anything, mock.Anything).Return(func(_ context.Context, in activities.FinishInput) error {
		f.finished = &in
		return nil
	})
	return f
}

func (f *fixture) run(t *testing.T) IssueRunResult {
	t.Helper()
	f.env.ExecuteWorkflow(IssueRun, testInput)
	if !f.env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := f.env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	var out IssueRunResult
	if err := f.env.GetWorkflowResult(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIssueRunOpensChangeRequest(t *testing.T) {
	f := newEnv(t)
	a := &activities.Activities{}
	f.env.OnActivity(a.LaunchAgentPod, mock.Anything, testInput, testRunInImage).Return(activities.AgentPod{Name: "gc-1-implement-x", IP: "10.42.0.9"}, nil)
	session := activities.SessionResult{Status: "succeeded", Branch: testRun.Branch, CommitSHA: "abc", Pushed: true, Result: "Added search."}
	f.env.OnActivity(a.RunAgentSession, mock.Anything, testInput, testRunInImage, activities.AgentPod{Name: "gc-1-implement-x", IP: "10.42.0.9"}).Return(session, nil)
	f.env.OnActivity(a.OpenChangeRequest, mock.Anything, testInput, testRunInImage, session).Return("https://github.com/misarescu/Goodreads-Clone/pull/4", nil)

	out := f.run(t)
	if !out.Succeeded || out.ChangeRequestURL == "" || out.RunID != testRun.RunID {
		t.Errorf("result = %+v", out)
	}
	if len(f.deleted) != 1 || f.deleted[0] != "gc-1-implement-x" {
		t.Errorf("deleted pods = %v", f.deleted)
	}
	if f.finished == nil || !f.finished.Succeeded || f.finished.StepRunID != testRun.StepRunID {
		t.Errorf("finish = %+v", f.finished)
	}
}

func TestIssueRunFailures(t *testing.T) {
	pod := activities.AgentPod{Name: "gc-1-implement-x", IP: "10.42.0.9"}
	tests := []struct {
		name       string
		launchErr  error
		session    activities.SessionResult
		wantReason string
		wantDelete bool
	}{
		{name: "agent failed", session: activities.SessionResult{Status: "failed", Reason: "tests did not pass"}, wantReason: "agent session failed", wantDelete: true},
		{name: "no changes", session: activities.SessionResult{Status: "succeeded"}, wantReason: "made no changes", wantDelete: true},
		{name: "launch failed", launchErr: errors.New("ImagePullBackOff"), wantReason: "agent step failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newEnv(t)
			a := &activities.Activities{}
			if tt.launchErr != nil {
				f.env.OnActivity(a.LaunchAgentPod, mock.Anything, testInput, testRunInImage).Return(activities.AgentPod{}, tt.launchErr)
			} else {
				f.env.OnActivity(a.LaunchAgentPod, mock.Anything, testInput, testRunInImage).Return(pod, nil)
				f.env.OnActivity(a.RunAgentSession, mock.Anything, testInput, testRunInImage, pod).Return(tt.session, nil)
			}
			out := f.run(t)
			if out.Succeeded || f.finished == nil || f.finished.Succeeded {
				t.Fatalf("result %+v, finish %+v; want a failed run", out, f.finished)
			}
			if !strings.Contains(out.Reason, tt.wantReason) {
				t.Errorf("reason %q, want it to contain %q", out.Reason, tt.wantReason)
			}
			if (len(f.deleted) == 1) != tt.wantDelete {
				t.Errorf("deleted pods = %v, want delete=%v", f.deleted, tt.wantDelete)
			}
		})
	}
}

func TestIssueRunLaunchesInTheBaseImageWithoutAToolchain(t *testing.T) {
	f := newEnvWithToolchain(t, "", nil)
	a := &activities.Activities{}
	pod := activities.AgentPod{Name: "gc-1-implement-x", IP: "10.42.0.9"}
	session := activities.SessionResult{Status: "succeeded", Branch: testRun.Branch, CommitSHA: "abc", Pushed: true}
	// testRun has no ToolchainImage: the pod falls back to the base image.
	f.env.OnActivity(a.LaunchAgentPod, mock.Anything, testInput, testRun).Return(pod, nil)
	f.env.OnActivity(a.RunAgentSession, mock.Anything, testInput, testRun, pod).Return(session, nil)
	f.env.OnActivity(a.OpenChangeRequest, mock.Anything, testInput, testRun, session).Return("https://github.com/misarescu/Goodreads-Clone/pull/4", nil)
	if out := f.run(t); !out.Succeeded {
		t.Errorf("result = %+v", out)
	}
}

// A toolchain build that fails fails the run before any agent pod exists, and
// the failure is recorded like any other.
func TestIssueRunFailsWhenTheToolchainImageFails(t *testing.T) {
	f := newEnvWithToolchain(t, "", errors.New("mise install exited 1"))
	out := f.run(t)
	if out.Succeeded || !strings.Contains(out.Reason, "toolchain image") || !strings.Contains(out.Reason, "mise install exited 1") {
		t.Errorf("result = %+v", out)
	}
	if f.finished == nil || f.finished.Succeeded || len(f.deleted) != 0 {
		t.Errorf("finish = %+v, deleted = %v; want a failed run and no pod", f.finished, f.deleted)
	}
}

// Invariant 3: the workflow must replay recorded histories. The fixture is the
// first successful M1 acceptance run on the homelab (issue gc-1 -> PR #3).
// A failure here means a change broke determinism and needs workflow.GetVersion.
func TestIssueRunReplaysRecordedHistory(t *testing.T) {
	r := worker.NewWorkflowReplayer()
	r.RegisterWorkflow(IssueRun)
	if err := r.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/issue_run_m1_success.json"); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

// The M2 path: a history recorded from this workflow with the toolchain-image
// version marker and EnsureToolchainImage (stub activities on a local Temporal
// dev server, exported with `temporal workflow show --output json`).
func TestIssueRunReplaysToolchainImageHistory(t *testing.T) {
	r := worker.NewWorkflowReplayer()
	r.RegisterWorkflow(IssueRun)
	if err := r.ReplayWorkflowHistoryFromJSONFile(nil, "testdata/issue_run_toolchain_image.json"); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

// Credentials go to the runner over gRPC, never into the workflow history.
func TestRecordedHistoryHasNoSecrets(t *testing.T) {
	for _, file := range []string{"testdata/issue_run_m1_success.json", "testdata/issue_run_toolchain_image.json"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{"sk-", "fgp1.", "github_pat_", "ANTHROPIC_AUTH_TOKEN"} {
			if strings.Contains(string(raw), marker) {
				t.Errorf("%s contains %q", file, marker)
			}
		}
	}
}
