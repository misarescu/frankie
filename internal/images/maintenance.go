package images

import (
	"context"
	"sync"
	"time"
)

// Maintenance keeps registry collection (D84) apart from image use: Ensure
// holds it shared while it looks up, builds and records an image, and a
// collection holds it alone, starting only when nothing uses it. The zero
// value is ready; a nil *Maintenance never waits.
type Maintenance struct {
	mu         sync.Mutex
	users      int
	collecting bool
	done       chan struct{} // closed when the running collection ends
}

// waitTick is how often Use reports that it still waits.
const waitTick = 10 * time.Second

// Use waits while a collection runs, then holds Maintenance shared until
// release is called. tick, when set, is called while it waits, e.g. to
// heartbeat a Temporal Activity.
func (m *Maintenance) Use(ctx context.Context, tick func()) (release func(), err error) {
	if m == nil {
		return func() {}, nil
	}
	for {
		m.mu.Lock()
		if !m.collecting {
			m.users++
			m.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					m.mu.Lock()
					m.users--
					m.mu.Unlock()
				})
			}, nil
		}
		done := m.done
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-done:
		case <-time.After(waitTick):
			if tick != nil {
				tick()
			}
		}
	}
}

// TryCollect holds Maintenance alone if nothing uses it, until release is
// called; ok is false, and nothing held, while an image is in use.
func (m *Maintenance) TryCollect() (release func(), ok bool) {
	if m == nil {
		return func() {}, true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.users > 0 || m.collecting {
		return nil, false
	}
	m.collecting, m.done = true, make(chan struct{})
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			m.collecting = false
			close(m.done)
			m.mu.Unlock()
		})
	}, true
}
