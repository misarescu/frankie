package config

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

func promptFiles(files map[string]string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		s, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("open %s: %w", name, fs.ErrNotExist)
		}
		return []byte(s), nil
	}
}

func TestParseRole(t *testing.T) {
	r, err := ParseRole([]byte(`
name: code-reviewer
version: 3
system_prompt_file: prompts/code-reviewer.md
allowed_tools: [read, grep, glob, bash:readonly]
mcp_servers: []
default_model: anthropic/claude-sonnet-5-5
output_contract:
  schema: review-verdict
`), promptFiles(map[string]string{"prompts/code-reviewer.md": "Review the change.\n"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Role{Name: "code-reviewer", Version: 3, SystemPrompt: "Review the change.\n",
		AllowedTools: []string{"read", "grep", "glob", "bash:readonly"}, DefaultModel: "anthropic/claude-sonnet-5-5", OutputContract: ContractReviewVerdict}
	if r.Name != want.Name || r.Version != want.Version || r.SystemPrompt != want.SystemPrompt || !slices.Equal(r.AllowedTools, want.AllowedTools) ||
		r.DefaultModel != want.DefaultModel || r.OutputContract != want.OutputContract {
		t.Fatalf("r = %+v", r)
	}
}

func TestParseRoleRejects(t *testing.T) {
	files := promptFiles(map[string]string{"p.md": "Do the work.", "empty.md": " \n", "big.md": strings.Repeat("x", maxSystemPrompt+1)})
	const head = "name: r\nversion: 1\n"
	tests := []struct{ name, yaml, want string }{
		{"unknown field", head + "system_prompt_file: p.md\ntemperature: 0\n", "unknown field"},
		{"bad name", "name: Coder\nversion: 1\nsystem_prompt_file: p.md\n", "name:"},
		{"no version", "name: r\nsystem_prompt_file: p.md\n", "version: must be at least 1"},
		{"no prompt", head, "system_prompt_file: is required"},
		{"prompt outside the roles directory", head + "system_prompt_file: ../secrets.md\n", "relative path inside the roles directory"},
		{"absolute prompt path", head + "system_prompt_file: /etc/passwd\n", "relative path inside the roles directory"},
		{"missing prompt", head + "system_prompt_file: nope.md\n", "file does not exist"},
		{"empty prompt", head + "system_prompt_file: empty.md\n", "is empty"},
		{"huge prompt", head + "system_prompt_file: big.md\n", "larger than"},
		{"skills", head + "system_prompt_file: p.md\nskills: [go-review]\n", "skills: not available yet"},
		{"mcp servers", head + "system_prompt_file: p.md\nmcp_servers: [github]\n", "mcp_servers: not available yet"},
		{"unknown tool", head + "system_prompt_file: p.md\nallowed_tools: [kubectl]\n", `unknown tool "kubectl"`},
		{"both shells", head + "system_prompt_file: p.md\nallowed_tools: [bash, bash:readonly]\n", "bash and bash:readonly together"},
		{"bad model", head + "system_prompt_file: p.md\ndefault_model: sonnet\n", "model ref"},
		{"model ref with a trailing slash", head + "system_prompt_file: p.md\ndefault_model: openrouter/openai/\n", "model ref"},
		{"unknown contract", head + "system_prompt_file: p.md\noutput_contract: {schema: schemas/mine.json}\n", "unknown contract"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRole([]byte(tt.yaml), files)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

var errNoRole = errors.New("no such role")

func lookupIn(roles ...Role) RoleLookup {
	return func(name string) (Role, error) {
		for _, r := range roles {
			if r.Name == name {
				return r, nil
			}
		}
		return Role{}, fmt.Errorf("%q: %w", name, errNoRole)
	}
}

var (
	coderRole    = Role{Name: "coder", Version: 2, SystemPrompt: "code"}
	reviewerRole = Role{Name: "code-reviewer", Version: 3, SystemPrompt: "review", OutputContract: ContractReviewVerdict}
)

// FR-WF-5: roles exist and are pinned to a version.
func TestResolveRoles(t *testing.T) {
	w, err := ParseWorkflow([]byte(`
name: w
version: 1
steps:
  - id: implement
    agent: {role: coder@2}
  - id: checks
    gate: {type: command, onFail: {goto: implement, maxLoops: 2}}
  - id: review
    parallel: [{agent: {role: code-reviewer}}]
    gate: {type: agent-verdict, onFail: {goto: implement, maxLoops: 2}}
  - id: fixup
    agent: {role: coder}
`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveRoles(w, lookupIn(coderRole, reviewerRole))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["coder"].Version != 2 || got["code-reviewer"].OutputContract != ContractReviewVerdict {
		t.Fatalf("roles = %+v", got)
	}
}

func TestResolveRolesRejects(t *testing.T) {
	const head = "name: w\nversion: 1\nsteps:\n"
	const checks = "  - id: checks\n    gate: {type: command}\n"
	foreign := Role{Name: "gpt-coder", Version: 1, SystemPrompt: "x", DefaultModel: "openai/gpt-x"}
	browser := Role{Name: "browser", Version: 1, SystemPrompt: "x", AllowedTools: []string{"read", "web-fetch"}}
	tests := []struct{ name, yaml, want string }{
		{"missing role", head + "  - id: x\n    agent: {role: ghost}\n", "no such role"},
		{"stale pin", head + "  - id: x\n    agent: {role: coder@1}\n", "pins version 1, the role is at version 2"},
		{"default model the harness cannot use", head + "  - id: x\n    agent: {role: gpt-coder}\n", "defaults to openai/gpt-x, which harness claude-code cannot use"},
		{"a tool the harness lacks", head + "  - id: x\n    agent: {role: browser, harness: pi}\n", "role browser allows web-fetch, which harness pi does not have"},
		{"reviewer outside a verdict group", head + "  - id: x\n    agent: {role: code-reviewer}\n", "put it in a parallel group with an agent-verdict gate"},
		{"coder judged by an agent-verdict gate", head + checks + "  - id: r\n    parallel: [{agent: {role: coder}}]\n    gate: {type: agent-verdict}\n", "does not return a review verdict"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := ParseWorkflow([]byte(tt.yaml))
			if err != nil {
				t.Fatal(err)
			}
			_, err = ResolveRoles(w, lookupIn(coderRole, reviewerRole, foreign, browser))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
	t.Run("an explicit model overrides the role default", func(t *testing.T) {
		w, err := ParseWorkflow([]byte(head + "  - id: x\n    agent: {role: gpt-coder, model: anthropic/claude-sonnet-5-5}\n"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveRoles(w, lookupIn(foreign)); err != nil {
			t.Fatal(err)
		}
	})
}
