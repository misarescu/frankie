package claudecode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

type emitted struct {
	typ     domain.EventType
	payload map[string]any
}

func collect(t *testing.T) (runner.Emit, *[]emitted) {
	t.Helper()
	var out []emitted
	return func(typ domain.EventType, p any) {
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("payload is not an object: %s", raw)
		}
		out = append(out, emitted{typ, m})
	}, &out
}

// A representative Claude Code stream-json session: init, text, a shell tool
// call and its result, a file tool call and its result, and the turn result.
var sessionFixture = []string{
	`{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-opus","tools":["Bash","Read"]}`,
	`{"type":"assistant","session_id":"sess-1","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Running the tests."}]}}`,
	`{"type":"assistant","session_id":"sess-1","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
	`{"type":"user","session_id":"sess-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"ok  \tpkg\t0.1s","is_error":false}]}}`,
	`{"type":"assistant","session_id":"sess-1","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_2","name":"Read","input":{"file_path":"/w/main.go"}}]}}`,
	`{"type":"user","session_id":"sess-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_2","content":[{"type":"text","text":"package main"},{"type":"image"}],"is_error":true}]}}`,
	``,
	`{"type":"result","subtype":"success","is_error":false,"result":"All green.","num_turns":3,"total_cost_usd":0.0123,"session_id":"sess-1","usage":{"input_tokens":120,"output_tokens":45,"cache_read_input_tokens":900,"cache_creation_input_tokens":30}}`,
}

func TestTranslateSession(t *testing.T) {
	emit, got := collect(t)
	tr := newTranslator(emit)
	ended := 0
	for _, l := range sessionFixture {
		if tr.handle([]byte(l)) {
			ended++
		}
	}

	want := []emitted{
		{domain.EventAssistantMessage, map[string]any{"text": "Running the tests."}},
		{domain.EventToolCall, map[string]any{"id": "tu_1", "name": "Bash", "input": map[string]any{"command": "go test ./..."}}},
		{domain.EventCommandOutput, map[string]any{"tool_call_id": "tu_1", "output": "ok  \tpkg\t0.1s", "is_error": false}},
		{domain.EventToolCall, map[string]any{"id": "tu_2", "name": "Read", "input": map[string]any{"file_path": "/w/main.go"}}},
		{domain.EventToolResult, map[string]any{"tool_call_id": "tu_2", "name": "Read", "content": "package main\n[image]", "is_error": true}},
		{domain.EventTokenUsage, map[string]any{"input_tokens": 120.0, "output_tokens": 45.0, "cache_read_input_tokens": 900.0,
			"cache_creation_input_tokens": 30.0, "cost_usd": 0.0123, "turns": 3.0}},
	}
	if len(*got) != len(want) {
		t.Fatalf("got %d events, want %d:\n%+v", len(*got), len(want), *got)
	}
	for i := range want {
		g, w := (*got)[i], want[i]
		gj, _ := json.Marshal(g.payload)
		wj, _ := json.Marshal(w.payload)
		if g.typ != w.typ || string(gj) != string(wj) {
			t.Errorf("event %d = %s %s, want %s %s", i, g.typ, gj, w.typ, wj)
		}
	}
	if ended != 1 {
		t.Errorf("turns ended = %d, want 1", ended)
	}
	if tr.sessionID != "sess-1" {
		t.Errorf("session id = %q", tr.sessionID)
	}
	if tr.last == nil || tr.last.isError || tr.last.text != "All green." {
		t.Errorf("last result = %+v", tr.last)
	}
}

func TestTranslateErrorResultAndGarbage(t *testing.T) {
	emit, got := collect(t)
	tr := newTranslator(emit)
	tr.handle([]byte(`not json at all`))
	tr.handle([]byte(`{"type":"user","message":{"role":"user","content":"plain string prompt echo"}}`))
	tr.handle([]byte(`{"type":"stream_event","event":{}}`))
	if !tr.handle([]byte(`{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":50}`)) {
		t.Fatal("error result did not end the turn")
	}
	if len(*got) != 2 || (*got)[0].typ != domain.EventError || (*got)[1].typ != domain.EventTokenUsage {
		t.Fatalf("events = %+v; want error then token_usage", *got)
	}
	if tr.last == nil || !tr.last.isError || tr.last.subtype != "error_max_turns" {
		t.Errorf("last result = %+v", tr.last)
	}
}

func TestTranslateTruncatesLargeOutput(t *testing.T) {
	emit, got := collect(t)
	tr := newTranslator(emit)
	big := strings.Repeat("x", maxOutputBytes+100)
	tr.handle([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t","name":"Bash","input":{}}]}}`))
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{
		"content": []map[string]any{{"type": "tool_result", "tool_use_id": "t", "content": big}}}})
	tr.handle(line)
	out := (*got)[1].payload["output"].(string)
	if len(out) > maxOutputBytes+64 || !strings.HasSuffix(out, "[truncated 100 bytes]") {
		t.Errorf("output not truncated: len %d, suffix %q", len(out), out[len(out)-30:])
	}
}

func TestArgs(t *testing.T) {
	args, err := Args(runner.SessionConfig{Model: "opus", SystemPrompt: "be terse", MaxTurns: 7})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	for _, want := range []string{"-p", "--input-format stream-json", "--output-format stream-json", "--verbose",
		"--dangerously-skip-permissions", "--model opus", "--append-system-prompt be terse", "--max-turns 7"} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
	bareArgs, _ := Args(runner.SessionConfig{})
	if bare := strings.Join(bareArgs, " "); strings.Contains(bare, "--model") || strings.Contains(bare, "--max-turns") {
		t.Errorf("empty config produced optional flags: %q", bare)
	}
}

// Shapes observed from Claude Code 2.1.323 answering two messages in one session:
// tokens and num_turns are per message, total_cost_usd is the session total.
func TestTranslateTokenUsageIsSessionTotals(t *testing.T) {
	emit, got := collect(t)
	tr := newTranslator(emit)
	tr.handle([]byte(`{"type":"result","subtype":"success","num_turns":1,"total_cost_usd":0.00206,"usage":{"input_tokens":2,"output_tokens":7,"cache_read_input_tokens":9004,"cache_creation_input_tokens":9840}}`))
	tr.handle([]byte(`{"type":"result","subtype":"success","num_turns":1,"total_cost_usd":0.00368,"usage":{"input_tokens":2,"output_tokens":5,"cache_read_input_tokens":12237,"cache_creation_input_tokens":7479}}`))

	if len(*got) != 2 {
		t.Fatalf("events = %+v", *got)
	}
	last := (*got)[1].payload
	want := map[string]float64{"input_tokens": 4, "output_tokens": 12, "cache_read_input_tokens": 21241,
		"cache_creation_input_tokens": 17319, "turns": 2, "cost_usd": 0.00368}
	for k, v := range want {
		if last[k] != v {
			t.Errorf("%s = %v, want %v", k, last[k], v)
		}
	}
}
