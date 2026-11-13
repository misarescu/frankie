package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
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
			if s.Source != "built-in" || s.Workflow.Name != "default-build" || len(s.Roles) != 2 {
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

func TestResolveInvalid(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"bad project config", map[string]string{".factory/config.yaml": "workflows: fast\n"}, "unknown field"},
		{"unknown workflow", map[string]string{".factory/config.yaml": "workflow: nope\n"}, `workflow "nope" is neither in .factory/workflows nor built in`},
		{"invalid definition", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": "name: fast\nversion: 1\nsteps: []\n"}, "at least one step"},
		{"definition named differently", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": strings.Replace(loopWorkflow, "name: fast", "name: slow", 1)}, `is named "slow"`},
		{"missing role", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": strings.Replace(loopWorkflow, "role: coder", "role: ghost", 1)}, "no built-in role"},
		{"role prompt missing", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": loopWorkflow,
			".factory/roles/coder.yaml": "name: coder\nversion: 1\nsystem_prompt_file: coder.md\n"}, ".factory/roles/coder.md not found"},
		{"role named differently", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": loopWorkflow,
			".factory/roles/coder.yaml": "name: hacker\nversion: 1\nsystem_prompt_file: coder.md\n", ".factory/roles/coder.md": "x"}, `is named "hacker"`},
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

// A failed read is retried later, never reported as the repo's mistake.
func TestResolveReadFailuresAreNotInvalid(t *testing.T) {
	files := map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": loopWorkflow,
		".factory/roles/coder.yaml": "name: coder\nversion: 1\nsystem_prompt_file: coder.md\n", ".factory/roles/coder.md": "x"}
	for _, path := range []string{".factory/config.yaml", ".factory/workflows/fast.yaml", ".factory/roles/coder.yaml", ".factory/roles/coder.md"} {
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
