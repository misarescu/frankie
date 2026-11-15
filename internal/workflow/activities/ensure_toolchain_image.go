package activities

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/images"
)

// EnsureToolchainImage returns the image for the code the agent step starts
// from (the issue's agent branch if it exists, else the base branch): its
// dependency image when the repo declares dependencies (M3.1), else its
// toolchain image built from mise.toml and mise.lock on the platform base
// image (D30), each reused when one of identical inputs is on record (D45). It
// returns "" when toolchain images are not configured or the repo declares no
// mise.toml, so the agent runs in the platform base image. Idempotent: images
// are pure functions of their inputs and concurrent builds share one Job. The
// Activity keeps its name so recorded histories still replay.
func (a *Activities) EnsureToolchainImage(ctx context.Context, in IssueRunInput) (string, error) {
	return a.ensureImage(ctx, in, nil)
}

// EnsureStepImage is EnsureToolchainImage for one execution of an agent step:
// a build the step waits for is recorded on the step's stream, with its log
// (D93), so the step says why it has not started.
func (a *Activities) EnsureStepImage(ctx context.Context, in IssueRunInput, runID, stepRunID string) (string, error) {
	run, err1 := uuid.Parse(runID)
	step, err2 := uuid.Parse(stepRunID)
	if err := errors.Join(err1, err2); err != nil {
		return "", temporal.NewNonRetryableApplicationError("bad id", "input", err)
	}
	return a.ensureImage(ctx, in, a.progressFor(ctx, run, step))
}

func (a *Activities) ensureImage(ctx context.Context, in IssueRunInput, progress images.Progress) (string, error) {
	if a.Toolchain == nil {
		return "", nil
	}
	start, err := a.startPoint(ctx, in)
	if err != nil {
		return "", err
	}
	ref, err := a.toolchainImage(ctx, in, start.FilesRef, progress)
	if errors.Is(err, errInvalidProjectConfig) {
		// The repo must fix it; retrying cannot.
		return "", temporal.NewNonRetryableApplicationError(err.Error(), "config", err)
	}
	return ref, err
}

// buildEgressTTL is how long a build's egress token lasts: longer than a
// build's deadline (toolchains.build.deadline, 50 minutes on the concrete
// profile) and the activity that waits for it.
const buildEgressTTL = 2 * time.Hour

// toolchainImage resolves the image steps of the run's repo at ref (a branch
// or a SHA) run in: its dependency image when the repo declares dependencies
// (M3.1), else its toolchain image; "" means the platform base image. Every
// file is read at one commit, so a push in between cannot mix versions. A
// configuration the repo got wrong is wrapped in errInvalidProjectConfig.
// Builds reach the project's egress hosts (D80).
func (a *Activities) toolchainImage(ctx context.Context, in IssueRunInput, ref string, progress images.Progress) (string, error) {
	if a.Toolchain == nil {
		return "", nil
	}
	repo := in.Repo
	proxy := ""
	if len(in.EgressAllow) > 0 {
		var err error
		if proxy, err = a.egressProxyURL(in, buildEgressTTL); err != nil {
			return "", err
		}
	}
	sha, err := a.GitHub.ResolveRef(ctx, repo, ref)
	if err != nil {
		return "", fmt.Errorf("resolve %s@%s: %w", repo, ref, err)
	}
	project, err := a.projectConfig(ctx, repo, sha)
	if err != nil {
		return "", err
	}
	toml, found, err := a.GitHub.FileAt(ctx, repo, "mise.toml", sha)
	if err != nil {
		return "", fmt.Errorf("read mise.toml: %w", err)
	}
	if !found {
		if project.Dependencies != nil {
			return "", fmt.Errorf("%w: dependencies need a mise.toml: the warm-up runs in the toolchain image", errInvalidProjectConfig)
		}
		a.Log.Info("repo declares no mise.toml; using the base image", "repo", repo, "sha", sha)
		return "", nil
	}
	lock, _, err := a.GitHub.FileAt(ctx, repo, "mise.lock", sha)
	if err != nil {
		return "", fmt.Errorf("read mise.lock: %w", err)
	}
	inputs := images.Inputs{Base: a.Agent.RunnerImage, MiseToml: toml, MiseLock: lock}
	if err := inputs.Validate(); err != nil {
		return "", fmt.Errorf("%w: toolchain: %w", errInvalidProjectConfig, err)
	}
	repository, err := images.Repository(repo)
	if err != nil {
		return "", temporal.NewNonRetryableApplicationError("invalid repo", "config", err)
	}
	activity.RecordHeartbeat(ctx, "resolving toolchain image")
	img, err := a.Toolchain.EnsureWith(ctx, repository, inputs, images.EnsureOptions{EgressProxyURL: proxy, Progress: progress})
	if err != nil {
		return "", err
	}
	a.Log.Info("toolchain image ready", "repo", repo, "sha", sha, "image", img.Ref, "built", img.Built)
	if project.Dependencies == nil {
		return img.Ref, nil
	}

	deps, err := a.dependencyFiles(ctx, repo, sha, *project.Dependencies)
	if err != nil {
		return "", err
	}
	// mise resolves a tool's shim from the mise.toml near the working
	// directory, so the warm-up needs the toolchain's files whatever the
	// declaration matched; they are already part of the toolchain digest.
	deps["mise.toml"] = toml
	if len(lock) > 0 {
		deps["mise.lock"] = lock
	}
	spec := images.Dependencies{Toolchain: img, Files: deps, Run: project.Dependencies.Run}
	if err := spec.Validate(); err != nil {
		return "", fmt.Errorf("%w: dependencies: %w", errInvalidProjectConfig, err)
	}
	depRepository, err := images.DependencyRepository(repo)
	if err != nil {
		return "", temporal.NewNonRetryableApplicationError("invalid repo", "config", err)
	}
	activity.RecordHeartbeat(ctx, "resolving dependency image")
	dep, err := a.Toolchain.EnsureWith(ctx, depRepository, spec, images.EnsureOptions{EgressProxyURL: proxy, Progress: progress})
	if err != nil {
		return "", err
	}
	a.Log.Info("dependency image ready", "repo", repo, "sha", sha, "image", dep.Ref, "built", dep.Built, "files", len(deps))
	return dep.Ref, nil
}

// dependencyFiles reads the files a repo's dependencies declaration matches at sha.
func (a *Activities) dependencyFiles(ctx context.Context, repo, sha string, d config.Dependencies) (map[string][]byte, error) {
	paths, complete, err := a.GitHub.ListFiles(ctx, repo, sha)
	if err != nil {
		return nil, err
	}
	if !complete {
		return nil, fmt.Errorf("%w: dependencies: the repo has more files than the provider lists, so the declared files cannot be found reliably", errInvalidProjectConfig)
	}
	files := map[string][]byte{}
	for _, p := range paths {
		if !d.Matches(p) {
			continue
		}
		b, found, err := a.GitHub.FileAt(ctx, repo, p, sha)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		if found {
			files[p] = b
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: dependencies.files match no file in the repo", errInvalidProjectConfig)
	}
	return files, nil
}
