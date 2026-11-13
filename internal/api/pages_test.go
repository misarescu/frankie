package api

import (
	"strings"
	"testing"
)

func TestPages(t *testing.T) {
	_, srv, run, _ := testServer(t)
	_, list := get(t, srv.URL+"/")
	if !strings.Contains(list, "/runs/"+run.ID.String()) || !strings.Contains(list, "gc-1") {
		t.Errorf("runs page lacks the run:\n%s", list)
	}
	if strings.Contains(list, "<b>search</b>") {
		t.Error("issue title was not HTML-escaped")
	}
	_, page := get(t, srv.URL+"/runs/"+run.ID.String())
	if !strings.Contains(page, `EventSource("/api/runs/`+run.ID.String()+`/stream")`) {
		t.Errorf("run page does not stream the run:\n%.300s", page)
	}
	if code, _ := get(t, srv.URL+"/runs/not-a-uuid"); code != 400 {
		t.Errorf("bad run id = %d", code)
	}
}
