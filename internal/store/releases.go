package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// NewRelease is the input for CreateRelease.
type NewRelease struct {
	ProjectID uuid.UUID
	Repo      string
	SHA       string
}

// CreateRelease records a release of a commit with the issues it contains
// (D110): the project's merged issues whose change request in the repo merged no
// later than the one that made the commit, or every merged issue when no recorded
// merge made it. A merged issue is in every release made until one containing it
// succeeds. Creating the release of the same commit again returns the first one,
// so retried Activities stay idempotent; created reports whether this call made it.
func (s *Store) CreateRelease(ctx context.Context, in NewRelease) (rel domain.Release, created bool, err error) {
	if in.ProjectID == uuid.Nil || in.Repo == "" || in.SHA == "" {
		return domain.Release{}, false, errors.New("store: release needs a project, repo and commit")
	}
	var id uuid.UUID
	err = s.inTx(ctx, func(q *db.Queries) error {
		row, err := q.InsertRelease(ctx, db.InsertReleaseParams{ID: domain.NewID(), ProjectID: in.ProjectID, Repo: in.Repo, Sha: in.SHA})
		if errors.Is(mapErr("", err), ErrNotFound) {
			cur, err := q.GetReleaseByCommit(ctx, db.GetReleaseByCommitParams{ProjectID: in.ProjectID, Repo: in.Repo, Sha: in.SHA})
			if err != nil {
				return mapErr("get release", err)
			}
			id = cur.ID
			return nil
		}
		if err != nil {
			return mapErr("insert release", err)
		}
		id, created = row.ID, true
		return mapErr("link release issues", q.LinkReleaseIssues(ctx, db.LinkReleaseIssuesParams{
			ReleaseID: row.ID, ProjectID: in.ProjectID, Repo: in.Repo, Sha: in.SHA}))
	})
	if err != nil {
		return domain.Release{}, false, err
	}
	rel, err = s.GetRelease(ctx, id)
	return rel, created, err
}

// GetRelease returns a release with its issues.
func (s *Store) GetRelease(ctx context.Context, id uuid.UUID) (domain.Release, error) {
	row, err := s.q.GetRelease(ctx, id)
	if err != nil {
		return domain.Release{}, mapErr("get release", err)
	}
	return s.releaseWithIssues(ctx, s.q, row)
}

// ListReleases returns a project's latest releases with their issues, newest first.
func (s *Store) ListReleases(ctx context.Context, projectID uuid.UUID, limit int) ([]domain.Release, error) {
	if limit < 1 {
		return nil, errors.New("store: list releases: limit must be positive")
	}
	rows, err := s.q.ListReleasesForProject(ctx, db.ListReleasesForProjectParams{ProjectID: projectID, MaxRows: int32(limit)})
	if err != nil {
		return nil, mapErr("list releases", err)
	}
	out := make([]domain.Release, 0, len(rows))
	for _, r := range rows {
		rel, err := s.releaseWithIssues(ctx, s.q, r)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, nil
}

// TransitionRelease moves a release between statuses with the same contract as
// TransitionRun: an invalid transition is rejected, a release found in to is
// returned as it is, and one in another status is a conflict. reason, when not
// empty, says why it failed or was rolled back. A release that
// succeeds has reached its last stage, so its issues still merged move to
// released in the same transaction, each with a state_change event (D110).
func (s *Store) TransitionRelease(ctx context.Context, id uuid.UUID, from, to domain.ReleaseStatus, reason string) (domain.Release, error) {
	if !domain.CanTransitionRelease(from, to) {
		return domain.Release{}, fmt.Errorf("store: release %s: %w: %s -> %s", id, domain.ErrInvalidTransition, from, to)
	}
	err := s.inTx(ctx, func(q *db.Queries) error {
		if _, err := q.TransitionRelease(ctx, db.TransitionReleaseParams{ID: id, FromStatus: string(from), ToStatus: string(to), Reason: reason}); err != nil {
			return mapErr("transition release", err)
		}
		if err := appendStateChange(ctx, q, nil, nil, domain.StateChange{Entity: "release", ID: id, From: string(from), To: string(to)}); err != nil {
			return err
		}
		if to != domain.ReleaseSucceeded {
			return nil
		}
		issueIDs, err := q.ListReleaseIssueIDs(ctx, id)
		if err != nil {
			return mapErr("list release issues", err)
		}
		for _, issueID := range issueIDs {
			_, err := q.TransitionIssue(ctx, db.TransitionIssueParams{ID: issueID,
				FromStatus: string(domain.IssueMerged), ToStatus: string(domain.IssueReleased)})
			if errors.Is(mapErr("", err), ErrNotFound) {
				continue // released by an earlier release
			}
			if err != nil {
				return mapErr("release issue", err)
			}
			if err := appendStateChange(ctx, q, nil, nil, domain.StateChange{Entity: "issue", ID: issueID,
				From: string(domain.IssueMerged), To: string(domain.IssueReleased)}); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		cur, getErr := s.GetRelease(ctx, id)
		if getErr != nil {
			return domain.Release{}, getErr
		}
		if cur.Status == to {
			return cur, nil
		}
		return domain.Release{}, fmt.Errorf("store: release %s: %w: expected %s, found %s", id, ErrConflict, from, cur.Status)
	}
	if err != nil {
		return domain.Release{}, err
	}
	return s.GetRelease(ctx, id)
}

// SetReleaseBugIssue records the bug issue a release's failed verification
// opened (D115). The first one recorded stays: a release reports one bug.
func (s *Store) SetReleaseBugIssue(ctx context.Context, id uuid.UUID, ref string) (domain.Release, error) {
	if strings.TrimSpace(ref) == "" {
		return domain.Release{}, errors.New("store: a bug issue needs a reference")
	}
	row, err := s.q.SetReleaseBugIssue(ctx, db.SetReleaseBugIssueParams{ID: id, BugIssueRef: ref})
	if err != nil {
		return domain.Release{}, mapErr("set release bug issue", err)
	}
	return s.releaseWithIssues(ctx, s.q, row)
}

func (s *Store) releaseWithIssues(ctx context.Context, q *db.Queries, r db.Release) (domain.Release, error) {
	ids, err := q.ListReleaseIssueIDs(ctx, r.ID)
	if err != nil {
		return domain.Release{}, mapErr("list release issues", err)
	}
	return domain.Release{
		ID: r.ID, ProjectID: r.ProjectID, Repo: r.Repo, SHA: r.Sha, Status: domain.ReleaseStatus(r.Status),
		IssueIDs: nonNil(ids), Reason: r.Reason, BugIssueRef: deref(r.BugIssueRef),
		Bump: domain.Bump(r.Bump), Notes: r.Notes, NotesSource: domain.NotesSource(r.NotesSource),
		Version: r.Version, ProviderReleaseURL: r.ProviderReleaseUrl, PublishError: r.PublishError,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, FinishedAt: r.FinishedAt,
	}, nil
}

// SetReleaseNotes records the bump and notes the release agent, or its
// fallback, prepared (D129).
func (s *Store) SetReleaseNotes(ctx context.Context, id uuid.UUID, bump domain.Bump, notes string, source domain.NotesSource) (domain.Release, error) {
	switch {
	case !bump.Valid():
		return domain.Release{}, fmt.Errorf("store: release notes: bump %q is not major, minor or patch", bump)
	case !source.Valid():
		return domain.Release{}, fmt.Errorf("store: release notes: source %q is not agent or fallback", source)
	}
	row, err := s.q.SetReleaseNotes(ctx, db.SetReleaseNotesParams{ID: id, Bump: string(bump), Notes: notes, NotesSource: string(source)})
	if err != nil {
		return domain.Release{}, mapErr("set release notes", err)
	}
	return s.releaseWithIssues(ctx, s.q, row)
}

// SetReleaseVersion records the version a succeeded release got, its provider
// release, and why publishing it failed, if it did (D129). A release that did
// not succeed gets no version: ErrNotFound.
func (s *Store) SetReleaseVersion(ctx context.Context, id uuid.UUID, version domain.Version, providerURL, publishError string) (domain.Release, error) {
	row, err := s.q.SetReleaseVersion(ctx, db.SetReleaseVersionParams{ID: id, Version: version.String(), ProviderReleaseUrl: providerURL, PublishError: publishError})
	if err != nil {
		return domain.Release{}, mapErr("set release version", err)
	}
	return s.releaseWithIssues(ctx, s.q, row)
}

// LatestReleaseVersion is the newest version a release of the project's repo
// got; ok is false when none has one yet.
func (s *Store) LatestReleaseVersion(ctx context.Context, projectID uuid.UUID, repo string) (v domain.Version, ok bool, err error) {
	raw, err := s.q.LatestReleaseVersion(ctx, db.LatestReleaseVersionParams{ProjectID: projectID, Repo: repo})
	if err != nil {
		if err = mapErr("latest release version", err); errors.Is(err, ErrNotFound) {
			return domain.Version{}, false, nil
		}
		return domain.Version{}, false, err
	}
	v, err = domain.ParseVersion(raw)
	if err != nil {
		return domain.Version{}, false, fmt.Errorf("store: latest release version: %w", err)
	}
	return v, true, nil
}

// NewDeployment is the input for CreateDeployment.
type NewDeployment struct {
	ReleaseID uuid.UUID
	Stage     string
	Driver    string
}

// CreateDeployment records a release's deployment to a stage, pending. Creating
// it again returns the first one; a second deployment to the stage with another
// driver is a conflict, since a stage names one driver.
func (s *Store) CreateDeployment(ctx context.Context, in NewDeployment) (domain.Deployment, error) {
	if in.ReleaseID == uuid.Nil || strings.TrimSpace(in.Stage) == "" || strings.TrimSpace(in.Driver) == "" {
		return domain.Deployment{}, errors.New("store: deployment needs a release, stage and driver")
	}
	var out domain.Deployment
	err := s.inTx(ctx, func(q *db.Queries) error {
		row, err := q.InsertDeployment(ctx, db.InsertDeploymentParams{ID: domain.NewID(), ReleaseID: in.ReleaseID, Stage: in.Stage, Driver: in.Driver})
		if errors.Is(mapErr("", err), ErrNotFound) {
			cur, err := q.GetDeploymentByStage(ctx, db.GetDeploymentByStageParams{ReleaseID: in.ReleaseID, Stage: in.Stage})
			if err != nil {
				return mapErr("get deployment", err)
			}
			if cur.Driver != in.Driver {
				return fmt.Errorf("store: release %s already deploys to %s with driver %s, not %s: %w", in.ReleaseID, in.Stage, cur.Driver, in.Driver, ErrConflict)
			}
			out = deploymentFromRow(cur)
			return nil
		}
		if err != nil {
			return mapErr("insert deployment", err)
		}
		out = deploymentFromRow(row)
		return nil
	})
	return out, err
}

// GetDeployment returns one deployment.
func (s *Store) GetDeployment(ctx context.Context, id uuid.UUID) (domain.Deployment, error) {
	row, err := s.q.GetDeployment(ctx, id)
	if err != nil {
		return domain.Deployment{}, mapErr("get deployment", err)
	}
	return deploymentFromRow(row), nil
}

// ListDeployments returns a release's deployments in the order they were created.
func (s *Store) ListDeployments(ctx context.Context, releaseID uuid.UUID) ([]domain.Deployment, error) {
	rows, err := s.q.ListDeployments(ctx, releaseID)
	if err != nil {
		return nil, mapErr("list deployments", err)
	}
	out := make([]domain.Deployment, 0, len(rows))
	for _, r := range rows {
		out = append(out, deploymentFromRow(r))
	}
	return out, nil
}

// TransitionDeployment moves a deployment between statuses with the contract of
// TransitionRelease; reason, when not empty, says why it failed or was rolled
// back. Entering in_progress stamps started_at; an outcome stamps finished_at.
func (s *Store) TransitionDeployment(ctx context.Context, id uuid.UUID, from, to domain.ReleaseStatus, reason string) (domain.Deployment, error) {
	if !domain.CanTransitionRelease(from, to) {
		return domain.Deployment{}, fmt.Errorf("store: deployment %s: %w: %s -> %s", id, domain.ErrInvalidTransition, from, to)
	}
	var out domain.Deployment
	err := s.inTx(ctx, func(q *db.Queries) error {
		row, err := q.TransitionDeployment(ctx, db.TransitionDeploymentParams{ID: id, FromStatus: string(from), ToStatus: string(to), Reason: reason})
		if err != nil {
			return mapErr("transition deployment", err)
		}
		out = deploymentFromRow(row)
		return appendStateChange(ctx, q, nil, nil, domain.StateChange{Entity: "deployment", ID: id, From: string(from), To: string(to)})
	})
	if errors.Is(err, ErrNotFound) {
		cur, getErr := s.GetDeployment(ctx, id)
		if getErr != nil {
			return domain.Deployment{}, getErr
		}
		if cur.Status == to {
			return cur, nil
		}
		return domain.Deployment{}, fmt.Errorf("store: deployment %s: %w: expected %s, found %s", id, ErrConflict, from, cur.Status)
	}
	return out, err
}

// SetDeploymentDriverState records what the deployment's driver did, replacing
// what it recorded before (D109). It must be a JSON object.
func (s *Store) SetDeploymentDriverState(ctx context.Context, id uuid.UUID, state json.RawMessage) (domain.Deployment, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(state, &obj); err != nil || obj == nil {
		return domain.Deployment{}, fmt.Errorf("store: deployment %s: driver state must be a JSON object", id)
	}
	row, err := s.q.SetDeploymentDriverState(ctx, db.SetDeploymentDriverStateParams{ID: id, DriverState: state})
	if err != nil {
		return domain.Deployment{}, mapErr("set deployment driver state", err)
	}
	return deploymentFromRow(row), nil
}

func deploymentFromRow(r db.Deployment) domain.Deployment {
	return domain.Deployment{
		ID: r.ID, ReleaseID: r.ReleaseID, Stage: r.Stage, Driver: r.Driver, DriverState: json.RawMessage(r.DriverState),
		Status: domain.ReleaseStatus(r.Status), Reason: r.Reason, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
