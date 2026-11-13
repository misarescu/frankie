package api

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

type sseEvent struct{ id, event, data string }

// readSSE reads events until "end" or the stream closes.
func readSSE(t *testing.T, url, lastEventID string, got chan<- sseEvent) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Error(err)
		close(got)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	var cur sseEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			cur.id = line[4:]
		case strings.HasPrefix(line, "event: "):
			cur.event = line[7:]
		case strings.HasPrefix(line, "data: "):
			cur.data = line[6:]
		case line == "" && cur.event != "":
			got <- cur
			if cur.event == "end" {
				close(got)
				return
			}
			cur = sseEvent{}
		}
	}
	close(got)
}

// FR-OB-1: a viewer sees stored events, then live ones as they are appended,
// in order, and the stream ends once the run is finished.
func TestStreamIsLiveAndEnds(t *testing.T) {
	st, srv, run, _ := testServer(t)
	got := make(chan sseEvent, 100)
	go readSSE(t, srv.URL+"/api/runs/"+run.ID.String()+"/stream", "", got)

	first := <-got // the stored state change from pending -> running
	if first.event != "state_change" || first.id == "" {
		t.Fatalf("first event = %+v", first)
	}
	for i := range 3 {
		time.Sleep(50 * time.Millisecond) // appended while the viewer is connected
		if _, err := st.AppendEvent(t.Context(), store.NewEvent{RunID: &run.ID, Type: domain.EventAssistantMessage,
			Payload: json.RawMessage(`{"text":"step ` + string(rune('a'+i)) + `"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		select {
		case e := <-got:
			if e.event != "assistant_message" || !strings.Contains(e.data, `step `+string(rune('a'+i))) {
				t.Fatalf("live event %d = %+v", i, e)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("live event %d did not arrive", i)
		}
	}
	if _, err := st.TransitionRun(t.Context(), run.ID, domain.ExecRunning, domain.ExecSucceeded); err != nil {
		t.Fatal(err)
	}
	var last sseEvent
	for e := range got {
		last = e
	}
	if last.event != "end" || !strings.Contains(last.data, "succeeded") {
		t.Errorf("stream ended with %+v", last)
	}
}

// A reconnecting viewer resumes after Last-Event-ID without duplicates.
func TestStreamResumesAfterLastEventID(t *testing.T) {
	st, srv, run, _ := testServer(t)
	for range 3 {
		if _, err := st.AppendEvent(t.Context(), store.NewEvent{RunID: &run.ID, Type: domain.EventToolCall}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.TransitionRun(t.Context(), run.ID, domain.ExecRunning, domain.ExecFailed); err != nil {
		t.Fatal(err)
	}
	all, _ := st.ListRunEvents(t.Context(), run.ID, 0, 100)
	cut := all[2].Seq
	got := make(chan sseEvent, 100)
	go readSSE(t, srv.URL+"/api/runs/"+run.ID.String()+"/stream", strconv.FormatInt(cut, 10), got)
	var ids []string
	for e := range got {
		if e.event != "end" {
			ids = append(ids, e.id)
		}
	}
	if len(ids) != len(all)-3 || ids[0] != strconv.FormatInt(all[3].Seq, 10) {
		t.Errorf("resumed ids %v after %d; all %d events", ids, cut, len(all))
	}
}

// get returns the status and body of a GET.
func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// FR-OB-2: a finished run replays from the store, page by page.
func TestListEventsPages(t *testing.T) {
	st, srv, run, _ := testServer(t)
	for range 4 {
		_, _ = st.AppendEvent(t.Context(), store.NewEvent{RunID: &run.ID, Type: domain.EventToolCall})
	}
	var page struct {
		Events []eventJSON `json:"events"`
		Next   int64       `json:"next"`
	}
	_, body := get(t, srv.URL+"/api/runs/"+run.ID.String()+"/events?limit=2")
	_ = json.Unmarshal([]byte(body), &page)
	if len(page.Events) != 2 || page.Next != page.Events[1].Seq {
		t.Fatalf("page = %+v", page)
	}
	for _, path := range []string{"/api/runs/not-a-uuid/events", "/api/runs/" + domain.NewID().String() + "/stream"} {
		if code, _ := get(t, srv.URL+path); code != http.StatusBadRequest && code != http.StatusNotFound {
			t.Errorf("%s = %d", path, code)
		}
	}
}
