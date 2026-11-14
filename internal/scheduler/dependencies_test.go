package scheduler

import (
	"encoding/json"
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

// M5.1: a refinement's input carries the issues it waits for, its own and its
// ancestors', with their status, change requests and spec, so the refiner does
// not write a spec that calls the issue blocked by work in flight.
func TestRefinementGetsDependencies(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	p, _ := s.CreateProject(ctx, "o/r", "or")
	mk := func(typ domain.IssueType, title, spec string, parent *domain.Issue, labels ...string) domain.Issue {
		t.Helper()
		in := store.NewIssue{ProjectID: p.ID, Type: typ, Title: title, Spec: spec, CreatedBy: "t", Labels: labels}
		if parent != nil {
			in.ParentID = &parent.ID
		}
		is, err := s.CreateIssue(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return is
	}
	api := mk(domain.IssueTask, "Add the count endpoint", "GET /books/count returns {count}.", nil)
	layout := mk(domain.IssueTask, "Base layout", strings.Repeat("é", maxDependencySpec), nil) // two bytes a rune
	epic := mk(domain.IssueEpic, "Counts", "", nil)
	docs := mk(domain.IssueTask, "Document the count", "Write docs.", &epic, "needs-spec-approval")
	for _, e := range [][2]domain.Issue{{docs, api}, {epic, layout}, {epic, api}} { // api twice: listed once
		if err := s.AddIssueDependency(ctx, e[0].ID, e[1].ID); err != nil {
			t.Fatal(err)
		}
	}
	// api's change request waits for a person.
	run, err := s.CreateRun(ctx, store.NewRun{IssueID: api.ID, WorkflowSnapshot: json.RawMessage(`{}`), WorkflowVersion: "1", TemporalWorkflowID: "issue-" + api.Key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertChangeRequest(ctx, store.NewChangeRequest{IssueID: api.ID, RunID: run.ID, Repo: "o/r", Number: 26,
		ProviderRef: "https://github.com/o/r/pull/26", Branch: "agent/" + api.Key + "/add-count", HeadSHA: "abc"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RequestIssueStart(ctx, []uuid.UUID{epic.ID}, "ana"); err != nil {
		t.Fatal(err)
	}
	starter := &fakeStarter{s: s}
	sch := &Scheduler{Store: s, Starter: starter, Projects: map[uuid.UUID]Project{p.ID: {Repo: "o/r"}},
		Limit: 5, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := sch.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(starter.refined) != 1 || starter.refined[0].IssueKey != docs.Key {
		t.Fatalf("refined %+v, want %s", starter.refined, docs.Key)
	}
	got := starter.refined[0].Dependencies
	if len(got) != 2 || got[0].Key != api.Key || got[1].Key != layout.Key {
		t.Fatalf("dependencies = %+v; want %s (its own) then %s (the epic's), each once", got, api.Key, layout.Key)
	}
	want := activities.IssueDependency{Key: api.Key, Title: api.Title, Status: "draft", Spec: api.Spec,
		ChangeRequests: []activities.DependencyChange{{URL: "https://github.com/o/r/pull/26", Branch: "agent/" + api.Key + "/add-count", Status: "open"}}}
	if a := got[0]; a.Title != want.Title || a.Status != want.Status || a.Spec != want.Spec || len(a.ChangeRequests) != 1 || a.ChangeRequests[0] != want.ChangeRequests[0] {
		t.Errorf("api dependency = %+v, want %+v", a, want)
	}
	if spec := got[1].Spec; len(spec) > maxDependencySpec+len("\n[cut]") || !strings.HasSuffix(spec, "é\n[cut]") {
		t.Errorf("long spec = %d bytes ending %q; want it cut on a rune boundary", len(spec), spec[len(spec)-10:])
	}
	// Build runs get no dependencies: they start once those are merged.
	for _, in := range starter.inputs {
		if len(in.Dependencies) != 0 {
			t.Errorf("run of %s carries dependencies %+v", in.IssueKey, in.Dependencies)
		}
	}
}
