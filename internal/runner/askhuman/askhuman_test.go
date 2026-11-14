package askhuman

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, ask Ask) string {
	t.Helper()
	srv := httptest.NewServer(&Handler{Ask: ask, Progress: 20 * time.Millisecond})
	t.Cleanup(srv.Close)
	return srv.URL
}

// reply is what the server answered.
type reply struct {
	status      int
	contentType string
}

func post(t *testing.T, url, body string) (reply, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{resp.StatusCode, resp.Header.Get("Content-Type")}, string(b)
}

func TestHandshakeAndToolList(t *testing.T) {
	url := serve(t, nil)
	_, init := post(t, url, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t"}}}`)
	if !strings.Contains(init, `"protocolVersion":"2025-06-18"`) || !strings.Contains(init, `"tools":{}`) {
		t.Errorf("initialize = %s", init)
	}
	_, newer := post(t, url, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01"}}`)
	if !strings.Contains(newer, `"protocolVersion":"`+protocolVersions[0]+`"`) {
		t.Errorf("unknown version answered %s", newer)
	}
	if r, _ := post(t, url, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); r.status != http.StatusAccepted {
		t.Errorf("notification: %d", r.status)
	}
	_, list := post(t, url, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var out struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(list), &out); err != nil || len(out.Result.Tools) != 1 || out.Result.Tools[0].Name != ToolName {
		t.Fatalf("tools/list = %s", list)
	}
	_, unknown := post(t, url, `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	if !strings.Contains(unknown, `"code":-32601`) {
		t.Errorf("unknown method = %s", unknown)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %v %v", resp, err)
	} else {
		_ = resp.Body.Close()
	}
}

// A call waits for the answer, reporting progress meanwhile, and returns it
// as the tool result.
func TestCallWaitsForTheAnswer(t *testing.T) {
	asked := make(chan string, 1)
	url := serve(t, func(_ context.Context, q string) (string, error) {
		asked <- q
		time.Sleep(70 * time.Millisecond)
		return "Use Postgres.", nil
	})
	r, body := post(t, url, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"ask_human","arguments":{"question":"Which database?"},"_meta":{"progressToken":"p1"}}}`)
	if ct := r.contentType; ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	if q := <-asked; q != "Which database?" {
		t.Errorf("asked %q", q)
	}
	var progress int
	var result string
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		switch {
		case strings.Contains(data, "notifications/progress"):
			if !strings.Contains(data, `"progressToken":"p1"`) {
				t.Errorf("progress without the token: %s", data)
			}
			progress++
		case strings.Contains(data, `"id":7`):
			result = data
		}
	}
	if progress == 0 {
		t.Error("no progress while waiting")
	}
	if !strings.Contains(result, "Use Postgres.") || !strings.Contains(result, `"isError":false`) {
		t.Errorf("result = %s", result)
	}
}

func TestCallFailures(t *testing.T) {
	url := serve(t, func(context.Context, string) (string, error) { return "", errors.New("the session ended") })
	_, ended := post(t, url, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ask_human","arguments":{"question":"Still there?"}}}`)
	if !strings.Contains(ended, "No answer: the session ended") || !strings.Contains(ended, `"isError":true`) {
		t.Errorf("ended session = %s", ended)
	}
	_, empty := post(t, url, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask_human","arguments":{"question":"  "}}}`)
	if !strings.Contains(empty, "question is empty") {
		t.Errorf("empty question = %s", empty)
	}
	_, other := post(t, url, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"rm_rf","arguments":{}}}`)
	if !strings.Contains(other, `"code":-32602`) {
		t.Errorf("unknown tool = %s", other)
	}
	_, garbage := post(t, url, `not json`)
	if !strings.Contains(garbage, `"code":-32700`) {
		t.Errorf("garbage = %s", garbage)
	}
}
