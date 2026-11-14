package domain

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// SchedIssue is an issue as the scheduler sees it (D62).
type SchedIssue struct {
	ID       uuid.UUID
	Key      string
	Type     IssueType
	ParentID *uuid.UUID
	Status   IssueStatus
	Priority int
	Number   int
	// Requested is set when someone asked to start this issue; a request on a
	// parent covers everything under it.
	Requested bool
	// Busy is set while the issue holds a slot: it has an active run, or the
	// scheduler started it moments ago and its run does not exist yet.
	Busy bool
}

// Waiting says why a requested issue does not start now.
type Waiting struct {
	Key    string
	Reason string
}

// SchedulePlan is what the scheduler does in one pass.
type SchedulePlan struct {
	Start   []SchedIssue // in order, at most the free slots
	Waiting []Waiting    // requested runnable issues that do not start, with why
}

// Runnable reports whether an issue runs a workflow itself: a leaf that is not an
// epic. Parents only group their children (D62).
func Runnable(is SchedIssue, hasChildren bool) bool {
	return !hasChildren && is.Type != IssueEpic
}

// issueDone reports whether an issue is finished for its dependents (D62): a leaf
// once merged or released, a parent once all its children that were not canceled
// are done.
func issueDone(id uuid.UUID, byID map[uuid.UUID]SchedIssue, children map[uuid.UUID][]uuid.UUID, seen map[uuid.UUID]bool) bool {
	if seen[id] {
		return false // a hierarchy loop; CheckIssueGraph keeps the store free of them
	}
	seen[id] = true
	defer delete(seen, id)
	kids := children[id]
	if len(kids) == 0 {
		s := byID[id].Status
		return s == IssueMerged || s == IssueReleased
	}
	for _, k := range kids {
		if byID[k].Status != IssueCanceled && !issueDone(k, byID, children, seen) {
			return false
		}
	}
	return true
}

// PlanSchedule decides which requested issues start now (D62). An issue starts
// when it is runnable, requested (itself or through an ancestor), ready, not busy,
// and every dependency of it and of its ancestors is done, while fewer than limit
// issues are busy. Candidates go by priority, highest first, then by number.
func PlanSchedule(issues []SchedIssue, deps []Dependency, limit int) SchedulePlan {
	byID := make(map[uuid.UUID]SchedIssue, len(issues))
	children := make(map[uuid.UUID][]uuid.UUID)
	busy := 0
	for _, is := range issues {
		byID[is.ID] = is
		if is.ParentID != nil {
			children[*is.ParentID] = append(children[*is.ParentID], is.ID)
		}
		if is.Busy {
			busy++
		}
	}
	depsOf := make(map[uuid.UUID][]uuid.UUID)
	for _, d := range deps {
		depsOf[d.IssueID] = append(depsOf[d.IssueID], d.DependsOnID)
	}
	ancestors := func(is SchedIssue) []SchedIssue {
		var out []SchedIssue
		seen := map[uuid.UUID]bool{is.ID: true}
		for p := is.ParentID; p != nil && !seen[*p]; {
			seen[*p] = true
			parent, ok := byID[*p]
			if !ok {
				break
			}
			out = append(out, parent)
			p = parent.ParentID
		}
		return out
	}

	var candidates []SchedIssue
	var plan SchedulePlan
	for _, is := range issues {
		if !Runnable(is, len(children[is.ID]) > 0) {
			continue
		}
		line := append([]SchedIssue{is}, ancestors(is)...)
		if !slices.ContainsFunc(line, func(x SchedIssue) bool { return x.Requested }) {
			continue
		}
		reason := ""
		switch {
		case is.Busy || is.Status == IssueRunning || is.Status == IssueAwaitingHuman || is.Status == IssueQueued:
			continue // already under way
		case is.Status == IssueMerged || is.Status == IssueReleased || is.Status == IssueCanceled:
			continue // finished
		case is.Status == IssueFailed:
			reason = "failed; retry it to run again"
		case is.Status != IssueReady:
			reason = fmt.Sprintf("%s; starts once ready", is.Status)
		}
		if reason == "" {
		deps:
			for _, x := range line {
				for _, d := range depsOf[x.ID] {
					if !issueDone(d, byID, children, map[uuid.UUID]bool{}) {
						reason = "waits for " + byID[d].Key
						break deps
					}
				}
			}
		}
		if reason != "" {
			plan.Waiting = append(plan.Waiting, Waiting{Key: is.Key, Reason: reason})
			continue
		}
		candidates = append(candidates, is)
	}
	slices.SortFunc(candidates, func(a, b SchedIssue) int {
		return cmp.Or(cmp.Compare(b.Priority, a.Priority), cmp.Compare(a.Number, b.Number))
	})
	for _, is := range candidates {
		if busy >= limit {
			plan.Waiting = append(plan.Waiting, Waiting{Key: is.Key, Reason: fmt.Sprintf("no free slot (%d of %d busy)", busy, limit)})
			continue
		}
		plan.Start = append(plan.Start, is)
		busy++
	}
	return plan
}
