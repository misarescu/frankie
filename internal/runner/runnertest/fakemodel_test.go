package runnertest

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFakeModelScript(t *testing.T) {
	f := NewFakeModel(t, "bash")
	post := func(path, body string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.URL()+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("x-api-key", "sk")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	tests := []struct {
		name, path, body string
		want             []string
	}{
		{"openai, streamed, calls the shell", "/v1/chat/completions",
			`{"model":"m","stream":true,"messages":[{"role":"user","content":"go"}],"tools":[{"function":{"name":"bash"}}]}`,
			[]string{`"name":"bash"`, `"tool_calls"`, FirstAnswer, "[DONE]"}},
		{"openai, after a tool result", "/v1/chat/completions",
			`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"go"}]},{"role":"tool","content":"ok"}],"tools":[{"function":{"name":"bash"}}]}`,
			[]string{FinalPrefix + "go", `"finish_reason":"stop"`}},
		{"anthropic, streamed, calls the shell", "/v1/messages",
			`{"model":"m","stream":true,"messages":[{"role":"user","content":"go"}],"tools":[{"name":"bash"}]}`,
			[]string{"event: message_start", `"type":"tool_use"`, `"stop_reason":"tool_use"`, "event: message_stop"}},
		{"anthropic, steer injected as a system entry", "/v1/messages",
			`{"model":"m","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"ok"}]},{"role":"system","content":[{"type":"text","text":"also bananas"}]}],"tools":[{"name":"bash"}]}`,
			[]string{FinalPrefix + "also bananas", `"stop_reason":"end_turn"`}},
		{"no tools: no tool call", "/v1/messages",
			`{"model":"m","messages":[{"role":"user","content":"name this"}]}`,
			[]string{FirstAnswer, `"stop_reason":"end_turn"`}},
	}
	for _, tt := range tests {
		got := post(tt.path, tt.body)
		for _, w := range tt.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: answer lacks %q:\n%s", tt.name, w, got)
			}
		}
	}
	reqs := f.Requests()
	if len(reqs) != len(tests) || reqs[0].API != "openai" || reqs[2].API != "anthropic" || reqs[0].Auth != "sk" ||
		!reqs[1].ToolResult || !reqs[3].SawUser("bananas") || len(f.TaskRequests()) != 4 {
		t.Errorf("requests = %+v", reqs)
	}
	if got := post("/v1/embeddings", `{}`); !strings.Contains(got, "404") {
		t.Errorf("unknown path answered %q", got)
	}
}
