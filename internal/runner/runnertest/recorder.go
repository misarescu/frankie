package runnertest

import (
	"slices"
	"sync"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// Recorder collects the normalized events a session emits.
type Recorder struct {
	mu     sync.Mutex
	events []Event
}

// Event is one recorded event.
type Event struct {
	Type    domain.EventType
	Payload any
}

// Emit is a runner.Emit.
func (r *Recorder) Emit(t domain.EventType, p any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, Event{t, p})
}

// All returns the events so far.
func (r *Recorder) All() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// Done returns the final done event, failing the test if the last event is not one.
func (r *Recorder) Done(t *testing.T) runner.Done {
	t.Helper()
	ev := r.All()
	if len(ev) == 0 || ev[len(ev)-1].Type != domain.EventDone {
		t.Fatalf("last event is not done: %+v", ev)
	}
	return ev[len(ev)-1].Payload.(runner.Done)
}
