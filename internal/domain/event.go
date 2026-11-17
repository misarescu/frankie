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
	// EventQuestion is the agent asking a person through the ask-human tool.
	EventQuestion EventType = "question"

	EventGateResult  EventType = "gate_result"
	EventStateChange EventType = "state_change"
	// EventIntervention is a person steering, aborting or answering a running
	// agent step.
	EventIntervention EventType = "intervention"
	// EventImageBuild is a step waiting for its image's build, and how the
	// build ended (D93); EventBuildLog is the build's log as it grows.
	EventImageBuild EventType = "image_build"
	EventBuildLog   EventType = "build_log"
	// EventFindings is what an investigating step found (D94): its
	// BugFindings, or why its result is not valid findings.
	EventFindings EventType = "findings"
)

// Image build statuses in an EventImageBuild.
const (
	ImageBuilding = "building"
	ImageBuilt    = "built"
	ImageFailed   = "failed"
)

// ImageBuild is the payload of an EventImageBuild.
type ImageBuild struct {
	// Image is "toolchain" or "dependencies".
	Image      string  `json:"image"`
	Repository string  `json:"repository"`
	Tag        string  `json:"tag"`
	Status     string  `json:"status"` // ImageBuilding, ImageBuilt or ImageFailed
	Seconds    float64 `json:"seconds,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// Intervention actions.
const (
	InterventionSteer = "steer"
	InterventionAbort = "abort"
	// InterventionAnswer answers a question the agent asked (D55).
	InterventionAnswer = "answer"
)

// Intervention is the payload of an EventIntervention: who did what to a
// running agent step, and whether its session still accepted it.
type Intervention struct {
	Action string `json:"action"` // InterventionSteer, InterventionAbort or InterventionAnswer
	By     string `json:"by"`
	// Text is the steering message, the reason for an abort, or the answer.
	Text string `json:"text"`
	// QuestionID is the runner's ID of the question an answer answers.
	QuestionID string `json:"question_id,omitempty"`
	// Delivered is false when the session had already finished, or no
	// longer waited for the question.
	Delivered bool `json:"delivered"`
}

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
	Entity string    `json:"entity"` // "issue", "run", "step_run", "change_request", "release" or "deployment"
	ID     uuid.UUID `json:"id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
}
