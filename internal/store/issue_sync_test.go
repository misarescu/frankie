package store

import (
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

const ghIssues = "https://github.com/o/r/issues/"

// src is a SourceIssue for GitHub issue n; parent and blockers are issue numbers.
func src(n string, typ domain.IssueType, parent string, blockedBy ...string) SourceIssue {
	in := SourceIssue{Ref: ghIssues + n, Type: typ, Title: "issue " + n, Spec: "body " + n, Labels: []string{"factory"},
		CreatedBy: "github:mi", Status: domain.IssueDraft}
	if parent != "" {
		in.ParentRef = ghIssues + parent
	}
	for _, b := range blockedBy {
		in.BlockedBy = append(in.BlockedBy, ghIssues+b)
	}
	return in
}

func mustSync(t *testing.T, s *Store, p domain.Project, tree ...SourceIssue) SyncResult {
	t.Helper()
	res, err := s.SyncIssues(t.Context(), p.ID, tree)
	if err != nil {
		t.Fatalf("SyncIssues: %v", err)
	}
	return res
}

func parentKey(t *testing.T, s *Store, res SyncResult, n string) string {
	t.Helper()
	is, err := s.GetIssue(t.Context(), res.Issues[ghIssues+n].ID)
	if err != nil {
		t.Fatal(err)
	}
	if is.ParentID == nil {
		return ""
	}
	parent, err := s.GetIssue(t.Context(), *is.ParentID)
	if err != nil {
		t.Fatal(err)
	}
	return parent.Key
}

func depKeys(t *testing.T, s *Store, res SyncResult, n string) string {
	t.Helper()
	deps, err := s.ListIssueDependencies(t.Context(), res.Issues[ghIssues+n].ID)
	if err != nil {
		t.Fatal(err)
	}
	return keys(deps)
}

// D60: the tree arrives as issues with parents and dependencies, and a second
// sync of the same tree changes nothing.
func TestSyncIssuesCreatesTree(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	leaf := src("4", domain.IssueTask, "2")
	leaf.Status = domain.IssueReady
	tree := []SourceIssue{
		src("1", domain.IssueEpic, ""),
		src("2", domain.IssueStory, "1"),
		leaf,
		src("5", domain.IssueTask, "2", "4"),
	}
	res := mustSync(t, s, p, tree...)
	if len(res.Created) != 4 || len(res.Warnings) != 0 {
		t.Fatalf("created %v, warnings %v", res.Created, res.Warnings)
	}
	epic, story, t4, t5 := res.Issues[ghIssues+"1"], res.Issues[ghIssues+"2"], res.Issues[ghIssues+"4"], res.Issues[ghIssues+"5"]
	if got := parentKey(t, s, res, "2"); got != epic.Key {
		t.Errorf("story parent = %q, want %s", got, epic.Key)
	}
	if got := parentKey(t, s, res, "5"); got != story.Key {
		t.Errorf("task parent = %q, want %s", got, story.Key)
	}
	if got := depKeys(t, s, res, "5"); got != t4.Key {
		t.Errorf("deps of #5 = %q, want %s", got, t4.Key)
	}
	if t4.Status != domain.IssueReady || t5.Status != domain.IssueDraft || t5.SourceRef != ghIssues+"5" || t5.CreatedBy != "github:mi" {
		t.Errorf("t4 = %+v, t5 = %+v", t4, t5)
	}

	again := mustSync(t, s, p, tree...)
	if len(again.Created) != 0 || len(again.Warnings) != 0 || again.Issues[ghIssues+"5"].UpdatedAt != res.Issues[ghIssues+"5"].UpdatedAt {
		t.Errorf("second sync changed something: created %v, warnings %v", again.Created, again.Warnings)
	}
}

// Drafts follow the source; once past draft the factory's copy is kept. Parents
// and dependencies always follow the source.
func TestSyncIssuesFollowsSource(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	ready := src("3", domain.IssueTask, "")
	ready.Status = domain.IssueReady
	res := mustSync(t, s, p, src("1", domain.IssueEpic, ""), src("2", domain.IssueTask, "1", "3"), ready)

	draft := src("2", domain.IssueBug, "", "")
	draft.Title, draft.Spec, draft.Labels, draft.BlockedBy = "renamed", "new body", []string{"factory", "bug"}, nil
	moved := src("3", domain.IssueTask, "1")
	moved.Title = "renamed too"
	res2 := mustSync(t, s, p, src("1", domain.IssueEpic, ""), draft, moved)

	d := res2.Issues[ghIssues+"2"]
	if d.Title != "renamed" || d.Spec != "new body" || d.Type != domain.IssueBug || !strings.Contains(strings.Join(d.Labels, ","), "bug") {
		t.Errorf("draft not refreshed: %+v", d)
	}
	if r := res2.Issues[ghIssues+"3"]; r.Title != "issue 3" {
		t.Errorf("ready issue took the source title %q", r.Title)
	}
	if got := parentKey(t, s, res2, "2"); got != "" {
		t.Errorf("#2 parent = %q after the source dropped it", got)
	}
	if got := parentKey(t, s, res2, "3"); got != res.Issues[ghIssues+"1"].Key {
		t.Errorf("#3 parent = %q, want the epic", got)
	}
	if got := depKeys(t, s, res2, "2"); got != "" {
		t.Errorf("#2 deps = %q after the source dropped them", got)
	}
}

func TestSyncIssuesSkipsBadLinks(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	res := mustSync(t, s, p,
		src("1", domain.IssueEpic, ""),
		src("2", domain.IssueTask, "1", "3"),
		src("3", domain.IssueTask, "1", "2"),      // closes a cycle with #2
		src("4", domain.IssueTask, "2"),           // a task cannot sit under a task
		src("5", domain.IssueTask, "1", "1"),      // waits for its own epic
		src("6", domain.IssueTask, "", "99", "6"), // outside the tree, and itself: ignored
	)
	if len(res.Warnings) != 3 {
		t.Fatalf("warnings = %q, want 3", res.Warnings)
	}
	if got := depKeys(t, s, res, "2"); got != res.Issues[ghIssues+"3"].Key {
		t.Errorf("#2 deps = %q, want the first edge of the cycle", got)
	}
	if got := depKeys(t, s, res, "3"); got != "" {
		t.Errorf("#3 deps = %q, want the closing edge skipped", got)
	}
	if got := parentKey(t, s, res, "4"); got != "" {
		t.Errorf("#4 parent = %q, want none", got)
	}
	if got := depKeys(t, s, res, "5") + depKeys(t, s, res, "6"); got != "" {
		t.Errorf("deps of #5, #6 = %q, want none", got)
	}
}

// A draft keeps its type when its children would not fit the source's new type.
func TestSyncIssuesKeepsTypeThatChildrenNeed(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	mustSync(t, s, p, src("1", domain.IssueEpic, ""), src("2", domain.IssueStory, "1"))
	res := mustSync(t, s, p, src("1", domain.IssueStory, ""), src("2", domain.IssueStory, "1"))
	if got := res.Issues[ghIssues+"1"].Type; got != domain.IssueEpic {
		t.Errorf("#1 type = %s, want epic kept", got)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "stays a epic") {
		t.Errorf("warnings = %q", res.Warnings)
	}
}

// Edges between issues the source did not report are not the source's to change.
func TestSyncIssuesLeavesOtherIssuesAlone(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	a := mustTyped(t, s, p.ID, domain.IssueTask, nil)
	b := mustTyped(t, s, p.ID, domain.IssueTask, nil)
	if err := s.AddIssueDependency(t.Context(), b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	mustSync(t, s, p, src("1", domain.IssueTask, ""))
	deps, err := s.ListIssueDependencies(t.Context(), b.ID)
	if err != nil || len(deps) != 1 {
		t.Fatalf("native edge = %v, %v; want kept", deps, err)
	}
}
