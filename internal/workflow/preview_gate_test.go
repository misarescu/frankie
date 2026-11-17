package workflow

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// previewDefinition checks the change request in a preview before the merge
// queue, as build-e2e does (D126).
const previewDefinition = `
name: checked
version: 1
steps:
  - id: implement
    agent: {role: coder}
  - id: checks
    gate: {type: command, onFail: {goto: implement, maxLoops: 2}}
  - id: open
    action: change-request.open
  - id: preview
    gate: {type: preview, onFail: {goto: implement, maxLoops: 2}}
  - id: merge
    action: merge-queue.enqueue
`

func previewSnapshot(t *testing.T) snapshot.Snapshot {
	t.Helper()
	snap := loopSnapshot(t, previewDefinition)
	snap.PreviewStage = &config.Stage{Name: "preview", Driver: "flux-preview", Config: json.RawMessage(`{}`),
		URL: "http://goodreads-previews.stages.svc.cluster.local/pr-{changeRequest}", Verify: config.VerifyE2ESuite}
	return snap
}

// M7 (D126): once the change request is open, the preview gate waits for its
// CI, deploys the head as a preview, waits until it runs it, runs the suite
// there and removes it; then the change request is enqueued.
func TestInterpretPreviewGate(t *testing.T) {
	f := newInterpretFixture(t)
	out := f.run(t, previewSnapshot(t))
	if !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	if got := f.stepIDs(); got != "implement/1 checks/1 open/1 preview-ci/1 preview/1 merge/1" {
		t.Errorf("steps = %s", got)
	}
	if want := []string{"ci@sha1", "publish@sha1", "await@sha1", "verify@sha1", "remove@sha1"}; !slices.Equal(f.previewOps, want) {
		t.Errorf("preview ops = %v, want %v", f.previewOps, want)
	}
	if len(f.previews) != 1 {
		t.Fatalf("previews = %+v", f.previews)
	}
	d := f.previews[0]
	if !d.Passed || !d.Ran || !d.Removed || d.ChangeRequest != 9 || d.SHA != "sha1" || d.Summary != "219 tests passed" ||
		d.URL != "http://goodreads-previews.stages.svc.cluster.local/pr-9" || d.Stage != "preview" {
		t.Errorf("details = %+v", d)
	}
	if len(f.enqueued) != 1 || f.enqueued[0].HeadSHA != "sha1" {
		t.Errorf("enqueued = %+v", f.enqueued)
	}
}

// Without a preview stage, the gate does not apply.
func TestInterpretPreviewGateWithoutAStage(t *testing.T) {
	f := newInterpretFixture(t)
	if out := f.run(t, loopSnapshot(t, previewDefinition)); !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	if got := f.stepIDs(); got != "implement/1 checks/1 open/1 merge/1" || len(f.previewOps) != 0 {
		t.Errorf("steps = %s, preview ops %v", got, f.previewOps)
	}
}

// Failing tests in the preview and a red CI go back to the coder with what
// failed; the next head gets its own preview.
func TestInterpretPreviewGateLoopsBack(t *testing.T) {
	t.Run("failing tests", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.verifyPreview = func(req activities.PreviewRequest) activities.Verification {
			if req.HeadSHA == "sha1" {
				return activities.Verification{Summary: "1 of 219 tests failed",
					Failures: []activities.TestFailure{{Test: "gc-9 list tags", IssueKeys: []string{"gc-9"}, Message: "expected 200, got 500"}}}
			}
			return activities.Verification{Passed: true, Summary: "219 tests passed"}
		}
		if out := f.run(t, previewSnapshot(t)); !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		if got := f.stepIDs(); got != "implement/1 checks/1 open/1 preview-ci/1 preview/1 implement/2 checks/2 open/2 preview-ci/2 preview/2 merge/1" {
			t.Errorf("steps = %s", got)
		}
		if !slices.Equal(f.loops, []string{"preview=1"}) || len(f.previews) != 2 || f.previews[0].Passed || !f.previews[0].Removed {
			t.Errorf("loops %v, previews %+v", f.loops, f.previews)
		}
		feedback := f.sessions[1].Step.Feedback
		for _, want := range []string{"The `preview` check failed on your last commit: 1 of 219 tests failed", "- gc-9 list tags: expected 200, got 500",
			"deployed at http://goodreads-previews.stages.svc.cluster.local/pr-9"} {
			if !strings.Contains(feedback, want) {
				t.Errorf("feedback lacks %q:\n%s", want, feedback)
			}
		}
	})
	t.Run("red CI", func(t *testing.T) {
		f := newInterpretFixture(t)
		f.ci = func(req activities.ProviderChecksRequest) activities.GateOutcome {
			if req.HeadSHA == "sha1" {
				return activities.GateOutcome{StepRunID: req.StepRunID, Summary: "the repo's CI failed: test", Feedback: "- test: 2 tests failed\n"}
			}
			return activities.GateOutcome{StepRunID: req.StepRunID, Passed: true, Summary: "the repo's CI passed"}
		}
		if out := f.run(t, previewSnapshot(t)); !out.Succeeded {
			t.Fatalf("result = %+v", out)
		}
		if got := f.stepIDs(); got != "implement/1 checks/1 open/1 preview-ci/1 implement/2 checks/2 open/2 preview-ci/2 preview/1 merge/1" {
			t.Errorf("steps = %s", got)
		}
		if want := []string{"ci@sha1", "ci@sha2", "publish@sha2", "await@sha2", "verify@sha2", "remove@sha2"}; !slices.Equal(f.previewOps, want) {
			t.Errorf("preview ops = %v, want no preview of the red head", f.previewOps)
		}
		if fb := f.sessions[1].Step.Feedback; !strings.Contains(fb, "the repo's CI failed: test") || !strings.Contains(fb, "- test: 2 tests failed") {
			t.Errorf("feedback:\n%s", fb)
		}
	})
}

// A check that cannot judge the head escalates to a person instead of
// sending the coder back (D127); a published preview is removed either way.
func TestInterpretPreviewGateInconclusive(t *testing.T) {
	tests := []struct {
		name    string
		set     func(f *interpretFixture)
		ops     []string
		summary string
	}{
		{"no room", func(f *interpretFixture) { f.noRoom = "no room for a preview on preview within 30m0s" },
			[]string{"ci@sha1", "publish@sha1"}, "no room for a preview"},
		{"the preview never runs the head", func(f *interpretFixture) { f.notReady = "preview did not run sha1 within 10m0s: answered 502" },
			[]string{"ci@sha1", "publish@sha1", "await@sha1", "remove@sha1"}, "answered 502"},
		{"the suite cannot judge", func(f *interpretFixture) {
			f.verifyPreview = func(activities.PreviewRequest) activities.Verification {
				return activities.Verification{Inconclusive: true, Summary: "the suite wrote no results"}
			}
		}, []string{"ci@sha1", "publish@sha1", "await@sha1", "verify@sha1", "remove@sha1"}, "the suite wrote no results"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newInterpretFixture(t)
			tt.set(f)
			f.decide(t, 3*time.Hour, 0, true, "the homelab was busy")
			if out := f.run(t, previewSnapshot(t)); !out.Succeeded {
				t.Fatalf("result = %+v", out)
			}
			if got := f.stepIDs(); got != "implement/1 checks/1 open/1 preview-ci/1 preview/1 preview-escalation/1 merge/1" {
				t.Errorf("steps = %s", got)
			}
			if !slices.Equal(f.previewOps, tt.ops) || len(f.loops) != 0 {
				t.Errorf("preview ops = %v, loops %v", f.previewOps, f.loops)
			}
			if len(f.opened) != 1 || !strings.Contains(f.opened[0].Reason, tt.summary) {
				t.Errorf("escalation = %+v", f.opened)
			}
		})
	}
}

// A preview that cannot be removed is recorded; the check's verdict stands.
func TestInterpretPreviewGateRemoveFails(t *testing.T) {
	f := newInterpretFixture(t)
	f.removeFails = true
	if out := f.run(t, previewSnapshot(t)); !out.Succeeded {
		t.Fatalf("result = %+v", out)
	}
	if d := f.previews[0]; !d.Passed || d.Removed || !strings.Contains(d.RemoveError, "github: 502") {
		t.Errorf("details = %+v", d)
	}
}
