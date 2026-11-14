package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

func TestCreateIssueDefaultsAndValidation(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)

	is := mustIssue(t, s, p.ID, "")
	if is.Status != domain.IssueDraft {
		t.Errorf("default status = %q, want draft", is.Status)
	}
	if is.ID.Version() != 7 {
		t.Errorf("issue ID version = %d, want 7", is.ID.Version())
	}
	got, err := s.GetIssue(t.Context(), is.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Title != is.Title || got.AcceptanceCriteria == nil || got.Labels == nil {
		t.Errorf("round trip mismatch: %+v", got)
	}

	bad := []NewIssue{
		{ProjectID: p.ID, Type: "chore", Title: "x", CreatedBy: "test"},
		{ProjectID: p.ID, Type: domain.IssueBug, Title: "", CreatedBy: "test"},
		{ProjectID: p.ID, Type: domain.IssueBug, Title: "x", CreatedBy: ""},
		{ProjectID: p.ID, Type: domain.IssueBug, Title: "x", CreatedBy: "test", Status: "bogus"},
	}
	for _, in := range bad {
		if _, err := s.CreateIssue(t.Context(), in); err == nil {
			t.Errorf("CreateIssue(%+v) succeeded, want validation error", in)
		}
	}
}

func TestGetProject(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	got, err := s.GetProject(t.Context(), p.ID)
	if err != nil || got.Name != p.Name || got.KeyPrefix != p.KeyPrefix {
		t.Fatalf("GetProject = %+v, %v; want %+v", got, err, p)
	}
	if _, err := s.GetProject(t.Context(), domain.NewID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject(unknown) = %v, want ErrNotFound", err)
	}
}

// An external issue syncs into at most one Issue per project.
func TestIssueSourceRefUniquePerProject(t *testing.T) {
	s := newTestStore(t)
	p1, p2 := mustProject(t, s), mustProject(t, s)
	in := NewIssue{ProjectID: p1.ID, Type: domain.IssueTask, Title: "t", CreatedBy: "test",
		SourceRef: "https://github.com/acme/app/issues/1"}

	first, err := s.CreateIssue(t.Context(), in)
	if err != nil {
		t.Fatalf("first CreateIssue: %v", err)
	}
	if _, err := s.CreateIssue(t.Context(), in); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate source ref: err = %v, want ErrConflict", err)
	}
	in.ProjectID = p2.ID
	if _, err := s.CreateIssue(t.Context(), in); err != nil {
		t.Fatalf("same source ref in another project: %v", err)
	}
	got, err := s.GetIssueBySourceRef(t.Context(), p1.ID, in.SourceRef)
	if err != nil || got.ID != first.ID {
		t.Fatalf("GetIssueBySourceRef = %v, %v; want %v", got.ID, err, first.ID)
	}
}

func TestTransitionIssue(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	is := mustIssue(t, s, p.ID, domain.IssueReady)
	ctx := t.Context()

	got, err := s.TransitionIssue(ctx, is.ID, domain.IssueReady, domain.IssueRunning)
	if err != nil || got.Status != domain.IssueRunning {
		t.Fatalf("ready -> running = %v, %v", got.Status, err)
	}

	t.Run("retry of an applied transition is a no-op", func(t *testing.T) {
		got, err := s.TransitionIssue(ctx, is.ID, domain.IssueReady, domain.IssueRunning)
		if err != nil || got.Status != domain.IssueRunning {
			t.Fatalf("repeat = %v, %v; want running, nil", got.Status, err)
		}
	})
	t.Run("stale from status conflicts", func(t *testing.T) {
		_, err := s.TransitionIssue(ctx, is.ID, domain.IssueReady, domain.IssueCanceled)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
	})
	t.Run("state machine is enforced before touching the database", func(t *testing.T) {
		_, err := s.TransitionIssue(ctx, is.ID, domain.IssueRunning, domain.IssueMerged)
		if !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("err = %v, want ErrInvalidTransition", err)
		}
		cur, _ := s.GetIssue(ctx, is.ID)
		if cur.Status != domain.IssueRunning {
			t.Fatalf("status changed to %q", cur.Status)
		}
	})
	t.Run("unknown issue", func(t *testing.T) {
		_, err := s.TransitionIssue(ctx, domain.NewID(), domain.IssueReady, domain.IssueRunning)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// Exactly one of many concurrent movers wins a compare-and-set transition.
func TestTransitionIssueConcurrentSingleWinner(t *testing.T) {
	s := newTestStore(t)
	is := mustIssue(t, s, mustProject(t, s).ID, domain.IssueReady)

	targets := []domain.IssueStatus{domain.IssueRunning, domain.IssueCanceled, domain.IssueFailed}
	var wg sync.WaitGroup
	errs := make([]error, len(targets))
	for i, to := range targets {
		wg.Go(func() { _, errs[i] = s.TransitionIssue(t.Context(), is.ID, domain.IssueReady, to) })
	}
	wg.Wait()

	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrConflict):
			t.Errorf("mover %s: unexpected error %v", targets[i], err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d movers won, want exactly 1 (errs: %v)", wins, errs)
	}
}

// D22: issues get <prefix>-<n> keys, numbered per project from 1.
func TestIssueKeys(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	frk, err := s.CreateProject(ctx, "frankie", "frk")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	toy, err := s.CreateProject(ctx, "toy", "toy")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for _, bad := range []string{"", "f", "Frk", "frk-1", "12ab", "abcdefghijk"} {
		if _, err := s.CreateProject(ctx, "bad-"+bad, bad); err == nil {
			t.Errorf("CreateProject accepted key prefix %q", bad)
		}
	}
	if _, err := s.CreateProject(ctx, "other", "frk"); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate key prefix: err = %v, want ErrConflict", err)
	}

	a, b := mustIssue(t, s, frk.ID, ""), mustIssue(t, s, frk.ID, "")
	c := mustIssue(t, s, toy.ID, "")
	if a.Key != "frk-1" || a.Number != 1 || b.Key != "frk-2" || c.Key != "toy-1" {
		t.Fatalf("keys = %s(%d), %s, %s", a.Key, a.Number, b.Key, c.Key)
	}
	got, err := s.GetIssueByKey(ctx, "frk-2")
	if err != nil || got.ID != b.ID {
		t.Fatalf("GetIssueByKey = %v, %v", got.ID, err)
	}
	if _, err := s.GetIssueByKey(ctx, "frk-99"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown key: err = %v, want ErrNotFound", err)
	}

	// A rejected create rolls back its number allocation.
	in := NewIssue{ProjectID: frk.ID, Type: domain.IssueTask, Title: "t", CreatedBy: "test", SourceRef: "gh#1"}
	if _, err := s.CreateIssue(ctx, in); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := s.CreateIssue(ctx, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate source ref: err = %v", err)
	}
	if next := mustIssue(t, s, frk.ID, ""); next.Key != "frk-4" {
		t.Errorf("after a rejected create, next key = %s, want frk-4", next.Key)
	}
}

func TestIssueNumbersUniqueUnderConcurrency(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	const n = 20
	keys := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			is, err := s.CreateIssue(t.Context(), NewIssue{ProjectID: p.ID, Type: domain.IssueTask,
				Title: fmt.Sprint("t", i), CreatedBy: "test"})
			if err != nil {
				t.Errorf("CreateIssue %d: %v", i, err)
				return
			}
			keys[i] = is.Key
		})
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Fatalf("duplicate key %s in %v", k, keys)
		}
		seen[k] = true
	}
	for i := 1; i <= n; i++ {
		if !seen[domain.IssueKey(p.KeyPrefix, i)] {
			t.Errorf("number %d not allocated; keys %v", i, keys)
		}
	}
}
