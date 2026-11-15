package workflows

import (
	"errors"
	"slices"
	"testing"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/roles"
)

func TestEveryBuiltInIsValid(t *testing.T) {
	names := Names()
	if !slices.Contains(names, Default) {
		t.Fatalf("Names() = %v, missing %q", names, Default)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			w, err := Get(name)
			if err != nil {
				t.Fatal(err)
			}
			// Built-in workflows run with built-in roles only.
			if _, err := config.ResolveRoles(w, roles.Get); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// M3 acceptance: default-build loops from the reviewer back to implement.
func TestDefaultBuildShape(t *testing.T) {
	w, err := Get(Default)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []config.StepKind
	for _, s := range w.Steps {
		kinds = append(kinds, s.Kind())
	}
	want := []config.StepKind{config.StepAgent, config.StepGate, config.StepParallel, config.StepAction, config.StepAction}
	if !slices.Equal(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	// M6 (D88): the change request is opened, then merged by the queue.
	if w.Steps[3].Action != config.ActionOpenChangeRequest || w.Steps[4].Action != config.ActionEnqueue {
		t.Errorf("actions = %q, %q", w.Steps[3].Action, w.Steps[4].Action)
	}
	// D89: what the queue sends back goes to the coder.
	if o := w.Steps[4].OnFail; o == nil || o.Action != config.OnFailGoto || o.Goto != "implement" || o.MaxLoops != 2 {
		t.Errorf("merge onFail = %+v", o)
	}
	for _, i := range []int{1, 2} {
		if o := w.Steps[i].Gate.OnFail; o.Action != config.OnFailGoto || o.Goto != "implement" {
			t.Errorf("steps[%d] onFail = %+v, want goto implement", i, o)
		}
	}
}

func TestGetUnknown(t *testing.T) {
	if _, err := Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// M6 (D96): bugs run bug-fix unless their project names another workflow.
func TestBugFixIsTheDefaultForBugs(t *testing.T) {
	if DefaultFor("bug") != "bug-fix" || DefaultFor("task") != Default || DefaultFor("") != Default {
		t.Errorf("defaults: bug %q, task %q, none %q", DefaultFor("bug"), DefaultFor("task"), DefaultFor(""))
	}
	w, err := Get("bug-fix")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range w.Steps {
		ids = append(ids, s.ID)
	}
	want := []string{"investigate", "decide", "reproduce", "shows-bug", "fix", "keeps-test", "checks", "review", "open-change-request", "merge"}
	if !slices.Equal(ids, want) {
		t.Fatalf("steps = %v", ids)
	}
	rs, err := config.ResolveRoles(w, roles.Get)
	if err != nil {
		t.Fatal(err)
	}
	if rs["bug-investigator"].OutputContract != config.ContractBugFindings || rs["bug-reviewer"].OutputContract != config.ContractReviewVerdict {
		t.Errorf("roles = %+v", rs)
	}
	byID := map[string]config.Step{}
	for _, s := range w.Steps {
		byID[s.ID] = s
	}
	for id, testable := range map[string]bool{"decide": false, "reproduce": true, "shows-bug": true, "keeps-test": true} {
		if c := byID[id].When; c == nil || c.Step != "investigate" || c.Field != "testable" || c.Equals != testable {
			t.Errorf("%s runs when %+v, want testable=%v", id, c, testable)
		}
	}
	for _, id := range []string{"investigate", "fix", "checks", "review", "merge"} {
		if byID[id].When != nil {
			t.Errorf("%s is conditional", id)
		}
	}
	if g := byID["shows-bug"].Gate; g.Expect != config.ExpectFail || g.OnFail.Goto != "reproduce" {
		t.Errorf("shows-bug = %+v", g)
	}
	if g := byID["keeps-test"].Gate; g.Step != "reproduce" || g.OnFail.Goto != "fix" {
		t.Errorf("keeps-test = %+v", g)
	}
	if g := byID["decide"].Gate; g.SendBack == nil || g.SendBack.Goto != "investigate" {
		t.Errorf("decide = %+v", g)
	}
	if o := byID["merge"].OnFail; o == nil || o.Goto != "fix" {
		t.Errorf("merge onFail = %+v", o)
	}
}
