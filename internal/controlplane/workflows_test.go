package controlplane

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/workflow"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

type repoFiles struct {
	files map[string]string
	err   error
}

func (r repoFiles) ResolveRef(context.Context, string, string) (string, error) { return "sha", r.err }

func (r repoFiles) FileAt(_ context.Context, _, path, _ string) ([]byte, bool, error) {
	s, ok := r.files[path]
	return []byte(s), ok, nil
}

const runnableDefinition = `name: fast
version: 1
steps:
  - id: implement
    agent: {role: coder}
  - id: checks
    gate: {type: command, onFail: {goto: implement, maxLoops: 2}}
  - id: open
    action: change-request.open
`

func TestChooseWorkflow(t *testing.T) {
	in := activities.IssueRunInput{IssueKey: "gc-9", Repo: "acme/app", BaseBranch: "main"}
	tests := []struct {
		name      string
		files     map[string]string
		wantError string // the snapshot error the run will fail with
	}{
		{"no workflow selected runs default-build", map[string]string{".factory/config.yaml": "services: [postgres:17]\n"}, ""},
		{"a runnable workflow runs on Interpret", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": runnableDefinition}, ""},
		{"an invalid selection fails on the run", map[string]string{".factory/config.yaml": "workflow: nope\n"}, "neither in .factory/workflows nor built in"},
		{"a definition the interpreter cannot run yet fails on the run", map[string]string{".factory/config.yaml": "workflow: gated\n",
			".factory/workflows/gated.yaml": strings.Replace(runnableDefinition, "name: fast", "name: gated", 1) + "  - id: approve\n    gate: {type: human}\n"}, "human gates are not supported yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf, arg, err := chooseWorkflow(t.Context(), repoFiles{files: tt.files}, in)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := arg.(workflow.InterpretInput)
			if reflect.ValueOf(wf).Pointer() != reflect.ValueOf(workflow.Interpret).Pointer() || !ok || !reflect.DeepEqual(got.Issue, in) {
				t.Fatalf("wf = %v, arg = %+v; want Interpret", wf, arg)
			}
			if tt.wantError == "" && (got.Snapshot.Error != "" || got.Snapshot.Workflow.Name == "") {
				t.Errorf("snapshot = %+v", got.Snapshot)
			}
			if tt.wantError != "" && !strings.Contains(got.Snapshot.Error, tt.wantError) {
				t.Errorf("snapshot error = %q, want %q", got.Snapshot.Error, tt.wantError)
			}
		})
	}

	t.Run("a read failure is retried by the next poll", func(t *testing.T) {
		if _, _, err := chooseWorkflow(t.Context(), repoFiles{err: errors.New("github: 502")}, in); err == nil {
			t.Fatal("no error")
		}
	})
}
