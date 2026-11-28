package api

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// postText posts a JSON body and returns the status and the body as text.
func postText(t *testing.T, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

// D70: a person cancels an issue and what is unfinished under it, with a
// reason, and completes a parent once its sub-issues are finished; refusals
// say why.
func TestCompleteAndCancelIssues(t *testing.T) {
	st, srv, run, _ := testServer(t)
	ctx := t.Context()
	is := boardFixture(t, st, run)
	url := func(key, action string) string { return srv.URL + "/api/issues/" + key + "/" + action }

	for _, c := range []struct {
		url, body string
		code      int
		says      string
	}{
		{url(is["epic"].Key, "complete"), `{"by":"ana"}`, http.StatusConflict, "unfinished sub-issues"},
		{url(is["running"].Key, "cancel"), `{"by":"ana","reason":"stop"}`, http.StatusConflict, "run in progress"},
		{url(is["epic"].Key, "cancel"), `{"by":"ana"}`, http.StatusConflict, "say why"},
		{url("gc-999", "cancel"), `{"reason":"x"}`, http.StatusNotFound, "no such issue"},
	} {
		if code, body := postText(t, c.url, c.body); code != c.code || !strings.Contains(body, c.says) {
			t.Errorf("POST %s %s = %d %q, want %d saying %q", c.url, c.body, code, body, c.code, c.says)
		}
	}

	var reply closeReply
	if code := postDecode(t, url(is["epic"].Key, "cancel"), `{"by":"ana","reason":"replaced by a new plan"}`, &reply); code != http.StatusOK {
		t.Fatalf("cancel epic = %d", code)
	}
	if keys := refKeys(reply.Closed); !slices.Contains(keys, is["epic"].Key) || len(keys) != 4 || keys[0] != is["epic"].Key {
		t.Errorf("canceled %v; want the epic first and its three tasks", keys)
	}
	var epic issueDetailJSON
	getJSON(t, srv.URL+"/api/issues/"+is["epic"].Key, &epic)
	if epic.Status != "canceled" || epic.ClosedBy != testActorName || epic.CloseReason != "replaced by a new plan" || epic.ClosedAt == nil {
		t.Errorf("epic = %+v", epic)
	}

	// A parent whose sub-issue is merged is completed.
	p, _ := st.GetProject(ctx, is["epic"].ProjectID)
	story, _ := st.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueStory, Title: "s", CreatedBy: "t"})
	task, _ := st.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "t", CreatedBy: "t", ParentID: &story.ID, Status: domain.IssueAwaitingHuman})
	if _, err := st.TransitionIssue(ctx, task.ID, domain.IssueAwaitingHuman, domain.IssueMerged); err != nil {
		t.Fatal(err)
	}
	if code := postDecode(t, url(story.Key, "complete"), `{"by":"bo"}`, &reply); code != http.StatusOK || len(reply.Closed) != 1 || reply.Closed[0].Status != "merged" {
		t.Fatalf("complete story = %d, %+v", code, reply)
	}
	if code, body := postText(t, url(story.Key, "complete"), `{}`); code != http.StatusConflict || !strings.Contains(body, "is merged") {
		t.Errorf("completing twice = %d %q, want 409", code, body)
	}
}
