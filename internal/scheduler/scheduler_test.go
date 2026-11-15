package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
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
	refined []activities.IssueRunInput
}

// StartRefinement creates the refinement's run and moves the issue to refining,
// like the Refine workflow's first Activity.
func (f *fakeStarter) StartRefinement(ctx context.Context, in activities.IssueRunInput) error {
	f.refined = append(f.refined, in)
	id := uuid.MustParse(in.IssueID)
	run, err := f.s.CreateRun(ctx, store.NewRun{IssueID: id, WorkflowSnapshot: json.RawMessage(`{}`), WorkflowVersion: "refine@1",
		TemporalWorkflowID: "refine-" + in.IssueKey})
	if err != nil {
		return err
	}
	if _, err := f.s.TransitionRun(ctx, run.ID, domain.ExecPending, domain.ExecRunning); err != nil {
		return err
	}
	_, err = f.s.TransitionIssue(ctx, id, domain.IssueDraft, domain.IssueRefining)
	return err
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

// M5.1: an issue started through a request on an ancestor logs that ancestor's
// requester and the issue the request was made on.
func TestSchedulerLogsRequesterThroughParent(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	p, _ := s.CreateProject(ctx, "o/r", "or")
	epic, _ := s.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueEpic, Title: "e", Status: domain.IssueReady, CreatedBy: "t"})
	story, _ := s.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueStory, Title: "s", Status: domain.IssueReady, CreatedBy: "t", ParentID: &epic.ID})
	task, _ := s.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "t", Status: domain.IssueReady, CreatedBy: "t", ParentID: &story.ID})
	direct, _ := s.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "d", Status: domain.IssueReady, CreatedBy: "t"})
	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{epic.ID}, "ana"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{direct.ID}, "bo"); err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	sch := &Scheduler{Store: s, Starter: &fakeStarter{s: s}, Projects: map[uuid.UUID]Project{p.ID: {Repo: "o/r"}},
		Limit: 5, Log: slog.New(slog.NewTextHandler(&logs, nil))}
	if err := sch.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	started := map[string]string{}
	for line := range strings.Lines(logs.String()) {
		if strings.Contains(line, `msg="issue started"`) {
			for _, k := range []string{task.Key, direct.Key} {
				if strings.Contains(line, "issue="+k+" ") {
					started[k] = line
				}
			}
		}
	}
	for _, c := range []struct{ key, want, notWant string }{
		{task.Key, "requested_by=ana requested_via=" + epic.Key, ""},
		{direct.Key, "requested_by=bo", "requested_via"},
	} {
		line := started[c.key]
		if !strings.Contains(line, c.want) || (c.notWant != "" && strings.Contains(line, c.notWant)) {
			t.Errorf("%s: log line = %q, want %q", c.key, line, c.want)
		}
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

// D64, D66: a requested draft whose spec needs no approval becomes ready and
// starts in the same pass; one that needs approval is refined first, waits for a
// person while its refined spec is pending, and starts once approved.
func TestSchedulerSpecApproval(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	p, _ := s.CreateProject(ctx, "o/r", "or")
	plain, _ := s.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "t", CreatedBy: "t"})
	gated, _ := s.CreateIssue(ctx, store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "t", CreatedBy: "t",
		Labels: []string{"needs-spec-approval"}})
	if _, err := s.RejectSpec(ctx, gated.ID, "name the endpoint"); err != nil { // an earlier rejection
		t.Fatal(err)
	}
	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{plain.ID, gated.ID}, "ana"); err != nil {
		t.Fatal(err)
	}
	starter := &fakeStarter{s: s}
	sch := &Scheduler{Store: s, Starter: starter, Projects: map[uuid.UUID]Project{p.ID: {Repo: "o/r"}},
		Limit: 5, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	pass := func() {
		t.Helper()
		if err := sch.Pass(ctx); err != nil {
			t.Fatal(err)
		}
	}
	pass()
	if strings.Join(starter.started, ",") != plain.Key {
		t.Fatalf("started %v, want only %s", starter.started, plain.Key)
	}
	if len(starter.refined) != 1 || starter.refined[0].IssueKey != gated.Key || starter.refined[0].SpecFeedback != "name the endpoint" {
		t.Fatalf("refined %+v, want %s with the earlier feedback", starter.refined, gated.Key)
	}

	// While the refiner works, and while its spec waits for a person, nothing more happens.
	pass()
	run, err := s.GetActiveRunForIssue(ctx, gated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRefinement(ctx, gated.ID, run.ID, domain.RefinedSpec{Spec: "GET /search", AcceptanceCriteria: []string{"returns matches"}}); err != nil {
		t.Fatal(err)
	}
	pass()
	if len(starter.refined) != 1 || len(starter.started) != 1 {
		t.Fatalf("refined %d, started %v; want nothing new before approval", len(starter.refined), starter.started)
	}
	if _, err := s.ApproveSpec(ctx, gated.ID, "ana"); err != nil {
		t.Fatal(err)
	}
	pass()
	if len(starter.started) != 2 || starter.started[1] != gated.Key || starter.inputs[1].Spec != "GET /search" {
		t.Fatalf("started %v (spec %q), want %s with its approved spec", starter.started, starter.inputs[len(starter.inputs)-1].Spec, gated.Key)
	}
	// M6 (D92): the run gets the issue's labels, which override project settings.
	if !slices.Contains(starter.inputs[1].Labels, "needs-spec-approval") {
		t.Errorf("labels = %v", starter.inputs[1].Labels)
	}
}
