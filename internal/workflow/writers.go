package workflow

import (
	"cmp"
	"fmt"
	"strings"

	"go.temporal.io/sdk/workflow"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// writerCount is how many of a group's branches write: agent steps whose role
// returns no contract, unlike reviewers.
func (it *interpreter) writerCount(s config.Step) int {
	n := 0
	for _, b := range s.Parallel {
		if b.Agent != nil && it.in.Snapshot.Roles[b.Agent.RoleName()].OutputContract == "" {
			n++
		}
	}
	return n
}

// alongside are the roles of a group's writers other than its i-th branch,
// in definition order, which work on the issue at the same time (D122).
func (it *interpreter) alongside(s config.Step, i int) []string {
	var roles []string
	for j, b := range s.Parallel {
		if j != i && b.Agent != nil {
			roles = append(roles, b.Agent.RoleName())
		}
	}
	return roles
}

// sideBranch is the branch of a writer after a group's first (D122), inside
// the git token's agent/<issue-key>/ scope.
func sideBranch(branch, stepID string) string { return branch + "--" + stepID }

// writerResult is one writer's execution in a group. unchanged is set for a
// writer that pushed nothing but may dispute the failure that sent it back.
type writerResult struct {
	session   activities.SessionResult
	stepRunID string
	reason    string
	branch    string
	unchanged bool
}

// writers runs a group's writers at the same time (D122), or only the one a
// loop back targets. The first works on the issue's branch; each other one on
// its own branch from the base branch, so it never sees the first one's work.
// The first failure cancels the others. Once all succeeded, each side branch
// that ran is merged into the issue's branch, in definition order.
//
// A writer a failed gate sent back may change nothing instead, disputing the
// failure (D128). When the first one does, the group's other writers run with
// its report and fix their work; when they too change nothing, or the writer
// sent back was another one, a person decides.
func (it *interpreter) writers(ctx workflow.Context, pc int, s config.Step, only string) (int, string) {
	disputable := only != "" && it.canDispute(ctx)
	return it.runWriters(ctx, pc, s, func(i int) bool { return only == "" || s.Parallel[i].ID == only }, disputable, nil)
}

// runWriters runs the writers pick selects. coder is the first writer's
// report when the others run because it disputed a failure.
func (it *interpreter) runWriters(ctx workflow.Context, pc int, s config.Step, pick func(int) bool, disputable bool, coder *agentReport) (int, string) {
	results := make([]*writerResult, len(s.Parallel))
	group, cancel := workflow.WithCancel(ctx)
	defer cancel()
	router := newSignalRouter()
	routing, stopRouting := workflow.WithCancel(ctx)
	defer stopRouting()
	workflow.Go(routing, router.route)

	wg := workflow.NewWaitGroup(ctx)
	failed := -1
	for i, b := range s.Parallel {
		if !pick(i) {
			continue
		}
		branch := it.run.Branch
		if i > 0 {
			branch = sideBranch(it.run.Branch, b.ID)
		}
		r := &writerResult{branch: branch}
		results[i] = r
		wg.Add(1)
		workflow.Go(group, func(c workflow.Context) {
			defer wg.Done()
			r.session, r.stepRunID, r.reason = it.agentSessionOn(c, b, branch, router, it.alongside(s, i))
			if r.stepRunID != "" {
				if r.reason == "" && !r.session.Pushed {
					if r.unchanged = disputable; !disputable {
						r.reason = noChanges
					}
				}
				it.finishStep(c, r.stepRunID, r.reason == "")
			}
			if r.reason != "" && failed < 0 {
				failed = i
				cancel()
			}
		})
	}
	wg.Wait(ctx)
	stopRouting()
	if failed >= 0 {
		return 0, fmt.Sprintf("branch %s: %s", s.Parallel[failed].ID, results[failed].reason)
	}

	var reports []agentReport
	if coder != nil {
		reports = append(reports, *coder)
	}
	pushed := false
	for i, r := range results {
		switch {
		case r == nil:
		case r.unchanged:
			reports = append(reports, agentReport{step: s.Parallel[i].ID, role: s.Parallel[i].Agent.RoleName(), text: r.session.Result})
			results[i] = nil
		default:
			pushed = true
		}
	}
	if !pushed {
		if coder == nil && len(s.Parallel) > 1 && len(reports) == 1 && reports[0].step == s.Parallel[0].ID {
			// The coder found the failure in the other writers' work.
			it.feedback = disputeFeedback(it.back, reports[0])
			return it.runWriters(ctx, pc, s, func(i int) bool { return i > 0 }, true, &reports[0])
		}
		it.dispute = &dispute{reports: reports}
		return it.escalateDispute(ctx)
	}

	for i, b := range s.Parallel {
		r := results[i]
		if r == nil {
			continue
		}
		if i == 0 {
			from := cmp.Or(it.session.CommitSHA, it.in.Snapshot.BaseSHA, it.in.Issue.BaseBranch)
			it.pushed[b.ID] = [2]string{from, r.session.CommitSHA}
			it.session = r.session
			continue
		}
		from := cmp.Or(it.sideHeads[b.ID], it.in.Snapshot.BaseSHA, it.in.Issue.BaseBranch)
		if coder != nil {
			// Fixed after a dispute: the whole side branch stays the writer's.
			from = cmp.Or(it.in.Snapshot.BaseSHA, it.in.Issue.BaseBranch)
		}
		it.pushed[b.ID] = [2]string{from, r.session.CommitSHA}
		it.sideHeads[b.ID] = r.session.CommitSHA
	}
	it.feedback, it.back = "", nil
	for i := range s.Parallel {
		if r := results[i]; r != nil && i > 0 {
			if why := it.mergeSide(ctx, s, r.branch); why != "" {
				return 0, why
			}
		}
	}
	return pc + 1, ""
}

// mergeSide merges a side writer's branch into the issue's branch, at the
// commit the run last checked, in the git-only pod the merge queue updates
// branches with (D87, D122), recorded as the group's merge step.
func (it *interpreter) mergeSide(ctx workflow.Context, s config.Step, side string) string {
	id := s.ID + "-merge"
	stepRunID, err := it.startStep(ctx, id, domain.StepAction)
	if err != nil {
		return err.Error()
	}
	var out activities.BranchUpdateResult
	err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, updateOptions), it.a.UpdateBranch, activities.BranchUpdateRequest{
		Issue: it.in.Issue, RunID: it.run.RunID, StepRunID: stepRunID, StepID: id,
		Branch: it.run.Branch, HeadSHA: it.session.CommitSHA, Method: domain.MergeCommit, From: side,
	}).Get(ctx, &out)
	if err != nil {
		it.finishStep(ctx, stepRunID, false)
		return fmt.Sprintf("step %s: merging %s failed: %s", id, side, err)
	}
	switch out.Outcome {
	case activities.BranchUpdated:
		it.session.CommitSHA = out.HeadSHA
	case activities.BranchUpToDate:
	case activities.BranchConflict:
		return fmt.Sprintf("step %s: %s conflicts with %s in %s; each writer must keep to its own files",
			id, side, it.run.Branch, strings.Join(out.Conflicts, ", "))
	default:
		return fmt.Sprintf("step %s: %s moved while %s was merged into it", id, it.run.Branch, side)
	}
	return ""
}
