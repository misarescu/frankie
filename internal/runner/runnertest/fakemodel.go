// Package runnertest helps test harness adapters against their real binaries
// without spending model usage: a scripted fake model endpoint speaking both
// gateway APIs, an event recorder, the agent pod's environment and a
// conformance suite every adapter runs.
package runnertest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Script of the fake model.
const (
	// FirstAnswer is the text of the first answer, which also calls the shell tool.
	FirstAnswer = "Writing the file."
	// ShellCommand is what that tool call runs.
	ShellCommand = "echo hello > hello.txt"
	// ToolCallID identifies that tool call.
	ToolCallID = "call_1"
	// FinalPrefix starts every answer after a tool result, followed by the
	// user messages the model saw.
	FinalPrefix = "Done. Users: "
)

// FakeModel is a scripted model endpoint serving OpenAI-compatible chat
// completions (/v1/chat/completions) and Anthropic Messages (/v1/messages),
// streamed or not. A request without a tool result in its history gets
// FirstAnswer and a call to the shell tool running ShellCommand; one with a
// tool result gets FinalPrefix followed by the user messages it saw. Every
// answer reports 100 input and 20 output tokens.
type FakeModel struct {
	srv       *httptest.Server
	shellTool string
	// Hold, when set before the first request, blocks the first scripted
	// answer until it is closed, so a test can act while the agent loop is
	// mid-turn.
	Hold chan struct{}

	mu       sync.Mutex
	requests []Request
	held     bool
}

// Request is what the fake model saw of one request.
type Request struct {
	API   string // "openai" or "anthropic"
	Auth  string // Authorization header, else x-api-key
	Model string
	Tools []string
	// Users are the texts of user messages, and of messages a harness
	// injects on the user's behalf mid-turn; tool results are excluded.
	Users []string
	// ToolResult is whether the history held a tool result.
	ToolResult bool
}

// NewFakeModel serves the fake until the test ends; shellTool is the name of
// the harness's shell tool (Claude Code: Bash, opencode: shell, pi: bash).
func NewFakeModel(t *testing.T, shellTool string) *FakeModel {
	t.Helper()
	f := &FakeModel{shellTool: shellTool}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the base URL, as the LLM gateway's (without /v1).
func (f *FakeModel) URL() string { return f.srv.URL }

// Requests returns the requests seen so far.
func (f *FakeModel) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// TaskRequests are the requests that carried the agent's tools, leaving out
// a harness's own side requests (titles, summaries).
func (f *FakeModel) TaskRequests() []Request {
	var out []Request
	for _, r := range f.Requests() {
		if len(r.Tools) > 0 {
			out = append(out, r)
		}
	}
	return out
}

// SawUser reports whether a user message containing text reached the model.
func (r Request) SawUser(text string) bool {
	return slices.ContainsFunc(r.Users, func(u string) bool { return strings.Contains(u, text) })
}

func (f *FakeModel) serve(w http.ResponseWriter, r *http.Request) {
	var req Request
	var stream bool
	var err error
	switch r.URL.Path {
	case "/v1/chat/completions":
		req, stream, err = decodeOpenAI(r)
	case "/v1/messages":
		req, stream, err = decodeAnthropic(r)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Auth = r.Header.Get("Authorization"); req.Auth == "" {
		req.Auth = r.Header.Get("x-api-key")
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	// Only a scripted task answer is held: side requests without tools
	// (titles, summaries) answer at once.
	hold := f.Hold != nil && !f.held && len(req.Tools) > 0
	if hold {
		f.held = true
	}
	f.mu.Unlock()
	if hold {
		<-f.Hold
	}

	a := answer{text: FirstAnswer, toolCall: !req.ToolResult, shellTool: f.shellTool}
	if req.ToolResult {
		a.text = FinalPrefix + strings.Join(req.Users, " | ")
	}
	if len(req.Tools) == 0 {
		a.toolCall = false
	}
	if req.API == "anthropic" {
		a.anthropic(w, stream)
	} else {
		a.openAI(w, stream)
	}
}

func decodeOpenAI(r *http.Request) (Request, bool, error) {
	var body struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return Request{}, false, err
	}
	req := Request{API: "openai", Model: body.Model}
	for _, t := range body.Tools {
		req.Tools = append(req.Tools, t.Function.Name)
	}
	for _, m := range body.Messages {
		switch m.Role {
		case "user":
			req.Users = append(req.Users, contentText(m.Content))
		case "tool":
			req.ToolResult = true
		}
	}
	return req, body.Stream, nil
}

func decodeAnthropic(r *http.Request) (Request, bool, error) {
	var body struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return Request{}, false, err
	}
	req := Request{API: "anthropic", Model: body.Model}
	for _, t := range body.Tools {
		req.Tools = append(req.Tools, t.Name)
	}
	for _, m := range body.Messages {
		// Claude Code delivers a message the user sent mid-turn as a system
		// entry after the tool result.
		if m.Role != "user" && m.Role != "system" {
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(m.Content, &blocks) != nil {
			req.Users = append(req.Users, contentText(m.Content))
			continue
		}
		var texts []string
		for _, b := range blocks {
			switch b.Type {
			case "tool_result":
				req.ToolResult = true
			case "text":
				texts = append(texts, b.Text)
			}
		}
		if len(texts) > 0 {
			req.Users = append(req.Users, strings.Join(texts, "\n"))
		}
	}
	return req, body.Stream, nil
}

// answer is one scripted model answer.
type answer struct {
	text      string
	toolCall  bool
	shellTool string
}

func (a answer) arguments() string {
	b, _ := json.Marshal(map[string]string{"command": ShellCommand, "description": "Write hello.txt"})
	return string(b)
}

func (a answer) openAI(w http.ResponseWriter, stream bool) {
	finish := "stop"
	if a.toolCall {
		finish = "tool_calls"
	}
	usage := map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}
	if !stream {
		msg := map[string]any{"role": "assistant", "content": a.text}
		if a.toolCall {
			msg["tool_calls"] = []any{map[string]any{"id": ToolCallID, "type": "function",
				"function": map[string]any{"name": a.shellTool, "arguments": a.arguments()}}}
		}
		writeJSON(w, map[string]any{"id": "c1", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}}, "usage": usage})
		return
	}
	send := sse(w, false)
	choice := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"id": "c1", "object": "chat.completion.chunk", "model": "fake",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	send("", choice(map[string]any{"role": "assistant", "content": a.text}, nil))
	if a.toolCall {
		send("", choice(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": ToolCallID, "type": "function",
			"function": map[string]any{"name": a.shellTool, "arguments": a.arguments()}}}}, nil))
	}
	send("", choice(map[string]any{}, finish))
	send("", map[string]any{"id": "c1", "object": "chat.completion.chunk", "choices": []any{}, "usage": usage})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func (a answer) anthropic(w http.ResponseWriter, stream bool) {
	stop := "end_turn"
	content := []any{map[string]any{"type": "text", "text": a.text}}
	if a.toolCall {
		stop = "tool_use"
		var input map[string]string
		_ = json.Unmarshal([]byte(a.arguments()), &input)
		content = append(content, map[string]any{"type": "tool_use", "id": ToolCallID, "name": a.shellTool, "input": input})
	}
	if !stream {
		writeJSON(w, map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": "fake", "content": content,
			"stop_reason": stop, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 100, "output_tokens": 20}})
		return
	}
	send := sse(w, true)
	send("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_1", "type": "message",
		"role": "assistant", "model": "fake", "content": []any{}, "stop_reason": nil, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 1}}})
	send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": a.text}})
	send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	if a.toolCall {
		send("content_block_start", map[string]any{"type": "content_block_start", "index": 1,
			"content_block": map[string]any{"type": "tool_use", "id": ToolCallID, "name": a.shellTool, "input": map[string]any{}}})
		send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": a.arguments()}})
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 1})
	}
	send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 20}})
	send("message_stop", map[string]any{"type": "message_stop"})
}

// sse starts an event stream; named events carry an event: line.
func sse(w http.ResponseWriter, named bool) func(event string, v any) {
	w.Header().Set("Content-Type", "text/event-stream")
	return func(event string, v any) {
		b, _ := json.Marshal(v)
		if named {
			_, _ = fmt.Fprintf(w, "event: %s\n", event)
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		w.(http.Flusher).Flush()
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// contentText flattens a message's content, a string or an array of parts.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return string(raw)
	}
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}
