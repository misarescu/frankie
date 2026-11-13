package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// feedbackFindingsBytes bounds the findings a loop back passes to the coder.
const feedbackFindingsBytes = 12 << 10

// RunVerdictGate judges an agent-verdict gate (docs/spec/03): it parses each
// reviewer's final message against the review-verdict contract, combines the
// verdicts as the gate requires, and records the result. A reviewer whose
// message is not a valid verdict makes the gate inconclusive rather than
// failed, since sending the coder back would not fix the reviewer. Idempotent:
// a retry after the gate finished reports what was recorded.
func (a *Activities) RunVerdictGate(ctx context.Context, req VerdictGateRequest) (GateOutcome, error) {
	id, err := uuid.Parse(req.StepRunID)
	if err != nil {
		return GateOutcome{}, temporal.NewNonRetryableApplicationError("bad step run id", "input", err)
	}
	step, err := a.Store.GetStepRun(ctx, id)
	if err != nil {
		return GateOutcome{}, err
	}
	if step.Status.Terminal() {
		return a.recordedVerdict(ctx, step)
	}
	if _, err := a.Store.SetStepRunCommit(ctx, step.ID, req.CommitSHA); err != nil {
		return GateOutcome{}, err
	}
	if _, err := a.Store.TransitionStepRun(ctx, step.ID, domain.ExecPending, domain.ExecRunning); err != nil && step.Status != domain.ExecRunning {
		return GateOutcome{}, err
	}

	details := domain.AgentVerdictDetails{Require: req.Require}
	for _, r := range req.Reviews {
		res := domain.ReviewResult{Step: r.StepID, Role: r.Role, CommitSHA: r.CommitSHA}
		v, err := parseVerdict(r.Result)
		switch {
		case err != nil:
			res.Error = err.Error()
		case r.CommitSHA != req.CommitSHA:
			res.Error = fmt.Sprintf("reviewed %s, not the commit under review %s", r.CommitSHA, req.CommitSHA)
		default:
			res.Verdict, res.Summary, res.Findings = v.Verdict, v.Summary, v.Findings
		}
		details.Reviews = append(details.Reviews, res)
	}

	raw, err := json.Marshal(details)
	if err != nil {
		return GateOutcome{}, fmt.Errorf("encode verdict details: %w", err)
	}
	verdict, status := domain.GateFail, domain.ExecFailed
	if details.Passed() {
		verdict, status = domain.GatePass, domain.ExecSucceeded
	}
	policy := req.Policy
	if !policy.Valid() {
		policy = domain.GateBlocking
	}
	if _, err := a.Store.RecordGateResult(ctx, store.NewGateResult{StepRunID: step.ID, Type: domain.GateAgentVerdict,
		Policy: policy, Verdict: verdict, Details: raw}); err != nil {
		return GateOutcome{}, err
	}
	if _, err := a.Store.TransitionStepRun(ctx, step.ID, domain.ExecRunning, status); err != nil {
		return GateOutcome{}, err
	}
	return verdictOutcome(step.ID.String(), details), nil
}

// recordedVerdict rebuilds the outcome of a gate an earlier attempt finished.
func (a *Activities) recordedVerdict(ctx context.Context, step domain.StepRun) (GateOutcome, error) {
	results, err := a.Store.ListGateResultsForRun(ctx, step.RunID)
	if err != nil {
		return GateOutcome{}, err
	}
	for _, r := range results {
		if r.StepRunID != step.ID {
			continue
		}
		var d domain.AgentVerdictDetails
		if err := json.Unmarshal(r.Details, &d); err != nil {
			return GateOutcome{}, fmt.Errorf("decode verdict details: %w", err)
		}
		return verdictOutcome(step.ID.String(), d), nil
	}
	return GateOutcome{}, fmt.Errorf("gate step %s is %s but has no result", step.ID, step.Status)
}

// parseVerdict takes the verdict out of a reviewer's final message. The role
// is told to answer with the JSON object alone; a surrounding code fence or a
// sentence before it is tolerated, anything else is not a verdict.
func parseVerdict(message string) (domain.ReviewVerdict, error) {
	s := strings.TrimSpace(message)
	start, end := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return domain.ReviewVerdict{}, errors.New("the final message holds no JSON verdict")
	}
	return domain.ParseReviewVerdict([]byte(s[start : end+1]))
}

// verdictOutcome summarizes a judged gate for the workflow.
func verdictOutcome(stepRunID string, d domain.AgentVerdictDetails) GateOutcome {
	out := GateOutcome{StepRunID: stepRunID, Passed: d.Passed(), Inconclusive: d.Inconclusive()}
	var parts []string
	for _, r := range d.Reviews {
		switch {
		case r.Error != "":
			parts = append(parts, fmt.Sprintf("%s returned no valid verdict (%s)", r.Step, r.Error))
		default:
			parts = append(parts, fmt.Sprintf("%s: %s%s", r.Step, r.Verdict, countFindings(r.Findings)))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no reviews")
	}
	out.Summary = strings.Join(parts, "; ")
	if !out.Passed && !out.Inconclusive {
		out.Feedback = reviewFeedback(d)
	}
	return out
}

// countFindings is " (1 blocker, 2 minor)", or "" without findings.
func countFindings(fs []domain.Finding) string {
	var parts []string
	for _, sev := range domain.Severities {
		n := 0
		for _, f := range fs {
			if f.Severity == sev {
				n++
			}
		}
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// reviewFeedback lists the failing reviews' findings for the coder, most
// severe first, within feedbackFindingsBytes.
func reviewFeedback(d domain.AgentVerdictDetails) string {
	var b strings.Builder
	for _, r := range d.Reviews {
		if r.Verdict != domain.GateFail {
			continue
		}
		fmt.Fprintf(&b, "Review by %s:", r.Step)
		if r.Summary != "" {
			fmt.Fprintf(&b, " %s", r.Summary)
		}
		b.WriteString("\n")
		for _, sev := range domain.Severities {
			for _, f := range r.Findings {
				if f.Severity != sev {
					continue
				}
				loc := f.File
				if f.Line > 0 {
					loc = fmt.Sprintf("%s:%d", f.File, f.Line)
				}
				if loc != "" {
					loc += ": "
				}
				line := fmt.Sprintf("- [%s] %s%s\n", f.Severity, loc, f.Message)
				if b.Len()+len(line) > feedbackFindingsBytes {
					b.WriteString("- (more findings omitted)\n")
					return b.String()
				}
				b.WriteString(line)
			}
		}
	}
	return b.String()
}
