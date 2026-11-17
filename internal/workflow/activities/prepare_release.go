package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// PrepareReleaseRequest asks to prepare a release of a commit of repo.
type PrepareReleaseRequest struct {
	Repo string
	SHA  string
}

// ReleaseStage is a stage as the release snapshotted it from the project's
// configuration at the released commit (invariant 4, D114).
type ReleaseStage struct {
	Name   string
	Driver string
	Config json.RawMessage
	URL    string `json:",omitempty"`
	Verify string `json:",omitempty"`
	// Rollback: roll a failed deployment back (the stage's rollback setting,
	// or whether its driver can when the stage does not say).
	Rollback bool `json:",omitempty"`
}

// ReleasePlan is a prepared release: its ID and the stages it moves through,
// in order. Skip says why nothing is released; Failed why the release was
// recorded as failed before any stage (its stages are invalid).
type ReleasePlan struct {
	ReleaseID string               `json:",omitempty"`
	Stages    []ReleaseStage       `json:",omitempty"`
	Skip      string               `json:",omitempty"`
	Failed    string               `json:",omitempty"`
	Issues    []string             `json:",omitempty"` // keys of the issues it contains
	Status    domain.ReleaseStatus `json:",omitempty"`
}

// PrepareRelease reads the project's stages from `.factory/config.yaml` at the
// commit and, when it has release stages, records the release of the commit
// with the issues it contains (D110) and starts it (D114):
//   - no project, no configuration or no release stages: nothing is released;
//   - stages that do not parse, that their drivers reject (FR-RL-3), or that
//     need what this deployment cannot do yet (a human gate, verification
//     without a verifier): the release is recorded as failed with the reason;
//   - a release of the commit that already ended is not released again.
//
// Idempotent: the release of a commit is created once and started once.
func (a *Activities) PrepareRelease(ctx context.Context, req PrepareReleaseRequest) (ReleasePlan, error) {
	if req.Repo == "" || req.SHA == "" {
		return ReleasePlan{}, temporal.NewNonRetryableApplicationError("prepare release: repo and commit are required", "input", nil)
	}
	project, err := a.Store.GetProjectByName(ctx, req.Repo)
	if errors.Is(err, store.ErrNotFound) {
		return ReleasePlan{Skip: req.Repo + " is not a project"}, nil
	}
	if err != nil {
		return ReleasePlan{}, err
	}
	raw, found, err := a.GitHub.FileAt(ctx, req.Repo, config.ProjectConfigPath, req.SHA)
	if err != nil {
		return ReleasePlan{}, err
	}
	if !found {
		return ReleasePlan{Skip: "no " + config.ProjectConfigPath + " at " + req.SHA}, nil
	}
	stages, invalid := a.releaseStages(ctx, raw)
	if invalid == "" && len(stages) == 0 {
		return ReleasePlan{Skip: "the project has no release stages"}, nil
	}

	rel, _, err := a.Store.CreateRelease(ctx, store.NewRelease{ProjectID: project.ID, Repo: req.Repo, SHA: req.SHA})
	if err != nil {
		return ReleasePlan{}, err
	}
	plan := ReleasePlan{ReleaseID: rel.ID.String(), Status: rel.Status}
	if plan.Issues, err = a.issueKeys(ctx, rel); err != nil {
		return ReleasePlan{}, err
	}
	switch rel.Status {
	case domain.ReleasePending:
	case domain.ReleaseInProgress:
		// A retry after starting it: go on.
	default:
		plan.Skip = fmt.Sprintf("the release of %s already %s", req.SHA, rel.Status)
		return plan, nil
	}
	if invalid != "" {
		if _, err := a.Store.TransitionRelease(ctx, rel.ID, rel.Status, domain.ReleaseFailed, invalid); err != nil {
			return ReleasePlan{}, err
		}
		plan.Failed, plan.Status = invalid, domain.ReleaseFailed
		return plan, nil
	}
	if rel.Status == domain.ReleasePending {
		if _, err := a.Store.TransitionRelease(ctx, rel.ID, domain.ReleasePending, domain.ReleaseInProgress, ""); err != nil {
			return ReleasePlan{}, err
		}
	}
	plan.Stages, plan.Status = stages, domain.ReleaseInProgress
	return plan, nil
}

// releaseStages parses and checks the configuration's stages and returns the
// release stages, or why they cannot be used.
func (a *Activities) releaseStages(ctx context.Context, raw []byte) (stages []ReleaseStage, invalid string) {
	p, err := config.ParseProject(raw, math.MaxInt)
	if err != nil {
		return nil, err.Error()
	}
	if len(p.Stages) == 0 {
		return nil, ""
	}
	reg := a.driverRegistry()
	if err := config.ValidateStages(ctx, p.Stages, reg); err != nil {
		return nil, err.Error()
	}
	var unsupported []string
	for _, s := range config.ReleaseStages(p.Stages, reg) {
		if s.Gate != "" {
			unsupported = append(unsupported, fmt.Sprintf("stages.%s: gate %s is not supported yet (M8)", s.Name, s.Gate))
		}
		if s.Verify != "" && a.Verifier == nil {
			unsupported = append(unsupported, fmt.Sprintf("stages.%s: verify %s is not available in this deployment yet", s.Name, s.Verify))
		}
		d, err := reg.Get(s.Driver)
		if err != nil {
			return nil, err.Error()
		}
		rollback := d.Capabilities().Rollback
		if s.Rollback != nil {
			rollback = *s.Rollback
		}
		stages = append(stages, ReleaseStage{Name: s.Name, Driver: s.Driver, Config: s.Config, URL: s.URL, Verify: s.Verify, Rollback: rollback})
	}
	if len(unsupported) > 0 {
		return nil, config.ProjectConfigPath + ": " + strings.Join(unsupported, "; ")
	}
	return stages, ""
}

// issueKeys names the issues a release contains.
func (a *Activities) issueKeys(ctx context.Context, rel domain.Release) ([]string, error) {
	keys := make([]string, 0, len(rel.IssueIDs))
	for _, id := range rel.IssueIDs {
		is, err := a.Store.GetIssue(ctx, id)
		if err != nil {
			return nil, err
		}
		keys = append(keys, is.Key)
	}
	return keys, nil
}
