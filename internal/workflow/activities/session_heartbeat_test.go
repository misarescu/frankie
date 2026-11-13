package activities

import (
	"context"
	"sync"
	"testing"
	"time"
)

// A quiet agent still heartbeats, with the last progress, so its session is
// not timed out as a dead worker (found by the first self-hosted run, whose
// agent ran Frankie's test suite for minutes without an event).
func TestSessionHeartbeatKeepsAQuietSessionAlive(t *testing.T) {
	var mu sync.Mutex
	var beats []sessionProgress
	h := &sessionHeartbeat{progress: sessionProgress{Started: true, LastSeq: 7}, record: func(p sessionProgress) {
		mu.Lock()
		beats = append(beats, p)
		mu.Unlock()
	}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { h.keepAlive(ctx, 5*time.Millisecond); close(done) }()

	time.Sleep(30 * time.Millisecond)
	h.set(sessionProgress{Started: true, LastSeq: 9})
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(beats) < 4 {
		t.Fatalf("beats = %d, want several while quiet", len(beats))
	}
	if beats[0].LastSeq != 7 || beats[len(beats)-1].LastSeq != 9 {
		t.Errorf("first beat %+v, last %+v; want the latest progress repeated", beats[0], beats[len(beats)-1])
	}
	n := len(beats)
	time.Sleep(20 * time.Millisecond)
	if len(beats) != n {
		t.Error("heartbeats continued after the session ended")
	}
}
