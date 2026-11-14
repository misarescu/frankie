package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Hierarchy rules from D57: epic > story > task, bugs under an epic or a story.
func TestValidParent(t *testing.T) {
	tests := []struct {
		child, parent IssueType
		want          bool
	}{
		{IssueStory, IssueEpic, true},
		{IssueTask, IssueEpic, true},
		{IssueTask, IssueStory, true},
		{IssueBug, IssueEpic, true},
		{IssueBug, IssueStory, true},
		{IssueEpic, IssueEpic, false},
		{IssueStory, IssueStory, false},
		{IssueStory, IssueTask, false},
		{IssueTask, IssueTask, false},
		{IssueTask, IssueBug, false},
		{IssueBug, IssueTask, false},
		{"chore", IssueEpic, false},
	}
	for _, tt := range tests {
		if got := ValidParent(tt.child, tt.parent); got != tt.want {
			t.Errorf("ValidParent(%s, %s) = %v, want %v", tt.child, tt.parent, got, tt.want)
		}
	}
}

func TestCheckIssueGraph(t *testing.T) {
	// epic > story > {t1, t2}; epic > t3; other is a root task.
	epic, story, t1, t2, t3, other := node("e"), node("s"), node("t1"), node("t2"), node("t3"), node("o")
	story.ParentID = &epic.ID
	t1.ParentID, t2.ParentID = &story.ID, &story.ID
	t3.ParentID = &epic.ID
	nodes := []IssueNode{epic, story, t1, t2, t3, other}
	dep := func(a, b IssueNode) Dependency { return Dependency{IssueID: a.ID, DependsOnID: b.ID} }

	tests := []struct {
		name  string
		deps  []Dependency
		cycle string // empty when the graph is valid
	}{
		{name: "no dependencies"},
		{name: "chain between siblings", deps: []Dependency{dep(t2, t1), dep(t3, t2)}},
		{name: "diamond", deps: []Dependency{dep(t2, t1), dep(t3, t1), dep(other, t2), dep(other, t3)}},
		{name: "epic waits for an outside task", deps: []Dependency{dep(epic, other)}},
		{name: "outside task waits for the epic", deps: []Dependency{dep(other, epic)}},
		{name: "two-issue cycle", deps: []Dependency{dep(t1, t2), dep(t2, t1)}, cycle: "t1 -> t2 -> t1"},
		{name: "three-issue cycle", deps: []Dependency{dep(t1, t2), dep(t2, t3), dep(t3, t1)}, cycle: "t1 -> t2 -> t3 -> t1"},
		// A task waiting for its own epic waits for itself: the epic finishes after the task.
		{name: "task depends on its ancestor", deps: []Dependency{dep(t1, epic)}, cycle: "t1"},
		// The task inherits the epic's dependency on itself.
		{name: "epic depends on its descendant", deps: []Dependency{dep(epic, t1)}, cycle: "t1"},
		// t1 inherits story -> t3, and the story finishes after t1, so t3 -> story closes the loop.
		{name: "cycle through an inherited dependency", deps: []Dependency{dep(story, t3), dep(t3, t1)}, cycle: "t3"},
		{name: "outside cycle through the epic", deps: []Dependency{dep(other, epic), dep(t1, other)}, cycle: "o"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckIssueGraph(nodes, tt.deps)
			if tt.cycle == "" {
				if err != nil {
					t.Fatalf("CheckIssueGraph = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrDependencyCycle) {
				t.Fatalf("CheckIssueGraph = %v, want ErrDependencyCycle", err)
			}
			if !strings.Contains(err.Error(), tt.cycle) {
				t.Errorf("error %q does not name %q", err, tt.cycle)
			}
		})
	}
}

func TestCheckIssueGraphParentLoop(t *testing.T) {
	a, b := node("a"), node("b")
	a.ParentID, b.ParentID = &b.ID, &a.ID
	if err := CheckIssueGraph([]IssueNode{a, b}, nil); !errors.Is(err, ErrInvalidParent) {
		t.Fatalf("CheckIssueGraph = %v, want ErrInvalidParent", err)
	}
}

func node(key string) IssueNode { return IssueNode{ID: uuid.New(), Key: key} }
