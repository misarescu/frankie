package workflow

import (
	"slices"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// withAgent turns the release agent on in the plan (release.agent, D129).
func withAgent(f *releaseFixture) {
	plan := f.plan
	f.plan = func(sha string) activities.ReleasePlan {
		p := plan(sha)
		p.Agent = true
		return p
	}
}

// M7 (D129): the release agent writes the notes while the stages deploy;
// the release finishes once they are recorded and then gets its version.
func TestReleaseAgentNotesAndVersion(t *testing.T) {
	f := newReleaseFixture(t, withAgent, func(f *releaseFixture) { f.sessionDelay, f.awaitDelay = time.Hour, 10*time.Minute })
	start := f.env.Now()
	f.run(t, shaA)
	rel := "rel-" + shaA
	want := []string{"session notes", "end sr-notes true", "notes", "finish succeeded", "publish " + rel, "finish-run " + rel}
	if !slices.Equal(f.agentLog, want) {
		t.Errorf("agent log = %v, want %v", f.agentLog, want)
	}
	// The notes ran next to the stages: the release took the session's hour,
	// not an hour plus the stages' 20 minutes.
	if took := f.env.Now().Sub(start); took >= time.Hour+20*time.Minute {
		t.Errorf("the release took %s: the notes did not run next to the stages", took)
	}
	if len(f.sessions) != 1 || f.sessions[0] != (activities.ReleaseAgentRequest{ReleaseID: rel, Repo: "misarescu/Goodreads-Clone", SHA: shaA, Step: activities.ReleaseStepNotes}) {
		t.Errorf("sessions = %+v", f.sessions)
	}
	if len(f.notes) != 1 || f.notes[0].Failed != "" || !strings.Contains(f.notes[0].Result, `"bump":"minor"`) {
		t.Errorf("notes = %+v", f.notes)
	}
}

// A failed notes session still releases; the notes fall back.
func TestReleaseAgentNotesFail(t *testing.T) {
	f := newReleaseFixture(t, withAgent, func(f *releaseFixture) {
		f.agent = func(activities.ReleaseAgentRequest) activities.SessionResult {
			return activities.SessionResult{Status: "failed", Reason: "the release's model budget of $1.00 is used up"}
		}
	})
	f.run(t, shaA)
	if len(f.notes) != 1 || !strings.Contains(f.notes[0].Failed, "agent session failed: the release's model budget") {
		t.Errorf("notes = %+v", f.notes)
	}
	if len(f.finished) != 1 || f.finished[0].Status != domain.ReleaseSucceeded || !slices.Contains(f.agentLog, "publish rel-"+shaA) {
		t.Errorf("finished %+v, log %v", f.finished, f.agentLog)
	}
}

// M7 (D129): a failed verification is read by the agent, whose analysis goes
// into the bug issue; the release gets no version.
func TestReleaseAgentAnalysesAFailedVerification(t *testing.T) {
	const analysis = `{"suspects":[{"issue":"gc-74","likelihood":"high","why":"w"}],"summary":"s"}`
	f := newReleaseFixture(t, withAgent, func(f *releaseFixture) {
		f.verify = func(activities.StageRequest) activities.Verification {
			return activities.Verification{Summary: "4 of 219 tests failed", Failures: []activities.TestFailure{{Test: "gc-74 ratings", IssueKeys: []string{"gc-74"}}}}
		}
		f.agent = func(req activities.ReleaseAgentRequest) activities.SessionResult {
			if req.Step == activities.ReleaseStepAnalysis {
				return activities.SessionResult{Status: "succeeded", Result: analysis}
			}
			return activities.SessionResult{Status: "succeeded", Result: `{"bump":"patch","reason":"r","notes":"n"}`}
		}
	})
	f.run(t, shaA)
	var asked *activities.ReleaseAgentRequest
	for i, s := range f.sessions {
		if s.Step == activities.ReleaseStepAnalysis {
			asked = &f.sessions[i]
		}
	}
	if asked == nil || asked.Failure == nil || asked.Failure.Stage != "staging" || !asked.Failure.RolledBack || asked.Failure.Verification.Summary != "4 of 219 tests failed" {
		t.Fatalf("analysis request = %+v", asked)
	}
	if len(f.reported) != 1 || f.reported[0].Analysis != analysis {
		t.Errorf("reported = %+v", f.reported)
	}
	if slices.ContainsFunc(f.agentLog, func(s string) bool { return strings.HasPrefix(s, "publish") }) || f.agentLog[len(f.agentLog)-1] != "finish-run rel-"+shaA {
		t.Errorf("agent log = %v", f.agentLog)
	}
}

// Without release.agent, and in histories from before D129, nothing of the
// agent runs.
func TestReleaseWithoutAgent(t *testing.T) {
	f := newReleaseFixture(t)
	f.run(t, shaA)
	if len(f.sessions) != 0 || len(f.notes) != 0 || slices.ContainsFunc(f.agentLog, func(s string) bool { return !strings.HasPrefix(s, "finish ") }) {
		t.Errorf("agent ran: sessions %v, log %v", f.sessions, f.agentLog)
	}
	g := newReleaseFixture(t, withAgent)
	g.env.OnGetVersion("release-agent", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	g.run(t, shaA)
	if len(g.sessions) != 0 {
		t.Errorf("an old history ran the agent: %v", g.sessions)
	}
}
