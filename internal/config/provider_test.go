package config

import (
	"strings"
	"testing"
)

var testProviders = Providers{
	{Name: "anthropic", Models: []string{"claude-sonnet-5-5", "claude-opus-5-5"}},
	{Name: "openrouter", Models: []string{"openai/gpt-5.6-terra", "moonshotai/kimi-k2.7-code"}},
}

func TestProvidersValidate(t *testing.T) {
	if err := testProviders.Validate(); err != nil {
		t.Fatalf("valid providers: %v", err)
	}
	if err := (Providers{}).Validate(); err != nil {
		t.Fatalf("no providers: %v", err)
	}
	tests := []struct {
		name string
		ps   Providers
		want string
	}{
		{"unknown provider", Providers{{Name: "acme", Models: []string{"fast"}}}, `unknown provider "acme"`},
		{"provider twice", Providers{{Name: "anthropic", Models: []string{"a"}}, {Name: "anthropic", Models: []string{"b"}}}, `"anthropic" listed twice`},
		{"no models", Providers{{Name: "anthropic"}}, "at least one model"},
		{"bad model id", Providers{{Name: "openrouter", Models: []string{"openai/"}}}, `"openai/" is not a model id`},
		{"model id with a space", Providers{{Name: "anthropic", Models: []string{"claude sonnet"}}}, "is not a model id"},
		{"same id from two providers", Providers{{Name: "openai", Models: []string{"gpt-x"}}, {Name: "openrouter", Models: []string{"gpt-x"}}}, `model "gpt-x" is also served by openai`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.ps.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestProvidersServes(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"anthropic/claude-sonnet-5-5", true},
		{"openrouter/openai/gpt-5.6-terra", true},
		{"anthropic/claude-haiku-5-5", false},     // not listed
		{"openai/gpt-5.6-terra", false},           // provider not configured
		{"openrouter/claude-sonnet-5-5", false},   // another provider's model
		{"claude-sonnet-5-5", false},              // no provider
		{"anthropic/openai/gpt-5.6-terra", false}, // id under the wrong provider
		{"openrouter/moonshotai/kimi-k2.7-code", true},
	}
	for _, tt := range tests {
		if got := testProviders.Serves(tt.ref); got != tt.want {
			t.Errorf("Serves(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestGatewayModel(t *testing.T) {
	for ref, want := range map[string]string{
		"anthropic/claude-sonnet-5-5":     "claude-sonnet-5-5",
		"openrouter/openai/gpt-5.6-terra": "openai/gpt-5.6-terra",
		"claude-sonnet-5-5":               "claude-sonnet-5-5",
		"":                                "",
	} {
		if got := GatewayModel(ref); got != want {
			t.Errorf("GatewayModel(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestCheckModels(t *testing.T) {
	const head = "name: w\nversion: 1\nsteps:\n"
	roles := map[string]Role{
		"coder":         {Name: "coder", Version: 1},
		"code-reviewer": {Name: "code-reviewer", Version: 1, DefaultModel: "anthropic/claude-opus-5-5", OutputContract: ContractReviewVerdict},
		"pinned":        {Name: "pinned", Version: 1, DefaultModel: "anthropic/claude-haiku-5-5"},
		// A role may default to a provider the step's harness cannot use as
		// long as every step using it sets its own model; CheckModels sees
		// only what would run.
		"router": {Name: "router", Version: 1, DefaultModel: "openrouter/openai/gpt-5.6-terra"},
	}
	parse := func(t *testing.T, yaml string) Workflow {
		t.Helper()
		w, err := ParseWorkflow([]byte(head + yaml))
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	const defaultModel = "anthropic/claude-sonnet-5-5"

	t.Run("served models pass", func(t *testing.T) {
		w := parse(t, "  - id: a\n    agent: {role: coder}\n"+
			"  - id: b\n    agent: {role: coder, model: anthropic/claude-opus-5-5}\n"+
			"  - id: c\n    agent: {role: router, model: anthropic/claude-sonnet-5-5}\n"+
			"  - id: d\n    agent: {role: router, harness: opencode}\n"+
			"  - id: e\n    agent: {role: coder, harness: opencode, model: anthropic/claude-opus-5-5}\n"+
			"  - id: checks\n    gate: {type: command}\n"+
			"  - id: review\n    parallel: [{agent: {role: code-reviewer}}]\n    gate: {type: agent-verdict}\n")
		if err := CheckModels(w, roles, defaultModel, testProviders); err != nil {
			t.Fatal(err)
		}
	})
	tests := []struct {
		name, yaml, defaultModel, want string
	}{
		{"step model not served", "  - id: a\n    agent: {role: coder, model: anthropic/claude-haiku-5-5}\n", defaultModel,
			"steps[0].agent: model anthropic/claude-haiku-5-5 is not served by this platform's providers (anthropic: claude-sonnet-5-5, claude-opus-5-5; openrouter: "},
		{"role default not served", "  - id: a\n    agent: {role: pinned}\n", defaultModel,
			"steps[0].agent: role pinned default model anthropic/claude-haiku-5-5 is not served"},
		{"platform default not served", "  - id: a\n    agent: {role: coder}\n", "anthropic/claude-haiku-5-5",
			"steps[0].agent: platform default model anthropic/claude-haiku-5-5 is not served"},
		{"platform default the harness cannot use", "  - id: a\n    agent: {role: coder}\n", "openrouter/openai/gpt-5.6-terra",
			`platform default model: harness claude-code cannot use provider "openrouter"`},
		{"branch model not served", "  - id: checks\n    gate: {type: command}\n  - id: review\n    parallel: [{agent: {role: code-reviewer, model: anthropic/claude-3}}]\n    gate: {type: agent-verdict}\n", defaultModel,
			"steps[1].parallel[0].agent: model anthropic/claude-3 is not served"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := ParseWorkflow([]byte(head + tt.yaml))
			if err != nil {
				// A step model the harness cannot use is a parse error, tested in workflow_test.go.
				t.Fatal(err)
			}
			if err := CheckModels(w, roles, tt.defaultModel, testProviders); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
	t.Run("no providers configured", func(t *testing.T) {
		err := CheckModels(parse(t, "  - id: a\n    agent: {role: coder}\n"), roles, defaultModel, nil)
		if err == nil || !strings.Contains(err.Error(), "(none configured)") {
			t.Fatalf("err = %v", err)
		}
	})
}
