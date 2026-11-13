package activities

import (
	"context"
	"sync"
	"time"
)

// sessionKeepAlive is how often a quiet session still heartbeats; well inside
// sessionOptions' heartbeat timeout in the workflow package (2 minutes).
const sessionKeepAlive = 30 * time.Second

// sessionHeartbeat records an agent session's progress as its Activity's
// heartbeat: whenever an event is persisted, and every interval while the
// agent is quiet, e.g. running a long test suite. Without the second, a quiet
// agent looks like a dead worker and Temporal times the Activity out; truly
// stuck sessions are bounded by the runner's idle timeout (FR-AG-3) instead.
type sessionHeartbeat struct {
	mu       sync.Mutex
	progress sessionProgress
	record   func(sessionProgress)
}

// set records new progress at once.
func (h *sessionHeartbeat) set(p sessionProgress) {
	h.mu.Lock()
	h.progress = p
	h.mu.Unlock()
	h.record(p)
}

// keepAlive repeats the last progress every interval until ctx ends.
func (h *sessionHeartbeat) keepAlive(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.mu.Lock()
			p := h.progress
			h.mu.Unlock()
			h.record(p)
		}
	}
}
