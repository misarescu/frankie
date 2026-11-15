package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// PreserveRequest is one execution of a preserve gate (D95): the commits the
// kept step's last execution went From and To, and the Head to check.
type PreserveRequest struct {
	RunID     string
	StepRunID string
	Step      string
	From, To  string
	Head      string
}

// CheckPreserved fails when a file the kept step changed (From..To) has
// changed again by Head, e.g. a fix that edits or deletes the test that shows
// the bug. It reads the provider's comparisons, records a preserve gate
// result and ends the StepRun; a retry of a finished one reports what was
// recorded. A step that changed nothing leaves nothing to keep.
func (a *Activities) CheckPreserved(ctx context.Context, in IssueRunInput, req PreserveRequest) (GateOutcome, error) {
	id, err := uuid.Parse(req.StepRunID)
	if err != nil {
		return GateOutcome{}, temporal.NewNonRetryableApplicationError("bad step run id", "input", err)
	}
	step, err := a.Store.GetStepRun(ctx, id)
	if err != nil {
		return GateOutcome{}, err
	}
	if step.Status.Terminal() {
		return a.recordedPreserve(ctx, step)
	}
	if req.Head != "" {
		if _, err := a.Store.SetStepRunCommit(ctx, id, req.Head); err != nil {
			return GateOutcome{}, err
		}
	}
	if _, err := a.Store.TransitionStepRun(ctx, id, domain.ExecPending, domain.ExecRunning); err != nil && step.Status != domain.ExecRunning {
		return GateOutcome{}, err
	}
	d, err := a.preserved(ctx, in.Repo, req)
	if err != nil {
		return GateOutcome{}, err
	}
	verdict, status := domain.GateFail, domain.ExecFailed
	if d.Passed() {
		verdict, status = domain.GatePass, domain.ExecSucceeded
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return GateOutcome{}, err
	}
	if _, err := a.Store.RecordGateResult(ctx, store.NewGateResult{StepRunID: id, Type: domain.GatePreserve, Policy: domain.GateBlocking,
		Verdict: verdict, Details: raw}); err != nil {
		return GateOutcome{}, err
	}
	if _, err := a.Store.TransitionStepRun(ctx, id, domain.ExecRunning, status); err != nil {
		return GateOutcome{}, err
	}
	return preserveOutcome(id.String(), d, req.To), nil
}

func (a *Activities) preserved(ctx context.Context, repo string, req PreserveRequest) (domain.PreserveDetails, error) {
	d := domain.PreserveDetails{Step: req.Step}
	if req.To == "" || req.From == req.To {
		return d, nil
	}
	kept, complete, err := a.GitHub.Compare(ctx, repo, req.From, req.To)
	if err != nil {
		return d, err
	}
	for _, f := range kept {
		if f.Status != "removed" {
			d.Files = append(d.Files, f.Filename)
		}
	}
	slices.Sort(d.Files)
	if !complete {
		d.Error = fmt.Sprintf("the provider did not list every file %s changed", req.Step)
		return d, nil
	}
	if req.Head == "" || req.Head == req.To {
		return d, nil
	}
	later, complete, err := a.GitHub.Compare(ctx, repo, req.To, req.Head)
	if err != nil {
		return d, err
	}
	if !complete {
		d.Error = fmt.Sprintf("the provider did not list every file changed since %s", req.Step)
		return d, nil
	}
	for _, f := range later {
		switch {
		case f.Status == "renamed" && slices.Contains(d.Files, f.PreviousFilename):
			d.Changed = append(d.Changed, domain.ChangedAgain{File: f.PreviousFilename, Status: "renamed"})
		case slices.Contains(d.Files, f.Filename) && f.Status != "unchanged":
			d.Changed = append(d.Changed, domain.ChangedAgain{File: f.Filename, Status: f.Status})
		}
	}
	return d, nil
}

func (a *Activities) recordedPreserve(ctx context.Context, step domain.StepRun) (GateOutcome, error) {
	results, err := a.Store.ListGateResultsForRun(ctx, step.RunID)
	if err != nil {
		return GateOutcome{}, err
	}
	for _, r := range results {
		if r.StepRunID != step.ID {
			continue
		}
		var d domain.PreserveDetails
		if err := json.Unmarshal(r.Details, &d); err != nil {
			return GateOutcome{}, fmt.Errorf("decode preserve details: %w", err)
		}
		return preserveOutcome(step.ID.String(), d, ""), nil
	}
	return GateOutcome{}, fmt.Errorf("preserve gate %s is %s but has no result", step.ID, step.Status)
}

// preserveOutcome tells the workflow, and the agent sent back, what changed.
func preserveOutcome(stepRunID string, d domain.PreserveDetails, keptAt string) GateOutcome {
	out := GateOutcome{StepRunID: stepRunID, Passed: d.Passed()}
	switch {
	case d.Error != "":
		out.Summary, out.Inconclusive = d.Error, true
	case out.Passed && len(d.Files) == 0:
		out.Summary = fmt.Sprintf("%s changed nothing to keep", d.Step)
	case out.Passed:
		out.Summary = fmt.Sprintf("the %d files %s changed are as it left them", len(d.Files), d.Step)
	default:
		var files []string
		for _, c := range d.Changed {
			files = append(files, fmt.Sprintf("%s (%s)", c.File, c.Status))
		}
		out.Summary = fmt.Sprintf("changed what %s wrote: %s", d.Step, strings.Join(files, ", "))
		restore := ""
		if keptAt != "" {
			restore = fmt.Sprintf(" (`git checkout %s -- <file>`)", keptAt)
		}
		out.Feedback = fmt.Sprintf("Your change modified files the `%s` step wrote, which must stay as they are: %s. "+
			"They hold the test that shows the bug. Restore them%s and fix the code under test instead.\n", d.Step, strings.Join(files, ", "), restore)
	}
	return out
}
