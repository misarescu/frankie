package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// schedFixture: epic e (requested) > story s > {a, b}, and e > c.
// b waits for a; c waits for a. Another root task o is not requested.
type schedFixture struct {
	byKey map[string]*SchedIssue
	order []string
	deps  []Dependency
}

func newSchedFixture() *schedFixture {
	f := &schedFixture{byKey: map[string]*SchedIssue{}}
	add := func(key string, typ IssueType, parent string) {
		is := &SchedIssue{ID: uuid.New(), Key: key, Type: typ, Status: IssueReady, Number: len(f.order) + 1}
		if parent != "" {
			is.ParentID = &f.byKey[parent].ID
		}
		f.byKey[key] = is
		f.order = append(f.order, key)
	}
	add("e", IssueEpic, "")
	add("s", IssueStory, "e")
	add("a", IssueTask, "s")
	add("b", IssueTask, "s")
	add("c", IssueTask, "e")
	add("o", IssueTask, "")
	f.byKey["e"].Requested = true
	f.dep("b", "a")
	f.dep("c", "a")
	return f
}

func (f *schedFixture) dep(from, to string) {
	f.deps = append(f.deps, Dependency{IssueID: f.byKey[from].ID, DependsOnID: f.byKey[to].ID})
}

func (f *schedFixture) plan(limit int) SchedulePlan {
	issues := make([]SchedIssue, len(f.order))
	for i, k := range f.order {
		issues[i] = *f.byKey[k]
	}
	return PlanSchedule(issues, f.deps, limit)
}

func startKeys(p SchedulePlan) string {
	var keys []string
	for _, is := range p.Start {
		keys = append(keys, is.Key)
	}
	return strings.Join(keys, ",")
}

func waitReason(p SchedulePlan, key string) string {
	for _, w := range p.Waiting {
		if w.Key == key {
			return w.Reason
		}
	}
	return ""
}

// M5 done-when: starting an epic runs its tasks in dependency order, and in
// parallel where independent, within the concurrency limit.
func TestPlanScheduleRunsAnEpicInDependencyOrder(t *testing.T) {
	f := newSchedFixture()

	// Only a can start: b and c wait for it; the epic and story never run; o was not asked for.
	p := f.plan(2)
	if got := startKeys(p); got != "a" {
		t.Fatalf("start = %q, want a", got)
	}
	if r := waitReason(p, "b"); r != "waits for a" {
		t.Errorf("b waits because %q", r)
	}
	if waitReason(p, "o") != "" || waitReason(p, "e") != "" || waitReason(p, "s") != "" {
		t.Errorf("waiting = %+v; unrequested issues and parents are not listed", p.Waiting)
	}

	// a runs, then waits for a person to merge: b and c still wait.
	f.byKey["a"].Status, f.byKey["a"].Busy = IssueRunning, true
	if got := startKeys(f.plan(2)); got != "" {
		t.Fatalf("start while a runs = %q", got)
	}
	f.byKey["a"].Status, f.byKey["a"].Busy = IssueAwaitingHuman, false
	if got := startKeys(f.plan(2)); got != "" {
		t.Fatalf("start before a is merged = %q", got)
	}

	// a merged: b and c are independent and both start with two slots.
	f.byKey["a"].Status = IssueMerged
	if got := startKeys(f.plan(2)); got != "b,c" {
		t.Fatalf("start with 2 slots = %q, want b,c", got)
	}
	// With one slot (the concrete profile) they go one at a time, by number.
	p = f.plan(1)
	if got := startKeys(p); got != "b" {
		t.Fatalf("start with 1 slot = %q, want b", got)
	}
	if r := waitReason(p, "c"); !strings.Contains(r, "no free slot") {
		t.Errorf("c waits because %q", r)
	}
}

func TestPlanScheduleCountsBusySlots(t *testing.T) {
	f := newSchedFixture()
	f.byKey["a"].Status = IssueMerged
	f.byKey["o"].Busy, f.byKey["o"].Status = true, IssueRunning // some other issue's run holds a slot
	if got := startKeys(f.plan(2)); got != "b" {
		t.Fatalf("start = %q, want only b", got)
	}
	if got := startKeys(f.plan(1)); got != "" {
		t.Fatalf("start = %q, want nothing: the slot is taken", got)
	}
}

// Priority first, then number.
func TestPlanScheduleOrder(t *testing.T) {
	f := newSchedFixture()
	f.byKey["a"].Status = IssueMerged
	f.byKey["c"].Priority = 5
	if got := startKeys(f.plan(1)); got != "c" {
		t.Fatalf("start = %q, want the higher priority c", got)
	}
}

// A dependency on, or of, a parent applies to everything under it.
func TestPlanScheduleInheritedDependencies(t *testing.T) {
	f := newSchedFixture()
	f.deps = nil
	f.byKey["o"].Requested = true
	f.dep("s", "o") // the story waits for o: so do a and b
	f.dep("o", "c") // o waits for c, a task directly under the epic

	if got := startKeys(f.plan(5)); got != "c" {
		t.Fatalf("start = %q, want c only", got)
	}
	f.byKey["c"].Status = IssueMerged
	if got := startKeys(f.plan(5)); got != "o" {
		t.Fatalf("start = %q, want o", got)
	}
	f.byKey["o"].Status = IssueMerged
	if got := startKeys(f.plan(5)); got != "a,b" {
		t.Fatalf("start = %q, want a,b", got)
	}
}

// A parent is done once its children are; canceled children do not hold it back.
func TestPlanScheduleWaitsForWholeParents(t *testing.T) {
	f := newSchedFixture()
	f.deps = nil
	f.byKey["o"].Requested = true
	f.dep("o", "s")
	f.byKey["a"].Status = IssueMerged
	f.byKey["e"].Requested = false
	if r := waitReason(f.plan(5), "o"); r != "waits for s" {
		t.Fatalf("o waits because %q, want s (b is not merged)", r)
	}
	f.byKey["b"].Status = IssueCanceled
	if got := startKeys(f.plan(5)); got != "o" {
		t.Fatalf("start = %q, want o once s's other child is canceled", got)
	}
}

// Requested issues that are not ready wait with the reason; finished ones are left out.
func TestPlanScheduleNotReady(t *testing.T) {
	f := newSchedFixture()
	f.deps = nil
	f.byKey["a"].Status = IssueDraft
	f.byKey["b"].Status = IssueFailed
	f.byKey["c"].Status = IssueMerged
	p := f.plan(5)
	if startKeys(p) != "" {
		t.Fatalf("start = %q", startKeys(p))
	}
	if r := waitReason(p, "a"); !strings.Contains(r, "draft") {
		t.Errorf("a waits because %q", r)
	}
	if r := waitReason(p, "b"); !strings.Contains(r, "retry") {
		t.Errorf("b waits because %q", r)
	}
	if waitReason(p, "c") != "" {
		t.Error("a merged issue is not waiting")
	}
}

func TestRunnable(t *testing.T) {
	if Runnable(SchedIssue{Type: IssueEpic}, false) || Runnable(SchedIssue{Type: IssueStory}, true) {
		t.Error("epics and parents do not run")
	}
	if !Runnable(SchedIssue{Type: IssueStory}, false) || !Runnable(SchedIssue{Type: IssueBug}, false) {
		t.Error("a leaf story or bug runs")
	}
}
