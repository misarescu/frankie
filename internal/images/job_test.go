package images

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func testConfig() BuildConfig {
	return BuildConfig{
		Namespace: "frankie-build", BuildKitImage: "moby/buildkit:v0.35.0", RuntimeClassName: "kata",
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

	t.Run("context holds the recipe and the repo files", func(t *testing.T) {
		files := contextFiles(t, cm)
		if !strings.HasPrefix(files["Dockerfile"], "FROM ghcr.io/misarescu/frankie-runner:sha-abc\n") {
			t.Errorf("Dockerfile does not start from the base image:\n%s", files["Dockerfile"])
		}
		if !strings.Contains(files["Dockerfile"], "mise install") {
			t.Errorf("Dockerfile does not install the toolchain:\n%s", files["Dockerfile"])
		}
		if files["mise.toml"] != string(testInputsSpec().MiseToml) || files["mise.lock"] != "lock" {
			t.Errorf("repo files not carried into the context: %v", files)
		}
		// The in-cluster registry is plain HTTP, for builds FROM a recorded image.
		if !strings.Contains(cm.Data["buildkitd.toml"], `[registry."frankie-registry.frankie.svc:5000"]`) || !strings.Contains(cm.Data["buildkitd.toml"], "http = true") {
			t.Errorf("buildkitd.toml = %q", cm.Data["buildkitd.toml"])
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

	t.Run("pod has no cluster access and builds through the proxy", func(t *testing.T) {
		spec := job.Spec.Template.Spec
		if *spec.AutomountServiceAccountToken || *spec.RuntimeClassName != "kata" {
			t.Errorf("token automount or runtime class wrong: %v %v", *spec.AutomountServiceAccountToken, *spec.RuntimeClassName)
		}
		c := spec.Containers[0]
		args := strings.Join(c.Args, " ")
		for _, want := range []string{
			"name=frankie-registry.frankie.svc:5000/toolchains/misarescu/repo,push=true,push-by-digest=true",
			"build-arg:HTTPS_PROXY=http://egress.frankie.svc:3128",
			"build-arg:NO_PROXY=localhost,127.0.0.1,.svc,.cluster.local",
			// D45: the digest is reported through the kubelet, never a tag pushed.
			"--metadata-file /tmp/build-metadata.json",
		} {
			if !strings.Contains(args, want) {
				t.Errorf("args missing %q:\n%s", want, args)
			}
		}
		cmd := strings.Join(c.Command, " ")
		if !strings.Contains(cmd, `buildctl-daemonless.sh "$@" && cat /tmp/build-metadata.json > /dev/termination-log`) {
			t.Errorf("the build does not report its result in the termination message: %s", cmd)
		}
		// The context is unpacked from a tar, never read through the ConfigMap's symlinks.
		if !strings.Contains(cmd, "tar -xf /ctx/context.tar -C /tmp/context") || !strings.Contains(args, "--local context=/tmp/context") ||
			!strings.Contains(args, "--local dockerfile=/tmp/context") || strings.Contains(args, "=/ctx") {
			t.Errorf("command %q, args %q", cmd, args)
		}
		if strings.Contains(args, ":"+req.Spec.Tag()) {
			t.Errorf("the build pushes a tag; only the control plane tags:\n%s", args)
		}
		// D31: rootless BuildKit does not work on Kata (no file capabilities on virtiofs), and
		// an unprivileged rootful one cannot create cgroups.
		if sc := c.SecurityContext; !*sc.Privileged || *sc.RunAsUser != 0 {
			t.Errorf("build container must be privileged root inside the Kata VM: %+v", sc)
		}
		if spec.SecurityContext != nil {
			t.Errorf("pod-level security context would conflict with root: %+v", spec.SecurityContext)
		}
		for _, e := range c.Env {
			if strings.Contains(strings.ToLower(e.Name), "token") || strings.Contains(strings.ToLower(e.Name), "key") {
				t.Errorf("credential-like env var %s on a build pod", e.Name)
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

// D37: the sandbox environment (e.g. GOPROXY) reaches the build's RUN step.
func TestBuildResourcesPassesSandboxEnv(t *testing.T) {
	cfg := testConfig()
	cfg.Env = map[string]string{"GOPROXY": "http://frankie-athens.frankie.svc:3000"}
	cm, job, err := BuildResources(cfg, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if df := contextFiles(t, cm)["Dockerfile"]; !strings.Contains(df, "\nARG GOPROXY\n") || strings.Index(df, "ARG GOPROXY") < strings.Index(df, "FROM ") {
		t.Errorf("Dockerfile does not declare the build arg after FROM:\n%s", df)
	}
	if args := strings.Join(job.Spec.Template.Spec.Containers[0].Args, " "); !strings.Contains(args, "build-arg:GOPROXY=http://frankie-athens.frankie.svc:3000") {
		t.Errorf("args = %s", args)
	}
	// Where downloads come from does not change what the image is.
	if _, plain, _ := BuildResources(testConfig(), testRequest()); plain.Name != job.Name {
		t.Errorf("job name changed with the environment: %s vs %s", plain.Name, job.Name)
	}
}
