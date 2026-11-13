package activities

import (
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
