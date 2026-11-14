package scheduler

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// Bounds on what a refinement's input carries about dependencies.
const (
	maxDependencies   = 10
	maxDependencySpec = 3000 // bytes of each dependency's spec
)

// dependencies collects what refinement needs to know about the issues this one
// waits for (M5.1): its own dependencies and its ancestors', since the
// scheduler holds an issue for both (D62), each once, nearest first. The run's
// input snapshots them (invariant 4).
func (s *Scheduler) dependencies(ctx context.Context, is domain.Issue) ([]activities.IssueDependency, error) {
	var out []activities.IssueDependency
	seen := map[uuid.UUID]bool{is.ID: true}
	for cur := is; ; {
		deps, err := s.Store.ListIssueDependencies(ctx, cur.ID)
		if err != nil {
			return nil, fmt.Errorf("dependencies of %s: %w", cur.Key, err)
		}
		for _, d := range deps {
			if seen[d.ID] || len(out) == maxDependencies {
				continue
			}
			seen[d.ID] = true
			dep := activities.IssueDependency{Key: d.Key, Title: d.Title, Status: string(d.Status), Spec: clip(d.Spec, maxDependencySpec)}
			crs, err := s.Store.ListChangeRequestsForIssue(ctx, d.ID)
			if err != nil {
				return nil, fmt.Errorf("change requests of %s: %w", d.Key, err)
			}
			for _, cr := range crs {
				dep.ChangeRequests = append(dep.ChangeRequests, activities.DependencyChange{URL: cr.ProviderRef, Branch: cr.Branch, Status: cr.Status})
			}
			out = append(out, dep)
		}
		if cur.ParentID == nil || seen[*cur.ParentID] {
			return out, nil
		}
		seen[*cur.ParentID] = true
		if cur, err = s.Store.GetIssue(ctx, *cur.ParentID); err != nil {
			return nil, fmt.Errorf("parent of %s: %w", is.Key, err)
		}
	}
}

// clip cuts text to at most n bytes on a rune boundary, saying it did.
func clip(text string, n int) string {
	if len(text) <= n {
		return text
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "\n[cut]"
}
