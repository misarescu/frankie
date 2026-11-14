package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/store/storetest"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// fakeStarter records starts and, like StartWorkflowRun, creates the issue's run.
type fakeStarter struct {
	s       *store.Store
	started []string
	inputs  []activities.IssueRunInput
	fail    map[string]bool
}

func (f *fakeStarter) StartIssueRun(ctx context.Context, in activities.IssueRunInput) error {
	if f.fail[in.IssueKey] {
		return errors.New("temporal unavailable")
	}
	f.started = append(f.started, in.IssueKey)
	f.inputs = append(f.inputs, in)
	id := uuid.MustParse(in.IssueID)
	if _, err := f.s.CreateRun(ctx, store.NewRun{IssueID: id, WorkflowSnapshot: json.RawMessage(`{}`), WorkflowVersion: "1",
		TemporalWorkflowID: "issue-" + in.IssueKey}); err != nil {
		return err
	}
	_, err := f.s.TransitionIssue(ctx, id, domain.IssueReady, domain.IssueRunning)
	return err
}

// finish ends an issue's active run the way a successful Interpret does, and
// lets a person merge its change request.
func finish(t *testing.T, s *store.Store, is domain.Issue) {
	t.Helper()
	ctx := t.Context()
	run, err := s.GetActiveRunForIssue(ctx, is.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]domain.ExecStatus{{domain.ExecPending, domain.ExecRunning}, {domain.ExecRunning, domain.ExecSucceeded}} {
		if _, err := s.TransitionRun(ctx, run.ID, step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range [][2]domain.IssueStatus{{domain.IssueRunning, domain.IssueAwaitingHuman}, {domain.IssueAwaitingHuman, domain.IssueMerged}} {
		if _, err := s.TransitionIssue(ctx, is.ID, step[0], step[1]); err != nil {
			t.Fatal(err)
		}
	}
}

// M5 done-when, end to end against the store: starting an epic runs its tasks in
// dependency order, one at a time with maxConcurrentIssues 1.
func TestSchedulerRunsAnEpic(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	p, err := s.CreateProject(ctx, "o/r", "or")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(typ domain.IssueType, parent *domain.Issue) domain.Issue {
		in := store.NewIssue{ProjectID: p.ID, Type: typ, Title: string(typ), Status: domain.IssueReady, CreatedBy: "t",
			SourceRef: "https://github.com/o/r/issues/" + uuid.NewString()[:4]}
		if parent != nil {
			in.ParentID = &parent.ID
		}
		is, err := s.CreateIssue(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return is
	}
	epic := mk(domain.IssueEpic, nil)
	a, b, c := mk(domain.IssueTask, &epic), mk(domain.IssueTask, &epic), mk(domain.IssueTask, &epic)
	for _, e := range [][2]domain.Issue{{b, a}, {c, a}} {
		if err := s.AddIssueDependency(ctx, e[0].ID, e[1].ID); err != nil {
			t.Fatal(err)
		}
	}
	starter := &fakeStarter{s: s}
	sch := &Scheduler{Store: s, Starter: starter, Projects: map[uuid.UUID]Project{p.ID: {Repo: "o/r", BaseBranch: "main"}},
		Limit: 1, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	pass := func() string {
		t.Helper()
		before := len(starter.started)
		if err := sch.Pass(ctx); err != nil {
			t.Fatal(err)
		}
		return strings.Join(starter.started[before:], ",")
	}

	if got := pass(); got != "" {
		t.Fatalf("started %q before anyone asked", got)
	}
	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{epic.ID}, "ana"); err != nil {
		t.Fatal(err)
	}
	if got := pass(); got != a.Key {
		t.Fatalf("first pass started %q, want %s", got, a.Key)
	}
	if got := pass(); got != "" {
		t.Fatalf("started %q while %s runs", got, a.Key)
	}
	finish(t, s, a)
	if got := pass(); got != b.Key {
		t.Fatalf("after %s merged: started %q, want %s (one slot)", a.Key, got, b.Key)
	}
	finish(t, s, b)
	if got := pass(); got != c.Key {
		t.Fatalf("started %q, want %s", got, c.Key)
	}

	// The run input carries what the run needs.
	in := starter.inputs[0]
	if in.IssueID != a.ID.String() || in.Repo != "o/r" || in.BaseBranch != "main" || in.SourceURL != a.SourceRef || in.Title != a.Title {
		t.Errorf("run input = %+v", in)
	}
}

// With two slots, independent tasks start together.
func TestSchedulerRunsIndependentIssuesInParallel(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	p, _ := s.CreateProject(ctx, "o/r", "or")
	var ids []uuid.UUID
	for range 3 {
		is, err := s.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "t", Status: domain.IssueReady, CreatedBy: "t"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, is.ID)
	}
	if _, err := s.RequestIssueStart(ctx, ids, "ana"); err != nil {
		t.Fatal(err)
	}
	starter := &fakeStarter{s: s}
	sch := &Scheduler{Store: s, Starter: starter, Projects: map[uuid.UUID]Project{p.ID: {Repo: "o/r", BaseBranch: "main"}},
		Limit: 2, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := sch.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(starter.started) != 2 {
		t.Fatalf("started %v, want two of three", starter.started)
	}
}

// A failed start holds no slot and is tried again; an unconfigured project is reported.
func TestSchedulerStartFailures(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	p, _ := s.CreateProject(ctx, "o/r", "or")
	other, _ := s.CreateProject(ctx, "x/y", "xy")
	is, _ := s.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "t", Status: domain.IssueReady, CreatedBy: "t"})
	stray, _ := s.CreateIssue(ctx, store.NewIssue{ProjectID: other.ID, Type: domain.IssueTask, Title: "t", Status: domain.IssueReady, CreatedBy: "t"})
	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{is.ID, stray.ID}, "ana"); err != nil {
		t.Fatal(err)
	}
	starter := &fakeStarter{s: s, fail: map[string]bool{is.Key: true}}
	sch := &Scheduler{Store: s, Starter: starter, Projects: map[uuid.UUID]Project{p.ID: {Repo: "o/r"}},
		Limit: 5, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	err := sch.Pass(ctx)
	if err == nil || !strings.Contains(err.Error(), "temporal unavailable") || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v", err)
	}
	starter.fail = nil
	if err := sch.Pass(ctx); err == nil || !strings.Contains(err.Error(), stray.Key) {
		t.Fatalf("second pass err = %v, want only the unconfigured project", err)
	}
	if len(starter.started) != 1 || starter.started[0] != is.Key {
		t.Fatalf("started %v, want %s on the retry", starter.started, is.Key)
	}
}

// D63: starting an issue that likely overlaps one in flight logs a warning and
// still starts it.
func TestSchedulerWarnsAboutOverlap(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	p, _ := s.CreateProject(ctx, "o/r", "or")
	busy, _ := s.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "t", Status: domain.IssueAwaitingHuman, CreatedBy: "t"})
	if err := s.SetIssueChangedPaths(ctx, busy.ID, []string{"internal/api/issues.go"}); err != nil {
		t.Fatal(err)
	}
	next, _ := s.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "t", Status: domain.IssueReady, CreatedBy: "t",
		FileHints: []string{"internal/api/schedule.go"}})
	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{next.ID}, "ana"); err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	starter := &fakeStarter{s: s}
	sch := &Scheduler{Store: s, Starter: starter, Projects: map[uuid.UUID]Project{p.ID: {Repo: "o/r"}},
		Limit: 1, Log: slog.New(slog.NewTextHandler(&logs, nil))}
	if err := sch.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(starter.started) != 1 {
		t.Fatalf("started %v; a warning does not hold the issue back", starter.started)
	}
	if out := logs.String(); !strings.Contains(out, "likely overlaps") || !strings.Contains(out, "with="+busy.Key) || !strings.Contains(out, "internal/api") {
		t.Errorf("logs = %s", out)
	}
}
