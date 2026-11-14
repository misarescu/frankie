package snapshot

import (
	"errors"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/config"
)

// D65: refinement runs the built-in refiner on the base branch head, with the
// project's issue budget.
func TestResolveRefinement(t *testing.T) {
	r := &repo{files: map[string]string{".factory/config.yaml": "budgets: {issueUSD: 7}\nworkflow: fast\n"}}
	s, err := ResolveRefinement(t.Context(), r, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	if s.Version() != "refine@1" || s.BaseSHA != baseSHA || s.IssueBudgetUSD != 7 || s.Source != "built-in" {
		t.Errorf("snapshot = %+v", s)
	}
	if len(s.Workflow.Steps) != 1 || s.Workflow.Steps[0].Agent.RoleName() != "refiner" {
		t.Errorf("steps = %+v", s.Workflow.Steps)
	}
	if s.Roles["refiner"].OutputContract != config.ContractRefinedSpec || len(s.Roles) != 1 {
		t.Errorf("roles = %+v", s.Roles)
	}
	for _, ref := range r.reads {
		if ref != baseSHA {
			t.Errorf("read at %q; every file must be read at the resolved commit", ref)
		}
	}
}

// A repo may replace the refiner's prompt, but its role must still return a refined spec.
func TestResolveRefinementRepoRole(t *testing.T) {
	files := map[string]string{
		".factory/roles/refiner.yaml": "name: refiner\nversion: 3\nsystem_prompt_file: refiner.md\nallowed_tools: [read]\noutput_contract: {schema: refined-spec}\n",
		".factory/roles/refiner.md":   "Refine for this repo.",
	}
	s, err := ResolveRefinement(t.Context(), &repo{files: files}, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Roles["refiner"]; got.Version != 3 || !strings.HasPrefix(got.SystemPrompt, "Refine for this repo.") {
		t.Errorf("refiner = %+v", got)
	}

	files[".factory/roles/refiner.yaml"] = "name: refiner\nversion: 3\nsystem_prompt_file: refiner.md\n"
	if _, err := ResolveRefinement(t.Context(), &repo{files: files}, "acme/app", "main"); !errors.Is(err, ErrInvalid) ||
		!strings.Contains(err.Error(), "does not return a refined spec") {
		t.Fatalf("a refiner without the contract: err = %v", err)
	}
	if _, err := ResolveRefinement(t.Context(), &repo{files: files, broken: map[string]bool{".factory/roles/refiner.yaml": true}}, "acme/app", "main"); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("a failed read: err = %v, want a read error", err)
	}
	if _, err := ResolveRefinement(t.Context(), &repo{files: map[string]string{".factory/config.yaml": "nope: 1\n"}}, "acme/app", "main"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an invalid project config: err = %v", err)
	}
}

// The built-in workflows must not be able to select the refiner.
func TestRefinementIsNotASelectableWorkflow(t *testing.T) {
	r := &repo{files: map[string]string{".factory/config.yaml": "workflow: refine\n"}}
	if _, err := Resolve(t.Context(), r, "acme/app", "main"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("workflow: refine resolved: %v", err)
	}
}
