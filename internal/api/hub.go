package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Channel is the Postgres notification channel the store signals on.
const Channel = "frankie_events"

// Hub fans Postgres notifications (one LISTEN connection) out to the streams
// following each run. A wake-up only says "look again"; streams read events from
// the store, so a missed notification costs latency, never data.
type Hub struct {
	databaseURL string
	log         *slog.Logger

	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

// NewHub returns a hub; call Run to start listening.
func NewHub(databaseURL string, log *slog.Logger) *Hub {
	return &Hub{databaseURL: databaseURL, log: log, subs: map[string]map[chan struct{}]struct{}{}}
}

// Subscribe returns a channel woken when the run has new events, and a cancel func.
func (h *Hub) Subscribe(runID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[runID] == nil {
		h.subs[runID] = map[chan struct{}]struct{}{}
	}
	h.subs[runID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[runID], ch)
		if len(h.subs[runID]) == 0 {
			delete(h.subs, runID)
		}
		h.mu.Unlock()
	}
}

func (h *Hub) wake(runID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[runID] {
		select {
		case ch <- struct{}{}:
		default: // already pending
		}
	}
}

// Run listens until ctx ends, reconnecting after errors.
func (h *Hub) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := h.listen(ctx); err != nil && ctx.Err() == nil {
			h.log.Warn("event hub: listen", "error", err)
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (h *Hub) listen(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, h.databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		h.wake(n.Payload)
	}
}
