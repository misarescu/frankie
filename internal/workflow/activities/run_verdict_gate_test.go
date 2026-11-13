package activities

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

const reviewedSHA = "0123456789abcdef0123456789abcdef01234567"

func verdictGate(t *testing.T, a *Activities, req VerdictGateRequest) GateOutcome {
	t.Helper()
	v, err := activityEnv(a).ExecuteActivity(a.RunVerdictGate, req)
	if err != nil {
		t.Fatalf("RunVerdictGate: %v", err)
	}
	var out GateOutcome
	_ = v.Get(&out)
	return out
}

func verdictFixture(t *testing.T) (*Activities, VerdictGateRequest) {
	t.Helper()
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog()}
	run := startedRun(t, a, in)
	stepRunID := startStep(t, a, StepStart{RunID: run.RunID, StepID: "review", Kind: domain.StepGate, Attempt: 1})
	return a, VerdictGateRequest{RunID: run.RunID, StepRunID: stepRunID, StepID: "review", Require: "all",
		Policy: domain.GateBlocking, CommitSHA: reviewedSHA}
}

func review(step, result string) Review {
	return Review{StepID: step, Role: "code-reviewer", StepRunID: "x", CommitSHA: reviewedSHA, Result: result}
}

// M3 acceptance: a reviewer's failing verdict fails the gate and its findings
// become the coder's feedback.
func TestRunVerdictGateFailsWithFindings(t *testing.T) {
	a, req := verdictFixture(t)
	req.Reviews = []Review{review("code-reviewer", "```json\n"+`{"verdict":"fail","summary":"Missing error handling.","findings":[`+
		`{"severity":"minor","message":"name the constant"},`+
		`{"severity":"blocker","file":"internal/server/version.go","line":12,"message":"the encode error is dropped"}]}`+"\n```")}
	out := verdictGate(t, a, req)
	if out.Passed || out.Inconclusive || out.Summary != "code-reviewer: fail (1 blocker, 1 minor)" {
		t.Fatalf("outcome = %+v", out)
	}
	blocker, minor := strings.Index(out.Feedback, "[blocker] internal/server/version.go:12: the encode error is dropped"), strings.Index(out.Feedback, "[minor] name the constant")
	if !strings.HasPrefix(out.Feedback, "Review by code-reviewer: Missing error handling.") || blocker < 0 || minor < blocker {
		t.Errorf("feedback (most severe first) = %q", out.Feedback)
	}

	step, _ := a.Store.GetStepRun(t.Context(), uuid.MustParse(req.StepRunID))
	results, _ := a.Store.ListGateResultsForRun(t.Context(), step.RunID)
	if step.Status != domain.ExecFailed || step.CommitSHA != reviewedSHA || len(results) != 1 ||
		results[0].Type != domain.GateAgentVerdict || results[0].Verdict != domain.GateFail {
		t.Fatalf("step %+v, results %+v", step, results)
	}
	var d domain.AgentVerdictDetails
	if err := json.Unmarshal(results[0].Details, &d); err != nil || len(d.Reviews) != 1 || len(d.Reviews[0].Findings) != 2 {
		t.Errorf("details = %s (%v)", results[0].Details, err)
	}

	// A retry reports the recorded outcome.
	if again := verdictGate(t, a, req); again != out {
		t.Errorf("retry = %+v, want %+v", again, out)
	}
}

func TestRunVerdictGateRequire(t *testing.T) {
	const pass, fail = `{"verdict":"pass","findings":[]}`, `{"verdict":"fail","findings":[{"severity":"major","message":"x"}]}`
	tests := []struct {
		require string
		results []string
		passed  bool
	}{
		{"all", []string{pass, pass}, true},
		{"all", []string{pass, fail}, false},
		{"any", []string{fail, pass}, true},
		{"majority", []string{pass, fail, pass}, true},
	}
	for _, tt := range tests {
		t.Run(tt.require, func(t *testing.T) {
			a, req := verdictFixture(t)
			req.Require = tt.require
			for i, r := range tt.results {
				req.Reviews = append(req.Reviews, review("reviewer-"+string(rune('a'+i)), r))
			}
			if out := verdictGate(t, a, req); out.Passed != tt.passed || out.Inconclusive {
				t.Errorf("outcome = %+v, want passed %v", out, tt.passed)
			}
		})
	}
}

// A reviewer that does not return a valid verdict leaves the gate inconclusive:
// the coder gets no feedback and the gate does not pass.
func TestRunVerdictGateInconclusive(t *testing.T) {
	tests := map[string]Review{
		"prose instead of JSON": review("code-reviewer", "Looks good to me!"),
		"invalid verdict":       review("code-reviewer", `{"verdict":"maybe","findings":[]}`),
		"another commit":        {StepID: "code-reviewer", CommitSHA: "ffff", Result: `{"verdict":"pass","findings":[]}`},
	}
	for name, r := range tests {
		t.Run(name, func(t *testing.T) {
			a, req := verdictFixture(t)
			req.Reviews = []Review{r}
			out := verdictGate(t, a, req)
			if out.Passed || !out.Inconclusive || out.Feedback != "" || !strings.Contains(out.Summary, "returned no valid verdict") {
				t.Errorf("outcome = %+v", out)
			}
		})
	}
}

func TestParseVerdictToleratesWrapping(t *testing.T) {
	for _, msg := range []string{
		`{"verdict":"pass","findings":[]}`,
		"Here is my verdict:\n```json\n{\"verdict\":\"pass\",\"findings\":[]}\n```",
	} {
		if v, err := parseVerdict(msg); err != nil || v.Verdict != domain.GatePass {
			t.Errorf("parseVerdict(%q) = %+v, %v", msg, v, err)
		}
	}
}
