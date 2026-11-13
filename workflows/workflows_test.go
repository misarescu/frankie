package workflows

import (
	"errors"
	"slices"
	"testing"

	"github.com/misarescu/frankie/internal/config"
)

func TestEveryBuiltInIsValid(t *testing.T) {
	names := Names()
	if !slices.Contains(names, Default) {
		t.Fatalf("Names() = %v, missing %q", names, Default)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			if _, err := Get(name); err != nil {
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
	want := []config.StepKind{config.StepAgent, config.StepGate, config.StepParallel, config.StepAction}
	if !slices.Equal(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
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
