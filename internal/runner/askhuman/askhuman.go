// Package askhuman is the runner's MCP server offering one tool, ask_human:
// an agent stuck on a decision only a person can make asks a question, and
// the call returns the person's answer (docs/spec/04, "Human intervention").
//
// It implements the part of MCP's Streamable HTTP transport the harnesses
// use (initialize, tools/list, tools/call, ping, notifications), with the
// standard library only. A tools/call waiting for its answer is answered as
// an event stream that sends progress notifications meanwhile, so clients
// whose request timeouts restart on progress keep waiting.
package askhuman

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Server names the MCP server and its tool as harnesses see them.
const (
	ServerName = "frankie"
	ToolName   = "ask_human"
)

// Ask delivers a question to a person and waits for the answer. It returns
// an error when no answer can come any more (the session ended).
type Ask func(ctx context.Context, question string) (string, error)

// Handler serves the MCP endpoint.
type Handler struct {
	Ask Ask
	// Progress is how often a waiting call reports progress; default 20s.
	Progress time.Duration
}

// protocolVersions are the MCP revisions the server speaks, newest first.
var protocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

const toolDescription = "Ask the person who assigned this work a question, and wait for the answer. " +
	"Use it only when you cannot proceed without a decision or information only they have; " +
	"otherwise make a reasonable choice and continue. The answer may take a while."

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodGet, http.MethodDelete:
		// No server-initiated stream and no sessions to end.
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
		return
	}
	if len(req.ID) == 0 { // a notification needs no answer
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case "initialize":
		writeJSON(w, response{JSONRPC: "2.0", ID: req.ID, Result: initializeResult(req.Params)})
	case "ping":
		writeJSON(w, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		writeJSON(w, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": []any{toolSchema()}}})
	case "tools/call":
		h.call(w, r, req)
	default:
		writeJSON(w, response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{-32601, "method not found: " + req.Method}})
	}
}

func initializeResult(params json.RawMessage) map[string]any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	version := protocolVersions[0]
	if slices.Contains(protocolVersions, p.ProtocolVersion) {
		version = p.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": ServerName, "version": "1"},
		"instructions":    "Use ask_human only when you cannot proceed without a person's decision.",
	}
}

func toolSchema() map[string]any {
	return map[string]any{
		"name":        ToolName,
		"description": toolDescription,
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "The question, with the context the person needs to answer it."},
			},
			"required": []string{"question"},
		},
	}
}

func (h *Handler) call(w http.ResponseWriter, r *http.Request, req request) {
	var p struct {
		Name      string `json:"name"`
		Arguments struct {
			Question string `json:"question"`
		} `json:"arguments"`
		Meta struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Name != ToolName {
		writeJSON(w, response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{-32602, "unknown tool " + p.Name}})
		return
	}
	question := strings.TrimSpace(p.Arguments.Question)
	if question == "" {
		writeJSON(w, response{JSONRPC: "2.0", ID: req.ID, Result: toolResult("The question is empty; ask again with a question.", true)})
		return
	}

	// Answer as an event stream: progress while waiting, then the result.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	send := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	if flusher != nil {
		flusher.Flush()
	}

	type outcome struct {
		answer string
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		answer, err := h.Ask(r.Context(), question)
		done <- outcome{answer, err}
	}()
	every := h.Progress
	if every <= 0 {
		every = 20 * time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for n := 1; ; n++ {
		select {
		case o := <-done:
			switch {
			case o.err == nil:
				send(response{JSONRPC: "2.0", ID: req.ID, Result: toolResult(o.answer, false)})
			case errors.Is(o.err, context.Canceled):
				// The client gave up; nobody reads an answer.
			default:
				send(response{JSONRPC: "2.0", ID: req.ID, Result: toolResult("No answer: "+o.err.Error(), true)})
			}
			return
		case <-ticker.C:
			if len(p.Meta.ProgressToken) > 0 {
				send(map[string]any{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]any{
					"progressToken": p.Meta.ProgressToken, "progress": n, "message": "waiting for a person to answer",
				}})
			} else {
				// Keep the stream alive for clients that time out on silence.
				_, _ = fmt.Fprint(w, ": waiting\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
