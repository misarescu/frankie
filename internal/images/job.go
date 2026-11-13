package images

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Labels on every build Job and its ConfigMap. RoleLabel is what the build
// namespace's NetworkPolicies select.
const (
	RoleLabel     = "frankie.dev/role"
	RoleBuild     = "toolchain-build"
	ImageTagLabel = "frankie.dev/toolchain-tag"
	contextMount  = "/ctx"
	// contextDir holds the build context as plain files. A ConfigMap volume's
	// files are symlinks into a hidden ..data directory, which BuildKit, sending
	// only what the Dockerfile's COPY patterns match, does not reliably follow:
	// with a mise.lock present, mise.toml went missing from the context.
	contextDir     = "/tmp/context"
	agentHomeMise  = "/home/agent/.local/share/mise"
	agentUID       = 10001
	toolchainDir   = "/home/agent/toolchain"
	dockerfileName = "Dockerfile"
	buildContainer = "buildkit"
)

// BuildConfig describes how toolchain images are built; it comes from the
// platform configuration, not from the repo being built.
type BuildConfig struct {
	Namespace          string
	BuildKitImage      string // a moby/buildkit image (rootful, D31)
	RuntimeClassName   string // kata: the VM is the isolation for the relaxed container profile
	ServiceAccountName string // has no RBAC bindings
	// PushHost is where the build pushes, reachable from the build pod and
	// served over plain HTTP, e.g. frankie-registry.frankie.svc:5000.
	PushHost       string
	EgressProxyURL string
	NoProxy        []string
	Deadline       time.Duration
	Limits         corev1.ResourceList // cpu, memory
	// ScratchSize bounds the BuildKit cache and layer storage (emptyDir).
	ScratchSize resource.Quantity
	// Env is set for the build's RUN steps, e.g. GOPROXY for the in-cluster Go
	// module proxy (D37). It does not change the image tag: it says where
	// downloads come from, not what is installed.
	Env map[string]string
}

func (c BuildConfig) validate() error {
	var errs []error
	for _, p := range []struct{ field, value string }{
		{"Namespace", c.Namespace}, {"BuildKitImage", c.BuildKitImage}, {"RuntimeClassName", c.RuntimeClassName},
		{"ServiceAccountName", c.ServiceAccountName}, {"PushHost", c.PushHost}, {"EgressProxyURL", c.EgressProxyURL},
	} {
		if p.value == "" {
			errs = append(errs, fmt.Errorf("%s is required", p.field))
		}
	}
	if c.Deadline <= 0 {
		errs = append(errs, errors.New("deadline is required"))
	}
	if c.ScratchSize.IsZero() {
		errs = append(errs, errors.New("scratch size is required"))
	}
	for _, r := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		if _, ok := c.Limits[r]; !ok {
			errs = append(errs, fmt.Errorf("%s limit is required", r))
		}
	}
	return errors.Join(errs...)
}

// BuildRequest is one toolchain image to build.
type BuildRequest struct {
	Repository string // registry repository, from Repository()
	Inputs     Inputs
}

// JobName is stable per toolchain tag, so concurrent or retried requests for the
// same image meet in one Job instead of building twice.
func (r BuildRequest) JobName() string {
	return "tc-" + strings.TrimPrefix(r.Inputs.Tag(), "tc-")[:20]
}

// Image is the repository the build pushes to, by digest only (D45): the
// control plane tags the image once it verified and recorded the digest.
func (r BuildRequest) Image(c BuildConfig) string {
	return fmt.Sprintf("%s/%s", c.PushHost, r.Repository)
}

// metadataFile is where buildctl writes the build result, including
// containerimage.digest. The build container then copies it to its
// termination message, which the kubelet records in the pod status: the
// build's own RUN steps, sandboxed by BuildKit, cannot write either file.
// buildctl writes atomically (temp file and rename), which fails on the
// kubelet's bind-mounted termination log, hence the copy.
const (
	metadataFile       = "/tmp/build-metadata.json"
	terminationLogPath = "/dev/termination-log"
	// buildScript copies the context out of the ConfigMap, runs buildctl with
	// the container's args, then reports.
	buildScript = `mkdir -p ` + contextDir + ` && cp -L ` + contextMount + `/* ` + contextDir + `/ && ` +
		`buildctl-daemonless.sh "$@" && cat ` + metadataFile + ` > ` + terminationLogPath
)

// Dockerfile is the build recipe: the base image plus the tools mise.toml pins,
// installed where the agent user finds them at run time. args are build
// arguments the RUN step sees (values come from --opt build-arg).
func Dockerfile(base string, args []string) string {
	var argLines strings.Builder
	for _, a := range args {
		fmt.Fprintf(&argLines, "ARG %s\n", a)
	}
	return fmt.Sprintf(`FROM %s
%sENV MISE_DATA_DIR=%[3]s \
    MISE_TRUSTED_CONFIG_PATHS=/workspace \
    PATH=%[3]s/shims:$PATH
COPY --chown=%[4]d:%[4]d mise.toml mise.lock* %[5]s/
RUN cd %[5]s && mise trust && mise install
`, base, argLines.String(), agentHomeMise, agentUID, toolchainDir)
}

// BuildResources returns the ConfigMap holding the build context and the Job
// that builds it. The context is generated by the control plane from files it
// read itself; the build pod never receives credentials or repo access.
func BuildResources(c BuildConfig, r BuildRequest) (*corev1.ConfigMap, *batchv1.Job, error) {
	if err := c.validate(); err != nil {
		return nil, nil, fmt.Errorf("images: %w", err)
	}
	if err := r.Inputs.Validate(); err != nil {
		return nil, nil, fmt.Errorf("images: %w", err)
	}
	if !repositoryRE.MatchString(r.Repository) {
		return nil, nil, fmt.Errorf("images: invalid repository %q", r.Repository)
	}
	name := r.JobName()
	labels := map[string]string{RoleLabel: RoleBuild, ImageTagLabel: r.Inputs.Tag()}

	data := map[string]string{
		dockerfileName: Dockerfile(r.Inputs.Base, slices.Sorted(maps.Keys(c.Env))),
		"mise.toml":    string(r.Inputs.MiseToml),
	}
	if len(r.Inputs.MiseLock) > 0 {
		data["mise.lock"] = string(r.Inputs.MiseLock)
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.Namespace, Labels: labels},
		Data:       data,
	}

	noProxy := strings.Join(append([]string{"localhost", "127.0.0.1", ".svc", ".cluster.local"}, c.NoProxy...), ",")
	limits := c.Limits.DeepCopy()
	limits[corev1.ResourceEphemeralStorage] = c.ScratchSize
	args := []string{
		"build",
		"--frontend", "dockerfile.v0",
		"--local", "context=" + contextDir,
		"--local", "dockerfile=" + contextDir,
		"--opt", "build-arg:HTTPS_PROXY=" + c.EgressProxyURL,
		"--opt", "build-arg:HTTP_PROXY=" + c.EgressProxyURL,
		"--opt", "build-arg:NO_PROXY=" + noProxy,
		"--output", fmt.Sprintf("type=image,name=%s,push=true,push-by-digest=true,registry.insecure=true", r.Image(c)),
		"--metadata-file", metadataFile,
	}
	for _, k := range slices.Sorted(maps.Keys(c.Env)) {
		args = append(args, "--opt", "build-arg:"+k+"="+c.Env[k])
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			// A failed build is reported to the caller, which decides whether to retry.
			BackoffLimit:            ptrTo(int32(0)),
			ActiveDeadlineSeconds:   ptrTo(int64(c.Deadline.Seconds())),
			TTLSecondsAfterFinished: ptrTo(int32(600)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RuntimeClassName:             ptrTo(c.RuntimeClassName),
					ServiceAccountName:           c.ServiceAccountName,
					AutomountServiceAccountToken: ptrTo(false),
					EnableServiceLinks:           ptrTo(false),
					RestartPolicy:                corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:    buildContainer,
						Image:   c.BuildKitImage,
						Command: []string{"/bin/sh", "-c", buildScript, buildContainer},
						Args:    args,
						Env: []corev1.EnvVar{
							{Name: "HTTPS_PROXY", Value: c.EgressProxyURL},
							{Name: "https_proxy", Value: c.EgressProxyURL},
							{Name: "HTTP_PROXY", Value: c.EgressProxyURL},
							{Name: "http_proxy", Value: c.EgressProxyURL},
							{Name: "NO_PROXY", Value: noProxy},
							{Name: "no_proxy", Value: noProxy},
						},
						Resources: corev1.ResourceRequirements{Limits: limits, Requests: limits.DeepCopy()},
						// D31: BuildKit runs as privileged root. Rootless mode needs
						// newuidmap with file capabilities, which Kata's virtiofs root does
						// not honor, and an unprivileged rootful BuildKit cannot create
						// cgroups (read-only /sys/fs/cgroup). The Kata VM is the isolation
						// boundary, so the build namespace does not enforce Pod Security
						// "restricted" (see the chart).
						SecurityContext: &corev1.SecurityContext{
							Privileged: ptrTo(true),
							RunAsUser:  ptrTo(int64(0)),
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "context", MountPath: contextMount, ReadOnly: true},
							{Name: "scratch", MountPath: "/var/lib/buildkit"},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "context", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: name},
						}}},
						{Name: "scratch", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptrTo(c.ScratchSize)}}},
					},
				},
			},
		},
	}
	return cm, job, nil
}

// repositoryRE is the OCI distribution name grammar for a repository path.
var repositoryRE = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)

// Repository maps a factory repo ("misarescu/Goodreads-Clone") to its registry
// repository for toolchain images. Registry names are lowercase.
func Repository(repo string) (string, error) {
	r := "toolchains/" + strings.ToLower(repo)
	if !repositoryRE.MatchString(r) {
		return "", fmt.Errorf("images: repo %q cannot be mapped to a registry repository", repo)
	}
	return r, nil
}

func ptrTo[T any](v T) *T { return &v }
