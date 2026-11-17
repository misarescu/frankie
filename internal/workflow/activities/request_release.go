package activities

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/client"
)

// ReleaseRequestSignal asks a repo's release workflow to release a commit
// (D114). Signals while a release runs are batched: the workflow then releases
// only the newest commit asked for.
const ReleaseRequestSignal = "release-request"

// ReleaseWorkflowID is the ID of repo's release workflow: one per repo, so its
// releases run one at a time.
func ReleaseWorkflowID(repo string) string { return "release/" + repo }

// releaseWorkflow is the release workflow's registered type name.
const releaseWorkflow = "Release"

// ReleaseInput is the release workflow's repo and the request carried over
// from its previous run.
type ReleaseInput struct {
	Repo    string
	Pending *ReleaseRequest `json:",omitempty"`
}

// ReleaseRequest asks for a release of a commit on the base branch.
type ReleaseRequest struct {
	SHA string
}

// requestRelease signals repo's release workflow to release sha, starting the
// workflow if none is running (signal-with-start). Without a Temporal client
// (some tests) nothing is requested.
func (a *Activities) requestRelease(ctx context.Context, repo, sha string) error {
	if a.Queue == nil {
		return nil
	}
	id := ReleaseWorkflowID(repo)
	_, err := a.Queue.SignalWithStartWorkflow(ctx, id, ReleaseRequestSignal, ReleaseRequest{SHA: sha},
		client.StartWorkflowOptions{ID: id, TaskQueue: a.TaskQueue}, releaseWorkflow, ReleaseInput{Repo: repo})
	if err != nil {
		return fmt.Errorf("request release on %s: %w", id, err)
	}
	return nil
}
