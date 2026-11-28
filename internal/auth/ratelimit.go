package auth

import (
	"sync"
	"time"
)

// RateLimiter bounds password sign-in and invite-acceptance attempts per key
// (IP or username) over a sliding window (D145).
type RateLimiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	now     func() time.Time
	hits    map[string][]time.Time
	lastGC  time.Time
	maxKeys int
}

// NewRateLimiter allows up to max attempts per key within window.
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		max:     max,
		window:  window,
		now:     time.Now,
		hits:    make(map[string][]time.Time),
		maxKeys: 10000,
	}
}

// Check reports whether key is currently under the rate limit without
// recording an attempt, returning how long to wait when denied.
func (l *RateLimiter) Check(key string) (bool, time.Duration) {
	if l == nil || key == "" || l.max <= 0 || l.window <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	kept := pruneOld(l.hits[key], now.Add(-l.window))
	if len(kept) == 0 {
		delete(l.hits, key)
		return true, 0
	}
	l.hits[key] = kept
	if len(kept) >= l.max {
		retry := kept[0].Add(l.window).Sub(now)
		if retry < time.Second {
			retry = time.Second
		}
		return false, retry
	}
	return true, 0
}

// Allow records one attempt for key and reports whether it is permitted, along
// with how long to wait before retrying when denied.
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	if l == nil || key == "" || l.max <= 0 || l.window <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)

	// Periodically evict stale keys so the map stays bounded under IP sprays.
	if now.Sub(l.lastGC) > l.window || len(l.hits) > l.maxKeys {
		for k, ts := range l.hits {
			kept := pruneOld(ts, cutoff)
			if len(kept) == 0 {
				delete(l.hits, k)
			} else {
				l.hits[k] = kept
			}
		}
		l.lastGC = now
	}

	kept := pruneOld(l.hits[key], cutoff)
	if len(kept) >= l.max {
		l.hits[key] = kept
		retry := kept[0].Add(l.window).Sub(now)
		if retry < time.Second {
			retry = time.Second
		}
		return false, retry
	}
	l.hits[key] = append(kept, now)
	return true, 0
}

// Reset clears recorded attempts for key (e.g. after a successful sign-in).
func (l *RateLimiter) Reset(key string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}

func pruneOld(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && !ts[i].After(cutoff) {
		i++
	}
	if i == 0 {
		return ts
	}
	return append([]time.Time(nil), ts[i:]...)
}
