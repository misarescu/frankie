package images

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func testConfig() BuildConfig {
	return BuildConfig{
		Namespace: "frankie-build", RuntimeClassName: "kata",
		ServiceAccountName: "frankie-build", PushHost: "frankie-registry.frankie.svc:5000",
		EgressProxyURL: "http://egress.frankie.svc:3128", Deadline: 30 * time.Minute,
		Limits:      corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("3Gi")},
		ScratchSize: resource.MustParse("10Gi"),
	}
}

func testInputsSpec() Inputs {
	return Inputs{Base: "ghcr.io/misarescu/frankie-runner:sha-abc", MiseToml: []byte("[tools]\ngo = \"1.27\"\n"), MiseLock: []byte("lock")}
}

func testRequest() BuildRequest {
	return BuildRequest{Repository: "toolchains/misarescu/repo", Spec: testInputsSpec()}
}

// flagValue is the argument after flag in a command line.
func flagValue(cmd []string, flag string) string {
	i := slices.Index(cmd, flag)
	if i < 0 || i+1 >= len(cmd) {
		return ""
	}
	return cmd[i+1]
}

// flagValues are the arguments after every occurrence of flag.
func flagValues(cmd []string, flag string) []string {
	var out []string
	for i, a := range cmd {
		if a == flag && i+1 < len(cmd) {
			out = append(out, cmd[i+1])
		}
	}
	return out
}

func envValue(env []corev1.EnvVar, name string) (string, bool) {
	for _, e := range env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

// contextFiles unpacks the build context a ConfigMap carries.
func contextFiles(t *testing.T, cm *corev1.ConfigMap) map[string]string {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(cm.BinaryData["context.tar"]))
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = string(b)
	}
}

func TestBuildResources(t *testing.T) {
	cfg, req := testConfig(), testRequest()
	cm, job, err := BuildResources(cfg, req)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("an autoscaler does not evict the build (D99)", func(t *testing.T) {
		if got := job.Spec.Template.Annotations[SafeToEvictAnnotation]; got != "false" {
			t.Errorf("pod template annotations = %v", job.Spec.Template.Annotations)
		}
	})

	t.Run("context holds the repo files", func(t *testing.T) {
		files := contextFiles(t, cm)
		if files["mise.toml"] != string(testInputsSpec().MiseToml) || files["mise.lock"] != "lock" || len(files) != 2 {
			t.Errorf("repo files not carried into the context: %v", files)
		}
	})

	// D81: the install runs in the base image itself, and the push appends
	// its layer to the base mirrored into the cluster's registry.
	t.Run("install and push run the base image", func(t *testing.T) {
		spec := job.Spec.Template.Spec
		if len(spec.InitContainers) != 1 || len(spec.Containers) != 1 {
			t.Fatalf("containers: %d init, %d", len(spec.InitContainers), len(spec.Containers))
		}
		install, push := spec.InitContainers[0], spec.Containers[0]
		base := "ghcr.io/misarescu/frankie-runner:sha-abc"
		if install.Image != base || push.Image != base {
			t.Errorf("images %q, %q; want the base image", install.Image, push.Image)
		}
		if install.Command[0] != "/usr/local/bin/frankie-imagebuild" || install.Command[1] != "layer" ||
			push.Command[0] != "/usr/local/bin/frankie-imagebuild" || push.Command[1] != "push" {
			t.Errorf("commands %v, %v", install.Command, push.Command)
		}
		for flag, want := range map[string]string{
			"--context": "/ctx/context.tar", "--dir": "/home/agent/toolchain",
			"--run": "mise trust && mise install", "--out": "/out/layer.tar",
		} {
			if got := flagValue(install.Command, flag); got != want {
				t.Errorf("install %s = %q, want %q", flag, got, want)
			}
		}
		if got := flagValues(install.Command, "--exclude"); !slices.Equal(got, []string{"/ctx", "/out"}) {
			t.Errorf("install excludes %v, want the mounts", got)
		}
		for flag, want := range map[string]string{
			"--base": base, "--layer": "/out/layer.tar",
			"--mirror":       "frankie-registry.frankie.svc:5000/base/frankie-runner:sha-abc",
			"--target":       "frankie-registry.frankie.svc:5000/toolchains/misarescu/repo",
			"--insecure":     "frankie-registry.frankie.svc:5000",
			"--prepend-path": "/home/agent/.local/share/mise/shims",
		} {
			if got := flagValue(push.Command, flag); got != want {
				t.Errorf("push %s = %q, want %q", flag, got, want)
			}
		}
		if got := flagValues(push.Command, "--env"); !slices.Equal(got, []string{"MISE_DATA_DIR=/home/agent/.local/share/mise", "MISE_TRUSTED_CONFIG_PATHS=/workspace"}) {
			t.Errorf("push env %v", got)
		}
		// D45: the build pushes by digest; only the control plane tags.
		if strings.Contains(strings.Join(push.Command, " "), req.Spec.Tag()) {
			t.Errorf("the build names its tag: %v", push.Command)
		}
		if v, _ := envValue(install.Env, "MISE_CACHE_DIR"); !strings.HasPrefix(v, "/tmp/") {
			t.Errorf("MISE_CACHE_DIR = %q", v)
		}
		if v, _ := envValue(install.Env, "HOME"); v != "/home/agent" {
			t.Errorf("HOME = %q", v)
		}
		mounted := func(c corev1.Container, path string) bool {
			return slices.ContainsFunc(c.VolumeMounts, func(m corev1.VolumeMount) bool { return m.MountPath == path })
		}
		if !mounted(install, "/out") || !mounted(push, "/out") || !mounted(install, "/ctx") || mounted(push, "/ctx") {
			t.Errorf("mounts: install %v, push %v", install.VolumeMounts, push.VolumeMounts)
		}
	})

	t.Run("job is named by the image so duplicate requests meet", func(t *testing.T) {
		_, job2, _ := BuildResources(cfg, testRequest())
		if job.Name != job2.Name || job.Name != cm.Name {
			t.Errorf("names differ: %s %s %s", job.Name, job2.Name, cm.Name)
		}
		other := testRequest()
		in := testInputsSpec()
		in.MiseToml = []byte("[tools]\ngo = \"1.28\"\n")
		other.Spec = in
		_, job3, _ := BuildResources(cfg, other)
		if job.Name == job3.Name {
			t.Error("different inputs share a job name")
		}
	})

	t.Run("pod has no cluster access, no privileges and builds through the proxy", func(t *testing.T) {
		spec := job.Spec.Template.Spec
		if *spec.AutomountServiceAccountToken || *spec.RuntimeClassName != "kata" {
			t.Errorf("token automount or runtime class wrong: %v %v", *spec.AutomountServiceAccountToken, *spec.RuntimeClassName)
		}
		// D81: Pod Security "restricted", as the agent user, without fsGroup
		// (it would set the setgid bit on the install's directories).
		ps := spec.SecurityContext
		if ps == nil || !*ps.RunAsNonRoot || *ps.RunAsUser != 10001 || *ps.RunAsGroup != 10001 || ps.FSGroup != nil ||
			ps.SeccompProfile == nil || ps.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
			t.Errorf("pod security context %+v", ps)
		}
		for _, c := range append(slices.Clone(spec.InitContainers), spec.Containers...) {
			sc := c.SecurityContext
			if sc == nil || sc.Privileged != nil || *sc.AllowPrivilegeEscalation || !slices.Equal(sc.Capabilities.Drop, []corev1.Capability{"ALL"}) {
				t.Errorf("%s security context %+v", c.Name, sc)
			}
			for name, want := range map[string]string{
				"HTTPS_PROXY": "http://egress.frankie.svc:3128", "https_proxy": "http://egress.frankie.svc:3128",
				"NO_PROXY": "localhost,127.0.0.1,.svc,.cluster.local",
			} {
				if v, _ := envValue(c.Env, name); v != want {
					t.Errorf("%s %s = %q, want %q", c.Name, name, v, want)
				}
			}
			for _, e := range c.Env {
				if strings.Contains(strings.ToLower(e.Name), "token") || strings.Contains(strings.ToLower(e.Name), "key") {
					t.Errorf("credential-like env var %s on a build pod", e.Name)
				}
			}
		}
		if *job.Spec.BackoffLimit != 0 || *job.Spec.ActiveDeadlineSeconds != 1800 {
			t.Errorf("backoff or deadline wrong")
		}
	})
}

func TestBuildResourcesRejectsBadInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BuildConfig, *BuildRequest)
		want   string
	}{
		{"no namespace", func(c *BuildConfig, _ *BuildRequest) { c.Namespace = "" }, "Namespace"},
		{"no proxy", func(c *BuildConfig, _ *BuildRequest) { c.EgressProxyURL = "" }, "EgressProxyURL"},
		{"no deadline", func(c *BuildConfig, _ *BuildRequest) { c.Deadline = 0 }, "deadline"},
		{"no mise.toml", func(_ *BuildConfig, r *BuildRequest) { in := testInputsSpec(); in.MiseToml = nil; r.Spec = in }, "mise.toml"},
		{"no spec", func(_ *BuildConfig, r *BuildRequest) { r.Spec = nil }, "no image to build"},
		{"bad repository", func(_ *BuildConfig, r *BuildRequest) { r.Repository = "Bad Repo/../x" }, "repository"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, r := testConfig(), testRequest()
			tt.mutate(&c, &r)
			if _, _, err := BuildResources(c, r); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestRepository(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{"misarescu/Goodreads-Clone", "toolchains/misarescu/goodreads-clone", false},
		{"owner/repo.name_x", "toolchains/owner/repo.name_x", false},
		{"owner/re po", "", true},
		{"../../etc", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Repository(tt.in)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("Repository(%q) = %q, %v", tt.in, got, err)
			}
		})
	}
}

// D37: the sandbox environment (e.g. GOPROXY) reaches the install.
func TestBuildResourcesPassesSandboxEnv(t *testing.T) {
	cfg := testConfig()
	cfg.Env = map[string]string{"GOPROXY": "http://frankie-athens.frankie.svc:3000"}
	_, job, err := BuildResources(cfg, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(job.Spec.Template.Spec.InitContainers[0].Env, "GOPROXY"); v != "http://frankie-athens.frankie.svc:3000" {
		t.Errorf("install GOPROXY = %q", v)
	}
	// Where downloads come from does not change what the image is.
	if _, plain, _ := BuildResources(testConfig(), testRequest()); plain.Name != job.Name {
		t.Errorf("job name changed with the environment: %s vs %s", plain.Name, job.Name)
	}
}

// M5.2 (D77): builds mostly wait on I/O, so they reserve little CPU but all
// of their memory.
func TestBuildResourcesCPURequest(t *testing.T) {
	c := testConfig()
	c.CPURequest = resource.MustParse("250m")
	_, job, err := BuildResources(c, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []corev1.Container{job.Spec.Template.Spec.InitContainers[0], job.Spec.Template.Spec.Containers[0]} {
		r := c.Resources
		if r.Requests.Cpu().String() != "250m" || r.Limits.Cpu().String() != "2" {
			t.Errorf("%s cpu request/limit = %s/%s, want 250m/2", c.Name, r.Requests.Cpu(), r.Limits.Cpu())
		}
		if !r.Requests.Memory().Equal(*r.Limits.Memory()) {
			t.Errorf("%s memory request %s, want its limit %s", c.Name, r.Requests.Memory(), r.Limits.Memory())
		}
	}
	c.CPURequest = resource.MustParse("3")
	if _, _, err := BuildResources(c, testRequest()); err == nil {
		t.Error("a cpu request above the limit was accepted")
	}
}

// M5.2 (D80): a build reaches the internet through the proxy URL its request
// carries (with the project's egress token), and the image stays the same.
func TestBuildResourcesEgressProxyURL(t *testing.T) {
	r := testRequest()
	r.EgressProxyURL = "http://frankie:feg1.x.y@egress.frankie.svc:3128"
	_, job, err := BuildResources(testConfig(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []corev1.Container{job.Spec.Template.Spec.InitContainers[0], job.Spec.Template.Spec.Containers[0]} {
		for _, e := range c.Env {
			if strings.EqualFold(e.Name, "https_proxy") && e.Value != r.EgressProxyURL {
				t.Errorf("%s %s = %q, want the request's proxy", c.Name, e.Name, e.Value)
			}
		}
	}
	if _, plain, _ := BuildResources(testConfig(), testRequest()); plain.Name != job.Name {
		t.Errorf("job name changed with the proxy: %s vs %s", plain.Name, job.Name)
	}
}

// D100: build pods run on the agent pool, like the steps that wait for them.
func TestBuildResourcesNodeSelector(t *testing.T) {
	cfg := testConfig()
	cfg.NodeSelector = map[string]string{"k8s.scaleway.com/pool-name": "frankie-agents"}
	_, job, err := BuildResources(cfg, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got := job.Spec.Template.Spec.NodeSelector["k8s.scaleway.com/pool-name"]; got != "frankie-agents" {
		t.Errorf("node selector = %v", job.Spec.Template.Spec.NodeSelector)
	}
	cfg.NodeSelector = map[string]string{"pool": "not a value!"}
	if _, _, err := BuildResources(cfg, testRequest()); err == nil {
		t.Error("a bad selector was accepted")
	}
}

// D101: build pods tolerate the agent pool's taint, like the steps that wait for them.
func TestBuildResourcesTolerations(t *testing.T) {
	cfg := testConfig()
	tol := corev1.Toleration{Key: "frankie.dev/agents", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}
	cfg.Tolerations = []corev1.Toleration{tol}
	_, job, err := BuildResources(cfg, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got := job.Spec.Template.Spec.Tolerations; len(got) != 1 || got[0] != tol {
		t.Errorf("tolerations = %v", got)
	}
	cfg.Tolerations = []corev1.Toleration{{Key: "pool", Operator: "In"}}
	if _, _, err := BuildResources(cfg, testRequest()); err == nil {
		t.Error("a bad toleration was accepted")
	}
}

// D103: build pods get VM headroom like the steps that wait for them.
func TestBuildResourcesGuestMemory(t *testing.T) {
	cfg := testConfig()
	cfg.GuestMemoryHeadroom = resource.MustParse("512Mi")
	_, job, err := BuildResources(cfg, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got := job.Spec.Template.Spec.Resources.Limits.Memory(); got.Cmp(resource.MustParse("3584Mi")) != 0 {
		t.Errorf("pod memory = %s, want 3584Mi", got)
	}
	cfg.GuestMemoryHeadroom = resource.MustParse("-1Mi")
	if _, _, err := BuildResources(cfg, testRequest()); err == nil {
		t.Error("a negative headroom was accepted")
	}
}
