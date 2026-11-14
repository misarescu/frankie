package claudecode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// maxOutputBytes caps tool output kept in a single event. Full transcripts move
// to object storage later (docs/spec/02-domain-model.md, Postgres conventions).
const maxOutputBytes = 256 << 10

// shellTool is the Claude Code tool whose results are reported as command_output.
const shellTool = "Bash"

// streamLine is the subset of a Claude Code stream-json output line the runner reads.
type streamLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Message   *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`

	// Fields of "result" lines.
	IsError  bool   `json:"is_error"`
	Result   string `json:"result"`
	NumTurns int    `json:"num_turns"`
	// QueuedTurnCount is how many messages still wait for their own turn.
	// Absent in older Claude Code versions.
	QueuedTurnCount *int    `json:"queued_turn_count"`
	TotalCostUSD    float64 `json:"total_cost_usd"`
	Usage           *struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// turnResult is the outcome of the most recent turn.
type turnResult struct {
	isError bool
	subtype string
	text    string
	queued  *int // messages still queued for their own turn; nil if not reported
}

// translator maps stream-json lines to normalized events. Not safe for concurrent use.
type translator struct {
	emit      runner.Emit
	toolNames map[string]string // tool_use id -> tool name
	sessionID string
	last      *turnResult
	totals    runner.TokenUsage // running session totals
}

func newTranslator(emit runner.Emit) *translator {
	return &translator{emit: emit, toolNames: map[string]string{}}
}

// handle processes one output line and reports whether it ended a turn.
func (t *translator) handle(raw []byte) (turnEnded bool) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return false
	}
	var l streamLine
	if err := json.Unmarshal(raw, &l); err != nil {
		t.emit(domain.EventError, runner.Error{Message: "unparseable harness output", Detail: truncate(string(raw), 512)})
		return false
	}
	if l.SessionID != "" {
		t.sessionID = l.SessionID
	}
	switch l.Type {
	case "assistant":
		for _, b := range t.blocks(l) {
			switch b.Type {
			case "text":
				if b.Text != "" {
					t.emit(domain.EventAssistantMessage, runner.AssistantMessage{Text: b.Text})
				}
			case "tool_use":
				t.toolNames[b.ID] = b.Name
				t.emit(domain.EventToolCall, runner.ToolCall{ID: b.ID, Name: b.Name, Input: rawOrNull(b.Input)})
			}
		}
	case "user":
		for _, b := range t.blocks(l) {
			if b.Type != "tool_result" {
				continue
			}
			name := t.toolNames[b.ToolUseID]
			content := truncate(contentText(b.Content), maxOutputBytes)
			if name == shellTool {
				t.emit(domain.EventCommandOutput, runner.CommandOutput{ToolCallID: b.ToolUseID, Output: content, IsError: b.IsError})
			} else {
				t.emit(domain.EventToolResult, runner.ToolResult{ToolCallID: b.ToolUseID, Name: name, Content: content, IsError: b.IsError})
			}
		}
	case "result":
		// Claude Code reports token counts and num_turns for the answered message only,
		// but total_cost_usd for the whole session; normalize to session totals.
		t.totals.Turns += l.NumTurns
		t.totals.CostUSD = l.TotalCostUSD
		if l.Usage != nil {
			t.totals.InputTokens += l.Usage.InputTokens
			t.totals.OutputTokens += l.Usage.OutputTokens
			t.totals.CacheReadInputTokens += l.Usage.CacheReadInputTokens
			t.totals.CacheCreationInputTokens += l.Usage.CacheCreationInputTokens
		}
		t.emit(domain.EventTokenUsage, t.totals)
		t.last = &turnResult{isError: l.IsError, subtype: l.Subtype, text: l.Result, queued: l.QueuedTurnCount}
		return true
	}
	// "system" and any other line types carry nothing for the normalized stream.
	return false
}

// blocks decodes message.content when it is an array; a plain-string content is ignored.
func (t *translator) blocks(l streamLine) []contentBlock {
	if l.Message == nil || len(l.Message.Content) == 0 || l.Message.Content[0] != '[' {
		return nil
	}
	var bs []contentBlock
	if err := json.Unmarshal(l.Message.Content, &bs); err != nil {
		t.emit(domain.EventError, runner.Error{Message: "unparseable message content", Detail: err.Error()})
		return nil
	}
	return bs
}

// contentText flattens tool_result content, which is a string or an array of blocks.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var bs []contentBlock
	if err := json.Unmarshal(raw, &bs); err != nil {
		return string(raw)
	}
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
