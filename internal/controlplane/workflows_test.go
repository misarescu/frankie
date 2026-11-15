package controlplane

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/config"
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
	models := Models{Default: "anthropic/claude-sonnet-5-5", Providers: config.Providers{{Name: "anthropic", Models: []string{"claude-sonnet-5-5"}}}}
	tests := []struct {
		name      string
		files     map[string]string
		wantError string // the snapshot error the run will fail with
	}{
		{"no workflow selected runs default-build", map[string]string{".factory/config.yaml": "services: [postgres:17]\n"}, ""},
		{"a runnable workflow runs on Interpret", map[string]string{".factory/config.yaml": "workflow: fast\n", ".factory/workflows/fast.yaml": runnableDefinition}, ""},
		{"an invalid selection fails on the run", map[string]string{".factory/config.yaml": "workflow: nope\n"}, "neither in .factory/workflows nor built in"},
		{"a definition with an action that does not exist yet fails on the run", map[string]string{".factory/config.yaml": "workflow: shipping\n",
			".factory/workflows/shipping.yaml": strings.Replace(runnableDefinition, "name: fast", "name: shipping", 1) + "  - id: ship\n    action: release.create\n"}, `"release.create" is not available yet`},
		{"a model the gateway does not serve fails on the run", map[string]string{".factory/config.yaml": "workflow: fast\n",
			".factory/workflows/fast.yaml": strings.Replace(runnableDefinition, "{role: coder}", "{role: coder, model: anthropic/claude-opus-5-5}", 1)},
			"the platform does not serve this workflow's models: steps[0].agent: model anthropic/claude-opus-5-5 is not served"},
		{"a repo role defaulting to an unserved model fails on the run", map[string]string{".factory/roles/coder.yaml": "name: coder\nversion: 1\nsystem_prompt_file: c.md\ndefault_model: anthropic/claude-haiku-5-5\n",
			".factory/roles/c.md": "Code.\n"}, "role coder default model anthropic/claude-haiku-5-5 is not served"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf, arg, err := chooseWorkflow(t.Context(), repoFiles{files: tt.files}, models, false, in)
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
		if _, _, err := chooseWorkflow(t.Context(), repoFiles{err: errors.New("github: 502")}, models, false, in); err == nil {
			t.Fatal("no error")
		}
	})
}

// M5.2 (D80): the hosts a project adds reach the run's activities, read at
// start; a deployment that does not allow them fails the run with the reason.
func TestChooseWorkflowEgress(t *testing.T) {
	in := activities.IssueRunInput{IssueKey: "gc-1", Repo: "acme/app", BaseBranch: "main"}
	models := Models{Default: "anthropic/claude-sonnet-5-5", Providers: config.Providers{{Name: "anthropic", Models: []string{"claude-sonnet-5-5"}}}}
	files := repoFiles{files: map[string]string{".factory/config.yaml": "egress: {allow: [nodejs.org]}\n"}}

	_, arg, err := chooseWorkflow(t.Context(), files, models, true, in)
	if err != nil {
		t.Fatal(err)
	}
	got := arg.(workflow.InterpretInput)
	if got.Snapshot.Error != "" || !reflect.DeepEqual(got.Issue.EgressAllow, []string{"nodejs.org"}) {
		t.Errorf("allowed: issue egress %v, snapshot error %q", got.Issue.EgressAllow, got.Snapshot.Error)
	}

	_, arg, err = chooseWorkflow(t.Context(), files, models, false, in)
	if err != nil {
		t.Fatal(err)
	}
	got = arg.(workflow.InterpretInput)
	if !strings.Contains(got.Snapshot.Error, "egressProxy.projectAllow") || got.Issue.EgressAllow != nil {
		t.Errorf("refused: issue egress %v, snapshot error %q", got.Issue.EgressAllow, got.Snapshot.Error)
	}

	_, arg, _ = chooseWorkflow(t.Context(), repoFiles{files: map[string]string{}}, models, false, in)
	if got := arg.(workflow.InterpretInput); got.Snapshot.Error != "" || got.Issue.EgressAllow != nil {
		t.Errorf("no hosts: %+v", got)
	}
}

// M6 (D92): the project's merge approval, overridden by the issue's labels,
// is fixed in the snapshot when the run starts.
func TestChooseWorkflowMergeApproval(t *testing.T) {
	models := Models{Default: "anthropic/claude-sonnet-5-5", Providers: config.Providers{{Name: "anthropic", Models: []string{"claude-sonnet-5-5"}}}}
	required := repoFiles{files: map[string]string{".factory/config.yaml": "mergeApproval: required\n"}}
	tests := []struct {
		name   string
		files  repoFiles
		labels []string
		want   bool
	}{
		{"off by default", repoFiles{files: map[string]string{}}, nil, false},
		{"the project requires it", required, []string{"factory"}, true},
		{"a label lifts it", required, []string{config.LabelNoMergeApproval}, false},
		{"a label asks for it", repoFiles{files: map[string]string{}}, []string{config.LabelNeedsMergeApproval}, true},
	}
	for _, tt := range tests {
		in := activities.IssueRunInput{IssueKey: "gc-1", Repo: "acme/app", BaseBranch: "main", Labels: tt.labels}
		_, arg, err := chooseWorkflow(t.Context(), tt.files, models, false, in)
		if err != nil {
			t.Fatal(err)
		}
		if got := arg.(workflow.InterpretInput).Snapshot; got.Error != "" || got.MergeApproval != tt.want {
			t.Errorf("%s: merge approval %v (error %q), want %v", tt.name, got.MergeApproval, got.Error, tt.want)
		}
	}
}
