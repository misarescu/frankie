package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ReleaseStatus is the lifecycle state shared by Releases and their
// Deployments (docs/spec/02-domain-model.md, D110).
type ReleaseStatus string

// Release and deployment statuses. rolled_back is a failure whose stage was
// put back to what it ran before.
const (
	ReleasePending    ReleaseStatus = "pending"
	ReleaseInProgress ReleaseStatus = "in_progress"
	ReleaseSucceeded  ReleaseStatus = "succeeded"
	ReleaseFailed     ReleaseStatus = "failed"
	ReleaseRolledBack ReleaseStatus = "rolled_back"
)

// releaseTransitions: a release (or deployment) starts, then succeeds or
// fails; a failed one is rolled back once its driver has undone it (FR-RL-2),
// or straight from in_progress when the rollback is part of the failure. One
// that never started can fail (e.g. it was superseded or its project's stages
// are invalid).
var releaseTransitions = map[ReleaseStatus][]ReleaseStatus{
	ReleasePending:    {ReleaseInProgress, ReleaseFailed},
	ReleaseInProgress: {ReleaseSucceeded, ReleaseFailed, ReleaseRolledBack},
	ReleaseFailed:     {ReleaseRolledBack},
	ReleaseSucceeded:  {},
	ReleaseRolledBack: {},
}

// Valid reports whether s is a known release status.
func (s ReleaseStatus) Valid() bool {
	_, ok := releaseTransitions[s]
	return ok
}

// Terminal reports whether nothing follows s. failed is not terminal: a
// rollback may still follow it.
func (s ReleaseStatus) Terminal() bool {
	return s.Valid() && len(releaseTransitions[s]) == 0
}

// Finished reports whether s is an outcome: succeeded, failed or rolled back.
func (s ReleaseStatus) Finished() bool {
	return s == ReleaseSucceeded || s == ReleaseFailed || s == ReleaseRolledBack
}

// CanTransitionRelease reports whether a Release or Deployment may move from
// one status to another.
func CanTransitionRelease(from, to ReleaseStatus) bool {
	for _, next := range releaseTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// Release is a commit of a project's repository plus the issues it contains
// (D110): the merged issues not yet released whose change requests merged no
// later than the one that made the commit. It moves through the project's
// stages; when it succeeds, its issues are released.
type Release struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Repo      string
	SHA       string
	Status    ReleaseStatus
	// IssueIDs are the issues it contains, in the order they merged.
	IssueIDs []uuid.UUID
	// Reason says why it failed or was rolled back.
	Reason string
	// BugIssueRef is the bug issue opened on the provider when its
	// verification failed (D115), e.g. a GitHub issue URL.
	BugIssueRef string
	// Bump, Notes and NotesSource are what the release agent, or its
	// fallback, prepared (D129).
	Bump        Bump
	Notes       string
	NotesSource NotesSource
	// Version is the version a succeeded release got, e.g. v0.3.0;
	// ProviderReleaseURL its provider release; PublishError why publishing it
	// failed (D129).
	Version            string
	ProviderReleaseURL string
	PublishError       string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	FinishedAt         *time.Time
}

// Verification is how a stage's verification of a release ended (D116), as
// its deployment records it (D130). Failures name the failed tests and the
// issues they are tagged with (FR-TA-3). Inconclusive: the verification could
// not judge the release, so its failure is the stage's, not a bug in it.
type Verification struct {
	Passed       bool          `json:"passed"`
	Inconclusive bool          `json:"inconclusive,omitempty"`
	Summary      string        `json:"summary"`
	Failures     []TestFailure `json:"failures,omitempty"`
}

// Deployment is a Release at one Stage: what its driver did and how it ended.
type Deployment struct {
	ID        uuid.UUID
	ReleaseID uuid.UUID
	Stage     string
	Driver    string
	// DriverState is the driver's own record of what it did (e.g. the GitOps
	// commit it made), opaque to the control plane and handed back to the
	// driver for Status and Rollback (D109).
	DriverState json.RawMessage
	Status      ReleaseStatus
	// Reason says why it failed or was rolled back.
	Reason string
	// Verification is how the stage's verification ended, once it ran (D130).
	Verification *Verification
	StartedAt    *time.Time
	FinishedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
