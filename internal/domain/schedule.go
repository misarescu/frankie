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
	// NeedsApproval is set when a person must approve the issue's spec before it
	// runs (D64); SpecRejected while a rejection holds the draft.
	NeedsApproval bool
	SpecRejected  bool
	// HasProposal is set while a refining issue's refined spec waits for approval (D66).
	HasProposal bool
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
	// MakeReady are requested drafts whose spec needs no approval: they become
	// ready now (D64), and may be in Start too.
	MakeReady []SchedIssue
	// Refine are requested drafts whose spec needs approval: the refiner runs
	// first (D66). Each takes a slot, like a start.
	Refine []SchedIssue
	// AwaitingApproval are refining issues whose refined spec waits for a person (D66).
	AwaitingApproval []SchedIssue
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

// PlanSchedule decides which requested issues start now (D62), and which
// requested drafts the refiner works on first (D66). An issue starts
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

	type candidate struct {
		SchedIssue
		refine bool
	}
	var candidates []candidate
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
		case is.Status == IssueDraft && !is.NeedsApproval:
			plan.MakeReady = append(plan.MakeReady, is) // and plan it as ready below
		case is.Status == IssueDraft && is.SpecRejected:
			reason = "spec rejected; edit the issue or start it again"
		case is.Status == IssueDraft:
			// The refiner needs no dependency done: it writes a spec, not code.
			candidates = append(candidates, candidate{SchedIssue: is, refine: true})
			continue
		case is.Status == IssueRefining && is.HasProposal:
			plan.AwaitingApproval = append(plan.AwaitingApproval, is)
			reason = "spec waits for approval"
		case is.Status == IssueRefining:
			reason = "the refiner is writing its spec"
		case is.Status != IssueReady:
			reason = fmt.Sprintf("%s; starts once its spec is approved", is.Status)
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
		candidates = append(candidates, candidate{SchedIssue: is})
	}
	slices.SortFunc(candidates, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(b.Priority, a.Priority), cmp.Compare(a.Number, b.Number))
	})
	for _, c := range candidates {
		if busy >= limit {
			plan.Waiting = append(plan.Waiting, Waiting{Key: c.Key, Reason: fmt.Sprintf("no free slot (%d of %d busy)", busy, limit)})
			continue
		}
		if c.refine {
			plan.Refine = append(plan.Refine, c.SchedIssue)
		} else {
			plan.Start = append(plan.Start, c.SchedIssue)
		}
		busy++
	}
	return plan
}
