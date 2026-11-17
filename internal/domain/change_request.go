package domain

import (
	"cmp"
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
	Name  string     `json:"name"`
	State CheckState `json:"state"` // pending, success or failure
	URL   string     `json:"url,omitempty"`
	// Summary is the provider's short description, e.g. a failure's title.
	Summary string `json:"summary,omitempty"`
	// ID is the provider's reference for the check, which the git provider
	// reads its log by; empty when the provider keeps no log for it.
	ID string `json:"id,omitempty"`
	// Log is the end of a failed check's log, the part that shows why it
	// failed (D120); empty when unread or unavailable.
	Log string `json:"log,omitempty"`
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

// CILogLimits bound what is read of failed CI checks' logs (D120), set per
// project in `.factory/config.yaml` (`merge.ciLogs`). Zero means the default.
type CILogLimits struct {
	// Checks is how many failed checks' logs are read (default 3).
	Checks int `json:"checks,omitempty"`
	// Jobs is how many failed jobs of one check are read, for a check made
	// of several, e.g. a GitHub Actions workflow run (default 2).
	Jobs int `json:"jobs,omitempty"`
	// Lines and Bytes bound what is kept of each job's log: the end of its
	// failed step (defaults 80 and 6000).
	Lines int `json:"lines,omitempty"`
	Bytes int `json:"bytes,omitempty"`
}

// Defaults and maximums of CILogLimits.
var (
	DefaultCILogLimits = CILogLimits{Checks: 3, Jobs: 2, Lines: 80, Bytes: 6000}
	MaxCILogLimits     = CILogLimits{Checks: 10, Jobs: 10, Lines: 1000, Bytes: 65536}
)

// WithDefaults fills in the limits left at zero.
func (l CILogLimits) WithDefaults() CILogLimits {
	d := DefaultCILogLimits
	return CILogLimits{
		Checks: cmp.Or(l.Checks, d.Checks), Jobs: cmp.Or(l.Jobs, d.Jobs),
		Lines: cmp.Or(l.Lines, d.Lines), Bytes: cmp.Or(l.Bytes, d.Bytes),
	}
}

// Validate checks that each limit is zero (the default) or between 1 and its
// maximum.
func (l CILogLimits) Validate() error {
	m := MaxCILogLimits
	var errs []error
	for _, f := range []struct {
		name     string
		got, max int
	}{{"checks", l.Checks, m.Checks}, {"jobs", l.Jobs, m.Jobs}, {"lines", l.Lines, m.Lines}, {"bytes", l.Bytes, m.Bytes}} {
		if f.got < 0 || f.got > f.max {
			errs = append(errs, fmt.Errorf("%s: %d must be between 1 and %d, or 0 for the default", f.name, f.got, f.max))
		}
	}
	return errors.Join(errs...)
}
