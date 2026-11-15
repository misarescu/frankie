package images

import (
	"cmp"
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
	// SafeToEvictAnnotation "false" on the build pod keeps a cluster autoscaler
	// from evicting a build mid-way when it removes an underused node (D99).
	SafeToEvictAnnotation = "cluster-autoscaler.kubernetes.io/safe-to-evict"
	contextMount          = "/ctx"
	contextArchive        = "context.tar"
	// outMount holds the layer between the install and the push.
	outMount      = "/out"
	agentHomeMise = "/home/agent/.local/share/mise"
	agentHome     = "/home/agent"
	agentUID      = 10001
	toolchainDir  = "/home/agent/toolchain"
	// imageBuildBin ships in the runner base image (D30, D81), so every image
	// a build extends has it, where the agent user cannot replace it.
	imageBuildBin    = "/usr/local/bin/frankie-imagebuild"
	installContainer = "install"
	pushContainer    = "push"
)

// BuildConfig describes how toolchain images are built; it comes from the
// platform configuration, not from the repo being built.
type BuildConfig struct {
	Namespace          string
	RuntimeClassName   string // kata
	ServiceAccountName string // has no RBAC bindings
	// PushHost is where the build pushes, reachable from the build pod and
	// served over plain HTTP, e.g. frankie-registry.frankie.svc:5000.
	PushHost       string
	EgressProxyURL string
	NoProxy        []string
	Deadline       time.Duration
	Limits         corev1.ResourceList // cpu, memory
	// CPURequest is what the scheduler reserves; zero means the CPU limit.
	// Builds mostly wait on I/O, and memory is requested at its limit (D77).
	CPURequest resource.Quantity
	// ScratchSize bounds what a build writes: the install in the container's
	// own filesystem and the layer archive (emptyDir).
	ScratchSize resource.Quantity
	// Env is set for the install, e.g. GOPROXY for the in-cluster Go module
	// proxy (D37). It does not change the image tag: it says where downloads
	// come from, not what is installed.
	Env map[string]string
}

func (c BuildConfig) validate() error {
	var errs []error
	for _, p := range []struct{ field, value string }{
		{"Namespace", c.Namespace}, {"RuntimeClassName", c.RuntimeClassName},
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
	if lim := c.Limits[corev1.ResourceCPU]; c.CPURequest.Sign() < 0 || c.CPURequest.Cmp(lim) > 0 {
		errs = append(errs, fmt.Errorf("cpu request %s must be between 0 and the cpu limit %s", c.CPURequest.String(), lim.String()))
	}
	return errors.Join(errs...)
}

// BuildRequest is one image to build.
type BuildRequest struct {
	Repository string // registry repository, from Repository() or DependencyRepository()
	Spec       Spec
	// EgressProxyURL replaces the config's for this build, e.g. with the
	// project's egress token (D80); empty means the config's.
	EgressProxyURL string
	// Progress, when set, gets the build's log as it grows (D93). It does not
	// change which Job builds the image.
	Progress Progress `json:"-"`
}

// JobName is stable per tag, so concurrent or retried requests for the same
// image meet in one Job instead of building twice.
func (r BuildRequest) JobName() string {
	tag := r.Spec.Tag() // tc-<hash> or dp-<hash>
	return tag[:min(len(tag), 23)]
}

// Image is the repository the build pushes to, by digest only (D45): the
// control plane tags the image once it verified and recorded the digest.
func (r BuildRequest) Image(c BuildConfig) string {
	return fmt.Sprintf("%s/%s", c.PushHost, r.Repository)
}

// BuildResources returns the ConfigMap holding the build's files and the Job
// that builds the image by appending a layer (D81). The files are generated
// by the control plane from what it read itself; the build pod never receives
// credentials or repo access.
//
// Both containers run the image being extended as the unprivileged agent
// user: "install" runs the recipe and writes what it changed to a layer
// archive; "push" appends the archive to the base image in the registry and
// reports the digest in its termination message, which the kubelet records
// (D45). The install cannot write that message: it is another container's.
func BuildResources(c BuildConfig, r BuildRequest) (*corev1.ConfigMap, *batchv1.Job, error) {
	if err := c.validate(); err != nil {
		return nil, nil, fmt.Errorf("images: %w", err)
	}
	if r.Spec == nil {
		return nil, nil, errors.New("images: no image to build")
	}
	if err := r.Spec.Validate(); err != nil {
		return nil, nil, fmt.Errorf("images: %w", err)
	}
	if !repositoryRE.MatchString(r.Repository) {
		return nil, nil, fmt.Errorf("images: invalid repository %q", r.Repository)
	}
	name := r.JobName()
	labels := map[string]string{RoleLabel: RoleBuild, ImageTagLabel: r.Spec.Tag()}

	recipe, err := r.Spec.Recipe(c.PushHost)
	if err != nil {
		return nil, nil, fmt.Errorf("images: %w", err)
	}
	archive, err := contextTar(recipe.Files)
	if err != nil {
		return nil, nil, err
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.Namespace, Labels: labels},
		BinaryData: map[string][]byte{contextArchive: archive},
	}

	noProxy := strings.Join(append([]string{"localhost", "127.0.0.1", ".svc", ".cluster.local"}, c.NoProxy...), ",")
	proxy := cmp.Or(r.EgressProxyURL, c.EgressProxyURL)
	proxyEnv := []corev1.EnvVar{
		{Name: "HTTPS_PROXY", Value: proxy},
		{Name: "https_proxy", Value: proxy},
		{Name: "HTTP_PROXY", Value: proxy},
		{Name: "http_proxy", Value: proxy},
		{Name: "NO_PROXY", Value: noProxy},
		{Name: "no_proxy", Value: noProxy},
	}
	installEnv := append(slices.Clone(proxyEnv), corev1.EnvVar{Name: "HOME", Value: agentHome})
	for _, env := range []map[string]string{c.Env, recipe.RunEnv} {
		for _, k := range slices.Sorted(maps.Keys(env)) {
			installEnv = append(installEnv, corev1.EnvVar{Name: k, Value: env[k]})
		}
	}

	limits := c.Limits.DeepCopy()
	limits[corev1.ResourceEphemeralStorage] = c.ScratchSize
	requests := limits.DeepCopy()
	if !c.CPURequest.IsZero() {
		requests[corev1.ResourceCPU] = c.CPURequest
	}
	resources := corev1.ResourceRequirements{Limits: limits, Requests: requests}
	layer := outMount + "/layer.tar"

	install := []string{
		imageBuildBin, "layer",
		"--context", contextMount + "/" + contextArchive,
		"--dir", recipe.Dir,
		"--run", recipe.Run,
		"--out", layer,
		"--exclude", contextMount,
		"--exclude", outMount,
	}
	push := []string{
		imageBuildBin, "push",
		"--base", recipe.Base,
		"--layer", layer,
		"--target", r.Image(c),
		"--insecure", c.PushHost,
		"--created-by", recipe.CreatedBy,
	}
	if recipe.Mirror != "" {
		push = append(push, "--mirror", c.PushHost+"/"+recipe.Mirror)
	}
	for _, k := range slices.Sorted(maps.Keys(recipe.ImageEnv)) {
		push = append(push, "--env", k+"="+recipe.ImageEnv[k])
	}
	if recipe.PrependPath != "" {
		push = append(push, "--prepend-path", recipe.PrependPath)
	}
	mounts := []corev1.VolumeMount{{Name: "out", MountPath: outMount}}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			// A failed build is reported to the caller, which decides whether to retry.
			BackoffLimit:            ptrTo(int32(0)),
			ActiveDeadlineSeconds:   ptrTo(int64(c.Deadline.Seconds())),
			TTLSecondsAfterFinished: ptrTo(int32(600)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: map[string]string{SafeToEvictAnnotation: "false"}},
				Spec: corev1.PodSpec{
					RuntimeClassName:             ptrTo(c.RuntimeClassName),
					ServiceAccountName:           c.ServiceAccountName,
					AutomountServiceAccountToken: ptrTo(false),
					EnableServiceLinks:           ptrTo(false),
					RestartPolicy:                corev1.RestartPolicyNever,
					// Pod Security "restricted", like agent pods: nothing in a
					// build needs root (D81 supersedes D31's privileged BuildKit).
					// No fsGroup: it would set the setgid bit on the install's
					// directories.
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptrTo(true),
						RunAsUser:      ptrTo(int64(agentUID)),
						RunAsGroup:     ptrTo(int64(agentUID)),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					InitContainers: []corev1.Container{{
						Name:            installContainer,
						Image:           recipe.Image,
						Command:         install,
						Env:             installEnv,
						Resources:       resources,
						SecurityContext: buildContainerSecurity(),
						VolumeMounts:    append(slices.Clone(mounts), corev1.VolumeMount{Name: "context", MountPath: contextMount, ReadOnly: true}),
					}},
					Containers: []corev1.Container{{
						Name:            pushContainer,
						Image:           recipe.Image,
						Command:         push,
						Env:             proxyEnv,
						Resources:       resources,
						SecurityContext: buildContainerSecurity(),
						VolumeMounts:    mounts,
					}},
					Volumes: []corev1.Volume{
						{Name: "context", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: name},
						}}},
						{Name: "out", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptrTo(c.ScratchSize)}}},
					},
				},
			},
		},
	}
	return cm, job, nil
}

func buildContainerSecurity() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptrTo(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
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
