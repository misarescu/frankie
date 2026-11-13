package sandbox

import (
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// RoleGate labels command gate pods; their NetworkPolicy differs from agents'
// (no LLM gateway).
const RoleGate = "gate"

// Container names in a gate pod. Commands run as init containers in order, so
// each one's exit code is recorded by the kubelet in the pod status and cannot
// be forged by what the checked code prints (invariant 2).
const (
	GateCloneContainer = "clone"
	gateDoneContainer  = "done"
)

// GateCommandContainer is the name of the container running command i.
func GateCommandContainer(i int) string { return fmt.Sprintf("check-%d", i) }

// GateRepoDir is where the gate pod checks out the commit.
const GateRepoDir = "/workspace/repo"

// cloneScript checks out exactly the commit under test. It is the only
// container holding the git proxy token, and it runs only git.
//
// The clone is the pod's first connection, made as soon as the pod starts, and
// the node's NetworkPolicy controller rejects a new pod's traffic until it has
// installed the pod's rules (measured on the homelab: the first connection is
// refused within 3 ms, the next one 60 ms later succeeds). So the clone is
// retried; anything that keeps failing still fails the gate.
const cloneScript = `set -eu
for attempt in 1 2 3 4 5; do
  rm -rf ` + GateRepoDir + `
  git clone --no-tags --single-branch --branch "$GATE_BRANCH" -- "$GATE_URL" ` + GateRepoDir + ` && break
  [ "$attempt" = 5 ] && exit 1
  sleep "$attempt"
done
cd ` + GateRepoDir + `
git checkout -q --detach "$GATE_SHA"
test "$(git rev-parse HEAD)" = "$GATE_SHA"
`

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// GatePodSpec describes one command gate pod (FR-SB-1, docs/spec/03).
type GatePodSpec struct {
	Name               string
	Namespace          string
	Image              string // the toolchain image of the commit under test
	RuntimeClassName   string
	ServiceAccountName string
	RunID              string
	StepRunID          string
	IssueKey           string
	Limits             corev1.ResourceList
	WorkspaceSize      resource.Quantity
	Deadline           time.Duration
	EgressProxyURL     string
	NoProxy            []string
	CloneURL           string // git proxy URL of the repo
	Token              string // read-only git proxy token (D33)
	Branch             string // agent branch holding the commit
	CommitSHA          string // the commit under test
	Commands           []string
	Services           []Service         // sidecars; their limits come out of Limits
	Env                map[string]string // extra environment for every container, e.g. GOPROXY
}

func (s GatePodSpec) validate() error {
	base := PodSpec{
		Name: s.Name, Namespace: s.Namespace, Image: s.Image, RuntimeClassName: s.RuntimeClassName,
		ServiceAccountName: s.ServiceAccountName, RunID: s.RunID, StepRunID: s.StepRunID, IssueKey: s.IssueKey,
		Limits: s.Limits, WorkspaceSize: s.WorkspaceSize, Deadline: s.Deadline, EgressProxyURL: s.EgressProxyURL,
	}
	errs := []error{base.validate()}
	if u, err := url.Parse(s.CloneURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		errs = append(errs, fmt.Errorf("clone url %q must be an http(s) URL without credentials", s.CloneURL))
	}
	if s.Token == "" {
		errs = append(errs, errors.New("token is required"))
	}
	if !strings.HasPrefix(s.Branch, "agent/") {
		errs = append(errs, fmt.Errorf("branch %q is not an agent branch", s.Branch))
	}
	if !shaRE.MatchString(s.CommitSHA) {
		errs = append(errs, fmt.Errorf("commit %q is not a full SHA", s.CommitSHA))
	}
	if len(s.Commands) == 0 {
		errs = append(errs, errors.New("at least one command is required"))
	}
	for i, c := range s.Commands {
		if strings.TrimSpace(c) == "" {
			errs = append(errs, fmt.Errorf("command %d is empty", i))
		}
	}
	if msgs := validation.IsDNS1123Label(GateCommandContainer(len(s.Commands))); len(msgs) > 0 {
		errs = append(errs, fmt.Errorf("too many commands: %s", strings.Join(msgs, "; ")))
	}
	return errors.Join(errs...)
}

// GatePod returns the pod for a command gate: a fresh checkout of the pushed
// commit, then each command in order, all in the commit's toolchain image.
func GatePod(s GatePodSpec) (*corev1.Pod, error) {
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	limits, err := carve(s.Limits, s.Services)
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	limits[corev1.ResourceEphemeralStorage] = s.WorkspaceSize
	sidecars, sidecarVolumes := serviceContainers(s.Services)
	resources := corev1.ResourceRequirements{Limits: limits, Requests: limits.DeepCopy()}
	mounts := []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}
	env := append(proxyEnv(s.EgressProxyURL, s.NoProxy),
		// Toolchain images trust /workspace too; repeated so the base image does.
		corev1.EnvVar{Name: "MISE_TRUSTED_CONFIG_PATHS", Value: "/workspace"},
		corev1.EnvVar{Name: "CI", Value: "true"},
	)
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		env = append(env, corev1.EnvVar{Name: k, Value: s.Env[k]})
	}

	cred := base64.StdEncoding.EncodeToString([]byte("x-token:" + s.Token))
	cloneEnv := append(append([]corev1.EnvVar{}, env...),
		corev1.EnvVar{Name: "GATE_URL", Value: s.CloneURL},
		corev1.EnvVar{Name: "GATE_BRANCH", Value: s.Branch},
		corev1.EnvVar{Name: "GATE_SHA", Value: s.CommitSHA},
		corev1.EnvVar{Name: "GIT_TERMINAL_PROMPT", Value: "0"},
		corev1.EnvVar{Name: "GIT_CONFIG_NOSYSTEM", Value: "1"},
		corev1.EnvVar{Name: "GIT_CONFIG_COUNT", Value: "1"},
		corev1.EnvVar{Name: "GIT_CONFIG_KEY_0", Value: "http." + s.CloneURL + ".extraHeader"},
		corev1.EnvVar{Name: "GIT_CONFIG_VALUE_0", Value: "Authorization: Basic " + cred},
	)
	// Services start first and stay up while the checkout and the commands run.
	inits := append(sidecars, corev1.Container{
		Name: GateCloneContainer, Image: s.Image, Command: []string{"/bin/sh", "-c", cloneScript},
		Env: cloneEnv, Resources: resources, SecurityContext: containerSecurity(), VolumeMounts: mounts,
	})
	for i, c := range s.Commands {
		inits = append(inits, corev1.Container{
			Name: GateCommandContainer(i), Image: s.Image, Command: []string{"/bin/sh", "-c", c}, WorkingDir: GateRepoDir,
			Env: env, Resources: resources, SecurityContext: containerSecurity(), VolumeMounts: mounts,
		})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.Name,
			Namespace: s.Namespace,
			Labels: map[string]string{
				RoleLabel:      RoleGate,
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
			SecurityContext:              podSecurity(),
			InitContainers:               inits,
			Containers: []corev1.Container{{
				Name: gateDoneContainer, Image: s.Image, Command: []string{"/bin/true"},
				Resources: resources, SecurityContext: containerSecurity(),
			}},
			Volumes: append([]corev1.Volume{{
				Name:         "workspace",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptrTo(s.WorkspaceSize)}},
			}}, sidecarVolumes...),
		},
	}, nil
}
