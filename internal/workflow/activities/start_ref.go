package activities

import (
	"context"
	"fmt"
)

// StartPoint is where an agent step starts: the issue's agent branch when an
// earlier step or attempt already pushed it (git history is the handoff between
// steps, FR-SB-6), else the base branch.
type StartPoint struct {
	CloneRef   string // branch the runner clones
	FilesRef   string // ref to read mise.toml and .factory/config.yaml at
	Continuing bool   // the agent branch exists and the step builds on it
}

// startPoint looks the agent branch up now. Every Activity of a step asks
// again, so they agree unless the branch is pushed in between, which only the
// step itself does.
func (a *Activities) startPoint(ctx context.Context, in IssueRunInput) (StartPoint, error) {
	branch := BranchName(in.IssueKey, in.Title)
	sha, found, err := a.GitHub.BranchHead(ctx, in.Repo, branch)
	if err != nil {
		return StartPoint{}, fmt.Errorf("look up %s: %w", branch, err)
	}
	if found {
		return StartPoint{CloneRef: branch, FilesRef: sha, Continuing: true}, nil
	}
	return StartPoint{CloneRef: in.BaseBranch, FilesRef: in.BaseBranch}, nil
}
