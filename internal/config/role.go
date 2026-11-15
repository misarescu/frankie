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

// ContractRefinedSpec is the output contract of the refiner role: a
// domain.RefinedSpec, described by roles/schemas/refined-spec.json (D65). Only
// refinement runs such a role, never a workflow definition.
const ContractRefinedSpec = "refined-spec"

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

var outputContracts = []string{ContractReviewVerdict, ContractRefinedSpec, ContractBugFindings}

// ContractBugFindings is the output contract of an investigating role: a
// domain.BugFindings, described by roles/schemas/bug-findings.json (D94). Its
// step reads the code without changing it, and later steps can depend on it.
const ContractBugFindings = "bug-findings"

// contractFields are the result fields of an output contract a step can be
// conditional on (`when`), with their kinds.
var contractFields = map[string]map[string]string{ContractBugFindings: domain.BugFindingsFields}

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
// returns the roles the workflow uses, by name, for the run's snapshot. A role
// returning a refined spec belongs to refinement and is rejected here.
func ResolveRoles(w Workflow, lookup RoleLookup) (map[string]Role, error) {
	return resolveRoles(w, lookup, false)
}

// ResolveRefinementRoles is ResolveRoles for the refinement definition (D65):
// its agent steps must use a role returning a refined spec.
func ResolveRefinementRoles(w Workflow, lookup RoleLookup) (map[string]Role, error) {
	return resolveRoles(w, lookup, true)
}

func resolveRoles(w Workflow, lookup RoleLookup, refinement bool) (map[string]Role, error) {
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
		for _, tool := range r.AllowedTools {
			if slices.Contains(harnesses[a.Harness].lacks, tool) {
				errs = append(errs, fmt.Errorf("%s.agent: role %s allows %s, which harness %s does not have", path, name, tool, a.Harness))
			}
		}
		if a.Model == "" && r.DefaultModel != "" {
			if err := harnessCanUse(a.Harness, r.DefaultModel); err != nil {
				errs = append(errs, fmt.Errorf("%s.agent: role %s defaults to %s, which harness %s cannot use (%w); set a model", path, name, r.DefaultModel, a.Harness, err))
			}
		}
		refiner := r.OutputContract == ContractRefinedSpec
		switch reviewer := r.OutputContract == ContractReviewVerdict; {
		case refinement && !refiner:
			errs = append(errs, fmt.Errorf("%s.agent.role: %s does not return a refined spec, so refinement cannot use it", path, name))
		case !refinement && refiner:
			errs = append(errs, fmt.Errorf("%s.agent.role: %s returns a refined spec; only refinement runs it, not a workflow", path, name))
		case verdictGroup && !reviewer:
			errs = append(errs, fmt.Errorf("%s.agent.role: %s does not return a review verdict, so an agent-verdict gate cannot judge it", path, name))
		case !verdictGroup && reviewer:
			errs = append(errs, fmt.Errorf("%s.agent.role: %s returns a review verdict; put it in a parallel group with an agent-verdict gate", path, name))
		case r.OutputContract == ContractBugFindings && strings.Contains(path, ".parallel["):
			errs = append(errs, fmt.Errorf("%s.agent.role: %s returns bug findings; run it as its own step, not in a parallel group", path, name))
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
	if !refinement {
		errs = append(errs, checkConditions(w, out)...)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("workflow %q: %w", w.Name, err)
	}
	return out, nil
}

// checkConditions checks each `when` against the role of the step it reads:
// that step returns a contract with the field, and the value has its kind (D94).
func checkConditions(w Workflow, roles map[string]Role) []error {
	contracts := map[string]string{}
	for _, s := range w.Steps {
		if s.Kind() == StepAgent {
			contracts[s.ID] = roles[s.Agent.RoleName()].OutputContract
		}
	}
	var errs []error
	for i, s := range w.Steps {
		if s.When == nil {
			continue
		}
		path := fmt.Sprintf("steps[%d].when", i)
		fields := contractFields[contracts[s.When.Step]]
		kind, ok := fields[s.When.Field]
		switch {
		case fields == nil:
			errs = append(errs, fmt.Errorf("%s: step %q returns no result a step can depend on", path, s.When.Step))
		case !ok:
			errs = append(errs, fmt.Errorf("%s: step %q's result has no field %q", path, s.When.Step, s.When.Field))
		case kind != s.When.kind():
			errs = append(errs, fmt.Errorf("%s: %s is a %s, not a %s", path, s.When.Field, kind, s.When.kind()))
		}
	}
	return errs
}
