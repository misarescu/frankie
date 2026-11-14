package runnertest

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
)

func TestFakeModelScript(t *testing.T) {
	f := NewFakeModel(t, "bash")
	post := func(body string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.URL()+"/v1/chat/completions", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var b strings.Builder
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			b.WriteString(sc.Text() + "\n")
		}
		return b.String()
	}
	first := post(`{"model":"m","messages":[{"role":"user","content":"go"}],"tools":[{"function":{"name":"bash"}}]}`)
	if !strings.Contains(first, `"name":"bash"`) || !strings.Contains(first, "tool_calls") {
		t.Errorf("first answer = %s", first)
	}
	second := post(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"go"}]},{"role":"tool","content":"ok"}]}`)
	if !strings.Contains(second, "Done. Users: go") || !strings.Contains(second, "[DONE]") {
		t.Errorf("second answer = %s", second)
	}
	reqs := f.Requests()
	if len(reqs) != 2 || reqs[0].Model != "m" || len(reqs[0].Tools) != 1 || reqs[1].Users[0] != "go" {
		t.Errorf("requests = %+v", reqs)
	}
}
