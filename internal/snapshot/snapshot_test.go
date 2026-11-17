package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/drivers/driverstest"
)

const baseSHA = "1111111111111111111111111111111111111111"

// repo is a fake FileReader holding one commit; it fails reads of paths in broken.
type repo struct {
	files  map[string]string
	broken map[string]bool
	reads  []string // refs files were read at
}

func (r *repo) ResolveRef(_ context.Context, _, ref string) (string, error) {
	if ref != "main" {
		return "", errors.New("unknown ref " + ref)
	}
	return baseSHA, nil
}

func (r *repo) FileAt(_ context.Context, _, path, ref string) ([]byte, bool, error) {
	r.reads = append(r.reads, ref)
	if r.broken[path] {
		return nil, false, errors.New("github: 502")
	}
	s, ok := r.files[path]
	return []byte(s), ok, nil
}

const loopWorkflow = `
name: fast
version: 4
steps:
  - id: implement
    agent: {role: coder}
  - id: checks
    gate: {type: command, run: ["mise run test"], onFail: {goto: implement, maxLoops: 2}}
  - id: open
    action: change-request.open
`

func TestResolveBuiltIn(t *testing.T) {
	r := &repo{files: map[string]string{".factory/config.yaml": "workflow: default-build\n"}}
	s, err := Resolve(t.Context(), r, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	if s.Source != "built-in" || s.Workflow.Name != "default-build" || s.BaseSHA != baseSHA || s.Version() != "default-build@1" {
		t.Errorf("snapshot = %+v", s)
	}
	if s.Roles["coder"].SystemPrompt == "" || s.Roles["code-reviewer"].OutputContract == "" || len(s.Roles) != 2 {
		t.Errorf("roles = %+v", s.Roles)
	}
	for _, ref := range r.reads {
		if ref != baseSHA {
			t.Errorf("read at %q; every file must be read at the resolved commit", ref)
		}
	}
}

func TestResolveRepoDefinitionAndRole(t *testing.T) {
	r := &repo{files: map[string]string{
		".factory/config.yaml":         "workflow: fast\nservices: [postgres:17]\n",
		".factory/workflows/fast.yaml": loopWorkflow,
		// The repo replaces the built-in coder.
		".factory/roles/coder.yaml":       "name: coder\nversion: 7\nsystem_prompt_file: prompts/coder.md\n",
		".factory/roles/prompts/coder.md": "Write Go like we do.\n",
		".factory/protected-paths.yaml":   "paths: [deploy/**/networkpolicy*]\n",
	}}
	s, err := Resolve(t.Context(), r, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	if s.Source != ".factory/workflows/fast.yaml" || s.Version() != "fast@4" {
		t.Errorf("source %q version %q", s.Source, s.Version())
	}
	if c := s.Roles["coder"]; c.Version != 7 || c.SystemPrompt != "Write Go like we do.\n" {
		t.Errorf("coder = %+v", c)
	}
	if !slices.Contains(s.ProtectedPaths, "deploy/**/networkpolicy*") || !slices.Contains(s.ProtectedPaths, ".factory/**") {
		t.Errorf("protected paths = %v (the repo's added to the defaults)", s.ProtectedPaths)
	}

	// Invariant 4: the snapshot travels as JSON in the workflow input.
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back Snapshot
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, back) {
		t.Errorf("JSON round trip changed the snapshot:\n%s", b)
	}
}

func TestResolveDefaultsToTheBuiltInWorkflow(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no project config":       {},
		"config without workflow": {".factory/config.yaml": "services: [redis:7]\n"},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Resolve(t.Context(), &repo{files: files}, "acme/app", "main")
			if err != nil {
				t.Fatal(err)
			}
			if s.Source != "built-in" || s.Workflow.Name != "default-build" || len(s.Roles) != 2 || !slices.Equal(s.ProtectedPaths, config.DefaultProtectedPaths) {
				t.Errorf("snapshot = %+v", s)
			}
		})
	}
	// A repo may still replace the default definition with its own of that name.
	s, err := Resolve(t.Context(), &repo{files: map[string]string{
		".factory/workflows/default-build.yaml": strings.Replace(loopWorkflow, "name: fast", "name: default-build", 1)}}, "acme/app", "main")
	if err != nil || s.Source != ".factory/workflows/default-build.yaml" {
		t.Errorf("repo default-build: %+v, %v", s, err)
	}
}

// M6 (D94): an issue's type picks the project's definition for that type,
// else the project's, else the platform's.
func TestResolveForIssueType(t *testing.T) {
	files := map[string]string{
		".factory/config.yaml":            "workflow: fast\nworkflows: {bug: careful}\n",
		".factory/workflows/fast.yaml":    loopWorkflow,
		".factory/workflows/careful.yaml": strings.Replace(loopWorkflow, "name: fast", "name: careful", 1),
	}
	for typ, want := range map[string]string{"bug": "careful", "task": "fast", "": "fast"} {
		s, err := ResolveFor(t.Context(), &repo{files: files}, "acme/app", "main", typ, nil)
		if err != nil || s.Workflow.Name != want {
			t.Errorf("%q: workflow %q, %v; want %q", typ, s.Workflow.Name, err, want)
		}
	}
	// D96: without a project setting, a bug runs the built-in bug-fix with
	// its built-in roles, and a task the default.
	s, err := ResolveFor(t.Context(), &repo{files: map[string]string{}}, "acme/app", "main", "bug", nil)
	if err != nil || s.Workflow.Name != "bug-fix" || s.Roles["bug-investigator"].OutputContract != config.ContractBugFindings {
		t.Errorf("a bug without a project setting: %q, %v", s.Workflow.Name, err)
	}
	if s, err := ResolveFor(t.Context(), &repo{files: map[string]string{}}, "acme/app", "main", "task", nil); err != nil || s.Workflow.Name != "default-build" {
		t.Errorf("a task without a project setting: %q, %v", s.Workflow.Name, err)
	}
}

func TestResolveInvalid(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"bad project config", map[string]string{".factory/config.yaml": "workflowz: fast\n"}, "unknown field"},
		{"unknown workflow", map[string]string{".factory/config.yaml": "workflow: nope\n"}, `workflow "nope" is neither in .factory/workflows nor built in`},
		{"invalid definition", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": "name: fast\nversion: 1\nsteps: []\n"}, "at least one step"},
		{"definition named differently", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": strings.Replace(loopWorkflow, "name: fast", "name: slow", 1)}, `is named "slow"`},
		{"missing role", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": strings.Replace(loopWorkflow, "role: coder", "role: ghost", 1)}, "no built-in role"},
		{"role prompt missing", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": loopWorkflow,
			".factory/roles/coder.yaml": "name: coder\nversion: 1\nsystem_prompt_file: coder.md\n"}, ".factory/roles/coder.md not found"},
		{"bad protected paths", map[string]string{".factory/protected-paths.yaml": "paths: [/etc]\n"}, "relative to the repo root"},
		{"role named differently", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": loopWorkflow,
			".factory/roles/coder.yaml": "name: hacker\nversion: 1\nsystem_prompt_file: coder.md\n", ".factory/roles/coder.md": "x"}, `is named "hacker"`},
		// M7 (D122): the e2e test writer needs the project's suite.
		{"build-e2e without an e2e suite", map[string]string{".factory/config.yaml": "workflow: build-e2e\n"}, "declare e2e.command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(t.Context(), &repo{files: tt.files}, "acme/app", "main")
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want ErrInvalid containing %q", err, tt.want)
			}
		})
	}
}

// M7 (D122): a project with an e2e suite runs the built-in build-e2e: the
// coder and the e2e test writer in one group, then the suite as a gate.
func TestResolveBuildE2E(t *testing.T) {
	r := &repo{files: map[string]string{".factory/config.yaml": "workflow: build-e2e\ne2e: {command: mise run e2e}\n"}}
	s, err := Resolve(t.Context(), r, "acme/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	if s.Source != "built-in" || s.Workflow.Name != "build-e2e" || s.Roles[E2ETestWriterRole].SystemPrompt == "" {
		t.Fatalf("snapshot = %s from %s with roles %v", s.Workflow.Name, s.Source, s.Roles)
	}
	build := s.Workflow.Steps[0]
	if build.ID != "build" || len(build.Parallel) != 2 || build.Parallel[1].Agent.RoleName() != E2ETestWriterRole ||
		slices.Contains(build.Parallel[1].Inputs, "diff") {
		t.Errorf("first step = %+v; want the coder and the e2e test writer, without the diff", build)
	}
	var gates []string
	for _, st := range s.Workflow.Steps {
		if st.Gate != nil && st.Gate.Type == "command" {
			gates = append(gates, strings.Join(st.Gate.Run, "; "))
		}
	}
	if !slices.Contains(gates, "mise run e2e:local") {
		t.Errorf("command gates = %v; want the suite run locally", gates)
	}
	// M7 (D126): version 2 checks the change request in a preview.
	if s.Workflow.Version != 2 || !s.Workflow.HasGate(domain.GatePreview) || s.PreviewStage != nil {
		t.Errorf("build-e2e@%d, preview gate %v, preview stage %+v", s.Workflow.Version, s.Workflow.HasGate(domain.GatePreview), s.PreviewStage)
	}
}

// A failed read is retried later, never reported as the repo's mistake.
func TestResolveReadFailuresAreNotInvalid(t *testing.T) {
	files := map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": loopWorkflow,
		".factory/roles/coder.yaml": "name: coder\nversion: 1\nsystem_prompt_file: coder.md\n", ".factory/roles/coder.md": "x"}
	for _, path := range []string{".factory/config.yaml", ".factory/protected-paths.yaml", ".factory/workflows/fast.yaml", ".factory/roles/coder.yaml", ".factory/roles/coder.md"} {
		t.Run(path, func(t *testing.T) {
			_, err := Resolve(t.Context(), &repo{files: files, broken: map[string]bool{path: true}}, "acme/app", "main")
			if err == nil || errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want a read error", err)
			}
		})
	}
	if _, err := Resolve(t.Context(), &repo{files: files}, "acme/app", "develop"); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("unresolvable base branch: err = %v", err)
	}
}

// Frankie is a factory project (docs/spec/08): its own .factory files must
// resolve, or its issues would fail before any step.
func TestFrankiesOwnConfigResolves(t *testing.T) {
	files := map[string]string{}
	for _, p := range []string{config.ProjectConfigPath, config.ProtectedPathsFile} {
		b, err := os.ReadFile(filepath.Join("..", "..", p))
		if err != nil {
			t.Fatal(err)
		}
		files[p] = string(b)
	}
	s, err := Resolve(t.Context(), &repo{files: files}, "misarescu/frankie", "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.ParseProject([]byte(files[config.ProjectConfigPath]), 1); err != nil {
		t.Fatalf("with one service per pod: %v", err)
	}
	for _, f := range []string{"deploy/helm/frankie/templates/agents.yaml", "internal/runner/server.go", "proto/frankie/runner/v1/runner.proto",
		"roles/prompts/coder.md", "workflows/default-build.yaml", "internal/config/protected.go", ".factory/protected-paths.yaml", "mise.toml"} {
		if _, ok := config.MatchProtected(s.ProtectedPaths, f); !ok {
			t.Errorf("%s is not protected", f)
		}
	}
	for _, f := range []string{"internal/api/approvals.go", "web/src/App.tsx", "docs/spec/03-workflows-and-gates.md", "internal/config/workflow.go"} {
		if pattern, ok := config.MatchProtected(s.ProtectedPaths, f); ok {
			t.Errorf("%s is protected by %s", f, pattern)
		}
	}
}

// D53: the project's issue budget is read at the base commit with the rest,
// so an agent cannot raise it on its own branch.
func TestResolveIssueBudget(t *testing.T) {
	r := &repo{files: map[string]string{".factory/config.yaml": "budgets: {issueUSD: 7.5}\n"}}
	s, err := Resolve(t.Context(), r, "acme/app", "main")
	if err != nil || s.IssueBudgetUSD != 7.5 {
		t.Fatalf("snapshot = %+v, %v", s, err)
	}
	if s, err := Resolve(t.Context(), &repo{files: map[string]string{}}, "acme/app", "main"); err != nil || s.IssueBudgetUSD != 0 {
		t.Fatalf("no budgets: %+v, %v", s.IssueBudgetUSD, err)
	}
}

// M6 (D88): the merge method is read at the base commit with the rest.
func TestResolveMergeMethod(t *testing.T) {
	r := &repo{files: map[string]string{".factory/config.yaml": "merge: {method: rebase}\n"}}
	if s, err := Resolve(t.Context(), r, "acme/app", "main"); err != nil || s.MergeMethod != domain.MergeRebase {
		t.Fatalf("snapshot = %+v, %v", s.MergeMethod, err)
	}
	if s, err := Resolve(t.Context(), &repo{files: map[string]string{}}, "acme/app", "main"); err != nil || s.MergeMethod != domain.MergeSquash {
		t.Fatalf("default: %+v, %v", s.MergeMethod, err)
	}
}

// M7 (D120): the project's CI log limits are read at the base commit with the
// rest, so the merge queue applies what the run started with.
func TestResolveCILogs(t *testing.T) {
	r := &repo{files: map[string]string{".factory/config.yaml": "merge: {ciLogs: {checks: 1, lines: 200}}\n"}}
	if s, err := Resolve(t.Context(), r, "acme/app", "main"); err != nil || s.CILogs != (domain.CILogLimits{Checks: 1, Lines: 200}) {
		t.Fatalf("snapshot = %+v, %v", s.CILogs, err)
	}
}

// M5.2 (D80): the hosts a project adds to the egress list are read at the
// base commit with the rest, so an agent cannot widen its own egress.
func TestResolveEgressAllow(t *testing.T) {
	r := &repo{files: map[string]string{".factory/config.yaml": "egress: {allow: [nodejs.org]}\n"}}
	s, err := Resolve(t.Context(), r, "acme/app", "main")
	if err != nil || len(s.EgressAllow) != 1 || s.EgressAllow[0] != "nodejs.org" {
		t.Fatalf("snapshot = %+v, %v", s, err)
	}
	if s, err := ResolveRefinement(t.Context(), r, "acme/app", "main"); err != nil || len(s.EgressAllow) != 1 {
		t.Fatalf("refinement: %+v, %v", s.EgressAllow, err)
	}
	for _, ref := range r.reads {
		if ref != baseSHA {
			t.Errorf("read at %q, want the base commit", ref)
		}
	}
	bad := &repo{files: map[string]string{".factory/config.yaml": "egress: {allow: ['*.npmjs.org']}\n"}}
	if _, err := Resolve(t.Context(), bad, "acme/app", "main"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a wildcard: err = %v, want ErrInvalid", err)
	}
}

const previewWorkflow = `
name: checked
version: 1
steps:
  - id: implement
    agent: {role: coder}
  - id: open
    action: change-request.open
  - id: preview
    gate: {type: preview, onFail: {goto: implement, maxLoops: 2}}
`

const previewStages = `
e2e: {command: mise run e2e}
stages:
  - name: preview
    driver: flux-preview
    url: http://previews.example/pr-{changeRequest}
    verify: e2e-suite
  - name: staging
    driver: flux
    url: http://staging.example
`

func previewDrivers(t *testing.T) *drivers.Registry {
	t.Helper()
	r, err := drivers.NewRegistry(
		&driverstest.Fake{DriverName: "flux", Caps: drivers.Capabilities{Rollback: true, Health: true}},
		&driverstest.Fake{DriverName: "flux-preview", Caps: drivers.Capabilities{Rollback: true, Health: true, Preview: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// M7 (D126): a workflow with a preview gate snapshots the project's preview
// stage, checked against its driver; without a preview stage the gate has none.
func TestResolvePreviewStage(t *testing.T) {
	files := func(config string) map[string]string {
		return map[string]string{".factory/config.yaml": "workflow: checked\n" + config, ".factory/workflows/checked.yaml": previewWorkflow}
	}
	ds := previewDrivers(t)
	s, err := ResolveFor(t.Context(), &repo{files: files(previewStages)}, "acme/app", "main", "task", ds)
	if err != nil {
		t.Fatal(err)
	}
	if p := s.PreviewStage; p == nil || p.Name != "preview" || p.URLFor(7) != "http://previews.example/pr-7" || p.Verify != config.VerifyE2ESuite {
		t.Errorf("preview stage = %+v", p)
	}
	// The JSON form, which workflow histories record, carries it.
	raw, _ := json.Marshal(s)
	var back Snapshot
	if err := json.Unmarshal(raw, &back); err != nil || back.PreviewStage == nil || back.PreviewStage.URL != s.PreviewStage.URL {
		t.Errorf("round trip = %+v, %v", back.PreviewStage, err)
	}

	if s, err := ResolveFor(t.Context(), &repo{files: files("stages: [{name: staging, driver: flux}]\n")}, "acme/app", "main", "task", ds); err != nil || s.PreviewStage != nil {
		t.Errorf("no preview stage: %+v, %v", s.PreviewStage, err)
	}
	if s, err := ResolveFor(t.Context(), &repo{files: files("")}, "acme/app", "main", "task", ds); err != nil || s.PreviewStage != nil {
		t.Errorf("no stages: %+v, %v", s.PreviewStage, err)
	}
	bad := files("stages: [{name: preview, driver: flux-preview, gate: human}]\n")
	if _, err := ResolveFor(t.Context(), &repo{files: bad}, "acme/app", "main", "task", ds); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "a preview stage has no gate") {
		t.Errorf("invalid preview stage: err = %v, want ErrInvalid", err)
	}
	if _, err := ResolveFor(t.Context(), &repo{files: files(previewStages)}, "acme/app", "main", "task", nil); err == nil || errors.Is(err, ErrInvalid) {
		t.Errorf("no drivers: err = %v, want an error that is not the project's", err)
	}
	// A workflow without a preview gate leaves the stages to releases.
	plain := map[string]string{".factory/config.yaml": "stages: [{name: preview, driver: flux-preview, gate: human}]\n"}
	if s, err := ResolveFor(t.Context(), &repo{files: plain}, "acme/app", "main", "task", ds); err != nil || s.PreviewStage != nil {
		t.Errorf("no preview gate: %+v, %v", s.PreviewStage, err)
	}
}

// M7 (D129): the release agent's role is read at the release commit: the
// repo's own, else the built-in; a broken one is the repo's to fix.
func TestRoleAt(t *testing.T) {
	const sha = "cccccccccccccccccccccccccccccccccccccccc"
	builtin, err := RoleAt(t.Context(), &repo{files: map[string]string{}}, "acme/app", sha, "release-agent")
	if err != nil || builtin.Name != "release-agent" || !strings.Contains(builtin.SystemPrompt, "release agent") {
		t.Fatalf("built-in = %+v, %v", builtin, err)
	}
	r := &repo{files: map[string]string{
		".factory/roles/release-agent.yaml": "name: release-agent\nversion: 3\nsystem_prompt_file: notes.md\n",
		".factory/roles/notes.md":           "Notes for librarians.\n",
	}}
	own, err := RoleAt(t.Context(), r, "acme/app", sha, "release-agent")
	if err != nil || own.Version != 3 || own.SystemPrompt != "Notes for librarians.\n" || !slices.Contains(r.reads, sha) {
		t.Errorf("repo's role = %+v, %v, reads %v", own, err, r.reads)
	}
	r.files[".factory/roles/release-agent.yaml"] = "name: notes\nversion: 1\nsystem_prompt_file: notes.md\n"
	if _, err := RoleAt(t.Context(), r, "acme/app", sha, "release-agent"); !errors.Is(err, ErrInvalid) {
		t.Errorf("misnamed role: err = %v, want ErrInvalid", err)
	}
	broken := &repo{files: map[string]string{}, broken: map[string]bool{".factory/roles/release-agent.yaml": true}}
	if _, err := RoleAt(t.Context(), broken, "acme/app", sha, "release-agent"); err == nil || errors.Is(err, ErrInvalid) {
		t.Errorf("failed read: err = %v, want a retryable error", err)
	}
}
