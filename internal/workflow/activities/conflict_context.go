package activities

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/misarescu/frankie/internal/connectors/github"
)

// ConflictRequest names a branch that conflicts with its base branch.
type ConflictRequest struct {
	Issue   IssueRunInput
	HeadSHA string   // the branch's head
	BaseSHA string   // the base branch commit it conflicts with
	Files   []string // the conflicting files the update reported
}

// Limits on what the coder is shown of each side.
const (
	conflictPatchBytes = 4000
	conflictTotalBytes = 24000
)

// ConflictContext shows, for each conflicting file, what the base branch and
// the agent's branch each changed since they forked (D89), so the coder can
// keep both sides where both are needed: in M5.1 conflicts were in
// registry-style files (interfaces, struct fields, imports) where both belong.
func (a *Activities) ConflictContext(ctx context.Context, req ConflictRequest) (string, error) {
	repo := req.Issue.Repo
	theirs, completeTheirs, err := a.GitHub.Compare(ctx, repo, req.HeadSHA, req.BaseSHA)
	if err != nil {
		return "", err
	}
	ours, completeOurs, err := a.GitHub.Compare(ctx, repo, req.BaseSHA, req.HeadSHA)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, f := range req.Files {
		if b.Len() > conflictTotalBytes {
			fmt.Fprintf(&b, "\n(More conflicting files; run `git diff` after the merge to see them.)\n")
			break
		}
		fmt.Fprintf(&b, "### %s\n\n", f)
		writeSide(&b, "Changed on "+req.Issue.BaseBranch+" since your branch forked", patchOf(theirs, f), completeTheirs)
		writeSide(&b, "Changed on your branch", patchOf(ours, f), completeOurs)
	}
	return b.String(), nil
}

func patchOf(files []github.ChangedFile, path string) *github.ChangedFile {
	i := slices.IndexFunc(files, func(f github.ChangedFile) bool { return f.Filename == path })
	if i < 0 {
		return nil
	}
	return &files[i]
}

func writeSide(b *strings.Builder, title string, f *github.ChangedFile, complete bool) {
	switch {
	case f == nil && complete:
		fmt.Fprintf(b, "%s: nothing.\n\n", title)
	case f == nil:
		fmt.Fprintf(b, "%s: not listed (the provider did not list every file).\n\n", title)
	case f.Patch == "":
		fmt.Fprintf(b, "%s: %s, no text diff (binary or too large).\n\n", title, f.Status)
	default:
		patch := f.Patch
		if len(patch) > conflictPatchBytes {
			patch = patch[:conflictPatchBytes] + "\n... (cut)"
		}
		fmt.Fprintf(b, "%s:\n```diff\n%s\n```\n\n", title, patch)
	}
}
