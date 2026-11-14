package intake

import (
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

// D60: labeled issues, their open sub-issues all the way down, and their open
// same-repo blockers; closed and other-repo issues stay out.
func TestCollectTree(t *testing.T) {
	epic := ext(1, "factory", "epic")
	epic.SubIssues = 3
	story := ext(2)
	story.SubIssues = 1
	task := ext(5)
	task.OpenBlockers = 2
	done := ext(3)
	done.Open = false
	blocker := ext(6)
	elsewhere := ext(7)
	elsewhere.Repo = "x/y"
	src := &fakeSource{
		issues: []domain.ExternalIssue{epic},
		subs: map[int][]domain.ExternalIssue{
			1: {story, done, elsewhere},
			2: {task},
		},
		blockedBy: map[int][]domain.ExternalIssue{5: {blocker, elsewhere}},
	}
	tr, err := collectTree(t.Context(), src, "O/R", "factory")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tr.order, []string{epic.Ref, story.Ref, task.Ref, blocker.Ref}) {
		t.Fatalf("order = %v", tr.order)
	}
	n := tr.nodes
	if !n[epic.Ref].labeled || n[story.Ref].labeled {
		t.Error("only the listed issue is labeled")
	}
	if n[story.Ref].parent != epic.Ref || n[task.Ref].parent != story.Ref || n[blocker.Ref].parent != "" {
		t.Errorf("parents = %q %q %q", n[story.Ref].parent, n[task.Ref].parent, n[blocker.Ref].parent)
	}
	if !slices.Equal(n[task.Ref].blockedBy, []string{blocker.Ref}) {
		t.Errorf("blockers = %v", n[task.Ref].blockedBy)
	}
}

func TestCollectTreeIsBounded(t *testing.T) {
	epic := ext(1, "factory", "epic")
	epic.SubIssues = maxTreeIssues
	var subs []domain.ExternalIssue
	for i := range maxTreeIssues {
		subs = append(subs, ext(i+2))
	}
	src := &fakeSource{issues: []domain.ExternalIssue{epic}, subs: map[int][]domain.ExternalIssue{1: subs}}
	if _, err := collectTree(t.Context(), src, "o/r", "factory"); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("err = %v", err)
	}
}

func TestTreeTypes(t *testing.T) {
	epic := ext(1, "factory") // no type label: inferred from the tree
	epic.SubIssues = 1
	story := ext(2)
	story.SubIssues = 1
	task := ext(3, "factory")
	bug := ext(4, "factory", "Bug")
	src := &fakeSource{issues: []domain.ExternalIssue{epic, task, bug},
		subs: map[int][]domain.ExternalIssue{1: {story}, 2: {task}}}
	tr, err := collectTree(t.Context(), src, "o/r", "factory")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		e   domain.ExternalIssue
		typ domain.IssueType
	}{
		{epic, domain.IssueEpic},   // has grandchildren
		{story, domain.IssueStory}, // has children
		{task, domain.IssueTask},   // a leaf
		{bug, domain.IssueBug},     // from its label
	} {
		if typ := tr.issueType(tr.nodes[tt.e.Ref]); typ != tt.typ {
			t.Errorf("#%d = %s, want %s", tt.e.Number, typ, tt.typ)
		}
	}
}
