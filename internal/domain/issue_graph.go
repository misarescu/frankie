package domain

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ErrInvalidParent is returned when an issue cannot sit under the parent it was given.
var ErrInvalidParent = errors.New("invalid parent")

// ErrInvalidDependency is returned for a dependency on itself or on another project's issue.
var ErrInvalidDependency = errors.New("invalid dependency")

// ErrDependencyCycle is returned when a dependency would make an issue wait for itself.
var ErrDependencyCycle = errors.New("dependency cycle")

// ValidParent reports whether an issue of type child may have a parent of type parent
// (D57): an epic is a root, a story sits under an epic, and a task or bug sits under an
// epic or a story. Every type except epic may also have no parent.
func ValidParent(child, parent IssueType) bool {
	switch child {
	case IssueStory:
		return parent == IssueEpic
	case IssueTask, IssueBug:
		return parent == IssueEpic || parent == IssueStory
	}
	return false
}

// IssueNode is an issue's place in its project's hierarchy.
type IssueNode struct {
	ID       uuid.UUID
	Key      string
	ParentID *uuid.UUID
}

// Dependency says that IssueID waits for DependsOnID.
type Dependency struct {
	IssueID     uuid.UUID
	DependsOnID uuid.UUID
}

// CheckIssueGraph reports a cycle in a project's issues, counting the hierarchy as well
// as the dependencies (D57). An issue waits for its own dependencies and for those of
// every ancestor, and a parent is finished only once all its children are. So a task
// that depends on its own epic, or an epic that depends on one of its own tasks, waits
// for itself just like a plain A -> B -> A cycle does. The error wraps
// ErrDependencyCycle and names the issues on the cycle.
func CheckIssueGraph(nodes []IssueNode, deps []Dependency) error {
	byID := make(map[uuid.UUID]IssueNode, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	direct := make(map[uuid.UUID][]uuid.UUID)
	for _, d := range deps {
		direct[d.IssueID] = append(direct[d.IssueID], d.DependsOnID)
	}
	// waits[x] lists what x cannot finish before: its children, its own dependencies,
	// and its ancestors' dependencies.
	waits := make(map[uuid.UUID][]uuid.UUID, len(nodes))
	for _, n := range nodes {
		if n.ParentID != nil {
			waits[*n.ParentID] = append(waits[*n.ParentID], n.ID)
		}
		seen := map[uuid.UUID]bool{n.ID: true}
		for cur, ok := n, true; ok; cur, ok = parentOf(byID, cur) {
			waits[n.ID] = append(waits[n.ID], direct[cur.ID]...)
			if cur.ParentID != nil && seen[*cur.ParentID] {
				return fmt.Errorf("%w: parent chain of %s loops", ErrInvalidParent, label(byID, n.ID))
			}
			if cur.ParentID != nil {
				seen[*cur.ParentID] = true
			}
		}
	}

	const (
		unvisited = iota
		onPath
		done
	)
	state := make(map[uuid.UUID]int, len(nodes))
	var path []uuid.UUID
	var visit func(id uuid.UUID) []uuid.UUID
	visit = func(id uuid.UUID) []uuid.UUID {
		state[id] = onPath
		path = append(path, id)
		for _, next := range waits[id] {
			switch state[next] {
			case onPath:
				for i, p := range path {
					if p == next {
						return append(append([]uuid.UUID{}, path[i:]...), next)
					}
				}
			case unvisited:
				if cycle := visit(next); cycle != nil {
					return cycle
				}
			}
		}
		path = path[:len(path)-1]
		state[id] = done
		return nil
	}
	for _, n := range nodes {
		if state[n.ID] != unvisited {
			continue
		}
		if cycle := visit(n.ID); cycle != nil {
			names := make([]string, len(cycle))
			for i, id := range cycle {
				names[i] = label(byID, id)
			}
			return fmt.Errorf("%w: %s", ErrDependencyCycle, strings.Join(names, " -> "))
		}
	}
	return nil
}

func parentOf(byID map[uuid.UUID]IssueNode, n IssueNode) (IssueNode, bool) {
	if n.ParentID == nil {
		return IssueNode{}, false
	}
	p, ok := byID[*n.ParentID]
	return p, ok
}

func label(byID map[uuid.UUID]IssueNode, id uuid.UUID) string {
	if n, ok := byID[id]; ok && n.Key != "" {
		return n.Key
	}
	return id.String()
}
