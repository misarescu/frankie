package domain

import (
	"time"

	"github.com/google/uuid"
)

// QuestionStatus is where an agent's ask-human question stands.
type QuestionStatus string

// Question statuses.
const (
	QuestionPending  QuestionStatus = "pending"
	QuestionAnswered QuestionStatus = "answered"
	// QuestionExpired is a question nobody answered while its session ran.
	QuestionExpired QuestionStatus = "expired"
)

// Valid reports whether s is a known question status.
func (s QuestionStatus) Valid() bool {
	switch s {
	case QuestionPending, QuestionAnswered, QuestionExpired:
		return true
	}
	return false
}

// Question is what an agent asked a person with the ask-human tool
// (docs/spec/04, "Human intervention") and, once answered, the answer and who
// gave it. QuestionID is the runner's ID, unique within the StepRun.
type Question struct {
	ID         uuid.UUID
	StepRunID  uuid.UUID
	RunID      uuid.UUID
	QuestionID string
	Text       string // what the agent asked
	Status     QuestionStatus
	Answer     string
	AnsweredBy string
	AnsweredAt *time.Time
	CreatedAt  time.Time
}
