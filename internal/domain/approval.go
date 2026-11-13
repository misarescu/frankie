package domain

import (
	"time"

	"github.com/google/uuid"
)

// ApprovalStatus is where a human gate's decision stands.
type ApprovalStatus string

// Approval statuses.
const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalTimedOut ApprovalStatus = "timed_out"
)

// Valid reports whether s is a known approval status.
func (s ApprovalStatus) Valid() bool {
	switch s {
	case ApprovalPending, ApprovalApproved, ApprovalRejected, ApprovalTimedOut:
		return true
	}
	return false
}

// Approval is the decision a human gate waits for (docs/spec/03): why it is
// asked, what to look at, and, once made, who decided and why.
type Approval struct {
	ID               uuid.UUID
	StepRunID        uuid.UUID
	RunID            uuid.UUID
	Reason           string
	CommitSHA        string
	ChangeRequestURL string
	Status           ApprovalStatus
	DecidedBy        string
	Comment          string
	ExpiresAt        time.Time
	DecidedAt        *time.Time
	CreatedAt        time.Time
}
