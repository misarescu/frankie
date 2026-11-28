package auth

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	rl := NewRateLimiter(3, 5*time.Minute)
	rl.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if ok, _ := rl.Allow("1.2.3.4"); !ok {
			t.Fatalf("attempt %d unexpectedly blocked", i+1)
		}
	}
	ok, retry := rl.Allow("1.2.3.4")
	if ok || retry <= 0 {
		t.Fatalf("4th attempt = ok:%v retry:%v, want blocked", ok, retry)
	}
	// A different key is unaffected.
	if ok, _ := rl.Allow("5.6.7.8"); !ok {
		t.Fatal("other key unexpectedly blocked")
	}
	// Advancing past the window allows again.
	now = now.Add(5*time.Minute + time.Second)
	if ok, _ := rl.Allow("1.2.3.4"); !ok {
		t.Fatal("expected key to be allowed after window")
	}
	// Reset clears the counter.
	rl.Reset("1.2.3.4")
	for i := 0; i < 3; i++ {
		if ok, _ := rl.Allow("1.2.3.4"); !ok {
			t.Fatalf("post-reset attempt %d blocked", i+1)
		}
	}
}
