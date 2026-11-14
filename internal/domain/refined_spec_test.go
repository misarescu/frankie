package domain

import (
	"strings"
	"testing"
)

// D65: the refiner's result is parsed strictly against the refined-spec contract.
func TestParseRefinedSpec(t *testing.T) {
	good := `{"spec":"Add a search box.","acceptance_criteria":["typing filters the list"],"file_hints":["web/src/Search.tsx","internal/api/"],"summary":"assumed client-side filtering"}`
	r, err := ParseRefinedSpec([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if r.Spec != "Add a search box." || len(r.AcceptanceCriteria) != 1 || len(r.FileHints) != 2 || r.Summary == "" {
		t.Errorf("parsed = %+v", r)
	}
	if _, err := ParseRefinedSpec([]byte(`{"spec":"x","acceptance_criteria":["y"]}`)); err != nil {
		t.Errorf("hints and summary are optional: %v", err)
	}

	criteria := func(n int) string { return `[` + strings.TrimSuffix(strings.Repeat(`"c",`, n), ",") + `]` }
	bad := map[string]string{
		"not json":           `spec: x`,
		"unknown field":      `{"spec":"x","acceptance_criteria":["y"],"priority":1}`,
		"trailing data":      `{"spec":"x","acceptance_criteria":["y"]} {}`,
		"empty spec":         `{"spec":"  ","acceptance_criteria":["y"]}`,
		"spec too long":      `{"spec":"` + strings.Repeat("x", maxRefinedSpec+1) + `","acceptance_criteria":["y"]}`,
		"no criteria":        `{"spec":"x","acceptance_criteria":[]}`,
		"criteria missing":   `{"spec":"x"}`,
		"too many criteria":  `{"spec":"x","acceptance_criteria":` + criteria(maxCriteria+1) + `}`,
		"empty criterion":    `{"spec":"x","acceptance_criteria":[""]}`,
		"criterion too long": `{"spec":"x","acceptance_criteria":["` + strings.Repeat("x", maxCriterion+1) + `"]}`,
		"absolute hint":      `{"spec":"x","acceptance_criteria":["y"],"file_hints":["/etc/passwd"]}`,
		"escaping hint":      `{"spec":"x","acceptance_criteria":["y"],"file_hints":["../x"]}`,
		"hint with spaces":   `{"spec":"x","acceptance_criteria":["y"],"file_hints":["see the api"]}`,
		"summary too long":   `{"spec":"x","acceptance_criteria":["y"],"summary":"` + strings.Repeat("x", maxRefinedSpecSummary+1) + `"}`,
	}
	for name, raw := range bad {
		if _, err := ParseRefinedSpec([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
