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

// CheckPreserved fails when what the kept step wrote (From..To) is gone by
// Head, e.g. a fix that edits or deletes the test that shows the bug: a kept
// file removed or renamed since, or modified so that a block of lines the
// step added is no longer there as it wrote it (D97); edits elsewhere in the
// file are fine. A file the provider shows no diff for (binary or too large)
// is kept whole. It reads the provider's comparisons and the kept files at
// Head, records a preserve gate result and ends the StepRun; a retry of a
// finished one reports what was recorded. A step that changed nothing leaves
// nothing to keep.
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

// keptFile is what a kept step wrote in one file: the blocks of lines it
// added, or the whole file when the provider showed no diff.
type keptFile struct {
	blocks [][]string
	whole  bool
}

func (a *Activities) preserved(ctx context.Context, repo string, req PreserveRequest) (domain.PreserveDetails, error) {
	d := domain.PreserveDetails{Step: req.Step}
	if req.To == "" || req.From == req.To {
		return d, nil
	}
	written, complete, err := a.GitHub.Compare(ctx, repo, req.From, req.To)
	if err != nil {
		return d, err
	}
	kept := map[string]keptFile{}
	for _, f := range written {
		if f.Status != "removed" {
			d.Files = append(d.Files, f.Filename)
			kept[f.Filename] = keptFile{blocks: addedBlocks(f.Patch), whole: f.Patch == ""}
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
		k, ok := kept[f.Filename]
		switch {
		case f.Status == "renamed" && slices.Contains(d.Files, f.PreviousFilename):
			d.Changed = append(d.Changed, domain.ChangedAgain{File: f.PreviousFilename, Status: "renamed"})
		case !ok || f.Status == "unchanged":
		case f.Status == "removed" || k.whole:
			d.Changed = append(d.Changed, domain.ChangedAgain{File: f.Filename, Status: f.Status})
		case len(k.blocks) > 0:
			content, found, err := a.GitHub.FileAt(ctx, repo, f.Filename, req.Head)
			if err != nil {
				return d, err
			}
			if !found {
				d.Changed = append(d.Changed, domain.ChangedAgain{File: f.Filename, Status: "removed"})
			} else if lost := lostBlocks(k.blocks, string(content)); len(lost) > 0 {
				d.Changed = append(d.Changed, domain.ChangedAgain{File: f.Filename, Status: f.Status, Lines: lost})
			}
		}
	}
	return d, nil
}

// maxLostLines caps the lines a preserve result quotes per file.
const maxLostLines = 5

// addedBlocks reads a unified diff's added lines as blocks: each run of
// consecutive added lines, trimmed, without blank lines. A block that is
// only blank lines is dropped.
func addedBlocks(patch string) [][]string {
	var blocks [][]string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, cur)
		}
		cur = nil
	}
	for line := range strings.SplitSeq(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+"):
			if t := strings.TrimSpace(line[1:]); t != "" {
				cur = append(cur, t)
			}
		case strings.HasPrefix(line, "\\"): // "\ No newline at end of file" belongs to the line before
		default:
			flush()
		}
	}
	flush()
	return blocks
}

// lostBlocks quotes each block no longer in content as written: blocks must
// appear in order, each as consecutive lines (ignoring indentation and blank
// lines), so a line inserted into an added block, or a line of it changed or
// removed, loses it. The quote is the block's first line that is gone, or its
// first line when every line is still there but no longer together.
func lostBlocks(blocks [][]string, content string) []string {
	var lines []string
	for line := range strings.SplitSeq(content, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			lines = append(lines, t)
		}
	}
	var lost []string
	pos := 0
	for _, b := range blocks {
		at := indexBlock(lines[pos:], b)
		if at < 0 {
			if len(lost) < maxLostLines {
				quote := b[0]
				if i := slices.IndexFunc(b, func(l string) bool { return !slices.Contains(lines[pos:], l) }); i >= 0 {
					quote = b[i]
				}
				lost = append(lost, quote)
			}
			continue
		}
		pos += at + len(b)
	}
	return lost
}

// indexBlock is the index of the first run of lines equal to block, or -1.
func indexBlock(lines, block []string) int {
	for i := 0; i+len(block) <= len(lines); i++ {
		if slices.Equal(lines[i:i+len(block)], block) {
			return i
		}
	}
	return -1
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
		out.Summary = fmt.Sprintf("the %d files %s changed keep what it wrote", len(d.Files), d.Step)
	default:
		var files []string
		var lost strings.Builder
		for _, c := range d.Changed {
			files = append(files, fmt.Sprintf("%s (%s)", c.File, c.Status))
			for _, l := range c.Lines {
				fmt.Fprintf(&lost, "- %s: `%s`\n", c.File, l)
			}
		}
		out.Summary = fmt.Sprintf("changed what %s wrote: %s", d.Step, strings.Join(files, ", "))
		restore := ""
		if keptAt != "" {
			restore = fmt.Sprintf(" (`git checkout %s -- <file>` restores a file as it wrote it)", keptAt)
		}
		out.Feedback = fmt.Sprintf("Your change modified what the `%s` step wrote, which must stay as it is: %s. "+
			"It holds the test that shows the bug. Restore it%s and fix the code under test instead; "+
			"you may change other parts of these files.\n", d.Step, strings.Join(files, ", "), restore)
		if lost.Len() > 0 {
			out.Feedback += "Lines it added that are no longer there as it wrote them:\n" + lost.String()
		}
	}
	return out
}
