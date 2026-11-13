package store

import (
	"sync"
	"testing"
)

// Migrations run at control plane startup behind an advisory lock (docs/spec/02, Postgres conventions):
// concurrent and repeated calls must all succeed.
func TestMigrateConcurrentAndRepeated(t *testing.T) {
	pool := newTestPool(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { errs[i] = Migrate(t.Context(), pool) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent Migrate #%d: %v", i, err)
		}
	}
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("repeated Migrate: %v", err)
	}
	if _, err := New(pool).CreateProject(t.Context(), "after-migrate", "am"); err != nil {
		t.Fatalf("schema not usable after migrate: %v", err)
	}
}
