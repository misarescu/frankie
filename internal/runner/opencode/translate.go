package opencode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// maxOutputBytes caps tool output kept in a single event, as for Claude Code.
const maxOutputBytes = 256 << 10

// shellTool is the opencode tool whose results are reported as command_output.
const shellTool = "shell"

// event is the subset of an opencode server event the runner reads.
type event struct {
	Type string    `json:"type"`
	Data eventData `json:"data"`
}

type eventData struct {
	SessionID string          `json:"sessionID"`
	ID        string          `json:"id"`   // tool call or permission request
	Name      string          `json:"name"` // tool name (tool.input.started)
	Text      string          `json:"text"` // text.ended
	Input     json.RawMessage `json:"input"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Metadata struct {
		Exit *int `json:"exit"`
	} `json:"metadata"`
	Error   *eventError `json:"error"`
	Attempt int         `json:"attempt"`
	Cost    float64     `json:"cost"`
	Tokens  *struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Action    string   `json:"action"`    // permission.asked
	Resources []string `json:"resources"` // permission.asked
}

type eventError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (e *eventError) String() string {
	if e == nil {
		return "unknown error"
	}
	return strings.TrimPrefix(e.Type+": "+e.Message, ": ")
}

// execution is how one agent-loop execution ended.
type execution int

const (
	executionRunning execution = iota
	executionSucceeded
	executionFailed
	executionInterrupted
)

// outcome is what an event means for the session's lifecycle.
type outcome struct {
	ended      execution
	reason     string // why it failed or was interrupted
	permission string // a permission request to refuse
}

// translator maps one session's opencode events to normalized events. Not
// safe for concurrent use.
type translator struct {
	emit      runner.Emit
	sessionID string
	toolNames map[string]string // tool call id -> tool name
	lastText  string
	turns     int
}

func newTranslator(emit runner.Emit, sessionID string) *translator {
	return &translator{emit: emit, sessionID: sessionID, toolNames: map[string]string{}}
}

// handle processes one event. Events of other sessions (subagents) and
// server-wide events carry nothing for the normalized stream.
func (t *translator) handle(raw []byte) outcome {
	var e event
	if err := json.Unmarshal(raw, &e); err != nil {
		t.emit(domain.EventError, runner.Error{Message: "unparseable harness event", Detail: truncate(string(raw), 512)})
		return outcome{}
	}
	if e.Data.SessionID != t.sessionID {
		return outcome{}
	}
	d := e.Data
	switch e.Type {
	case "session.text.ended":
		if d.Text != "" {
			t.lastText = d.Text
			t.emit(domain.EventAssistantMessage, runner.AssistantMessage{Text: d.Text})
		}
	case "session.tool.input.started":
		t.toolNames[d.ID] = d.Name
	case "session.tool.called":
		t.emit(domain.EventToolCall, runner.ToolCall{ID: d.ID, Name: t.toolNames[d.ID], Input: rawOrNull(d.Input)})
	case "session.tool.success", "session.tool.failed":
		t.toolResult(e.Type == "session.tool.failed", d)
	case "session.step.ended":
		t.turns++
	case "session.usage.updated":
		if d.Tokens != nil {
			// opencode reports session totals; reasoning tokens are billed as output.
			t.emit(domain.EventTokenUsage, runner.TokenUsage{
				InputTokens: d.Tokens.Input, OutputTokens: d.Tokens.Output + d.Tokens.Reasoning,
				CacheReadInputTokens: d.Tokens.Cache.Read, CacheCreationInputTokens: d.Tokens.Cache.Write,
				CostUSD: d.Cost, Turns: t.turns,
			})
		}
	case "session.retry.scheduled":
		t.emit(domain.EventError, runner.Error{Message: fmt.Sprintf("model request failed, retry %d scheduled", d.Attempt), Detail: d.Error.String()})
	case "session.step.failed":
		t.emit(domain.EventError, runner.Error{Message: "model step failed", Detail: d.Error.String()})
	case "permission.asked":
		t.emit(domain.EventError, runner.Error{Message: "harness asked for a permission; refused",
			Detail: d.Action + " " + strings.Join(d.Resources, " ")})
		return outcome{permission: d.ID}
	case "session.execution.succeeded":
		return outcome{ended: executionSucceeded}
	case "session.execution.failed":
		return outcome{ended: executionFailed, reason: d.Error.String()}
	case "session.execution.interrupted":
		return outcome{ended: executionInterrupted, reason: "execution interrupted"}
	}
	return outcome{}
}

func (t *translator) toolResult(failed bool, d eventData) {
	parts := make([]string, 0, len(d.Content))
	for _, c := range d.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		} else {
			parts = append(parts, "["+c.Type+"]")
		}
	}
	content := strings.Join(parts, "\n")
	if failed && d.Error != nil {
		content = strings.TrimSpace(d.Error.String() + "\n" + content)
	}
	content = truncate(content, maxOutputBytes)
	name := t.toolNames[d.ID]
	if name == shellTool {
		isError := failed || (d.Metadata.Exit != nil && *d.Metadata.Exit != 0)
		t.emit(domain.EventCommandOutput, runner.CommandOutput{ToolCallID: d.ID, Output: content, IsError: isError})
		return
	}
	t.emit(domain.EventToolResult, runner.ToolResult{ToolCallID: d.ID, Name: name, Content: content, IsError: failed})
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
