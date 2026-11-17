package store

import (
	"sync"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
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

// M7, D111: issues merged before releases existed are released by the migration
// that follows the releases tables; other statuses are kept.
func TestMigrationReleasesIssuesMergedBeforeReleases(t *testing.T) {
	pool := newTestPool(t)
	if err := migrateTo(t.Context(), pool, 20261116235845); err != nil {
		t.Fatal(err)
	}
	s := New(pool)
	p := mustProject(t, s)
	want := map[domain.IssueStatus]domain.IssueStatus{
		domain.IssueMerged:   domain.IssueReleased,
		domain.IssueRunning:  domain.IssueRunning,
		domain.IssueQueued:   domain.IssueQueued,
		domain.IssueCanceled: domain.IssueCanceled,
		domain.IssueFailed:   domain.IssueFailed,
	}
	before := map[domain.IssueStatus]domain.Issue{}
	for from := range want {
		before[from] = mustIssue(t, s, p.ID, from)
	}
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	for from, is := range before {
		got, err := s.GetIssue(t.Context(), is.ID)
		if err != nil || got.Status != want[from] {
			t.Errorf("issue %s was %s: now %s, %v; want %s", is.Key, from, got.Status, err, want[from])
		}
	}
}
