package config

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/misarescu/frankie/internal/domain"
)

// Role is a resolved role (docs/spec/03-workflows-and-gates.md, Roles): the
// role file with its system prompt read in, ready to be snapshotted into a
// run's input with the workflow so a running workflow never sees a later
// prompt. Its JSON form is part of recorded workflow histories.
type Role struct {
	Name         string `json:"name"`
	Version      int    `json:"version"`
	SystemPrompt string `json:"systemPrompt"`
	// AllowedTools are harness-neutral tool names; empty means the harness's defaults.
	AllowedTools []string `json:"allowedTools,omitempty"`
	DefaultModel string   `json:"defaultModel,omitempty"`
	// OutputContract names the structured result the role returns, if any.
	OutputContract string `json:"outputContract,omitempty"`
}

// ContractReviewVerdict is the output contract of reviewer roles: a
// domain.ReviewVerdict, described to the agent by roles/schemas/review-verdict.json.
const ContractReviewVerdict = "review-verdict"

// roleFile is a role as written in roles/<name>.yaml or .factory/roles/<name>.yaml.
type roleFile struct {
	Name             string          `json:"name"`
	Version          int             `json:"version"`
	SystemPromptFile string          `json:"system_prompt_file"`
	Skills           []string        `json:"skills,omitempty"`
	AllowedTools     []string        `json:"allowed_tools,omitempty"`
	MCPServers       []string        `json:"mcp_servers,omitempty"`
	DefaultModel     string          `json:"default_model,omitempty"`
	OutputContract   *outputContract `json:"output_contract,omitempty"`
}

type outputContract struct {
	Schema string `json:"schema"`
}

// Tools a role may allow, by harness-neutral name. bash:readonly is a shell
// limited to commands that do not change the workspace.
var roleTools = []string{"read", "grep", "glob", "edit", "write", "bash", "bash:readonly", "web-fetch"}

var outputContracts = []string{ContractReviewVerdict}

const maxSystemPrompt = 64 << 10

// ParseRole parses and validates a role file. readFile reads the files it
// references (the system prompt), by slash-separated path relative to the
// directory holding the role.
func ParseRole(raw []byte, readFile func(name string) ([]byte, error)) (Role, error) {
	var f roleFile
	if err := yaml.UnmarshalStrict(raw, &f); err != nil {
		return Role{}, fmt.Errorf("role: %w", err)
	}
	r := Role{Name: f.Name, Version: f.Version, AllowedTools: f.AllowedTools, DefaultModel: f.DefaultModel}
	var errs []error
	errf := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if !identifierRE.MatchString(f.Name) || len(f.Name) > maxIdentifierLength {
		errf("name: %q must be lowercase letters, digits and dashes, at most %d characters", f.Name, maxIdentifierLength)
	}
	if f.Version < 1 {
		errf("version: must be at least 1")
	}
	switch {
	case f.SystemPromptFile == "":
		errf("system_prompt_file: is required")
	case !fs.ValidPath(f.SystemPromptFile) || f.SystemPromptFile == ".":
		errf("system_prompt_file: %q must be a relative path inside the roles directory", f.SystemPromptFile)
	default:
		b, err := readFile(f.SystemPromptFile)
		switch {
		case err != nil:
			errf("system_prompt_file: %w", err)
		case len(b) > maxSystemPrompt:
			errf("system_prompt_file: %s is larger than %d bytes", f.SystemPromptFile, maxSystemPrompt)
		case strings.TrimSpace(string(b)) == "":
			errf("system_prompt_file: %s is empty", f.SystemPromptFile)
		default:
			r.SystemPrompt = string(b)
		}
	}
	// The runner cannot install skills or MCP servers yet (docs/spec/04).
	if len(f.Skills) > 0 {
		errf("skills: not available yet")
	}
	if len(f.MCPServers) > 0 {
		errf("mcp_servers: not available yet")
	}
	for _, t := range f.AllowedTools {
		if !slices.Contains(roleTools, t) {
			errf("allowed_tools: unknown tool %q (known: %s)", t, strings.Join(roleTools, ", "))
		}
	}
	if slices.Contains(f.AllowedTools, "bash") && slices.Contains(f.AllowedTools, "bash:readonly") {
		errf("allowed_tools: bash and bash:readonly together")
	}
	if f.DefaultModel != "" && !modelRefRE.MatchString(f.DefaultModel) {
		errf("default_model: %q must be a model ref <provider>/<model>", f.DefaultModel)
	}
	if f.OutputContract != nil {
		if !slices.Contains(outputContracts, f.OutputContract.Schema) {
			errf("output_contract.schema: unknown contract %q (known: %s)", f.OutputContract.Schema, strings.Join(outputContracts, ", "))
		}
		r.OutputContract = f.OutputContract.Schema
	}
	if err := errors.Join(errs...); err != nil {
		return Role{}, fmt.Errorf("role %q: %w", f.Name, err)
	}
	return r, nil
}

// RoleLookup finds a role by name.
type RoleLookup func(name string) (Role, error)

// ResolveRoles cross-checks a validated workflow against its roles (FR-WF-5):
// every role exists, a pinned version matches the role's, the role's default
// model suits the step's harness, and reviewer roles (those returning a
// verdict) sit exactly in parallel groups judged by an agent-verdict gate. It
// returns the roles the workflow uses, by name, for the run's snapshot.
func ResolveRoles(w Workflow, lookup RoleLookup) (map[string]Role, error) {
	out := map[string]Role{}
	var errs []error
	check := func(path string, s Step, verdictGroup bool) {
		a := s.Agent
		name := a.RoleName()
		r, ok := out[name]
		if !ok {
			var err error
			if r, err = lookup(name); err != nil {
				errs = append(errs, fmt.Errorf("%s.agent.role: %w", path, err))
				return
			}
			out[name] = r
		}
		if _, pin, pinned := strings.Cut(a.Role, "@"); pinned && pin != fmt.Sprint(r.Version) {
			errs = append(errs, fmt.Errorf("%s.agent.role: %s pins version %s, the role is at version %d", path, a.Role, pin, r.Version))
		}
		if a.Model == "" && r.DefaultModel != "" {
			if err := harnessCanUse(a.Harness, r.DefaultModel); err != nil {
				errs = append(errs, fmt.Errorf("%s.agent: role %s defaults to %s, which harness %s cannot use (%w); set a model", path, name, r.DefaultModel, a.Harness, err))
			}
		}
		switch reviewer := r.OutputContract == ContractReviewVerdict; {
		case verdictGroup && !reviewer:
			errs = append(errs, fmt.Errorf("%s.agent.role: %s does not return a review verdict, so an agent-verdict gate cannot judge it", path, name))
		case !verdictGroup && reviewer:
			errs = append(errs, fmt.Errorf("%s.agent.role: %s returns a review verdict; put it in a parallel group with an agent-verdict gate", path, name))
		}
	}
	for i, s := range w.Steps {
		path := fmt.Sprintf("steps[%d]", i)
		switch s.Kind() {
		case StepAgent:
			check(path, s, false)
		case StepParallel:
			verdictGroup := s.Gate != nil && s.Gate.Type == domain.GateAgentVerdict
			for j, b := range s.Parallel {
				check(fmt.Sprintf("%s.parallel[%d]", path, j), b, verdictGroup)
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("workflow %q: %w", w.Name, err)
	}
	return out, nil
}
