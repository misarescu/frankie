// Package runner implements the in-pod runner: one agent session per pod, driven
// over the RunnerService gRPC contract, with harness output normalized into a
// single event stream (docs/spec/04-agents-harnesses-models.md). Adding a harness
// means adding an implementation of Harness; nothing outside the runner changes.
package runner

import (
	"context"
	"errors"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

// ErrSessionFinished is returned when a message is sent after the session stopped accepting input.
var ErrSessionFinished = errors.New("runner: session finished")

// DoneStatus is the outcome reported in the final done event.
type DoneStatus string

// Done statuses.
const (
	DoneSucceeded DoneStatus = "succeeded"
	DoneFailed    DoneStatus = "failed"
	DoneAborted   DoneStatus = "aborted"
	DoneTimeout   DoneStatus = "timeout"
)

// SessionConfig is what a harness needs to start a session.
type SessionConfig struct {
	Workdir      string
	Model        string
	SystemPrompt string
	Env          map[string]string
	MaxTurns     int
	// AllowedTools are harness-neutral tool names (StartRequest.allowed_tools);
	// empty means the harness's defaults.
	AllowedTools []string
	// Gateway is the LLM gateway and virtual key (StartRequest.gateway); zero
	// when the control plane passed them in Env instead.
	Gateway Gateway
	// MCPServers are HTTP MCP servers the harness connects to, such as the
	// runner's ask-human server; the only ones it may use.
	MCPServers []MCPServer
}

// MCPServer is an HTTP MCP server for a session's harness.
type MCPServer struct {
	Name  string
	URL   string
	Tools []string // tool names, for harnesses that allow tools by name
}

// MCPToolTimeout bounds one MCP tool call in every harness: an ask-human
// question can wait as long as the session lasts, which the runner bounds.
const MCPToolTimeout = 24 * time.Hour

// ReadOnlyCommands are the shell commands the bash:readonly tool allows, for
// every harness: they inspect the workspace and its history without changing
// them. This narrows what a reviewer does; the boundary is still the read-only
// workspace, which the runner never commits or pushes.
var ReadOnlyCommands = []string{
	"git diff", "git log", "git show", "git status", "git blame", "git grep",
	"git ls-files", "git rev-parse", "git merge-base",
	"ls", "cat", "head", "tail", "wc", "grep", "rg", "tree", "pwd",
}

// ModelAPI is the API the gateway serves the session's model with.
type ModelAPI string

// Model APIs (StartRequest.gateway.api).
const (
	ModelAPIAnthropic ModelAPI = "anthropic"
	ModelAPIOpenAI    ModelAPI = "openai"
)

// Gateway is how a session reaches its model.
type Gateway struct {
	BaseURL string
	APIKey  string
	API     ModelAPI // empty: unspecified, the harness's own default
}

// Emit appends a normalized event to the session's stream. A harness must emit
// exactly one domain.EventDone, as its last event, when the session ends.
type Emit func(typ domain.EventType, payload any)

// Harness starts sessions for one agent CLI.
type Harness interface {
	Start(ctx context.Context, cfg SessionConfig, emit Emit) (Session, error)
}

// Session is a running harness session.
type Session interface {
	// Send delivers a user message. The first message is the task; later ones
	// steer the session at its next turn boundary. Returns ErrSessionFinished
	// once the session no longer accepts input.
	Send(text string) error
	// Abort stops the session; the done event carries status and reason.
	// Aborting a finished session does nothing.
	Abort(status DoneStatus, reason string)
	// Done is closed after the done event was emitted.
	Done() <-chan struct{}
}

// Payloads of the normalized events (schemas documented in runner.proto).
type (
	AssistantMessage struct {
		Text string `json:"text"`
	}
	ToolCall struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Input any    `json:"input"`
	}
	ToolResult struct {
		ToolCallID string `json:"tool_call_id"`
		Name       string `json:"name"`
		Content    string `json:"content"`
		IsError    bool   `json:"is_error"`
	}
	CommandOutput struct {
		ToolCallID string `json:"tool_call_id"`
		Output     string `json:"output"`
		IsError    bool   `json:"is_error"`
	}
	// TokenUsage holds session totals. As in Anthropic's API, InputTokens is
	// only the input that was neither read from nor written to the prompt
	// cache; the two cache fields hold the rest of the input.
	TokenUsage struct {
		InputTokens              int64   `json:"input_tokens"`
		OutputTokens             int64   `json:"output_tokens"`
		CacheReadInputTokens     int64   `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64   `json:"cache_creation_input_tokens"`
		CostUSD                  float64 `json:"cost_usd"`
		Turns                    int     `json:"turns"`
	}
	Error struct {
		Message string `json:"message"`
		Detail  string `json:"detail,omitempty"`
	}
	Question struct {
		QuestionID string `json:"question_id"`
		Question   string `json:"question"`
	}
	Done struct {
		Status           DoneStatus `json:"status"`
		Reason           string     `json:"reason,omitempty"`
		Result           string     `json:"result,omitempty"`
		HarnessSessionID string     `json:"harness_session_id,omitempty"`
		ExitCode         int        `json:"exit_code"`
		// Set when the session had a workspace and succeeded.
		Branch    string `json:"branch,omitempty"`
		CommitSHA string `json:"commit_sha,omitempty"`
		Pushed    bool   `json:"pushed,omitempty"`
	}
)
