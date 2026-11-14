package intake

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/store/storetest"
)

// fakeSource serves labeled issues, sub-issues and blockers by issue number.
type fakeSource struct {
	issues    []domain.ExternalIssue
	subs      map[int][]domain.ExternalIssue
	blockedBy map[int][]domain.ExternalIssue
	err       error
	relErr    error
}

func (f *fakeSource) ListLabeledIssues(context.Context, string, string) ([]domain.ExternalIssue, error) {
	return f.issues, f.err
}

func (f *fakeSource) SubIssues(_ context.Context, _ string, n int) ([]domain.ExternalIssue, error) {
	return f.subs[n], f.relErr
}

func (f *fakeSource) BlockedBy(_ context.Context, _ string, n int) ([]domain.ExternalIssue, error) {
	return f.blockedBy[n], f.relErr
}

func TestPollTakesInLabeledIssuesOnce(t *testing.T) {
	s := storetest.New(t)
	p, err := s.CreateProject(t.Context(), "misarescu/Goodreads-Clone", "gc")
	if err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{issues: []domain.ExternalIssue{{Number: 3, Title: "Add search", Body: "Users can search.", Open: true,
		Ref: "https://github.com/misarescu/Goodreads-Clone/issues/3", Repo: "misarescu/Goodreads-Clone", Author: "mi", Labels: []string{"factory"}}}}
	in := &Intake{Store: s, Source: src, Label: "factory", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Projects: []Project{{ID: p.ID, Repo: "misarescu/Goodreads-Clone", BaseBranch: "main"}}}

	if err := in.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	is, err := s.GetIssueBySourceRef(t.Context(), p.ID, src.issues[0].Ref)
	if err != nil || is.Key != "gc-1" || is.Status != domain.IssueReady || is.CreatedBy != "github:mi" || is.Spec != "Users can search." {
		t.Fatalf("issue = %+v, %v", is, err)
	}
	// D62: intake starts nothing; the scheduler starts what someone asked for.
	if is.StartRequestedAt != nil {
		t.Errorf("intake asked to start %s", is.Key)
	}

	// A second poll never creates a second issue.
	if err := in.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if next, _ := s.CreateIssue(t.Context(), store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "x", CreatedBy: "t"}); next.Key != "gc-2" {
		t.Errorf("a duplicate issue was created: next key %s", next.Key)
	}
}

func TestPollReportsSourceErrors(t *testing.T) {
	s := storetest.New(t)
	p, _ := s.CreateProject(t.Context(), "o/r", "or")
	in := &Intake{Store: s, Source: &fakeSource{err: errors.New("401 bad credentials")},
		Label: "factory", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Projects: []Project{{ID: p.ID, Repo: "o/r"}}}
	if err := in.Poll(t.Context()); err == nil || !strings.Contains(err.Error(), "bad credentials") {
		t.Errorf("err = %v", err)
	}
}

func ext(n int, labels ...string) domain.ExternalIssue {
	return domain.ExternalIssue{Ref: "https://github.com/o/r/issues/" + strconv.Itoa(n), Repo: "o/r", Number: n,
		Title: "issue " + strconv.Itoa(n), Author: "mi", Labels: labels, Open: true}
}

// D60: a labeled epic brings its sub-issues and their blockers, all ready (D62)
// and none started.
func TestPollSyncsTree(t *testing.T) {
	s := storetest.New(t)
	p, err := s.CreateProject(t.Context(), "o/r", "or")
	if err != nil {
		t.Fatal(err)
	}
	epic := ext(1, "factory", "epic")
	epic.SubIssues = 2
	api, ui := ext(2), ext(3)
	ui.OpenBlockers = 1
	solo := ext(4, "factory")
	src := &fakeSource{issues: []domain.ExternalIssue{epic, solo},
		subs: map[int][]domain.ExternalIssue{1: {api, ui}}, blockedBy: map[int][]domain.ExternalIssue{3: {api}}}
	in := &Intake{Store: s, Source: src, Label: "factory", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Projects: []Project{{ID: p.ID, Repo: "o/r", BaseBranch: "main"}}}
	if err := in.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}

	get := func(e domain.ExternalIssue) domain.Issue {
		is, err := s.GetIssueBySourceRef(t.Context(), p.ID, e.Ref)
		if err != nil {
			t.Fatalf("issue #%d not synced: %v", e.Number, err)
		}
		return is
	}
	e, a, u, o := get(epic), get(api), get(ui), get(solo)
	if e.Type != domain.IssueEpic {
		t.Errorf("epic type = %s", e.Type)
	}
	if a.ParentID == nil || *a.ParentID != e.ID || u.ParentID == nil || *u.ParentID != e.ID {
		t.Errorf("sub-issue parents = %v, %v; want the epic", a.ParentID, u.ParentID)
	}
	for _, is := range []domain.Issue{e, a, u, o} {
		if is.Status != domain.IssueReady || is.StartRequestedAt != nil {
			t.Errorf("%s = %s, requested %v; want ready and not requested", is.Key, is.Status, is.StartRequestedAt)
		}
	}
	deps, err := s.ListIssueDependencies(t.Context(), u.ID)
	if err != nil || len(deps) != 1 || deps[0].ID != a.ID {
		t.Errorf("ui deps = %v, %v; want api", deps, err)
	}
}

// A failed relation listing would make the source look like it dropped links,
// so the project's sync stops before it changes anything.
func TestPollStopsOnRelationErrors(t *testing.T) {
	s := storetest.New(t)
	p, _ := s.CreateProject(t.Context(), "o/r", "or")
	epic := ext(1, "factory", "epic")
	epic.SubIssues = 1
	in := &Intake{Store: s, Source: &fakeSource{issues: []domain.ExternalIssue{epic}, relErr: errors.New("502")},
		Label: "factory", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Projects: []Project{{ID: p.ID, Repo: "o/r"}}}
	if err := in.Poll(t.Context()); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.GetIssueBySourceRef(t.Context(), p.ID, epic.Ref); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("epic synced despite the error: %v", err)
	}
}
