package api

import (
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

func TestHubWakesSubscribersOfTheRun(t *testing.T) {
	st, _, run, hub := testServer(t)
	mine, cancelMine := hub.Subscribe(run.ID.String())
	defer cancelMine()
	other, cancelOther := hub.Subscribe(domain.NewID().String())
	defer cancelOther()

	// LISTEN starts asynchronously; keep appending until the subscriber wakes.
	deadline := time.After(5 * time.Second)
	for woken := false; !woken; {
		if _, err := st.AppendEvent(t.Context(), store.NewEvent{RunID: &run.ID, Type: domain.EventAssistantMessage}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-mine:
			woken = true
		case <-time.After(200 * time.Millisecond):
		case <-deadline:
			t.Fatal("subscriber of the run was not woken")
		}
	}
	select {
	case <-other:
		t.Error("subscriber of another run was woken")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestHubCancelUnsubscribes(t *testing.T) {
	h := NewHub("", nil)
	_, cancel := h.Subscribe("r")
	cancel()
	if len(h.subs) != 0 {
		t.Errorf("subscriptions left: %v", h.subs)
	}
	h.wake("r") // no subscribers: must not block or panic
}
