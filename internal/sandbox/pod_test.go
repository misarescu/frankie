package sandbox

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func validSpec() PodSpec {
	return PodSpec{
		Name: "frk-42-implement-1", Namespace: "frankie-agents", Image: "ghcr.io/misarescu/frankie-runner:sha-abc",
		RuntimeClassName: "kata", ServiceAccountName: "frankie-agent",
		RunID: "0198f2a4-1111-7000-8000-000000000001", StepRunID: "0198f2a4-2222-7000-8000-000000000002", IssueKey: "frk-42",
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("3Gi"),
		},
		WorkspaceSize:  resource.MustParse("10Gi"),
		Deadline:       2 * time.Hour,
		EgressProxyURL: "http://frankie-egress-proxy.frankie.svc:3128",
		NoProxy:        []string{"frankie-controlplane.frankie"},
		Env:            map[string]string{"ANTHROPIC_BASE_URL": "http://gateway:4000", "A_FIRST": "1"},
	}
}

// The guarantees of docs/spec/05 hold for every pod the builder returns.
func TestAgentPodSandboxGuarantees(t *testing.T) {
	pod, err := AgentPod(validSpec())
	if err != nil {
		t.Fatalf("AgentPod: %v", err)
	}
	spec := pod.Spec
	c := spec.Containers[0]

	checks := []struct {
		name string
		ok   bool
	}{
		// FR-SB-1: Kata, non-root, no host mounts.
		{"kata runtime class", spec.RuntimeClassName != nil && *spec.RuntimeClassName == "kata"},
		{"runs as non-root", *spec.SecurityContext.RunAsNonRoot && *spec.SecurityContext.RunAsUser == AgentUID},
		{"no host namespaces", !spec.HostNetwork && !spec.HostPID && !spec.HostIPC},
		{"only an emptyDir volume", len(spec.Volumes) == 1 && spec.Volumes[0].EmptyDir != nil && spec.Volumes[0].HostPath == nil},
		// FR-SB-5: no cluster credentials.
		{"no service account token", spec.AutomountServiceAccountToken != nil && !*spec.AutomountServiceAccountToken},
		{"dedicated service account", spec.ServiceAccountName == "frankie-agent"},
		{"no service env injection", spec.EnableServiceLinks != nil && !*spec.EnableServiceLinks},
		// Pod Security "restricted".
		{"no privilege escalation", c.SecurityContext.AllowPrivilegeEscalation != nil && !*c.SecurityContext.AllowPrivilegeEscalation},
		{"all capabilities dropped", len(c.SecurityContext.Capabilities.Drop) == 1 && c.SecurityContext.Capabilities.Drop[0] == "ALL"},
		{"no privileged container", c.SecurityContext.Privileged == nil || !*c.SecurityContext.Privileged},
		{"runtime default seccomp", spec.SecurityContext.SeccompProfile.Type == corev1.SeccompProfileTypeRuntimeDefault},
		// FR-SB-7: limits, including ephemeral storage, and Guaranteed QoS.
		{"cpu limit", c.Resources.Limits.Cpu().String() == "2"},
		{"memory limit", c.Resources.Limits.Memory().String() == "3Gi"},
		{"ephemeral storage limit", c.Resources.Limits.StorageEphemeral().String() == "10Gi"},
		{"requests equal limits", c.Resources.Requests.Memory().Equal(*c.Resources.Limits.Memory()) && c.Resources.Requests.Cpu().Equal(*c.Resources.Limits.Cpu())},
		{"workspace size limit", spec.Volumes[0].EmptyDir.SizeLimit.String() == "10Gi"},
		// FR-SB-8: labels, deadline, no restarts.
		{"role label for NetworkPolicies", pod.Labels[RoleLabel] == RoleAgent},
		{"run and step labels", pod.Labels[RunIDLabel] != "" && pod.Labels[StepRunIDLabel] != "" && pod.Labels[IssueKeyLabel] == "frk-42"},
		{"deadline", spec.ActiveDeadlineSeconds != nil && *spec.ActiveDeadlineSeconds == 7200},
		{"never restarts", spec.RestartPolicy == corev1.RestartPolicyNever},
		{"workspace mounted", len(c.VolumeMounts) == 1 && c.VolumeMounts[0].MountPath == "/workspace"},
		{"image entrypoint by default", c.Command == nil},
	}
	for _, ch := range checks {
		if !ch.ok {
			t.Errorf("%s: not satisfied", ch.name)
		}
	}
}

// D20: all egress goes through the proxy; cluster-internal names bypass it.
func TestAgentPodProxyEnvironment(t *testing.T) {
	pod, err := AgentPod(validSpec())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	var order []string
	for _, e := range pod.Spec.Containers[0].Env {
		env[e.Name] = e.Value
		order = append(order, e.Name)
	}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if env[k] != "http://frankie-egress-proxy.frankie.svc:3128" {
			t.Errorf("%s = %q", k, env[k])
		}
	}
	for _, want := range []string{".svc", ".cluster.local", "localhost", "frankie-controlplane.frankie"} {
		if !strings.Contains(env["NO_PROXY"], want) || env["no_proxy"] != env["NO_PROXY"] {
			t.Errorf("NO_PROXY %q missing %q", env["NO_PROXY"], want)
		}
	}
	if env["ANTHROPIC_BASE_URL"] != "http://gateway:4000" {
		t.Error("extra env not passed")
	}
	// Extra env comes after the proxy settings, in a stable order.
	if tail := strings.Join(order[len(order)-2:], ","); tail != "ANTHROPIC_BASE_URL,A_FIRST" {
		t.Errorf("extra env order = %s", tail)
	}
}

func TestAgentPodRejectsIncompleteSpecs(t *testing.T) {
	for name, mutate := range map[string]func(*PodSpec){
		"no runtime class":     func(s *PodSpec) { s.RuntimeClassName = "" },
		"no service account":   func(s *PodSpec) { s.ServiceAccountName = "" },
		"no proxy":             func(s *PodSpec) { s.EgressProxyURL = "" },
		"no deadline":          func(s *PodSpec) { s.Deadline = 0 },
		"no memory limit":      func(s *PodSpec) { delete(s.Limits, corev1.ResourceMemory) },
		"no cpu limit":         func(s *PodSpec) { delete(s.Limits, corev1.ResourceCPU) },
		"no workspace size":    func(s *PodSpec) { s.WorkspaceSize = resource.Quantity{} },
		"invalid name":         func(s *PodSpec) { s.Name = "Not_A_DNS_Name" },
		"invalid label value":  func(s *PodSpec) { s.IssueKey = "frk 42" },
		"missing step run id":  func(s *PodSpec) { s.StepRunID = "" },
		"missing image":        func(s *PodSpec) { s.Image = "" },
		"missing namespace":    func(s *PodSpec) { s.Namespace = "" },
		"missing run id":       func(s *PodSpec) { s.RunID = "" },
		"missing issue key":    func(s *PodSpec) { s.IssueKey = "" },
		"negative deadline":    func(s *PodSpec) { s.Deadline = -time.Second },
		"label value too long": func(s *PodSpec) { s.RunID = strings.Repeat("a", 64) },
	} {
		s := validSpec()
		s.Limits = s.Limits.DeepCopy()
		mutate(&s)
		if _, err := AgentPod(s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAgentPodDoesNotShareLimits(t *testing.T) {
	s := validSpec()
	if _, err := AgentPod(s); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Limits[corev1.ResourceEphemeralStorage]; ok {
		t.Error("AgentPod mutated the caller's limits")
	}
}
