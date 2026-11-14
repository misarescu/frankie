package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ExecStatus is the lifecycle state shared by Runs and StepRuns.
type ExecStatus string

// Execution statuses.
const (
	ExecPending   ExecStatus = "pending"
	ExecRunning   ExecStatus = "running"
	ExecSucceeded ExecStatus = "succeeded"
	ExecFailed    ExecStatus = "failed"
	ExecCanceled  ExecStatus = "canceled"
)

var execTransitions = map[ExecStatus][]ExecStatus{
	ExecPending:   {ExecRunning, ExecFailed, ExecCanceled},
	ExecRunning:   {ExecSucceeded, ExecFailed, ExecCanceled},
	ExecSucceeded: {},
	ExecFailed:    {},
	ExecCanceled:  {},
}

// Valid reports whether s is a known execution status.
func (s ExecStatus) Valid() bool {
	_, ok := execTransitions[s]
	return ok
}

// Terminal reports whether s is a final status.
func (s ExecStatus) Terminal() bool {
	return s.Valid() && len(execTransitions[s]) == 0
}

// CanTransitionExec reports whether a Run or StepRun may move from one status to another.
func CanTransitionExec(from, to ExecStatus) bool {
	for _, next := range execTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// Run is one execution of a workflow for an issue. A retry creates a new Run.
type Run struct {
	ID                 uuid.UUID
	IssueID            uuid.UUID
	WorkflowSnapshot   json.RawMessage // definition snapshotted at start (invariant 4)
	WorkflowVersion    string
	TemporalWorkflowID string // stable per issue, reused by reruns
	TemporalRunID      string // one execution; unique
	Status             ExecStatus
	LoopCounts         map[string]int // step ID -> loops taken
	StartedAt          *time.Time
	FinishedAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// StepKind is the kind of workflow step a StepRun executes.
type StepKind string

// Step kinds.
const (
	StepAgent    StepKind = "agent"
	StepGate     StepKind = "gate"
	StepAction   StepKind = "action"
	StepParallel StepKind = "parallel"
)

// Valid reports whether k is a known step kind.
func (k StepKind) Valid() bool {
	switch k {
	case StepAgent, StepGate, StepAction, StepParallel:
		return true
	}
	return false
}

// StepRun is one attempt at one workflow step within a Run.
type StepRun struct {
	ID        uuid.UUID
	RunID     uuid.UUID
	StepID    string
	Attempt   int
	Kind      StepKind
	Status    ExecStatus
	PodName   string
	CommitSHA string
	// Usage is an agent session's model usage (D53).
	Usage      Usage
	StartedAt  *time.Time
	FinishedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Usage is model usage: cost as the LLM gateway recorded it, tokens as the
// harness reported them (D53). InputTokens counts all input, including what
// the model read from or wrote to its prompt cache.
type Usage struct {
	CostUSD      float64
	InputTokens  int64
	OutputTokens int64
}
