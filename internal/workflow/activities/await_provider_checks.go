package activities

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// ProviderChecksRequest waits for the repo's own CI on a commit the merge
// queue is about to merge (D90), as one execution of a gate StepRun.
type ProviderChecksRequest struct {
	Issue     IssueRunInput
	StepRunID string
	HeadSHA   string
	// Timeout bounds the wait (zero: 60 minutes). Grace is how long a commit
	// without any check waits for one to appear (zero: 2 minutes), since CI
	// registers its checks a moment after the push. Poll is the interval
	// between reads (zero: 15 seconds).
	Timeout, Grace, Poll time.Duration
}

// AwaitProviderChecks waits until the repo's CI on HeadSHA has finished and
// records the result as a provider-checks gate result (D90): it passes when
// every check passed or the commit has none after the grace period, fails
// when one failed, and is inconclusive when the checks did not finish in time.
// A retry of a finished StepRun reports what was recorded.
func (a *Activities) AwaitProviderChecks(ctx context.Context, req ProviderChecksRequest) (GateOutcome, error) {
	id, err := uuid.Parse(req.StepRunID)
	if err != nil {
		return GateOutcome{}, temporal.NewNonRetryableApplicationError("bad step run id", "input", err)
	}
	step, err := a.Store.GetStepRun(ctx, id)
	if err != nil {
		return GateOutcome{}, err
	}
	if step.Status.Terminal() {
		return a.recordedChecks(ctx, step)
	}
	if _, err := a.Store.SetStepRunCommit(ctx, id, req.HeadSHA); err != nil {
		return GateOutcome{}, err
	}
	if _, err := a.Store.TransitionStepRun(ctx, id, domain.ExecPending, domain.ExecRunning); err != nil && step.Status != domain.ExecRunning {
		return GateOutcome{}, err
	}
	timeout, grace, poll := cmp.Or(req.Timeout, 60*time.Minute), cmp.Or(req.Grace, 2*time.Minute), cmp.Or(req.Poll, 15*time.Second)
	start := time.Now()
	for {
		st, err := a.GitHub.CommitStatus(ctx, req.Issue.Repo, req.HeadSHA)
		if err != nil {
			return GateOutcome{}, err
		}
		waited := time.Since(start)
		waiting := st.State == domain.CheckPending || (st.State == domain.CheckNone && waited < grace)
		switch {
		case !waiting:
			if st.State == domain.CheckFailure {
				a.readFailedLogs(ctx, req.Issue.Repo, st.Checks)
			}
			return a.finishChecks(ctx, step, domain.ProviderChecksDetails{SHA: req.HeadSHA, State: st.State, Checks: st.Checks})
		case waited >= timeout:
			return a.finishChecks(ctx, step, domain.ProviderChecksDetails{SHA: req.HeadSHA, State: st.State, Checks: st.Checks,
				Error: fmt.Sprintf("the repo's CI did not finish within %s", timeout)})
		}
		activity.RecordHeartbeat(ctx, string(st.State))
		select {
		case <-ctx.Done():
			return GateOutcome{}, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// maxCheckLogs bounds the failed checks whose logs are read (D120).
const maxCheckLogs = 3

// readFailedLogs fills in the log of each failed check (D120), so the coder
// sees why CI failed without access to the provider. A log that cannot be
// read is left out: the check's URL still says where it is.
func (a *Activities) readFailedLogs(ctx context.Context, repo string, checks []domain.Check) {
	read := 0
	for i, c := range checks {
		if c.State != domain.CheckFailure || c.ID == "" || read == maxCheckLogs {
			continue
		}
		read++
		activity.RecordHeartbeat(ctx, "reading the log of "+c.Name)
		log, err := a.GitHub.CheckLog(ctx, repo, c)
		if err != nil {
			activity.GetLogger(ctx).Warn("read a failed check's log", "check", c.Name, "error", err)
			continue
		}
		checks[i].Log = log
	}
}

func (a *Activities) finishChecks(ctx context.Context, step domain.StepRun, d domain.ProviderChecksDetails) (GateOutcome, error) {
	verdict, status := domain.GateFail, domain.ExecFailed
	if d.Passed() {
		verdict, status = domain.GatePass, domain.ExecSucceeded
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return GateOutcome{}, err
	}
	if _, err := a.Store.RecordGateResult(ctx, store.NewGateResult{StepRunID: step.ID, Type: domain.GateProviderChecks,
		Policy: domain.GateBlocking, Verdict: verdict, Details: raw}); err != nil {
		return GateOutcome{}, err
	}
	if _, err := a.Store.TransitionStepRun(ctx, step.ID, domain.ExecRunning, status); err != nil {
		return GateOutcome{}, err
	}
	return checksOutcome(step.ID.String(), d), nil
}

func (a *Activities) recordedChecks(ctx context.Context, step domain.StepRun) (GateOutcome, error) {
	results, err := a.Store.ListGateResultsForRun(ctx, step.RunID)
	if err != nil {
		return GateOutcome{}, err
	}
	for _, r := range results {
		if r.StepRunID != step.ID {
			continue
		}
		var d domain.ProviderChecksDetails
		if err := json.Unmarshal(r.Details, &d); err != nil {
			return GateOutcome{}, fmt.Errorf("decode provider checks: %w", err)
		}
		return checksOutcome(step.ID.String(), d), nil
	}
	return GateOutcome{}, fmt.Errorf("provider checks step %s is %s but has no result", step.ID, step.Status)
}

// checksOutcome summarizes the checks for the queue and, when they failed,
// tells the coder which ones, where to read them and how their logs end.
func checksOutcome(stepRunID string, d domain.ProviderChecksDetails) GateOutcome {
	out := GateOutcome{StepRunID: stepRunID, Passed: d.Passed(), Inconclusive: d.Error != ""}
	var failed []domain.Check
	for _, c := range d.Checks {
		if c.State == domain.CheckFailure {
			failed = append(failed, c)
		}
	}
	switch {
	case d.Error != "":
		out.Summary = d.Error
	case d.State == domain.CheckNone:
		out.Summary = "the repo has no CI checks on this commit"
	case out.Passed:
		out.Summary = fmt.Sprintf("the repo's CI passed (%d checks)", len(d.Checks))
	default:
		names := make([]string, len(failed))
		var b strings.Builder
		b.WriteString("The repo's own CI failed on the commit Frankie's checks passed:\n")
		for i, c := range failed {
			names[i] = c.Name
			fmt.Fprintf(&b, "- %s", c.Name)
			if c.Summary != "" {
				fmt.Fprintf(&b, ": %s", c.Summary)
			}
			if c.URL != "" {
				fmt.Fprintf(&b, " (%s)", c.URL)
			}
			b.WriteString("\n")
			if c.Log != "" {
				fmt.Fprintf(&b, "  The end of its log:\n```\n%s\n```\n", c.Log)
			}
		}
		b.WriteString("Reproduce it with the commands the repo's CI runs, fix it and push.\n")
		out.Summary, out.Feedback = "the repo's CI failed: "+strings.Join(names, ", "), b.String()
	}
	return out
}
