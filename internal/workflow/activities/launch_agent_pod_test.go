package activities

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	testInput = IssueRunInput{IssueID: "0198f2a4-1111-7000-8000-000000000001", IssueKey: "gc-1", Title: "Add search",
		Spec: "Users can search books.", Repo: "misarescu/Goodreads-Clone", BaseBranch: "main", SourceNumber: 3}
	testRun = StartRunResult{RunID: "0198f2a4-2222-7000-8000-000000000002", StepRunID: "0198f2a4-3333-7000-8000-000000000003", Branch: "agent/gc-1/add-search"}
)

func TestPodNameIsStablePerStep(t *testing.T) {
	a, b := podName(testInput, testRun), podName(testInput, testRun)
	if a != b || a != "gc-1-implement-00000003" {
		t.Errorf("podName = %q, %q", a, b)
	}
}

func TestAgentPodSpecImage(t *testing.T) {
	a := &Activities{Agent: AgentConfig{
		Namespace: "frankie-agents", RunnerImage: "ghcr.io/misarescu/frankie-runner:sha-abc", RuntimeClass: "kata",
		ServiceAccount: "frankie-agent", EgressProxyURL: "http://proxy:3128", PodDeadline: time.Hour,
		Limits:        corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
		WorkspaceSize: resource.MustParse("1Gi"),
	}}
	tests := []struct {
		name, toolchain, want string
	}{
		{"toolchain image when resolved", "localhost:30500/toolchains/o/r:tc-1", "localhost:30500/toolchains/o/r:tc-1"},
		{"base image otherwise", "", "ghcr.io/misarescu/frankie-runner:sha-abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := testRun
			run.ToolchainImage = tt.toolchain
			pod, err := a.agentPodSpec(testInput, run, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := pod.Spec.Containers[0].Image; got != tt.want {
				t.Errorf("image = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentPodSpecCarriesTheSandboxEnv(t *testing.T) {
	a := &Activities{Agent: AgentConfig{
		Namespace: "frankie-agents", RunnerImage: "img", RuntimeClass: "kata", ServiceAccount: "frankie-agent",
		EgressProxyURL: "http://proxy:3128", PodDeadline: time.Hour, WorkspaceSize: resource.MustParse("1Gi"),
		Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
		Env:    map[string]string{"GOPROXY": "http://frankie-athens.frankie.svc:3000"},
	}}
	pod, err := a.agentPodSpec(testInput, testRun, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range pod.Spec.Containers[0].Env {
		found = found || (e.Name == "GOPROXY" && e.Value == "http://frankie-athens.frankie.svc:3000")
	}
	if !found {
		t.Errorf("runner env has no GOPROXY: %v", pod.Spec.Containers[0].Env)
	}
}

func TestPodNameUsesTheDefinitionStep(t *testing.T) {
	run := testRun
	run.Step = &AgentStep{StepID: "fix-review-findings"}
	if got := podName(testInput, run); got != "gc-1-fix-review-findings-00000003" {
		t.Errorf("podName = %q", got)
	}
	long := stepPodName("verylongkey-12345", strings.Repeat("step-", 8), testRun.StepRunID)
	if len(long) > 63 || !strings.HasPrefix(long, "verylongkey-12345-step-") || !strings.HasSuffix(long, "-00000003") || strings.Contains(long, "--") {
		t.Errorf("stepPodName = %q (%d characters)", long, len(long))
	}
}

// M5.2 (D77): read-only steps (reviewers, the refiner) request less CPU than
// coding steps; both keep the same limits.
func TestAgentPodSpecCPURequestByStep(t *testing.T) {
	a := &Activities{Agent: AgentConfig{
		Namespace: "frankie-agents", RunnerImage: "img", RuntimeClass: "kata", ServiceAccount: "frankie-agent",
		EgressProxyURL: "http://proxy:3128", PodDeadline: time.Hour, WorkspaceSize: resource.MustParse("1Gi"),
		Limits:     corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("3Gi")},
		CPURequest: resource.MustParse("500m"), ReviewerCPURequest: resource.MustParse("250m"),
	}}
	for name, tt := range map[string]struct {
		step *AgentStep
		want string
	}{
		"coding step": {&AgentStep{StepID: "implement"}, "500m"},
		"reviewer":    {&AgentStep{StepID: "review", Contract: "review-verdict"}, "250m"},
		"M2 run":      {nil, "500m"},
	} {
		t.Run(name, func(t *testing.T) {
			run := testRun
			run.Step = tt.step
			pod, err := a.agentPodSpec(testInput, run, nil)
			if err != nil {
				t.Fatal(err)
			}
			r := pod.Spec.Containers[0].Resources
			if got := r.Requests.Cpu().String(); got != tt.want {
				t.Errorf("cpu request = %s, want %s", got, tt.want)
			}
			if r.Limits.Cpu().String() != "2" || r.Requests.Memory().String() != "3Gi" {
				t.Errorf("limits %v, requests %v: want cpu limit 2 and memory requested at its limit", r.Limits, r.Requests)
			}
		})
	}
}

// M5.2 (D80): the agent's proxy URL carries the project's egress token.
func TestAgentPodSpecCarriesTheEgressToken(t *testing.T) {
	s := egressSigner(t)
	a := &Activities{Egress: s, Log: quietLog(), Agent: AgentConfig{
		Namespace: "frankie-agents", RunnerImage: "img", RuntimeClass: "kata", ServiceAccount: "frankie-agent",
		EgressProxyURL: "http://proxy:3128", PodDeadline: time.Hour, WorkspaceSize: resource.MustParse("1Gi"),
		Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
	}}
	in := testInput
	in.EgressAllow = []string{"nodejs.org"}
	pod, err := a.agentPodSpec(in, testRun, nil)
	if err != nil {
		t.Fatal(err)
	}
	var proxy string
	for _, e := range pod.Spec.Containers[0].Env {
		if e.Name == "HTTPS_PROXY" {
			proxy = e.Value
		}
	}
	if g, ok := egressGrant(t, s, proxy); !ok || g.Hosts[0] != "nodejs.org" {
		t.Errorf("HTTPS_PROXY = %q, want the project's egress token", proxy)
	}
}

// D100, D101: agent pods go to the nodes the deployment names, e.g. the agent
// pool, and tolerate its taint.
func TestAgentPodSpecNodeSelector(t *testing.T) {
	a := &Activities{Agent: AgentConfig{
		Namespace: "frankie-agents", RunnerImage: "img", RuntimeClass: "kata", ServiceAccount: "frankie-agent",
		EgressProxyURL: "http://proxy:3128", PodDeadline: time.Hour,
		Limits:              corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
		WorkspaceSize:       resource.MustParse("1Gi"),
		NodeSelector:        map[string]string{"k8s.scaleway.com/pool-name": "frankie-agents"},
		Tolerations:         []corev1.Toleration{{Key: "frankie.dev/agents", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}},
		GuestMemoryHeadroom: resource.MustParse("512Mi"),
	}}
	pod, err := a.agentPodSpec(testInput, testRun, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pod.Spec.NodeSelector["k8s.scaleway.com/pool-name"] != "frankie-agents" {
		t.Errorf("node selector = %v", pod.Spec.NodeSelector)
	}
	if len(pod.Spec.Tolerations) != 1 || pod.Spec.Tolerations[0].Key != "frankie.dev/agents" {
		t.Errorf("tolerations = %v", pod.Spec.Tolerations)
	}
	if pod.Spec.Resources == nil || pod.Spec.Resources.Limits.Memory().Cmp(resource.MustParse("1536Mi")) != 0 { // D103
		t.Errorf("pod resources = %v", pod.Spec.Resources)
	}
}

// D104: a read-only step's pod gets the reviewer memory limit, with the guest
// headroom on top (D103); a coder's pod keeps the agent limit.
func TestAgentPodSpecReviewerMemory(t *testing.T) {
	a := &Activities{Agent: AgentConfig{
		Namespace: "frankie-agents", RunnerImage: "img", RuntimeClass: "kata", ServiceAccount: "frankie-agent",
		EgressProxyURL: "http://proxy:3128", PodDeadline: time.Hour, SessionTimeout: time.Hour,
		Limits:              corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")},
		WorkspaceSize:       resource.MustParse("1Gi"),
		ReviewerMemory:      resource.MustParse("2Gi"),
		GuestMemoryHeadroom: resource.MustParse("512Mi"),
	}}
	reviewer := testRun
	reviewer.Step = &AgentStep{StepID: "code-reviewer", Contract: "review-verdict", AllowedTools: []string{"read"}}
	pod, err := a.agentPodSpec(testInput, reviewer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pod.Spec.Containers[0].Resources.Limits.Memory(); got.Cmp(resource.MustParse("2Gi")) != 0 {
		t.Errorf("reviewer memory = %s, want 2Gi", got)
	}
	if got := pod.Spec.Resources.Limits.Memory(); got.Cmp(resource.MustParse("2560Mi")) != 0 {
		t.Errorf("reviewer pod-level memory = %s, want 2560Mi", got)
	}
	if got := a.Agent.Limits.Memory(); got.Cmp(resource.MustParse("4Gi")) != 0 {
		t.Errorf("the agent limits were changed: %s", got)
	}

	coder := testRun
	coder.Step = &AgentStep{StepID: "implement"}
	if pod, err = a.agentPodSpec(testInput, coder, nil); err != nil {
		t.Fatal(err)
	}
	if got := pod.Spec.Containers[0].Resources.Limits.Memory(); got.Cmp(resource.MustParse("4Gi")) != 0 {
		t.Errorf("coder memory = %s, want 4Gi", got)
	}
}
