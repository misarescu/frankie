package activities

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/images"
)

// EnsureToolchainImage returns the toolchain image for the code the agent step
// starts from (the issue's agent branch if it exists, else the base branch): built from its mise.toml and mise.lock on top of the platform base
// image, or found in the registry when an identical one exists (D30). It returns
// "" when toolchain images are not configured or the repo declares no mise.toml,
// so the agent runs in the platform base image. Idempotent: the image is a pure
// function of the inputs and concurrent builds of it share one Job.
func (a *Activities) EnsureToolchainImage(ctx context.Context, in IssueRunInput) (string, error) {
	if a.Toolchain == nil {
		return "", nil
	}
	start, err := a.startPoint(ctx, in)
	if err != nil {
		return "", err
	}
	return a.toolchainImage(ctx, in.Repo, start.FilesRef)
}

// toolchainImage resolves the toolchain image of repo at ref (a branch or a
// SHA); "" means the platform base image.
func (a *Activities) toolchainImage(ctx context.Context, repo, ref string) (string, error) {
	if a.Toolchain == nil {
		return "", nil
	}
	// Read both files at one commit, so a push in between cannot mix versions.
	sha, err := a.GitHub.ResolveRef(ctx, repo, ref)
	if err != nil {
		return "", fmt.Errorf("resolve %s@%s: %w", repo, ref, err)
	}
	toml, found, err := a.GitHub.FileAt(ctx, repo, "mise.toml", sha)
	if err != nil {
		return "", fmt.Errorf("read mise.toml: %w", err)
	}
	if !found {
		a.Log.Info("repo declares no mise.toml; using the base image", "repo", repo, "sha", sha)
		return "", nil
	}
	lock, _, err := a.GitHub.FileAt(ctx, repo, "mise.lock", sha)
	if err != nil {
		return "", fmt.Errorf("read mise.lock: %w", err)
	}
	inputs := images.Inputs{Base: a.Agent.RunnerImage, MiseToml: toml, MiseLock: lock}
	if err := inputs.Validate(); err != nil {
		return "", temporal.NewNonRetryableApplicationError("invalid toolchain inputs", "config", err)
	}
	activity.RecordHeartbeat(ctx, "resolving toolchain image")
	img, err := a.Toolchain.Ensure(ctx, repo, inputs)
	if err != nil {
		return "", err
	}
	a.Log.Info("toolchain image ready", "repo", repo, "sha", sha, "image", img.Ref, "built", img.Built)
	return img.Ref, nil
}
