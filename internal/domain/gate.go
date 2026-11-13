package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// GateType is how a gate is evaluated (docs/spec/03-workflows-and-gates.md).
type GateType string

// Gate types.
const (
	GateCommand      GateType = "command"
	GateAgentVerdict GateType = "agent-verdict"
	GateHuman        GateType = "human"
)

// Valid reports whether t is a known gate type.
func (t GateType) Valid() bool {
	return t == GateCommand || t == GateAgentVerdict || t == GateHuman
}

// GatePolicy says whether a failed gate stops the run.
type GatePolicy string

// Gate policies.
const (
	GateBlocking GatePolicy = "blocking"
	GateAdvisory GatePolicy = "advisory"
)

// Valid reports whether p is a known gate policy.
func (p GatePolicy) Valid() bool { return p == GateBlocking || p == GateAdvisory }

// GateVerdict is a gate's outcome.
type GateVerdict string

// Gate verdicts.
const (
	GatePass      GateVerdict = "pass"
	GateFail      GateVerdict = "fail"
	GateEscalated GateVerdict = "escalated"
)

// Valid reports whether v is a known verdict.
func (v GateVerdict) Valid() bool { return v == GatePass || v == GateFail || v == GateEscalated }

// GateResult is the outcome of one gate StepRun; the commit it judged is the
// StepRun's CommitSHA.
type GateResult struct {
	ID        uuid.UUID
	StepRunID uuid.UUID
	Type      GateType
	Policy    GatePolicy
	Verdict   GateVerdict
	Details   json.RawMessage // per gate type; for command gates, CommandGateDetails
	CreatedAt time.Time
}

// CommandResult is one command of a command gate, as the pod status recorded it.
type CommandResult struct {
	Command  string `json:"command"`
	ExitCode *int   `json:"exitCode"` // nil when the command never ran
	Reason   string `json:"reason,omitempty"`
	LogTail  string `json:"logTail,omitempty"`
}

// CommandGateDetails is the Details of a command gate result.
type CommandGateDetails struct {
	Image    string          `json:"image"`
	Commands []CommandResult `json:"commands"`
	Error    string          `json:"error,omitempty"` // why the gate could not run the commands
}

// Passed reports whether every command ran and exited 0 (docs/spec/03: pass = all exit 0).
func (d CommandGateDetails) Passed() bool {
	if d.Error != "" || len(d.Commands) == 0 {
		return false
	}
	for _, c := range d.Commands {
		if c.ExitCode == nil || *c.ExitCode != 0 {
			return false
		}
	}
	return true
}
