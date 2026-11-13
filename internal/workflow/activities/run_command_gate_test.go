package activities

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/sandbox"
)

const pushedSHA = "89abcdef0123456789abcdef0123456789abcdef"

// finishGatePods makes every created gate pod end with the given exit codes for
// clone, check-0, check-1, ...; a nil entry means that container never ran.
func finishGatePods(kube *fake.Clientset, created *[]*corev1.Pod, exits ...*int32) {
	kube.PrependReactor("create", "pods", func(act k8stesting.Action) (bool, runtime.Object, error) {
		p := act.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		*created = append(*created, p.DeepCopy())
		phase := corev1.PodSucceeded
		for i, c := range p.Spec.InitContainers {
			s := corev1.ContainerStatus{Name: c.Name}
			if i < len(exits) && exits[i] != nil {
				s.State.Terminated = &corev1.ContainerStateTerminated{ExitCode: *exits[i]}
				if *exits[i] != 0 {
					phase = corev1.PodFailed
				}
			}
			p.Status.InitContainerStatuses = append(p.Status.InitContainerStatuses, s)
		}
		p.Status.Phase = phase
		return false, p, nil
	})
}

func code(c int32) *int32 { return &c }

type gateFixture struct {
	a       *Activities
	signer  *gitproxy.Signer
	kube    *fake.Clientset
	created []*corev1.Pod
	in      IssueRunInput
	run     StartRunResult
	session SessionResult
}

func newGateFixture(t *testing.T, exits ...*int32) *gateFixture {
	t.Helper()
	return newGateFixtureWithFiles(t, map[string]string{}, exits...)
}

func newGateFixtureWithFiles(t *testing.T, files map[string]string, exits ...*int32) *gateFixture {
	t.Helper()
	st, in := readyIssue(t)
	signer, err := gitproxy.NewSigner([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	f := &gateFixture{signer: signer, kube: fake.NewClientset(), in: in}
	finishGatePods(f.kube, &f.created, exits...)
	f.a = &Activities{
		Store: st, Kube: f.kube, Signer: signer, Log: quietLog(), GitHub: fakeRepo(t, files),
		Services: ServicesConfig{MaxPerPod: 1, Limits: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("512Mi")}},
		Agent: AgentConfig{
			Namespace: "frankie-agents", RunnerImage: "ghcr.io/misarescu/frankie-runner:sha-abc", RuntimeClass: "kata",
			ServiceAccount: "frankie-agent", EgressProxyURL: "http://proxy:3128", GitProxyURL: "http://cp:8081",
		},
		Gate: GateConfig{
			Limits:        corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("3Gi")},
			WorkspaceSize: resource.MustParse("10Gi"), Deadline: 30 * time.Minute,
		},
	}
	v, err := activityEnv(f.a).ExecuteActivity(f.a.StartRun, in)
	if err != nil {
		t.Fatal(err)
	}
	_ = v.Get(&f.run)
	// The agent says it succeeded and pushed.
	f.session = SessionResult{Status: "succeeded", Result: "All tests pass.", Branch: f.run.Branch, CommitSHA: pushedSHA, Pushed: true}
	return f
}

func (f *gateFixture) gate(t *testing.T) GateOutcome {
	t.Helper()
	v, err := activityEnv(f.a).ExecuteActivity(f.a.RunCommandGate, f.in, f.run, f.session)
	if err != nil {
		t.Fatalf("RunCommandGate: %v", err)
	}
	var out GateOutcome
	if err := v.Get(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// M2 done-when (docs/spec/10): the agent claimed success, but the tests fail when
// the gate re-runs them from the pushed commit, so the gate fails.
func TestRunCommandGateFailsWhenTestsFailDespiteAgentClaim(t *testing.T) {
	f := newGateFixture(t, code(0), code(0), code(1))
	out := f.gate(t)
	if out.Passed || out.Summary != "mise run test exited 1" {
		t.Fatalf("outcome = %+v", out)
	}

	stepID := uuid.MustParse(out.StepRunID)
	step, _ := f.a.Store.GetStepRun(t.Context(), stepID)
	if step.Status != domain.ExecFailed || step.Kind != domain.StepGate || step.CommitSHA != pushedSHA {
		t.Errorf("step = %+v", step)
	}
	results, _ := f.a.Store.ListGateResultsForRun(t.Context(), uuid.MustParse(f.run.RunID))
	if len(results) != 1 || results[0].Verdict != domain.GateFail || results[0].Type != domain.GateCommand {
		t.Fatalf("results = %+v", results)
	}
	var d domain.CommandGateDetails
	_ = json.Unmarshal(results[0].Details, &d)
	if len(d.Commands) != 2 || *d.Commands[0].ExitCode != 0 || *d.Commands[1].ExitCode != 1 || d.Image != f.a.Agent.RunnerImage {
		t.Errorf("details = %+v", d)
	}
	if pods, _ := f.kube.CoreV1().Pods("frankie-agents").List(t.Context(), metav1.ListOptions{}); len(pods.Items) != 0 {
		t.Errorf("gate pod not deleted")
	}
}

func TestRunCommandGatePod(t *testing.T) {
	f := newGateFixture(t, code(0), code(0), code(0))
	if out := f.gate(t); !out.Passed || out.Summary != "all checks passed" {
		t.Fatalf("outcome = %+v", out)
	}
	if len(f.created) != 1 {
		t.Fatalf("pods created = %d", len(f.created))
	}
	pod := f.created[0]
	if pod.Labels[sandbox.RoleLabel] != sandbox.RoleGate {
		t.Errorf("labels = %v", pod.Labels)
	}
	var cmds []string
	for _, c := range pod.Spec.InitContainers[1:] {
		cmds = append(cmds, c.Command[2])
	}
	if strings.Join(cmds, ",") != "mise run lint,mise run test" {
		t.Errorf("commands = %v", cmds)
	}

	// The token the clone container gets is read-only and bound to this gate step.
	var header string
	for _, e := range pod.Spec.InitContainers[0].Env {
		if e.Name == "GIT_CONFIG_VALUE_0" {
			header = e.Value
		}
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Authorization: Basic "))
	grant, err := f.signer.Verify(strings.TrimPrefix(string(raw), "x-token:"))
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if !grant.ReadOnly || grant.Repo != f.in.Repo || grant.IssueKey != f.in.IssueKey {
		t.Errorf("grant = %+v; want read-only for the issue", grant)
	}
}

func TestRunCommandGateFailures(t *testing.T) {
	tests := []struct {
		name  string
		exits []*int32
		want  string
	}{
		{"checkout failed", []*int32{code(128)}, "checking out the commit failed"},
		{"lint failed, so test never ran", []*int32{code(0), code(2)}, "mise run lint exited 2; mise run test did not run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newGateFixture(t, tt.exits...)
			out := f.gate(t)
			if out.Passed || !strings.HasPrefix(out.Summary, tt.want) {
				t.Errorf("outcome = %+v, want summary starting %q", out, tt.want)
			}
		})
	}
}

// A retry after the gate finished reports the recorded verdict without
// running the commands again.
func TestRunCommandGateRetryReusesTheResult(t *testing.T) {
	f := newGateFixture(t, code(0), code(0), code(1))
	first := f.gate(t)
	second := f.gate(t)
	if second != first || len(f.created) != 1 {
		t.Errorf("second = %+v (first %+v), pods created = %d", second, first, len(f.created))
	}
}

// FR-SB-9: the services the pushed commit declares run next to the commands.
func TestRunCommandGateRunsDeclaredServices(t *testing.T) {
	f := newGateFixtureWithFiles(t, map[string]string{".factory/config.yaml": "services: [postgres:17]\n"}, code(0), code(0), code(0), code(0))
	if out := f.gate(t); !out.Passed {
		t.Fatalf("outcome = %+v", out)
	}
	var names []string
	for _, c := range f.created[0].Spec.InitContainers {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "svc-postgres,clone,check-0,check-1" {
		t.Errorf("init containers = %s", got)
	}
}

// A broken declaration in the agent's commit fails the gate without a pod.
func TestRunCommandGateFailsOnInvalidProjectConfig(t *testing.T) {
	f := newGateFixtureWithFiles(t, map[string]string{".factory/config.yaml": "services: [postgres:17, redis:7]\n"})
	out := f.gate(t)
	if out.Passed || !strings.Contains(out.Summary, "the platform allows 1") {
		t.Errorf("outcome = %+v", out)
	}
	if len(f.created) != 0 {
		t.Errorf("pods created = %d, want none", len(f.created))
	}
	results, _ := f.a.Store.ListGateResultsForRun(t.Context(), uuid.MustParse(f.run.RunID))
	if len(results) != 1 || results[0].Verdict != domain.GateFail {
		t.Errorf("results = %+v", results)
	}
}

// Interpret's gate runs the definition's commands, under its step ID, and
// tells the agent what failed.
func TestRunGateUsesTheDefinition(t *testing.T) {
	f := newGateFixture(t, code(0), code(0), code(3))
	stepRunID := startStep(t, f.a, StepStart{RunID: f.run.RunID, StepID: "unit-tests", Kind: domain.StepGate, Attempt: 2})
	req := GateRequest{RunID: f.run.RunID, StepRunID: stepRunID, StepID: "unit-tests", Commands: []string{"go vet ./...", "go test ./..."},
		Timeout: 10 * time.Minute, Branch: f.session.Branch, CommitSHA: pushedSHA}
	v, err := activityEnv(f.a).ExecuteActivity(f.a.RunGate, f.in, req)
	if err != nil {
		t.Fatal(err)
	}
	var out GateOutcome
	_ = v.Get(&out)
	if out.Passed || out.StepRunID != stepRunID || out.Summary != "go test ./... exited 3" {
		t.Fatalf("outcome = %+v", out)
	}
	if !strings.Contains(out.Feedback, "`go test ./...` exited 3") || strings.Contains(out.Feedback, "go vet") {
		t.Errorf("feedback = %q", out.Feedback)
	}
	if len(f.created) != 1 {
		t.Fatalf("pods = %d", len(f.created))
	}
	p := f.created[0]
	if !strings.Contains(p.Name, "-unit-tests-") || p.Spec.ActiveDeadlineSeconds == nil || *p.Spec.ActiveDeadlineSeconds != 600 {
		t.Errorf("pod %s deadline %v", p.Name, p.Spec.ActiveDeadlineSeconds)
	}
	var cmds []string
	for _, c := range p.Spec.InitContainers[1:] {
		cmds = append(cmds, strings.Join(c.Command, " "))
	}
	if joined := strings.Join(cmds, "|"); !strings.Contains(joined, "go vet ./...") || !strings.Contains(joined, "go test ./...") || strings.Contains(joined, "mise run") {
		t.Errorf("commands = %v", cmds)
	}
	step, _ := f.a.Store.GetStepRun(t.Context(), uuid.MustParse(stepRunID))
	if step.Status != domain.ExecFailed || step.CommitSHA != pushedSHA || step.Attempt != 2 {
		t.Errorf("step = %+v", step)
	}
}

func TestFeedbackTail(t *testing.T) {
	long := strings.Repeat("noise line\n", 2000) + "--- FAIL: TestSearch\n"
	got := feedback(domain.CommandGateDetails{Commands: []domain.CommandResult{{Command: "mise run test", ExitCode: ptr(1), LogTail: long}}})
	if len(got) > feedbackTailBytes+200 || !strings.Contains(got, "--- FAIL: TestSearch") || !strings.HasPrefix(got, "`mise run test` exited 1") {
		t.Errorf("feedback is %d bytes:\n%.200s", len(got), got)
	}
	if feedback(domain.CommandGateDetails{Commands: []domain.CommandResult{{Command: "x", ExitCode: ptr(0)}}}) != "" {
		t.Error("a passed gate has feedback")
	}
}
