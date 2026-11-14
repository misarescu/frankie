// Package runnertest helps test harness adapters against their real binaries
// without spending model usage: a scripted fake model endpoint, an event
// recorder and the agent pod's environment.
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

// FakeModel is a scripted OpenAI-compatible chat completions endpoint. Its
// first answer writes "Writing the file." and calls the shell tool to run
// `echo hello > hello.txt`; any answer after a tool result says
// "Done. Users: " followed by the user messages it saw.
type FakeModel struct {
	srv       *httptest.Server
	shellTool string
	// Hold, when set before the first request, blocks the first answer until
	// it is closed, so a test can act while the agent loop is mid-turn.
	Hold chan struct{}

	mu       sync.Mutex
	requests []Request
}

// Request is what the fake model saw of one request.
type Request struct {
	Auth  string // Authorization header
	Model string
	Tools []string
	Users []string // user message contents, flattened to text
}

// NewFakeModel serves the fake until the test ends; shellTool is the name of
// the harness's shell tool (opencode: shell, pi: bash).
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

func (f *FakeModel) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Model    string `json:"model"`
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
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := Request{Auth: r.Header.Get("Authorization"), Model: body.Model}
	for _, t := range body.Tools {
		req.Tools = append(req.Tools, t.Function.Name)
	}
	toolResult := false
	for _, m := range body.Messages {
		switch m.Role {
		case "user":
			req.Users = append(req.Users, contentText(m.Content))
		case "tool":
			toolResult = true
		}
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	first := len(f.requests) == 1
	f.mu.Unlock()
	if first && f.Hold != nil {
		<-f.Hold
	}

	w.Header().Set("Content-Type", "text/event-stream")
	send := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		w.(http.Flusher).Flush()
	}
	choice := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"id": "c1", "object": "chat.completion.chunk", "model": body.Model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	if !toolResult {
		send(choice(map[string]any{"role": "assistant", "content": "Writing the file."}, nil))
		send(choice(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function",
			"function": map[string]any{"name": f.shellTool, "arguments": `{"command":"echo hello > hello.txt","description":"Write hello.txt"}`}}}}, nil))
		send(choice(map[string]any{}, "tool_calls"))
	} else {
		send(choice(map[string]any{"role": "assistant", "content": "Done. Users: " + strings.Join(req.Users, " | ")}, nil))
		send(choice(map[string]any{}, "stop"))
	}
	send(map[string]any{"id": "c1", "object": "chat.completion.chunk", "choices": []any{},
		"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
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
