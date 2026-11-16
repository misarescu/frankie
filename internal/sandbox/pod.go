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

// SafeToEvictAnnotation set to "false" keeps a cluster autoscaler from evicting
// the pod when it removes an underused node (D99): a step evicted mid-run fails.
// Every pod the sandbox launches carries it; without an autoscaler it does nothing.
const SafeToEvictAnnotation = "cluster-autoscaler.kubernetes.io/safe-to-evict"

// ValidateNodeSelector checks a node selector's keys and values are valid
// label keys and values. An empty selector is valid: the pod may run anywhere.
func ValidateNodeSelector(sel map[string]string) error {
	var errs []error
	for _, k := range slices.Sorted(maps.Keys(sel)) {
		if msgs := validation.IsQualifiedName(k); len(msgs) > 0 {
			errs = append(errs, fmt.Errorf("node selector key %q: %s", k, strings.Join(msgs, "; ")))
		}
		if msgs := validation.IsValidLabelValue(sel[k]); len(msgs) > 0 {
			errs = append(errs, fmt.Errorf("node selector value %q: %s", sel[k], strings.Join(msgs, "; ")))
		}
	}
	return errors.Join(errs...)
}

// ValidateTolerations checks tolerations the way the API server would, so a bad
// one fails when the configuration loads rather than when a step's pod is
// created. No tolerations is valid: the pod then keeps off every tainted node.
func ValidateTolerations(tols []corev1.Toleration) error {
	var errs []error
	for i, t := range tols {
		if t.Key != "" {
			if msgs := validation.IsQualifiedName(t.Key); len(msgs) > 0 {
				errs = append(errs, fmt.Errorf("toleration %d: key %q: %s", i, t.Key, strings.Join(msgs, "; ")))
			}
		}
		switch t.Operator {
		case corev1.TolerationOpEqual, "":
			if t.Key == "" {
				errs = append(errs, fmt.Errorf("toleration %d: operator Equal needs a key", i))
			}
			if msgs := validation.IsValidLabelValue(t.Value); len(msgs) > 0 {
				errs = append(errs, fmt.Errorf("toleration %d: value %q: %s", i, t.Value, strings.Join(msgs, "; ")))
			}
		case corev1.TolerationOpExists:
			if t.Value != "" {
				errs = append(errs, fmt.Errorf("toleration %d: operator Exists takes no value", i))
			}
		default:
			errs = append(errs, fmt.Errorf("toleration %d: operator %q is not Equal or Exists", i, t.Operator))
		}
		switch t.Effect {
		case "", corev1.TaintEffectNoSchedule, corev1.TaintEffectPreferNoSchedule, corev1.TaintEffectNoExecute:
		default:
			errs = append(errs, fmt.Errorf("toleration %d: effect %q is not NoSchedule, PreferNoSchedule or NoExecute", i, t.Effect))
		}
		if t.TolerationSeconds != nil && t.Effect != corev1.TaintEffectNoExecute {
			errs = append(errs, fmt.Errorf("toleration %d: tolerationSeconds needs effect NoExecute", i))
		}
	}
	return errors.Join(errs...)
}

// noEvict returns the annotations every sandbox pod carries.
func noEvict() map[string]string { return map[string]string{SafeToEvictAnnotation: "false"} }

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
	CPURequest         resource.Quantity   // reserved of the CPU limit; zero means the limit (D77)
	WorkspaceSize      resource.Quantity   // emptyDir and ephemeral-storage limit
	Deadline           time.Duration       // activeDeadlineSeconds (FR-SB-8)
	EgressProxyURL     string              // e.g. http://frankie-egress-proxy.frankie.svc:3128
	NoProxy            []string            // cluster-internal names reached directly
	Env                map[string]string   // extra environment
	Services           []Service           // sidecars; their limits come out of Limits
	NodeSelector       map[string]string   // nodes it may run on, e.g. the agent pool (D100); empty means any
	Tolerations        []corev1.Toleration // taints it tolerates, e.g. the agent pool's (D101)
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
	if err := validateCPURequest(s.CPURequest, s.Limits); err != nil {
		errs = append(errs, err)
	}
	if err := ValidateNodeSelector(s.NodeSelector); err != nil {
		errs = append(errs, err)
	}
	if err := ValidateTolerations(s.Tolerations); err != nil {
		errs = append(errs, err)
	}
	if s.WorkspaceSize.IsZero() {
		errs = append(errs, errors.New("workspace size is required"))
	}
	if s.Deadline <= 0 {
		errs = append(errs, errors.New("deadline is required"))
	}
	if err := validateServices(s.Services); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// AgentPod returns the pod for an agent step.
func AgentPod(s PodSpec) (*corev1.Pod, error) {
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	limits, err := carve(s.Limits, s.Services)
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	limits[corev1.ResourceEphemeralStorage] = s.WorkspaceSize
	sidecars, sidecarVolumes := serviceContainers(s.Services)
	requests := requestsFor(limits, s.CPURequest)

	env := proxyEnv(s.EgressProxyURL, s.NoProxy)
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		env = append(env, corev1.EnvVar{Name: k, Value: s.Env[k]})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        s.Name,
			Namespace:   s.Namespace,
			Annotations: noEvict(),
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
			NodeSelector:                 s.NodeSelector,
			Tolerations:                  s.Tolerations,
			AutomountServiceAccountToken: ptrTo(false),
			EnableServiceLinks:           ptrTo(false),
			RestartPolicy:                corev1.RestartPolicyNever,
			ActiveDeadlineSeconds:        ptrTo(int64(s.Deadline.Seconds())),
			SecurityContext:              podSecurity(),
			InitContainers:               sidecars,
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
				SecurityContext: containerSecurity(),
				VolumeMounts:    []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}},
			}},
			Volumes: append([]corev1.Volume{{
				Name: "workspace",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptrTo(s.WorkspaceSize)},
				},
			}}, sidecarVolumes...),
		},
	}, nil
}

func ptrTo[T any](v T) *T { return &v }

func validateCPURequest(req resource.Quantity, limits corev1.ResourceList) error {
	if req.Sign() < 0 {
		return fmt.Errorf("cpu request %s is negative", req.String())
	}
	if lim, ok := limits[corev1.ResourceCPU]; ok && req.Cmp(lim) > 0 {
		return fmt.Errorf("cpu request %s exceeds the cpu limit %s", req.String(), lim.String())
	}
	return nil
}

// requestsFor is what a container with these limits requests (D77): memory
// at its limit, so a pod is never killed for memory the node lent it, and CPU
// at cpuRequest (capped at the limit), so pods that mostly wait do not keep
// others Pending. A zero cpuRequest requests the CPU limit.
func requestsFor(limits corev1.ResourceList, cpuRequest resource.Quantity) corev1.ResourceList {
	requests := limits.DeepCopy()
	if lim, ok := limits[corev1.ResourceCPU]; ok && !cpuRequest.IsZero() && cpuRequest.Cmp(lim) < 0 {
		requests[corev1.ResourceCPU] = cpuRequest
	}
	return requests
}

// proxyEnv routes a pod's traffic through the egress proxy (D20), except
// cluster-internal names.
func proxyEnv(proxyURL string, extraNoProxy []string) []corev1.EnvVar {
	noProxy := strings.Join(append([]string{"localhost", "127.0.0.1", ".svc", ".cluster.local"}, extraNoProxy...), ",")
	return []corev1.EnvVar{
		{Name: "HTTPS_PROXY", Value: proxyURL},
		{Name: "https_proxy", Value: proxyURL},
		{Name: "HTTP_PROXY", Value: proxyURL},
		{Name: "http_proxy", Value: proxyURL},
		{Name: "NO_PROXY", Value: noProxy},
		{Name: "no_proxy", Value: noProxy},
	}
}

// podSecurity runs every container as the non-root agent user.
func podSecurity() *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		RunAsNonRoot:   ptrTo(true),
		RunAsUser:      ptrTo(int64(AgentUID)),
		RunAsGroup:     ptrTo(int64(AgentUID)),
		FSGroup:        ptrTo(int64(AgentUID)),
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

// containerSecurity drops every capability and privilege escalation.
func containerSecurity() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptrTo(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

// runnerArgs points the image entrypoint at RunnerWorkdir; a later flag wins.
func runnerArgs(command []string) []string {
	if command != nil {
		return nil
	}
	return []string{"--workdir", RunnerWorkdir}
}
