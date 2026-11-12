package runner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

func TestEventLogDoneIsLast(t *testing.T) {
	l := NewEventLog(time.Now)
	l.Append(domain.EventAssistantMessage, AssistantMessage{Text: "a"})
	if !l.Append(domain.EventDone, Done{Status: DoneSucceeded}) {
		t.Fatal("done rejected")
	}
	if l.Append(domain.EventAssistantMessage, AssistantMessage{Text: "late"}) || !l.Closed() {
		t.Fatal("append after done was accepted")
	}
	events, closed, _ := l.after(0)
	if !closed || len(events) != 2 || events[1].Type != domain.EventDone {
		t.Fatalf("events = %+v, closed = %v", events, closed)
	}
}

// Concurrent followers all see every event exactly once, in order.
func TestEventLogConcurrentFollowers(t *testing.T) {
	l := NewEventLog(time.Now)
	const n = 200
	var wg sync.WaitGroup
	results := make([][]int64, 5)
	for i := range results {
		wg.Go(func() {
			err := l.Follow(t.Context(), 0, func(e Event) error {
				results[i] = append(results[i], e.Seq)
				return nil
			})
			if err != nil {
				t.Errorf("follower %d: %v", i, err)
			}
		})
	}
	for i := range n {
		l.Append(domain.EventToolCall, ToolCall{ID: string(rune('a' + i%26))})
	}
	l.Append(domain.EventDone, Done{Status: DoneSucceeded})
	wg.Wait()
	for i, seqs := range results {
		if len(seqs) != n+1 {
			t.Fatalf("follower %d saw %d events, want %d", i, len(seqs), n+1)
		}
		for j, s := range seqs {
			if s != int64(j+1) {
				t.Fatalf("follower %d event %d has seq %d", i, j, s)
			}
		}
	}
}

func TestEventLogFollowStops(t *testing.T) {
	l := NewEventLog(time.Now)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := l.Follow(ctx, 0, func(Event) error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Follow on idle log = %v, want deadline exceeded", err)
	}
	l.Append(domain.EventError, Error{Message: "x"})
	sendErr := errors.New("client gone")
	if err := l.Follow(t.Context(), 0, func(Event) error { return sendErr }); !errors.Is(err, sendErr) {
		t.Errorf("Follow with failing send = %v, want %v", err, sendErr)
	}
}

func TestEventLogUnencodablePayloadBecomesError(t *testing.T) {
	l := NewEventLog(time.Now)
	l.Append(domain.EventToolCall, map[string]any{"bad": func() {}})
	events, _, _ := l.after(0)
	if events[0].Type != domain.EventError {
		t.Errorf("type = %s, want error", events[0].Type)
	}
}
