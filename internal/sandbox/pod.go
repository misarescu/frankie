// Package sandbox builds the Kubernetes pods agent steps run in
// (docs/spec/05-sandbox-and-toolchains.md). Every agent pod gets the same
// guarantees from one place: Kata runtime class, no service account token, no
// host access, non-root, all capabilities dropped, resource and time limits, and
// egress only through the egress proxy.
package sandbox

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Labels on every agent pod (FR-SB-8). RoleLabel is what NetworkPolicies select.
const (
	RoleLabel      = "frankie.dev/role"
	RoleAgent      = "agent"
	RunIDLabel     = "frankie.dev/run-id"
	StepRunIDLabel = "frankie.dev/step-run-id"
	IssueKeyLabel  = "frankie.dev/issue"
)

// RunnerPort is where the runner serves RunnerService.
const RunnerPort = 7070

// AgentUID is the non-root user of the runner image.
const AgentUID = 10001

// RunnerWorkdir is where the runner clones the repo: a directory it creates
// itself inside the workspace volume, so the agent user owns it. Kubernetes
// creates the volume root as root-owned, which git rejects ("dubious ownership").
const RunnerWorkdir = "/workspace/repo"

// PodSpec describes one agent pod.
type PodSpec struct {
	Name               string
	Namespace          string
	Image              string
	RuntimeClassName   string // "kata"
	ServiceAccountName string // has no RBAC bindings (FR-SB-5)
	RunID              string
	StepRunID          string
	IssueKey           string
	Limits             corev1.ResourceList // cpu and memory, from the profile (FR-SB-7)
	WorkspaceSize      resource.Quantity   // emptyDir and ephemeral-storage limit
	Deadline           time.Duration       // activeDeadlineSeconds (FR-SB-8)
	EgressProxyURL     string              // e.g. http://frankie-egress-proxy.frankie.svc:3128
	NoProxy            []string            // cluster-internal names reached directly
	Env                map[string]string   // extra environment
	Command            []string            // overrides the image entrypoint; tests only
}

func (s PodSpec) validate() error {
	var errs []error
	for _, p := range []struct{ field, value string }{
		{"Name", s.Name}, {"Namespace", s.Namespace}, {"Image", s.Image}, {"RuntimeClassName", s.RuntimeClassName},
		{"ServiceAccountName", s.ServiceAccountName}, {"RunID", s.RunID}, {"StepRunID", s.StepRunID},
		{"IssueKey", s.IssueKey}, {"EgressProxyURL", s.EgressProxyURL},
	} {
		if p.value == "" {
			errs = append(errs, fmt.Errorf("%s is required", p.field))
		}
	}
	if msgs := validation.IsDNS1123Subdomain(s.Name); len(msgs) > 0 {
		errs = append(errs, fmt.Errorf("name %q: %s", s.Name, strings.Join(msgs, "; ")))
	}
	for _, v := range []string{s.RunID, s.StepRunID, s.IssueKey} {
		if msgs := validation.IsValidLabelValue(v); len(msgs) > 0 {
			errs = append(errs, fmt.Errorf("label value %q: %s", v, strings.Join(msgs, "; ")))
		}
	}
	if _, ok := s.Limits[corev1.ResourceCPU]; !ok {
		errs = append(errs, errors.New("cpu limit is required"))
	}
	if _, ok := s.Limits[corev1.ResourceMemory]; !ok {
		errs = append(errs, errors.New("memory limit is required"))
	}
	if s.WorkspaceSize.IsZero() {
		errs = append(errs, errors.New("workspace size is required"))
	}
	if s.Deadline <= 0 {
		errs = append(errs, errors.New("deadline is required"))
	}
	return errors.Join(errs...)
}

// AgentPod returns the pod for an agent step.
func AgentPod(s PodSpec) (*corev1.Pod, error) {
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	limits := s.Limits.DeepCopy()
	limits[corev1.ResourceEphemeralStorage] = s.WorkspaceSize
	// Requests equal limits: agent pods are Guaranteed and never overcommit the node.
	requests := limits.DeepCopy()

	noProxy := strings.Join(append([]string{"localhost", "127.0.0.1", ".svc", ".cluster.local"}, s.NoProxy...), ",")
	env := []corev1.EnvVar{
		{Name: "HTTPS_PROXY", Value: s.EgressProxyURL},
		{Name: "https_proxy", Value: s.EgressProxyURL},
		{Name: "HTTP_PROXY", Value: s.EgressProxyURL},
		{Name: "http_proxy", Value: s.EgressProxyURL},
		{Name: "NO_PROXY", Value: noProxy},
		{Name: "no_proxy", Value: noProxy},
	}
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		env = append(env, corev1.EnvVar{Name: k, Value: s.Env[k]})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name,
			Namespace: s.Namespace,
			Labels: map[string]string{
				RoleLabel:      RoleAgent,
				RunIDLabel:     s.RunID,
				StepRunIDLabel: s.StepRunID,
				IssueKeyLabel:  s.IssueKey,
			},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName:             ptrTo(s.RuntimeClassName),
			ServiceAccountName:           s.ServiceAccountName,
			AutomountServiceAccountToken: ptrTo(false),
			EnableServiceLinks:           ptrTo(false),
			RestartPolicy:                corev1.RestartPolicyNever,
			ActiveDeadlineSeconds:        ptrTo(int64(s.Deadline.Seconds())),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptrTo(true),
				RunAsUser:      ptrTo(int64(AgentUID)),
				RunAsGroup:     ptrTo(int64(AgentUID)),
				FSGroup:        ptrTo(int64(AgentUID)),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:    "runner",
				Image:   s.Image,
				Command: s.Command,
				Args:    runnerArgs(s.Command),
				Ports:   []corev1.ContainerPort{{Name: "runner", ContainerPort: RunnerPort}},
				Env:     env,
				Resources: corev1.ResourceRequirements{
					Limits:   limits,
					Requests: requests,
				},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptrTo(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}},
			}},
			Volumes: []corev1.Volume{{
				Name: "workspace",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptrTo(s.WorkspaceSize)},
				},
			}},
		},
	}, nil
}

func ptrTo[T any](v T) *T { return &v }

// runnerArgs points the image entrypoint at RunnerWorkdir; a later flag wins.
func runnerArgs(command []string) []string {
	if command != nil {
		return nil
	}
	return []string{"--workdir", RunnerWorkdir}
}
