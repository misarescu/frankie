package domain

import (
	"time"

	"github.com/google/uuid"
)

// Project groups repos, issues and configuration. M1 carries only identity.
type Project struct {
	ID        uuid.UUID
	Name      string
	KeyPrefix string // immutable; see ValidKeyPrefix
	CreatedAt time.Time
	UpdatedAt time.Time
}

// IssueType is the level of an issue in the epic > story > task hierarchy, or a bug.
type IssueType string

// Issue types.
const (
	IssueEpic  IssueType = "epic"
	IssueStory IssueType = "story"
	IssueTask  IssueType = "task"
	IssueBug   IssueType = "bug"
)

// Valid reports whether t is a known issue type.
func (t IssueType) Valid() bool {
	switch t {
	case IssueEpic, IssueStory, IssueTask, IssueBug:
		return true
	}
	return false
}

// IssueStatus is the lifecycle state of an issue.
type IssueStatus string

// Issue statuses.
const (
	IssueDraft         IssueStatus = "draft"
	IssueRefining      IssueStatus = "refining"
	IssueReady         IssueStatus = "ready"
	IssueRunning       IssueStatus = "running"
	IssueAwaitingHuman IssueStatus = "awaiting_human"
	IssueQueued        IssueStatus = "queued"
	IssueMerged        IssueStatus = "merged"
	IssueReleased      IssueStatus = "released"
	IssueFailed        IssueStatus = "failed"
	IssueCanceled      IssueStatus = "canceled"
)

// Valid reports whether s is a known issue status.
func (s IssueStatus) Valid() bool {
	_, ok := issueTransitions[s]
	return ok
}

// issueTransitions is the issue state machine (docs/spec/02-domain-model.md, "Issue status").
// failed and canceled are reachable from every state before merged; failed -> ready re-arms
// an issue for a new Run. awaiting_human -> merged records a change request merged on the
// provider by a person (D61), until the merge queue (M6) merges them itself.
var issueTransitions = map[IssueStatus][]IssueStatus{
	IssueDraft:         {IssueRefining, IssueReady, IssueFailed, IssueCanceled},
	IssueRefining:      {IssueReady, IssueFailed, IssueCanceled},
	IssueReady:         {IssueRunning, IssueFailed, IssueCanceled},
	IssueRunning:       {IssueAwaitingHuman, IssueQueued, IssueFailed, IssueCanceled},
	IssueAwaitingHuman: {IssueRunning, IssueMerged, IssueFailed, IssueCanceled},
	IssueQueued:        {IssueRunning, IssueMerged, IssueFailed, IssueCanceled},
	IssueMerged:        {IssueReleased},
	IssueReleased:      {},
	IssueFailed:        {IssueReady},
	IssueCanceled:      {},
}

// CanTransitionIssue reports whether an issue may move from one status to another.
func CanTransitionIssue(from, to IssueStatus) bool {
	for _, next := range issueTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// Issue is a spec: the unit of work for the build lifecycle.
type Issue struct {
	ID                 uuid.UUID
	ProjectID          uuid.UUID
	Number             int        // per project, from 1
	Key                string     // <project key prefix>-<number>, e.g. frk-42
	ParentID           *uuid.UUID // nil for a root issue; see ValidParent
	Type               IssueType
	Title              string
	Spec               string
	AcceptanceCriteria []string
	Status             IssueStatus
	Priority           int
	SourceRef          string // empty when the issue is native
	Labels             []string
	CreatedBy          string
	// StartRequestedAt is set once someone asked to start the issue or a parent
	// of it (D62); StartRequestedBy says who.
	StartRequestedAt *time.Time
	StartRequestedBy string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ChangeRequestStatus is a change request's state on its provider.
type ChangeRequestStatus string

// Change request statuses.
const (
	ChangeRequestOpen   ChangeRequestStatus = "open"
	ChangeRequestMerged ChangeRequestStatus = "merged"
	ChangeRequestClosed ChangeRequestStatus = "closed" // closed without merging
)

// ChangeRequest is a provider-neutral pull/merge request for an issue's branch.
type ChangeRequest struct {
	ID          uuid.UUID
	IssueID     uuid.UUID
	RunID       uuid.UUID
	Repo        string
	Number      int
	ProviderRef string // e.g. the PR URL
	Branch      string
	HeadSHA     string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
