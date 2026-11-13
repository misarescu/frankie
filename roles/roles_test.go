package roles

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
)

func TestEveryBuiltInIsValid(t *testing.T) {
	names := Names()
	for _, want := range []string{"coder", "code-reviewer"} {
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
