package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// EventType classifies an entry in the append-only event stream.
type EventType string

// Event types. The first group is the runner's normalized harness stream
// (docs/spec/04-agents-harnesses-models.md, adapter contract); the second is
// produced by the control plane (docs/spec/02-domain-model.md, "Event").
const (
	EventAssistantMessage EventType = "assistant_message"
	EventToolCall         EventType = "tool_call"
	EventToolResult       EventType = "tool_result"
	EventFileDiff         EventType = "file_diff"
	EventCommandOutput    EventType = "command_output"
	EventTokenUsage       EventType = "token_usage"
	EventError            EventType = "error"
	EventDone             EventType = "done"

	EventGateResult  EventType = "gate_result"
	EventStateChange EventType = "state_change"
)

// Event is one append-only entry. Seq is a strict total order across all events.
type Event struct {
	ID        uuid.UUID
	Seq       int64
	RunID     *uuid.UUID
	StepRunID *uuid.UUID
	SessionID *uuid.UUID
	Ts        time.Time
	Type      EventType
	Payload   json.RawMessage
	CreatedAt time.Time
}

// StateChange is the payload of an EventStateChange.
type StateChange struct {
	Entity string    `json:"entity"` // "issue", "run" or "step_run"
	ID     uuid.UUID `json:"id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
}
