package activities

import (
	"context"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/store"
)

// OpenChangeRequest opens (or finds) the PR for the pushed branch and records it.
func (a *Activities) OpenChangeRequest(ctx context.Context, in IssueRunInput, run StartRunResult, s SessionResult) (string, error) {
	pr, err := a.GitHub.OpenPullRequest(ctx, in.Repo, s.Branch, in.BaseBranch, in.IssueKey+": "+in.Title, PRBody(in, s))
	if err != nil {
		return "", err
	}
	issueID, _ := uuid.Parse(in.IssueID)
	runID, _ := uuid.Parse(run.RunID)
	head := s.CommitSHA
	if pr.Head != "" {
		head = pr.Head
	}
	if _, err := a.Store.UpsertChangeRequest(ctx, store.NewChangeRequest{IssueID: issueID, RunID: runID, Repo: in.Repo,
		Number: pr.Number, ProviderRef: pr.URL, Branch: s.Branch, HeadSHA: head}); err != nil {
		return "", err
	}
	return pr.URL, nil
}
