package opencode

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// testdata/session.sse is the event stream of a real opencode 2.2.7 session
// against a fake model: a task, a shell tool call, a steering message
// delivered mid-turn, and a final answer.
const recordedSession = "ses_e28a654dbffe3hREjkQCzklCxQ"

func TestTranslateRecordedSession(t *testing.T) {
	f, err := os.Open("testdata/session.sse")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rec := &recorder{}
	tr := newTranslator(rec.emit, recordedSession)
	var ends []execution
	if err := readSSE(f, func(d []byte) {
		if out := tr.handle(d); out.ended != executionRunning {
			ends = append(ends, out.ended)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ends, []execution{executionSucceeded}) {
		t.Errorf("execution ends = %v, want one success", ends)
	}
	var got []domain.EventType
	for _, e := range rec.all() {
		got = append(got, e.typ)
	}
	want := []domain.EventType{
		domain.EventTokenUsage, domain.EventToolCall, domain.EventAssistantMessage, domain.EventCommandOutput,
		domain.EventTokenUsage, domain.EventAssistantMessage,
	}
	// Token usage and text interleave as opencode reports them; compare as multisets plus key order.
	slices.Sort(got)
	sortedWant := slices.Clone(want)
	slices.Sort(sortedWant)
	if !slices.Equal(got, sortedWant) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for _, e := range rec.all() {
		switch p := e.payload.(type) {
		case runner.ToolCall:
			var in map[string]string
			if p.Name != "shell" || p.ID != "call_1" || json.Unmarshal(p.Input.(json.RawMessage), &in) != nil || in["command"] != "echo hello > hello.txt" {
				t.Errorf("tool call = %+v", p)
			}
		case runner.CommandOutput:
			if p.ToolCallID != "call_1" || p.IsError {
				t.Errorf("command output = %+v", p)
			}
		}
	}
	if tr.lastText == "" || tr.turns != 2 {
		t.Errorf("last text %q, turns %d", tr.lastText, tr.turns)
	}
}

func TestTranslateEvents(t *testing.T) {
	const sid = "ses_1"
	tests := []struct {
		name    string
		events  []string
		want    []recorded
		outcome outcome
	}{
		{"another session is ignored",
			[]string{`{"type":"session.text.ended","data":{"sessionID":"ses_child","text":"hi"}}`}, nil, outcome{}},
		{"server-wide events are ignored",
			[]string{`{"type":"server.connected","data":{}}`}, nil, outcome{}},
		{"failed non-shell tool",
			[]string{`{"type":"session.tool.input.started","data":{"sessionID":"ses_1","id":"c1","name":"edit"}}`,
				`{"type":"session.tool.failed","data":{"sessionID":"ses_1","id":"c1","error":{"type":"permission.rejected","message":"Permission denied: edit"},"content":[]}}`},
			[]recorded{{domain.EventToolResult, runner.ToolResult{ToolCallID: "c1", Name: "edit", Content: "permission.rejected: Permission denied: edit", IsError: true}}}, outcome{}},
		{"shell with a failing exit code",
			[]string{`{"type":"session.tool.input.started","data":{"sessionID":"ses_1","id":"c2","name":"shell"}}`,
				`{"type":"session.tool.success","data":{"sessionID":"ses_1","id":"c2","content":[{"type":"text","text":"FAIL"}],"metadata":{"exit":1}}}`},
			[]recorded{{domain.EventCommandOutput, runner.CommandOutput{ToolCallID: "c2", Output: "FAIL", IsError: true}}}, outcome{}},
		{"usage counts reasoning as output",
			[]string{`{"type":"session.step.ended","data":{"sessionID":"ses_1"}}`,
				`{"type":"session.usage.updated","data":{"sessionID":"ses_1","cost":0.25,"tokens":{"input":10,"output":3,"reasoning":2,"cache":{"read":4,"write":5}}}}`},
			[]recorded{{domain.EventTokenUsage, runner.TokenUsage{InputTokens: 10, OutputTokens: 5, CacheReadInputTokens: 4, CacheCreationInputTokens: 5, CostUSD: 0.25, Turns: 1}}}, outcome{}},
		{"retry",
			[]string{`{"type":"session.retry.scheduled","data":{"sessionID":"ses_1","attempt":2,"error":{"type":"provider.transport","message":"ECONNRESET"}}}`},
			[]recorded{{domain.EventError, runner.Error{Message: "model request failed, retry 2 scheduled", Detail: "provider.transport: ECONNRESET"}}}, outcome{}},
		{"execution failed",
			[]string{`{"type":"session.execution.failed","data":{"sessionID":"ses_1","error":{"type":"provider.auth","message":"budget exceeded"}}}`},
			nil, outcome{ended: executionFailed, reason: "provider.auth: budget exceeded"}},
		{"permission asked",
			[]string{`{"type":"permission.asked","data":{"id":"per_1","sessionID":"ses_1","action":"external_directory","resources":["/etc"]}}`},
			[]recorded{{domain.EventError, runner.Error{Message: "harness asked for a permission; refused", Detail: "external_directory /etc"}}}, outcome{permission: "per_1"}},
		{"garbage",
			[]string{`not json`},
			[]recorded{{domain.EventError, runner.Error{Message: "unparseable harness event", Detail: "not json"}}}, outcome{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			tr := newTranslator(rec.emit, sid)
			var last outcome
			for _, e := range tt.events {
				last = tr.handle([]byte(e))
			}
			if last != tt.outcome {
				t.Errorf("outcome = %+v, want %+v", last, tt.outcome)
			}
			got := rec.all()
			if len(got) != len(tt.want) {
				t.Fatalf("events = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i].typ != tt.want[i].typ || got[i].payload != tt.want[i].payload {
					t.Errorf("event %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
