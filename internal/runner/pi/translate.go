package pi

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// maxOutputBytes caps tool output kept in a single event, as for the other harnesses.
const maxOutputBytes = 256 << 10

// shellTool is the pi tool whose results are reported as command_output.
const shellTool = "bash"

// record is the subset of a pi RPC output record the runner reads: command
// responses and session events.
type record struct {
	Type string `json:"type"`

	// response
	ID      string `json:"id"`
	Command string `json:"command"`
	// Success is set on responses and on auto_retry_end.
	Success *bool           `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`

	// message_end
	Message *message `json:"message"`

	// tool_execution_end
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Result     *struct {
		Content []block `json:"content"`
	} `json:"result"`
	IsError bool `json:"isError"`

	// agent_settled
	Aborted bool `json:"aborted"`

	// auto_retry_start, auto_retry_end, compaction_end, extension_error
	Attempt      int    `json:"attempt"`
	MaxAttempts  int    `json:"maxAttempts"`
	ErrorMessage string `json:"errorMessage"`
	FinalError   string `json:"finalError"`
	Event        string `json:"event"`
	Extension    string `json:"extensionPath"`
}

type message struct {
	Role string `json:"role"`
	// Content is a string (system, some user messages) or an array of blocks.
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	Usage        *struct {
		Input      int64 `json:"input"`
		Output     int64 `json:"output"`
		CacheRead  int64 `json:"cacheRead"`
		CacheWrite int64 `json:"cacheWrite"`
		Cost       struct {
			Total float64 `json:"total"`
		} `json:"cost"`
	} `json:"usage"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// translator maps pi's session events to normalized events. Not safe for
// concurrent use.
type translator struct {
	emit      runner.Emit
	totals    runner.TokenUsage
	lastText  string
	lastError string // the last assistant message's error, cleared by a later success
}

func newTranslator(emit runner.Emit) *translator {
	return &translator{emit: emit}
}

// handle processes one session event and reports whether pi settled (has no
// automatic work left), and whether that was an abort.
func (t *translator) handle(r record) (settled, aborted bool) {
	switch r.Type {
	case "message_end":
		if r.Message != nil && r.Message.Role == "assistant" {
			t.assistant(r.Message)
		}
	case "tool_execution_end":
		content := ""
		if r.Result != nil {
			content = truncate(blocksText(r.Result.Content), maxOutputBytes)
		}
		if r.ToolName == shellTool {
			t.emit(domain.EventCommandOutput, runner.CommandOutput{ToolCallID: r.ToolCallID, Output: content, IsError: r.IsError})
		} else {
			t.emit(domain.EventToolResult, runner.ToolResult{ToolCallID: r.ToolCallID, Name: r.ToolName, Content: content, IsError: r.IsError})
		}
	case "auto_retry_start":
		t.emit(domain.EventError, runner.Error{Message: fmt.Sprintf("model request failed, retry %d of %d scheduled", r.Attempt, r.MaxAttempts), Detail: r.ErrorMessage})
	case "auto_retry_end":
		if r.Success != nil && !*r.Success {
			t.emit(domain.EventError, runner.Error{Message: "model request failed after retries", Detail: r.FinalError})
		}
	case "compaction_end":
		if r.ErrorMessage != "" {
			t.emit(domain.EventError, runner.Error{Message: "context compaction failed", Detail: r.ErrorMessage})
		}
	case "extension_error":
		t.emit(domain.EventError, runner.Error{Message: "harness extension failed: " + r.Event, Detail: r.Error})
	case "agent_settled":
		return true, r.Aborted
	}
	// Streaming deltas, turn and queue events carry nothing the final
	// messages do not.
	return false, false
}

// assistant handles a completed assistant message: its text, its tool
// calls and its usage, one model turn.
func (t *translator) assistant(m *message) {
	var texts []string
	for _, b := range contentBlocks(m.Content) {
		switch b.Type {
		case "text":
			if b.Text != "" {
				texts = append(texts, b.Text)
				t.emit(domain.EventAssistantMessage, runner.AssistantMessage{Text: b.Text})
			}
		case "toolCall":
			t.emit(domain.EventToolCall, runner.ToolCall{ID: b.ID, Name: b.Name, Input: rawOrNull(b.Arguments)})
		}
	}
	if len(texts) > 0 {
		t.lastText = strings.Join(texts, "\n")
	}
	if m.Usage != nil {
		// pi reports usage per message; normalize to session totals. Its
		// output already counts reasoning tokens.
		t.totals.InputTokens += m.Usage.Input
		t.totals.OutputTokens += m.Usage.Output
		t.totals.CacheReadInputTokens += m.Usage.CacheRead
		t.totals.CacheCreationInputTokens += m.Usage.CacheWrite
		t.totals.CostUSD += m.Usage.Cost.Total
	}
	t.totals.Turns++
	t.emit(domain.EventTokenUsage, t.totals)
	switch m.StopReason {
	case "error":
		t.lastError = cmp.Or(m.ErrorMessage, "model request failed")
		t.emit(domain.EventError, runner.Error{Message: "model request failed", Detail: t.lastError})
	case "aborted":
	default:
		t.lastError = ""
	}
}

// contentBlocks decodes message content when it is an array of blocks.
func contentBlocks(raw json.RawMessage) []block {
	var bs []block
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &bs) != nil {
		return nil
	}
	return bs
}

func blocksText(bs []block) string {
	parts := make([]string, 0, len(bs))
	for _, b := range bs {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		} else {
			parts = append(parts, "["+b.Type+"]")
		}
	}
	return strings.Join(parts, "\n")
}

func rawOrNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n[truncated %d bytes]", len(s)-limit)
}
