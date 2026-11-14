package pi

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
	"github.com/misarescu/frankie/internal/runner/runnertest"
)

// testdata/session.jsonl is the RPC output of a real pi 1.5.2 session against
// a fake model: a task, a bash tool call, a steering message queued mid-turn,
// and a final answer.
func TestTranslateRecordedSession(t *testing.T) {
	f, err := os.Open("testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rec := &runnertest.Recorder{}
	tr := newTranslator(rec.Emit)
	settles := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.Type == "response" {
			continue
		}
		if settled, aborted := tr.handle(r); settled {
			settles++
			if aborted {
				t.Error("settled as aborted")
			}
		}
	}
	if settles != 1 {
		t.Errorf("settled %d times", settles)
	}
	var got []domain.EventType
	for _, e := range rec.All() {
		got = append(got, e.Type)
	}
	want := []domain.EventType{
		domain.EventAssistantMessage, domain.EventToolCall, domain.EventTokenUsage, domain.EventCommandOutput,
		domain.EventAssistantMessage, domain.EventTokenUsage,
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
	all := rec.All()
	if c := all[1].Payload.(runner.ToolCall); c.Name != "bash" || c.ID != "call_1" {
		t.Errorf("tool call = %+v", c)
	}
	if o := all[3].Payload.(runner.CommandOutput); o.ToolCallID != "call_1" || o.IsError || o.Output != "(no output)" {
		t.Errorf("command output = %+v", o)
	}
	if u := all[5].Payload.(runner.TokenUsage); u.InputTokens != 200 || u.OutputTokens != 40 || u.Turns != 2 {
		t.Errorf("usage = %+v", u)
	}
	if tr.lastText == "" || tr.lastError != "" {
		t.Errorf("last text %q, error %q", tr.lastText, tr.lastError)
	}
}

func TestTranslateEvents(t *testing.T) {
	tests := []struct {
		name    string
		records []string
		want    []runnertest.Event
		settled bool
		lastErr string
	}{
		{"failed non-shell tool",
			[]string{`{"type":"tool_execution_end","toolCallId":"c1","toolName":"edit","result":{"content":[{"type":"text","text":"no such file"}]},"isError":true}`},
			[]runnertest.Event{{Type: domain.EventToolResult, Payload: runner.ToolResult{ToolCallID: "c1", Name: "edit", Content: "no such file", IsError: true}}}, false, ""},
		{"model error, then retries give up",
			[]string{`{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"401 bad key"}}`,
				`{"type":"auto_retry_start","attempt":1,"maxAttempts":3,"errorMessage":"429"}`,
				`{"type":"auto_retry_end","success":false,"finalError":"429 again"}`},
			[]runnertest.Event{
				{Type: domain.EventTokenUsage, Payload: runner.TokenUsage{Turns: 1}},
				{Type: domain.EventError, Payload: runner.Error{Message: "model request failed", Detail: "401 bad key"}},
				{Type: domain.EventError, Payload: runner.Error{Message: "model request failed, retry 1 of 3 scheduled", Detail: "429"}},
				{Type: domain.EventError, Payload: runner.Error{Message: "model request failed after retries", Detail: "429 again"}},
			}, false, "401 bad key"},
		{"a successful retry clears the error",
			[]string{`{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"overloaded"}}`,
				`{"type":"auto_retry_end","success":true,"attempt":2}`,
				`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"stopReason":"stop","usage":{"input":5,"output":1,"cacheRead":2,"cacheWrite":3,"cost":{"total":0.5}}}}`},
			[]runnertest.Event{
				{Type: domain.EventTokenUsage, Payload: runner.TokenUsage{Turns: 1}},
				{Type: domain.EventError, Payload: runner.Error{Message: "model request failed", Detail: "overloaded"}},
				{Type: domain.EventAssistantMessage, Payload: runner.AssistantMessage{Text: "ok"}},
				{Type: domain.EventTokenUsage, Payload: runner.TokenUsage{InputTokens: 5, OutputTokens: 1, CacheReadInputTokens: 2, CacheCreationInputTokens: 3, CostUSD: 0.5, Turns: 2}},
			}, false, ""},
		{"user, system and tool result messages are ignored",
			[]string{`{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
				`{"type":"message_end","message":{"role":"system","content":""}}`,
				`{"type":"message_end","message":{"role":"toolResult","content":[{"type":"text","text":"x"}]}}`}, nil, false, ""},
		{"settled after an abort",
			[]string{`{"type":"agent_settled","aborted":true}`}, nil, true, ""},
		{"extension error",
			[]string{`{"type":"extension_error","extensionPath":"/x.ts","event":"tool_call","error":"boom"}`},
			[]runnertest.Event{{Type: domain.EventError, Payload: runner.Error{Message: "harness extension failed: tool_call", Detail: "boom"}}}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &runnertest.Recorder{}
			tr := newTranslator(rec.Emit)
			settled := false
			for _, raw := range tt.records {
				var r record
				if err := json.Unmarshal([]byte(raw), &r); err != nil {
					t.Fatal(err)
				}
				s, _ := tr.handle(r)
				settled = settled || s
			}
			if settled != tt.settled || tr.lastError != tt.lastErr {
				t.Errorf("settled %v, last error %q", settled, tr.lastError)
			}
			got := rec.All()
			if len(got) != len(tt.want) {
				t.Fatalf("events = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i].Type != tt.want[i].Type || got[i].Payload != tt.want[i].Payload {
					t.Errorf("event %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
