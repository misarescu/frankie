package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

func TestEventValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	r := mustRun(t, s, mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning).ID)

	if _, err := s.AppendEvent(ctx, NewEvent{RunID: &r.ID, Type: domain.EventToolCall, Payload: json.RawMessage(`nope`)}); err == nil {
		t.Error("AppendEvent accepted invalid JSON")
	}
	if _, err := s.AppendEvent(ctx, NewEvent{RunID: &r.ID}); err == nil {
		t.Error("AppendEvent accepted an empty type")
	}
	if _, err := s.ListRunEvents(ctx, r.ID, 0, 0); err == nil {
		t.Error("ListRunEvents accepted limit 0")
	}
	e, err := s.AppendEvent(ctx, NewEvent{RunID: &r.ID, Type: domain.EventToolCall})
	if err != nil {
		t.Fatalf("AppendEvent with defaults: %v", err)
	}
	if string(e.Payload) != "{}" || e.Ts.IsZero() {
		t.Errorf("defaults not applied: payload %s, ts %v", e.Payload, e.Ts)
	}
}

// FR-OB-2: a client pages through a run's stream with a cursor and sees each
// event exactly once, in order, without events of other runs.
func TestListRunEventsPaging(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	p := mustProject(t, s)
	r := mustRun(t, s, mustIssue(t, s, p.ID, domain.IssueRunning).ID)
	other := mustRun(t, s, mustIssue(t, s, p.ID, domain.IssueRunning).ID)

	for i := range 5 {
		for _, run := range []uuid.UUID{r.ID, other.ID} {
			if _, err := s.AppendEvent(ctx, NewEvent{RunID: &run, Type: domain.EventAssistantMessage,
				Payload: json.RawMessage(fmt.Sprintf(`{"n":%d}`, i))}); err != nil {
				t.Fatalf("AppendEvent: %v", err)
			}
		}
	}
	var seen []string
	var cursor int64
	for {
		page, err := s.ListRunEvents(ctx, r.ID, cursor, 2)
		if err != nil {
			t.Fatalf("ListRunEvents: %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, e := range page {
			if e.RunID == nil || *e.RunID != r.ID {
				t.Fatalf("event of another run: %+v", e)
			}
			seen = append(seen, string(e.Payload))
			cursor = e.Seq
		}
	}
	if got := strings.Join(seen, ","); got != `{"n": 0},{"n": 1},{"n": 2},{"n": 3},{"n": 4}` {
		t.Errorf("paged events = %s", got)
	}
}
