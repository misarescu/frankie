package sandbox

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func gateSpec() GatePodSpec {
	return GatePodSpec{
		Name: "gc-1-checks-00000004", Namespace: "frankie-agents", Image: "localhost:30500/toolchains/o/r:tc-1",
		RuntimeClassName: "kata", ServiceAccountName: "frankie-agent",
		RunID: "run-1", StepRunID: "step-2", IssueKey: "gc-1",
		Limits:        corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("3Gi")},
		WorkspaceSize: resource.MustParse("10Gi"), Deadline: 30 * time.Minute,
		EgressProxyURL: "http://proxy:3128",
		CloneURL:       "http://frankie-controlplane.frankie.svc:8081/git/o/r.git", Token: "fgp1.secret.sig",
		Branch: "agent/gc-1/add-search", CommitSHA: "0123456789abcdef0123456789abcdef01234567",
		Commands: []string{"mise run lint", "mise run test"},
	}
}

func TestGatePod(t *testing.T) {
	pod, err := GatePod(gateSpec())
	if err != nil {
		t.Fatal(err)
	}
	spec := pod.Spec

	t.Run("runs the commands in order after a checkout of the exact commit", func(t *testing.T) {
		var names []string
		for _, c := range spec.InitContainers {
			names = append(names, c.Name)
		}
		if got := strings.Join(names, ","); got != "clone,check-0,check-1" {
			t.Fatalf("init containers = %s", got)
		}
		if c := spec.InitContainers[1]; strings.Join(c.Command, " ") != "/bin/sh -c mise run lint" || c.WorkingDir != GateRepoDir {
			t.Errorf("check-0 = %v in %s", c.Command, c.WorkingDir)
		}
		clone := spec.InitContainers[0]
		if !strings.Contains(clone.Command[2], `git checkout -q --detach "$GATE_SHA"`) || !strings.Contains(clone.Command[2], `test "$(git rev-parse HEAD)" = "$GATE_SHA"`) {
			t.Errorf("clone does not pin the commit:\n%s", clone.Command[2])
		}
		if !strings.Contains(clone.Command[2], "for attempt in 1 2 3 4 5") {
			t.Errorf("clone is not retried; a new pod's first connection can be refused while NetworkPolicies sync:\n%s", clone.Command[2])
		}
		if envValue(clone.Env, "GATE_SHA") != "0123456789abcdef0123456789abcdef01234567" {
			t.Errorf("GATE_SHA = %q", envValue(clone.Env, "GATE_SHA"))
		}
	})

	t.Run("only the clone container holds the git token", func(t *testing.T) {
		if v := envValue(spec.InitContainers[0].Env, "GIT_CONFIG_VALUE_0"); !strings.HasPrefix(v, "Authorization: Basic ") {
			t.Errorf("clone has no credential header: %q", v)
		}
		for _, c := range append(spec.InitContainers[1:], spec.Containers...) {
			for _, e := range c.Env {
				if strings.HasPrefix(e.Name, "GIT_CONFIG") || strings.Contains(e.Value, "Basic ") || strings.Contains(e.Value, "fgp1.") {
					t.Errorf("container %s carries a credential in %s", c.Name, e.Name)
				}
			}
		}
	})

	t.Run("sandboxed like an agent pod but labeled as a gate", func(t *testing.T) {
		if pod.Labels[RoleLabel] != RoleGate || pod.Labels[StepRunIDLabel] != "step-2" {
			t.Errorf("labels = %v", pod.Labels)
		}
		if *spec.RuntimeClassName != "kata" || *spec.AutomountServiceAccountToken || !*spec.SecurityContext.RunAsNonRoot ||
			spec.RestartPolicy != corev1.RestartPolicyNever || *spec.ActiveDeadlineSeconds != 1800 {
			t.Errorf("pod hardening missing: %+v", spec)
		}
		for _, c := range append(spec.InitContainers, spec.Containers...) {
			if *c.SecurityContext.AllowPrivilegeEscalation || c.SecurityContext.Capabilities.Drop[0] != "ALL" {
				t.Errorf("container %s is not locked down", c.Name)
			}
			if envValue(c.Env, "HTTPS_PROXY") == "" && c.Name != gateDoneContainer {
				t.Errorf("container %s bypasses the egress proxy", c.Name)
			}
		}
	})
}

func TestGatePodRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GatePodSpec)
		want   string
	}{
		{"short sha", func(s *GatePodSpec) { s.CommitSHA = "abc123" }, "full SHA"},
		{"non-agent branch", func(s *GatePodSpec) { s.Branch = "main" }, "agent branch"},
		{"no commands", func(s *GatePodSpec) { s.Commands = nil }, "at least one command"},
		{"blank command", func(s *GatePodSpec) { s.Commands = []string{" "} }, "command 0 is empty"},
		{"credentials in url", func(s *GatePodSpec) { s.CloneURL = "http://u:p@host/git/o/r.git" }, "without credentials"},
		{"no token", func(s *GatePodSpec) { s.Token = "" }, "token"},
		{"no proxy", func(s *GatePodSpec) { s.EgressProxyURL = "" }, "EgressProxyURL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := gateSpec()
			tt.mutate(&s)
			if _, err := GatePod(s); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func envValue(env []corev1.EnvVar, name string) string {
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

func TestGatePodEnvReachesEveryCommand(t *testing.T) {
	s := gateSpec()
	s.Env = map[string]string{"GOPROXY": "http://frankie-athens.frankie.svc:3000"}
	pod, err := GatePod(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range pod.Spec.InitContainers {
		if envValue(c.Env, "GOPROXY") != "http://frankie-athens.frankie.svc:3000" {
			t.Errorf("container %s has no GOPROXY", c.Name)
		}
	}
}
