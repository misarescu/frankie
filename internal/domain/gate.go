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

// ReviewResult is one reviewer's verdict in an agent-verdict gate. Error is set
// instead of the verdict when the reviewer returned none that the contract
// accepts.
type ReviewResult struct {
	Step      string      `json:"step"`
	Role      string      `json:"role"`
	CommitSHA string      `json:"commit"`
	Verdict   GateVerdict `json:"verdict,omitempty"`
	Summary   string      `json:"summary,omitempty"`
	Findings  []Finding   `json:"findings,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// AgentVerdictDetails is the Details of an agent-verdict gate result.
type AgentVerdictDetails struct {
	Require string         `json:"require"` // all, any or majority
	Reviews []ReviewResult `json:"reviews"`
}

// Inconclusive reports a gate that cannot be judged: a reviewer returned no
// valid verdict, or there were no reviews.
func (d AgentVerdictDetails) Inconclusive() bool {
	if len(d.Reviews) == 0 {
		return true
	}
	for _, r := range d.Reviews {
		if r.Error != "" {
			return true
		}
	}
	return false
}

// Passed combines the reviewers' verdicts as Require says (docs/spec/03).
func (d AgentVerdictDetails) Passed() bool {
	if d.Inconclusive() {
		return false
	}
	passes := 0
	for _, r := range d.Reviews {
		if r.Verdict == GatePass {
			passes++
		}
	}
	switch d.Require {
	case "any":
		return passes > 0
	case "majority":
		return 2*passes > len(d.Reviews)
	default: // all
		return passes == len(d.Reviews)
	}
}
