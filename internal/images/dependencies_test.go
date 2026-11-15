package images

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func testDependencies() Dependencies {
	return Dependencies{
		Toolchain: Image{
			Ref:        "localhost:30500/toolchains/misarescu/frankie@sha256:" + strings.Repeat("c", 64),
			Repository: "toolchains/misarescu/frankie", Digest: "sha256:" + strings.Repeat("c", 64),
		},
		Files: map[string][]byte{"mise.toml": []byte("[tasks.\"deps:warm\"]\nrun = \"go mod download\"\n"), "go.mod": []byte("module x\n"),
			"go.sum": []byte("sum\n"), "web/bun.lock": []byte("lock\n")},
		Run: "mise run deps:warm",
	}
}

// M3.1, D81: the warm-up runs in the recorded toolchain image, pulled by
// digest, with the declared files at their repo paths, and its layer is
// appended to that same image in the in-cluster registry.
func TestDependenciesRecipe(t *testing.T) {
	cm, job, err := BuildResources(testConfig(), BuildRequest{Repository: "dependencies/misarescu/frankie", Spec: testDependencies()})
	if err != nil {
		t.Fatal(err)
	}
	files := contextFiles(t, cm)
	if files["web/bun.lock"] != "lock\n" || files["go.mod"] != "module x\n" || len(files) != 4 {
		t.Errorf("context = %v; want the declared files at their repo paths", files)
	}
	spec := job.Spec.Template.Spec
	install, push := spec.InitContainers[0], spec.Containers[0]
	toolchain := "localhost:30500/toolchains/misarescu/frankie@sha256:" + strings.Repeat("c", 64)
	if install.Image != toolchain || push.Image != toolchain {
		t.Errorf("images %q, %q; want the toolchain image by digest", install.Image, push.Image)
	}
	if got := flagValue(install.Command, "--dir"); got != "/workspace/deps" {
		t.Errorf("--dir = %q", got)
	}
	if got := flagValue(install.Command, "--run"); got != "mise run deps:warm" {
		t.Errorf("--run = %q", got)
	}
	if got := flagValue(push.Command, "--base"); got != "frankie-registry.frankie.svc:5000/toolchains/misarescu/frankie@sha256:"+strings.Repeat("c", 64) {
		t.Errorf("--base = %q", got)
	}
	if got := flagValue(push.Command, "--target"); !strings.HasPrefix(job.Name, "dp-") || got != "frankie-registry.frankie.svc:5000/dependencies/misarescu/frankie" {
		t.Errorf("job %s, --target %q", job.Name, got)
	}
	// The toolchain image is already in the cluster's registry.
	if slices.Contains(push.Command, "--mirror") || slices.Contains(push.Command, "--prepend-path") {
		t.Errorf("push = %v", push.Command)
	}
}

// A warm-up command is one argument of the install, never more of its
// command line.
func TestDependenciesRunIsOneArgument(t *testing.T) {
	d := testDependencies()
	d.Run = "true --out /etc/passwd\n--dir /"
	_, job, err := BuildResources(testConfig(), BuildRequest{Repository: "dependencies/misarescu/frankie", Spec: d})
	if err != nil {
		t.Fatal(err)
	}
	cmd := job.Spec.Template.Spec.InitContainers[0].Command
	if got := flagValue(cmd, "--run"); got != d.Run {
		t.Errorf("--run = %q, want %q", got, d.Run)
	}
	if got := flagValue(cmd, "--out"); got != "/out/layer.tar" {
		t.Errorf("--out = %q", got)
	}
}

func TestDependenciesTag(t *testing.T) {
	base := testDependencies()
	if !strings.HasPrefix(base.Tag(), "dp-") || base.Tag() != testDependencies().Tag() {
		t.Fatalf("tag %q is not stable", base.Tag())
	}
	changes := map[string]func(*Dependencies){
		"a declared file":     func(d *Dependencies) { d.Files["go.sum"] = []byte("other\n") },
		"a new file":          func(d *Dependencies) { d.Files["web/package.json"] = []byte("{}") },
		"the toolchain":       func(d *Dependencies) { d.Toolchain.Digest = "sha256:" + strings.Repeat("d", 64) },
		"the warm-up":         func(d *Dependencies) { d.Run = "mise run deps:warm:all" },
		"bytes between files": func(d *Dependencies) { d.Files["go.mod"] = []byte("module x\nsum\n"); d.Files["go.sum"] = nil },
	}
	for name, change := range changes {
		d := testDependencies()
		change(&d)
		if d.Tag() == base.Tag() {
			t.Errorf("changing %s kept the tag", name)
		}
	}
}

func TestDependenciesValidate(t *testing.T) {
	tests := map[string]func(*Dependencies){
		"no toolchain digest":     func(d *Dependencies) { d.Toolchain.Digest = "latest" },
		"a toolchain ref by tag":  func(d *Dependencies) { d.Toolchain.Ref = "localhost:30500/toolchains/misarescu/frankie:latest" },
		"no files":                func(d *Dependencies) { d.Files = nil },
		"a path leaving the repo": func(d *Dependencies) { d.Files["../etc/passwd"] = []byte("x") },
		"too large":               func(d *Dependencies) { d.Files["big.lock"] = make([]byte, maxDependencyBytes+1) },
		"no warm-up":              func(d *Dependencies) { d.Run = " " },
	}
	for name, mutate := range tests {
		d := testDependencies()
		mutate(&d)
		if err := d.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Ensure builds a dependency image like any other, recorded by digest.
func TestEnsureDependencyImage(t *testing.T) {
	e, b, _, d := newEnsurer()
	img, err := e.Ensure(context.Background(), "dependencies/misarescu/frankie", testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	if !img.Built || img.Repository != "dependencies/misarescu/frankie" || !strings.HasPrefix(img.Ref, "localhost:30500/dependencies/misarescu/frankie@sha256:") ||
		d["dependencies/misarescu/frankie:"+testDependencies().Tag()] != img.Digest || len(b.builds) != 1 {
		t.Fatalf("image = %+v, recorded %v", img, d)
	}
	again, err := e.Ensure(context.Background(), "dependencies/misarescu/frankie", testDependencies())
	if err != nil || again.Built || again.Ref != img.Ref || len(b.builds) != 1 {
		t.Fatalf("unchanged dependencies: %+v, %v, builds %d", again, err, len(b.builds))
	}
}

func TestDependencyRepository(t *testing.T) {
	if r, err := DependencyRepository("misarescu/Goodreads-Clone"); err != nil || r != "dependencies/misarescu/goodreads-clone" {
		t.Errorf("DependencyRepository = %q, %v", r, err)
	}
}
