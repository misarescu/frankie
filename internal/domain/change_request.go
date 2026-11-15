package domain

import (
	"errors"
	"fmt"
)

// MergeMethod is how a change request lands on its base branch (D85).
type MergeMethod string

// Merge methods. Squash is the default: one commit per issue on the base
// branch, whatever loops its branch went through.
const (
	MergeSquash MergeMethod = "squash"
	MergeRebase MergeMethod = "rebase"
	MergeCommit MergeMethod = "merge"
)

// Validate rejects unknown merge methods.
func (m MergeMethod) Validate() error {
	switch m {
	case MergeSquash, MergeRebase, MergeCommit:
		return nil
	}
	return fmt.Errorf("merge method %q is not squash, rebase or merge", m)
}

// MergeRequest asks a provider to merge a change request.
type MergeRequest struct {
	// HeadSHA is the commit the change request must still point to: what the
	// gates checked. A change request that moved is not merged.
	HeadSHA string
	Method  MergeMethod
	// Title and Message are the merge or squash commit's; empty lets the
	// provider choose.
	Title, Message string
}

// Validate checks the request before it reaches a provider.
func (r MergeRequest) Validate() error {
	if r.HeadSHA == "" {
		return errors.New("merge request: head SHA is required")
	}
	return r.Method.Validate()
}

// Errors a provider's merge reports, so the merge queue can tell why.
var (
	// ErrHeadMoved means the change request no longer points to HeadSHA.
	ErrHeadMoved = errors.New("change request head moved")
	// ErrNotMergeable means the provider refuses the merge: a conflict, a
	// branch protection rule or a change request that is closed.
	ErrNotMergeable = errors.New("change request not mergeable")
)

// CheckState is the state of a commit's provider checks (CI).
type CheckState string

// Check states. None means the provider has no checks for the commit (yet).
const (
	CheckNone    CheckState = "none"
	CheckPending CheckState = "pending"
	CheckSuccess CheckState = "success"
	CheckFailure CheckState = "failure"
)

// Check is one provider check of a commit, e.g. a CI job.
type Check struct {
	Name  string
	State CheckState // pending, success or failure
	URL   string
	// Summary is the provider's short description, e.g. a failure's title.
	Summary string
}

// CommitStatus is a commit's provider checks and their combined state.
type CommitStatus struct {
	SHA    string
	State  CheckState
	Checks []Check
}

// CombineChecks is the state of a set of checks: failure if any failed,
// else pending if any is not done, else success; none without checks.
func CombineChecks(checks []Check) CheckState {
	if len(checks) == 0 {
		return CheckNone
	}
	state := CheckSuccess
	for _, c := range checks {
		switch c.State {
		case CheckFailure:
			return CheckFailure
		case CheckSuccess:
		default:
			state = CheckPending
		}
	}
	return state
}
