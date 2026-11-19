package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/drivers/driverstest"
	"github.com/misarescu/frankie/internal/store"
)

const (
	previewHead = "dddddddddddddddddddddddddddddddddddddddd"
	previewCR   = "https://github.com/misarescu/Goodreads-Clone/pull/152"
)

// previewFixture is an issue run with an open change request #152 and a
// preview stage served by a fake driver.
type previewFixture struct {
	a        *Activities
	in       IssueRunInput
	run      StartRunResult
	driver   *driverstest.Fake
	verdict  Verification
	verified []VerifyRequest
}

func (f *previewFixture) Verify(_ context.Context, req VerifyRequest) (Verification, error) {
	f.verified = append(f.verified, req)
	return f.verdict, nil
}

func newPreviewFixture(t *testing.T) *previewFixture {
	t.Helper()
	g := newGateFixture(t)
	if _, err := g.a.Store.UpsertChangeRequest(t.Context(), store.NewChangeRequest{IssueID: uuid.MustParse(g.in.IssueID), RunID: uuid.MustParse(g.run.RunID),
		Repo: g.in.Repo, Number: 152, ProviderRef: previewCR, Branch: g.run.Branch, HeadSHA: previewHead}); err != nil {
		t.Fatal(err)
	}
	f := &previewFixture{a: g.a, in: g.in, run: g.run, verdict: Verification{Passed: true, Summary: "219 tests passed"},
		driver: &driverstest.Fake{DriverName: "flux-preview", Caps: drivers.Capabilities{Preview: true, Rollback: true, Health: true}}}
	reg, err := drivers.NewRegistry(f.driver)
	if err != nil {
		t.Fatal(err)
	}
	f.a.Drivers, f.a.Verifier = reg, f
	f.a.Release = ReleaseConfig{PollInterval: 5 * time.Millisecond, PreviewTimeout: 50 * time.Millisecond, RoomTimeout: 50 * time.Millisecond}
	return f
}

func (f *previewFixture) request(t *testing.T) PreviewRequest {
	t.Helper()
	id := startStep(t, f.a, StepStart{RunID: f.run.RunID, StepID: "preview", Kind: domain.StepGate, Attempt: 1})
	return PreviewRequest{Issue: f.in, RunID: f.run.RunID, StepRunID: id, Branch: f.run.Branch, HeadSHA: previewHead, ChangeRequestURL: previewCR,
		Stage: config.Stage{Name: "preview", Driver: "flux-preview", Config: json.RawMessage(`{}`), Verify: config.VerifyE2ESuite,
			URL: "http://goodreads-previews.stages.svc.cluster.local/pr-{changeRequest}"}}
}

func execute[T any](t *testing.T, a *Activities, fn any, arg any) (T, error) {
	t.Helper()
	var out T
	v, err := activityEnv(a).ExecuteActivity(fn, arg)
	if err == nil {
		_ = v.Get(&out)
	}
	return out, err
}

// M7, D126: the preview gate publishes the head to the preview stage as the
// gate's deployment, waits until it runs it, verifies it at the change
// request's URL from its agent branch, removes it and records what it found.
func TestPreviewCheck(t *testing.T) {
	f := newPreviewFixture(t)
	req := f.request(t)
	pub, err := execute[PreviewPublished](t, f.a, f.a.PublishPreview, req)
	if err != nil || pub.NoRoom != "" || len(pub.State) == 0 || pub.ChangeRequest != 152 {
		t.Fatalf("PublishPreview = %+v, %v", pub, err)
	}
	step, _ := f.a.Store.GetStepRun(t.Context(), uuid.MustParse(req.StepRunID))
	if step.Status != domain.ExecRunning || step.CommitSHA != previewHead {
		t.Errorf("step %s at %q", step.Status, step.CommitSHA)
	}
	call := f.driver.Calls()[0]
	if call.Op != driverstest.OpPublish || call.Request.Deployment.ID.String() != req.StepRunID || call.Request.Subject.ChangeRequest != 152 ||
		call.Request.Subject.SHA != previewHead || call.Request.Subject.Repo != f.in.Repo {
		t.Errorf("publish call = %+v", call)
	}

	req.State, req.ChangeRequest = pub.State, pub.ChangeRequest
	if st, err := execute[StageStatus](t, f.a, f.a.AwaitPreview, req); err != nil || !st.Ready {
		t.Fatalf("AwaitPreview = %+v, %v", st, err)
	}
	v, err := execute[Verification](t, f.a, f.a.VerifyPreview, req)
	if err != nil || !v.Passed {
		t.Fatalf("VerifyPreview = %+v, %v", v, err)
	}
	if len(f.verified) != 1 {
		t.Fatalf("verified %d times", len(f.verified))
	}
	if vr := f.verified[0]; vr.URL != "http://goodreads-previews.stages.svc.cluster.local/pr-152" || vr.Branch != f.run.Branch || vr.SHA != previewHead ||
		vr.RunID != f.run.RunID || vr.IssueKey != f.in.IssueKey || vr.DeploymentID != req.StepRunID || vr.ReleaseID != "" || vr.Kind != config.VerifyE2ESuite {
		t.Errorf("verify request = %+v", vr)
	}

	if _, err := execute[struct{}](t, f.a, f.a.RemovePreview, req); err != nil {
		t.Fatal(err)
	}
	if ops := f.driver.Ops(); !slices.Equal(ops, []string{driverstest.OpPublish, driverstest.OpStatus, driverstest.OpRollback}) ||
		string(f.driver.Calls()[2].Request.Deployment.State) != string(pub.State) {
		t.Errorf("driver ops = %v", ops)
	}

	d := domain.PreviewDetails{Stage: "preview", ChangeRequest: 152, SHA: previewHead, URL: req.URL(), Ran: true, Passed: true, Summary: v.Summary, Removed: true}
	out, err := execute[GateOutcome](t, f.a, f.a.FinishPreview, PreviewResult{StepRunID: req.StepRunID, Details: d})
	if err != nil || !out.Passed || out.Summary != "219 tests passed" || out.Feedback != "" {
		t.Fatalf("FinishPreview = %+v, %v", out, err)
	}
	step, _ = f.a.Store.GetStepRun(t.Context(), uuid.MustParse(req.StepRunID))
	results, _ := f.a.Store.ListGateResultsForRun(t.Context(), uuid.MustParse(f.run.RunID))
	var got domain.PreviewDetails
	if step.Status != domain.ExecSucceeded || len(results) != 1 || results[0].Type != domain.GatePreview || results[0].Verdict != domain.GatePass ||
		json.Unmarshal(results[0].Details, &got) != nil || got.URL != d.URL || !got.Removed {
		t.Errorf("step %s, results %+v", step.Status, results)
	}
	// A retry of the finished step reports what was recorded.
	if again, err := execute[GateOutcome](t, f.a, f.a.FinishPreview, PreviewResult{StepRunID: req.StepRunID}); err != nil || again != out {
		t.Errorf("retry = %+v, %v; want %+v", again, err, out)
	}
}

// With no room for a preview, publishing is asked again until there is, or
// until the room timeout, which is reported rather than failing the activity.
func TestPublishPreviewWaitsForRoom(t *testing.T) {
	f := newPreviewFixture(t)
	full := fmt.Errorf("2 previews run: %w", drivers.ErrNoRoom)
	f.driver.Errs = map[string]error{driverstest.OpPublish: full}
	f.driver.FailFirst = map[string]int{driverstest.OpPublish: 2}
	if pub, err := execute[PreviewPublished](t, f.a, f.a.PublishPreview, f.request(t)); err != nil || pub.NoRoom != "" || len(pub.State) == 0 || len(f.driver.Calls()) != 3 {
		t.Errorf("PublishPreview = %+v, %v after %d calls; want the third to succeed", pub, err, len(f.driver.Calls()))
	}

	f.driver.FailFirst = nil
	pub, err := execute[PreviewPublished](t, f.a, f.a.PublishPreview, f.request(t))
	if err != nil || !strings.Contains(pub.NoRoom, "no room for a preview on preview within 50ms") || !strings.Contains(pub.NoRoom, "2 previews run") {
		t.Errorf("PublishPreview = %+v, %v; want no room reported", pub, err)
	}

	other := f.request(t)
	other.ChangeRequestURL = "https://github.com/misarescu/Goodreads-Clone/pull/9"
	if _, err := execute[PreviewPublished](t, f.a, f.a.PublishPreview, other); err == nil || !strings.Contains(err.Error(), "has no change request") {
		t.Errorf("an unknown change request = %v", err)
	}

	f.driver.Errs = map[string]error{driverstest.OpPublish: fmt.Errorf("bad inputs file: %w", drivers.ErrInvalidConfig)}
	if _, err := execute[PreviewPublished](t, f.a, f.a.PublishPreview, f.request(t)); err == nil || !strings.Contains(err.Error(), "bad inputs file") {
		t.Errorf("a refusal = %v, want the activity to fail", err)
	}
}

// A preview that never runs the head is reported with the driver's last word;
// a stage without verification passes once the preview runs the head; a
// preview never published is not removed.
func TestPreviewEdges(t *testing.T) {
	f := newPreviewFixture(t)
	req := f.request(t)
	req.ChangeRequest = 152
	f.driver.SetPhase(drivers.PhaseProgressing, "the version URL answered 502")
	if st, err := execute[StageStatus](t, f.a, f.a.AwaitPreview, req); err != nil || st.Ready || !strings.Contains(st.Reason, "answered 502") ||
		!strings.Contains(st.Reason, "within 50ms") {
		t.Errorf("AwaitPreview = %+v, %v", st, err)
	}
	// M7.1, D131: the preview stage's readyTimeout replaces the platform's.
	req.Stage.ReadyTimeout = "80ms"
	if st, err := execute[StageStatus](t, f.a, f.a.AwaitPreview, req); err != nil || st.Ready || !strings.Contains(st.Reason, "within 80ms") {
		t.Errorf("AwaitPreview with readyTimeout = %+v, %v", st, err)
	}
	req.Stage.ReadyTimeout = ""
	req.Stage.Verify = ""
	if v, err := execute[Verification](t, f.a, f.a.VerifyPreview, req); err != nil || !v.Passed || len(f.verified) != 0 {
		t.Errorf("unverified stage = %+v, %v", v, err)
	}
	calls := len(f.driver.Calls())
	if _, err := execute[struct{}](t, f.a, f.a.RemovePreview, req); err != nil || len(f.driver.Calls()) != calls {
		t.Errorf("RemovePreview without a state = %v, %d calls", err, len(f.driver.Calls())-calls)
	}
}

// Failing tests go back to the coder; a check that could not judge the head
// is inconclusive, with nothing for the coder.
func TestPreviewOutcome(t *testing.T) {
	failed := domain.PreviewDetails{URL: "http://p/pr-152", Ran: true, Summary: "2 of 219 tests failed",
		Failures: []domain.TestFailure{{Test: "gc-9 list tags", Message: "expected 200, got 500"}, {Test: "gc-9 prefix filter"}}}
	out := PreviewOutcome("s", failed)
	if out.Passed || out.Inconclusive {
		t.Errorf("outcome = %+v", out)
	}
	for _, want := range []string{"deployed at http://p/pr-152", "2 of 219 tests failed", "- gc-9 list tags: expected 200, got 500\n", "- gc-9 prefix filter\n"} {
		if !strings.Contains(out.Feedback, want) {
			t.Errorf("feedback lacks %q:\n%s", want, out.Feedback)
		}
	}
	many := failed
	many.Failures = make([]domain.TestFailure, maxPreviewFailures+3)
	if out := PreviewOutcome("s", many); !strings.Contains(out.Feedback, "- and 3 more\n") {
		t.Errorf("feedback does not cap the failures:\n%s", out.Feedback)
	}
	for _, d := range []domain.PreviewDetails{
		{Summary: "no room for a preview on preview within 30m0s", Inconclusive: true},
		{Summary: "preview did not run ddddddd within 10m0s: the version URL answered 502", Inconclusive: true},
		{Ran: true, Summary: "the suite wrote no results", Inconclusive: true},
	} {
		if out := PreviewOutcome("s", d); out.Passed || !out.Inconclusive || out.Feedback != "" || out.Summary != d.Summary {
			t.Errorf("%q: outcome = %+v", d.Summary, out)
		}
	}
	if got := PreviewFailures([]TestFailure{{Test: "a", IssueKeys: []string{"gc-9"}, Message: "m"}}); len(got) != 1 || got[0].IssueKeys[0] != "gc-9" {
		t.Errorf("PreviewFailures = %+v", got)
	}
}
