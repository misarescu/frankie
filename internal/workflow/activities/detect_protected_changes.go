package activities

import (
	"context"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/guard"
)

// changedFileStatus maps the provider's file statuses to guard's; copied and
// changed files count as modified.
var changedFileStatus = map[string]guard.FileStatus{
	"added": guard.Added, "removed": guard.Removed, "renamed": guard.Renamed,
}

// DetectProtectedChanges inspects the change the run pushed, as the provider
// diffs it from the run's base commit, for protected paths and deleted or
// skipped tests (invariant 6, docs/spec/05). It runs here, in the control
// plane, never in the agent's pod. It also records the changed files on the
// issue for overlap warnings (D63); that record is best effort and never
// changes the report.
func (a *Activities) DetectProtectedChanges(ctx context.Context, in IssueRunInput, req GuardRequest) (guard.Report, error) {
	changed, complete, err := a.GitHub.Compare(ctx, in.Repo, req.BaseSHA, req.HeadSHA)
	if err != nil {
		return guard.Report{}, err
	}
	files := make([]guard.File, 0, len(changed))
	paths := make([]string, 0, len(changed))
	for _, c := range changed {
		paths = append(paths, c.Filename)
		if c.PreviousFilename != "" {
			paths = append(paths, c.PreviousFilename)
		}
		status, ok := changedFileStatus[c.Status]
		if !ok {
			status = guard.Modified
		}
		files = append(files, guard.File{Path: c.Filename, PreviousPath: c.PreviousFilename, Status: status,
			Patch: c.Patch, PatchMissing: c.Patch == "" && status != guard.Renamed})
	}
	if id, err := uuid.Parse(in.IssueID); err == nil {
		if err := a.Store.SetIssueChangedPaths(ctx, id, paths); err != nil {
			a.Log.Warn("record changed paths", "issue", in.IssueKey, "error", err)
		}
	}
	incomplete := ""
	if !complete {
		incomplete = "The change has more files than the provider lists, so it could not be inspected in full"
	}
	return guard.Check(files, req.Patterns, incomplete), nil
}
