package runner

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

// Event is one normalized harness event. Seq is assigned by the EventLog.
type Event struct {
	Seq     int64
	Time    time.Time
	Type    domain.EventType
	Payload json.RawMessage
}

// EventLog is a session's append-only, in-memory event history. Readers can
// replay from any seq and then follow live appends. The log closes after a done
// event; later appends are dropped so done is always last.
type EventLog struct {
	mu      sync.Mutex
	events  []Event
	closed  bool
	changed chan struct{} // closed and replaced on every append
	last    time.Time
	now     func() time.Time
}

// NewEventLog returns an empty log using now for timestamps.
func NewEventLog(now func() time.Time) *EventLog {
	return &EventLog{changed: make(chan struct{}), now: now, last: now()}
}

// Append adds an event and wakes readers. It returns false if the log is closed.
func (l *EventLog) Append(typ domain.EventType, payload any) bool {
	raw, err := json.Marshal(payload)
	if err != nil {
		raw, _ = json.Marshal(map[string]string{"message": "unencodable event payload", "detail": err.Error()})
		typ = domain.EventError
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	l.last = l.now()
	l.events = append(l.events, Event{Seq: int64(len(l.events)) + 1, Time: l.last, Type: typ, Payload: raw})
	if typ == domain.EventDone {
		l.closed = true
	}
	close(l.changed)
	l.changed = make(chan struct{})
	return true
}

// LastActivity is the time of the latest append (or creation).
func (l *EventLog) LastActivity() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.last
}

// Closed reports whether the done event has been appended.
func (l *EventLog) Closed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// after returns events with seq > afterSeq, whether the log is closed, and a
// channel that is closed on the next append.
func (l *EventLog) after(afterSeq int64) ([]Event, bool, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	start := min(max(afterSeq, 0), int64(len(l.events)))
	out := make([]Event, len(l.events)-int(start))
	copy(out, l.events[start:])
	return out, l.closed, l.changed
}

// Follow calls send for every event after afterSeq, waiting for new events,
// until the done event has been sent (returns nil), send fails, or ctx ends.
func (l *EventLog) Follow(ctx context.Context, afterSeq int64, send func(Event) error) error {
	cursor := afterSeq
	for {
		events, closed, changed := l.after(cursor)
		for _, e := range events {
			if err := send(e); err != nil {
				return err
			}
			cursor = e.Seq
		}
		if closed {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
