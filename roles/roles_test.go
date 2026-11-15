package roles

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
)

func TestEveryBuiltInIsValid(t *testing.T) {
	names := Names()
	for _, want := range []string{"coder", "code-reviewer", "refiner", "bug-investigator", "bug-reproducer", "bug-reviewer"} {
		if !slices.Contains(names, want) {
			t.Errorf("Names() = %v, missing %q", names, want)
		}
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			if _, err := Get(name); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCodeReviewerReturnsAVerdict(t *testing.T) {
	r, err := Get("code-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if r.OutputContract != config.ContractReviewVerdict {
		t.Errorf("contract = %q", r.OutputContract)
	}
	if slices.ContainsFunc(r.AllowedTools, func(t string) bool { return t == "edit" || t == "write" || t == "bash" }) {
		t.Errorf("a reviewer must not change the workspace: tools = %v", r.AllowedTools)
	}
	schema, _ := ContractSchema(config.ContractReviewVerdict)
	if !strings.HasSuffix(r.SystemPrompt, string(schema)) || !strings.Contains(r.SystemPrompt, "final message must be only") {
		t.Errorf("the platform's contract instructions are not appended:\n%s", r.SystemPrompt)
	}
}

func TestCoderHasNoContract(t *testing.T) {
	r, err := Get("coder")
	if err != nil {
		t.Fatal(err)
	}
	if r.OutputContract != "" || strings.Contains(r.SystemPrompt, "JSON Schema") {
		t.Errorf("coder = %+v", r)
	}
}

// The JSON Schema shown to reviewers and the Go parser the gate uses must agree.
func TestReviewVerdictSchemaMatchesParser(t *testing.T) {
	raw, err := ContractSchema(config.ContractReviewVerdict)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Required   []string `json:"required"`
		Properties struct {
			Verdict  struct{ Enum []string } `json:"verdict"`
			Findings struct {
				MaxItems int `json:"maxItems"`
				Items    struct {
					Required   []string `json:"required"`
					Properties struct {
						Severity struct{ Enum []string } `json:"severity"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"findings"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Properties.Verdict.Enum, []string{string(domain.GatePass), string(domain.GateFail)}) {
		t.Errorf("verdict enum = %v", s.Properties.Verdict.Enum)
	}
	var sev []string
	for _, v := range domain.Severities {
		sev = append(sev, string(v))
	}
	if !slices.Equal(s.Properties.Findings.Items.Properties.Severity.Enum, sev) {
		t.Errorf("severity enum = %v, Go has %v", s.Properties.Findings.Items.Properties.Severity.Enum, sev)
	}
	if !slices.Equal(s.Required, []string{"verdict", "findings"}) || !slices.Equal(s.Properties.Findings.Items.Required, []string{"severity", "message"}) {
		t.Errorf("required = %v / %v", s.Required, s.Properties.Findings.Items.Required)
	}
	tooMany := `{"verdict":"fail","findings":[` + strings.Repeat(`{"severity":"info","message":"x"},`, s.Properties.Findings.MaxItems) + `{"severity":"info","message":"x"}]}`
	if _, err := domain.ParseReviewVerdict([]byte(tooMany)); err == nil {
		t.Errorf("the parser accepts more than the schema's maxItems (%d)", s.Properties.Findings.MaxItems)
	}
}

func TestParseRepoRoleGetsContractInstructions(t *testing.T) {
	r, err := Parse([]byte("name: go-reviewer\nversion: 2\nsystem_prompt_file: go.md\noutput_contract: {schema: review-verdict}\n"),
		func(string) ([]byte, error) { return []byte("Review Go code.\n"), nil })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.SystemPrompt, "Review Go code.\n\nYour final message") {
		t.Errorf("prompt = %q", r.SystemPrompt[:min(80, len(r.SystemPrompt))])
	}
}

func TestGetUnknown(t *testing.T) {
	if _, err := Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// D65: the refiner is read-only, returns a refined spec, and is told the format.
func TestRefinerReturnsARefinedSpec(t *testing.T) {
	r, err := Get("refiner")
	if err != nil {
		t.Fatal(err)
	}
	if r.OutputContract != config.ContractRefinedSpec {
		t.Errorf("contract = %q", r.OutputContract)
	}
	if slices.ContainsFunc(r.AllowedTools, func(t string) bool { return t == "edit" || t == "write" || t == "bash" }) {
		t.Errorf("the refiner must not change the workspace: tools = %v", r.AllowedTools)
	}
	schema, _ := ContractSchema(config.ContractRefinedSpec)
	if !strings.HasSuffix(r.SystemPrompt, string(schema)) {
		t.Error("the platform's contract instructions are not appended")
	}
}

// The JSON Schema shown to the refiner and the Go parser must agree.
func TestRefinedSpecSchemaMatchesParser(t *testing.T) {
	raw, err := ContractSchema(config.ContractRefinedSpec)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			MaxLength int `json:"maxLength"`
			MaxItems  int `json:"maxItems"`
			Items     struct {
				MaxLength int `json:"maxLength"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Required, []string{"spec", "acceptance_criteria"}) {
		t.Errorf("required = %v", s.Required)
	}
	keys := slices.Sorted(maps.Keys(s.Properties))
	if !slices.Equal(keys, []string{"acceptance_criteria", "file_hints", "spec", "summary"}) {
		t.Errorf("properties = %v, the parser knows acceptance_criteria, file_hints, spec, summary", keys)
	}
	p := s.Properties
	at := func(field string, n int, extra string) []byte {
		switch field {
		case "spec":
			return []byte(`{"spec":"` + strings.Repeat("x", n) + `","acceptance_criteria":["c"]` + extra + `}`)
		case "criteria":
			return []byte(`{"spec":"x","acceptance_criteria":[` + strings.TrimSuffix(strings.Repeat(`"c",`, n), ",") + `]}`)
		case "criterion":
			return []byte(`{"spec":"x","acceptance_criteria":["` + strings.Repeat("c", n) + `"]}`)
		case "hints":
			return []byte(`{"spec":"x","acceptance_criteria":["c"],"file_hints":[` + strings.TrimSuffix(strings.Repeat(`"a/b",`, n), ",") + `]}`)
		}
		return []byte(`{"spec":"x","acceptance_criteria":["c"],"summary":"` + strings.Repeat("x", n) + `"}`)
	}
	for _, tt := range []struct {
		field string
		limit int
	}{
		{"spec", p["spec"].MaxLength}, {"criteria", p["acceptance_criteria"].MaxItems},
		{"criterion", p["acceptance_criteria"].Items.MaxLength}, {"hints", p["file_hints"].MaxItems},
		{"summary", p["summary"].MaxLength},
	} {
		if _, err := domain.ParseRefinedSpec(at(tt.field, tt.limit, "")); err != nil {
			t.Errorf("%s at the schema's limit %d: %v", tt.field, tt.limit, err)
		}
		if _, err := domain.ParseRefinedSpec(at(tt.field, tt.limit+1, "")); err == nil {
			t.Errorf("%s over the schema's limit %d is accepted", tt.field, tt.limit)
		}
	}
}

// M6 (D96): the investigator only reads and returns findings; the reproducer
// writes code; the bug reviewer returns a verdict.
func TestBugRoles(t *testing.T) {
	inv, err := Get("bug-investigator")
	if err != nil {
		t.Fatal(err)
	}
	if inv.OutputContract != config.ContractBugFindings || slices.ContainsFunc(inv.AllowedTools, func(t string) bool { return t == "edit" || t == "write" || t == "bash" }) {
		t.Errorf("investigator = %+v", inv)
	}
	schema, _ := ContractSchema(config.ContractBugFindings)
	if !strings.HasSuffix(inv.SystemPrompt, string(schema)) {
		t.Error("the findings schema is not appended to the investigator's prompt")
	}
	rep, _ := Get("bug-reproducer")
	if rep.OutputContract != "" || len(rep.AllowedTools) != 0 || !strings.Contains(rep.SystemPrompt, "Do not change the code under test") {
		t.Errorf("reproducer = %+v", rep)
	}
	rev, _ := Get("bug-reviewer")
	if rev.OutputContract != config.ContractReviewVerdict || !strings.Contains(rev.SystemPrompt, "root cause") {
		t.Errorf("reviewer = %+v", rev)
	}
}

// The findings schema shown to investigators and the parser must agree.
func TestBugFindingsSchemaMatchesParser(t *testing.T) {
	raw, err := ContractSchema(config.ContractBugFindings)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Required   []string `json:"required"`
		Properties struct {
			Confidence struct{ Enum []string } `json:"confidence"`
			Evidence   struct {
				MaxItems int `json:"maxItems"`
			} `json:"evidence"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Required, []string{"root_cause", "evidence", "reproduce", "testable", "confidence"}) {
		t.Errorf("required = %v", s.Required)
	}
	want := []string{string(domain.ConfidenceHigh), string(domain.ConfidenceMedium), string(domain.ConfidenceLow)}
	if !slices.Equal(s.Properties.Confidence.Enum, want) {
		t.Errorf("confidence enum = %v", s.Properties.Confidence.Enum)
	}
	ev := `{"file":"a.go","note":"x"},`
	tooMany := `{"root_cause":"x","evidence":[` + strings.Repeat(ev, s.Properties.Evidence.MaxItems) + `{"file":"a.go","note":"x"}],"reproduce":"x","testable":true,"test_plan":"x","confidence":"high"}`
	if _, err := domain.ParseBugFindings([]byte(tooMany)); err == nil {
		t.Errorf("the parser accepts more evidence than the schema's maxItems (%d)", s.Properties.Evidence.MaxItems)
	}
}
