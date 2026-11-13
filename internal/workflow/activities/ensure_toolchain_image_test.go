package activities

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/images"
)

const testBaseSHA = "0123456789abcdef0123456789abcdef01234567"

// fakeRepo serves the GitHub endpoints the activities read. files maps a path
// to its content; "<sha>:<path>" overrides it at one commit, and
// "branch:<name>" makes a branch exist at the given commit.
func fakeRepo(t *testing.T, files map[string]string) *github.Client {
	t.Helper()
	c, _ := fakeRepoWithReads(t, files)
	return c
}

// fakeRepoWithReads also records each file read as "<path>@<ref>".
func fakeRepoWithReads(t *testing.T, files map[string]string) (*github.Client, *[]string) {
	t.Helper()
	var reads []string
	const prefix = "/repos/misarescu/Goodreads-Clone/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, prefix)
		switch {
		case path == "commits/main":
			_, _ = w.Write([]byte(testBaseSHA))
		case strings.HasPrefix(path, "commits/") && len(path) == len("commits/")+40:
			_, _ = w.Write([]byte(strings.TrimPrefix(path, "commits/")))
		case strings.HasPrefix(path, "git/ref/heads/"):
			sha, ok := files["branch:"+strings.TrimPrefix(path, "git/ref/heads/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = fmt.Fprintf(w, `{"object":{"sha":%q}}`, sha)
		case strings.HasPrefix(path, "git/trees/"):
			// The files at a commit: those keyed "<sha>:<path>" for it, and the plain ones.
			sha := strings.TrimPrefix(path, "git/trees/")
			seen := map[string]bool{}
			var tree []string
			for k := range files {
				p := k
				if before, after, ok := strings.Cut(k, ":"); ok {
					if before != sha {
						continue
					}
					p = after
				}
				if !seen[p] {
					seen[p] = true
					tree = append(tree, fmt.Sprintf(`{"path":%q,"type":"blob"}`, p))
				}
			}
			slices.Sort(tree)
			_, _ = fmt.Fprintf(w, `{"tree":[%s],"truncated":false}`, strings.Join(tree, ","))
		case strings.HasPrefix(path, "contents/"):
			// Files are read at a commit: the resolved base, the pushed one, or a branch head.
			ref, file := r.URL.Query().Get("ref"), strings.TrimPrefix(path, "contents/")
			if !shaRE.MatchString(ref) {
				http.Error(w, "files must be read at a resolved commit, got ref "+ref, http.StatusBadRequest)
				return
			}
			reads = append(reads, file+"@"+ref)
			content, ok := files[ref+":"+file]
			if !ok {
				content, ok = files[file]
			}
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(content))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return github.NewClient("pat", srv.URL), &reads
}

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// memRegistry holds manifests by repository@digest.
type memRegistry map[string]bool

func (m memRegistry) HasManifest(_ context.Context, repository, digest string) (bool, error) {
	return m[repository+"@"+digest], nil
}

func (m memRegistry) Tag(context.Context, string, string, string) error { return nil }

// memDigests is the control plane's record of built images.
type memDigests map[string]string

func (m memDigests) Lookup(_ context.Context, repository, tag string) (string, bool, error) {
	d, ok := m[repository+":"+tag]
	return d, ok, nil
}

func (m memDigests) Record(_ context.Context, repository, tag, digest string) error {
	m[repository+":"+tag] = digest
	return nil
}

type memBuilder struct {
	reg    memRegistry
	builds []images.BuildRequest
	err    error
}

func (b *memBuilder) Build(_ context.Context, r images.BuildRequest) (string, error) {
	b.builds = append(b.builds, r)
	if b.err != nil {
		return "", b.err
	}
	digest := fmt.Sprintf("sha256:%064x", len(b.builds))
	b.reg[r.Repository+"@"+digest] = true
	return digest, nil
}

func toolchainActivities(t *testing.T, files map[string]string) (*Activities, *memBuilder) {
	t.Helper()
	reg := memRegistry{}
	b := &memBuilder{reg: reg}
	return &Activities{
		GitHub:    fakeRepo(t, files),
		Toolchain: &images.Ensurer{Registry: reg, Builder: b, Digests: memDigests{}, PullHost: "localhost:30500"},
		Agent:     AgentConfig{RunnerImage: "ghcr.io/misarescu/frankie-runner:sha-abc"},
		Log:       quietLog(),
	}, b
}

func ensure(t *testing.T, a *Activities) (string, error) {
	t.Helper()
	val, err := activityEnv(a).ExecuteActivity(a.EnsureToolchainImage, testInput)
	if err != nil {
		return "", err
	}
	var ref string
	if err := val.Get(&ref); err != nil {
		t.Fatal(err)
	}
	return ref, nil
}

func TestEnsureToolchainImageBuildsThenReuses(t *testing.T) {
	a, b := toolchainActivities(t, map[string]string{"mise.toml": "[tools]\ngo = \"1.27\"\n", "mise.lock": "lock"})
	first, err := ensure(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "localhost:30500/toolchains/misarescu/goodreads-clone@sha256:") {
		t.Errorf("ref = %q", first)
	}
	second, err := ensure(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if second != first || len(b.builds) != 1 {
		t.Errorf("second ref %q, builds %d; want the first image reused", second, len(b.builds))
	}
	got, _ := b.builds[0].Spec.(images.Inputs)
	if got.Base != "ghcr.io/misarescu/frankie-runner:sha-abc" || string(got.MiseLock) != "lock" {
		t.Errorf("build inputs = %+v", got)
	}
}

func TestEnsureToolchainImageRebuildsWhenMiseTomlChanges(t *testing.T) {
	files := map[string]string{"mise.toml": "[tools]\ngo = \"1.27\"\n"}
	a, b := toolchainActivities(t, files)
	first, _ := ensure(t, a)
	files["mise.toml"] = "[tools]\ngo = \"1.28\"\n"
	second, err := ensure(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if second == first || len(b.builds) != 2 {
		t.Errorf("refs %q %q, builds %d; want a second image", first, second, len(b.builds))
	}
}

func TestEnsureToolchainImageWithoutToolchain(t *testing.T) {
	t.Run("no mise.toml in the repo", func(t *testing.T) {
		a, b := toolchainActivities(t, map[string]string{})
		ref, err := ensure(t, a)
		if err != nil || ref != "" || len(b.builds) != 0 {
			t.Errorf("ref %q, err %v, builds %d; want the base image and no build", ref, err, len(b.builds))
		}
	})
	t.Run("toolchain images not configured", func(t *testing.T) {
		a, _ := toolchainActivities(t, map[string]string{"mise.toml": "x"})
		a.Toolchain = nil
		if ref, err := ensure(t, a); err != nil || ref != "" {
			t.Errorf("ref %q, err %v", ref, err)
		}
	})
}

func TestEnsureToolchainImageFailures(t *testing.T) {
	t.Run("build failure is returned", func(t *testing.T) {
		a, b := toolchainActivities(t, map[string]string{"mise.toml": "x"})
		b.err = errors.New("mise install exited 1")
		if _, err := ensure(t, a); err == nil || !strings.Contains(err.Error(), "mise install exited 1") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("oversized mise.toml is not retried", func(t *testing.T) {
		a, b := toolchainActivities(t, map[string]string{"mise.toml": strings.Repeat("x", 300<<10)})
		_, err := ensure(t, a)
		if err == nil || len(b.builds) != 0 {
			t.Fatalf("err %v, builds %d", err, len(b.builds))
		}
		var app interface{ NonRetryable() bool }
		if !errors.As(err, &app) || !app.NonRetryable() {
			t.Errorf("want a non-retryable error, got %T %v", err, err)
		}
	})
}

// builtKinds counts the builds of toolchain and dependency images.
func builtKinds(b *memBuilder) (toolchains, dependencies int) {
	for _, r := range b.builds {
		switch r.Spec.(type) {
		case images.Inputs:
			toolchains++
		case images.Dependencies:
			dependencies++
		}
	}
	return toolchains, dependencies
}

const depsConfig = "dependencies:\n  files: [go.mod, go.sum, \"web/**/bun.lock\"]\n  run: mise run deps:warm\n"

func depsRepo() map[string]string {
	return map[string]string{
		"mise.toml": "[tools]\ngo = \"1.27\"\n", ".factory/config.yaml": depsConfig,
		"go.mod": "module x\n", "go.sum": "a v1 h1:x\n", "web/bun.lock": "lock\n", "main.go": "package main\n",
	}
}

// M3.1: a repo declaring dependencies runs in its dependency image, built
// once on top of the toolchain image and reused while nothing changes.
func TestEnsureToolchainImageWithDependencies(t *testing.T) {
	a, b := toolchainActivities(t, depsRepo())
	first, err := ensure(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "localhost:30500/dependencies/misarescu/goodreads-clone@sha256:") {
		t.Fatalf("ref = %q; want the dependency image", first)
	}
	if tc, dp := builtKinds(b); tc != 1 || dp != 1 {
		t.Fatalf("built %d toolchain and %d dependency images", tc, dp)
	}
	deps, _ := b.builds[1].Spec.(images.Dependencies)
	// mise.toml is added although the declaration does not name it: the warm-up's tools resolve from it.
	if len(deps.Files) != 4 || deps.Files["mise.toml"] == nil || deps.Files["web/bun.lock"] == nil || deps.Files["main.go"] != nil || deps.Run != "mise run deps:warm" ||
		deps.Toolchain.Repository != "toolchains/misarescu/goodreads-clone" {
		t.Errorf("dependency spec = %+v (files %v)", deps.Toolchain, len(deps.Files))
	}
	second, err := ensure(t, a)
	if err != nil || second != first || len(b.builds) != 2 {
		t.Fatalf("second = %q, %v, builds %d; want both images reused", second, err, len(b.builds))
	}
}

// M3.1 done-when: a changed go.sum rebuilds only the dependency layer; a
// changed toolchain rebuilds both.
func TestEnsureToolchainImageRebuildsOnlyWhatChanged(t *testing.T) {
	files := depsRepo()
	a, b := toolchainActivities(t, files)
	if _, err := ensure(t, a); err != nil {
		t.Fatal(err)
	}
	files["go.sum"] = "a v1 h1:x\nb v2 h1:y\n"
	if _, err := ensure(t, a); err != nil {
		t.Fatal(err)
	}
	if tc, dp := builtKinds(b); tc != 1 || dp != 2 {
		t.Fatalf("after go.sum changed: %d toolchain and %d dependency builds; want only the dependency layer rebuilt", tc, dp)
	}
	// Code that is not declared does not touch either image.
	files["main.go"] = "package main\n\nfunc main() {}\n"
	if _, err := ensure(t, a); err != nil {
		t.Fatal(err)
	}
	if tc, dp := builtKinds(b); tc != 1 || dp != 2 {
		t.Fatalf("after a code change: %d toolchain and %d dependency builds; want none", tc, dp)
	}
	files["mise.toml"] = "[tools]\ngo = \"1.28\"\n"
	if _, err := ensure(t, a); err != nil {
		t.Fatal(err)
	}
	if tc, dp := builtKinds(b); tc != 2 || dp != 3 {
		t.Fatalf("after mise.toml changed: %d toolchain and %d dependency builds; want both", tc, dp)
	}
}

func TestEnsureToolchainImageRefusesBadDependencyDeclarations(t *testing.T) {
	tests := map[string]map[string]string{
		"files match nothing": {"mise.toml": "[tools]\n", ".factory/config.yaml": "dependencies: {files: [pom.xml], run: mvn -q dependency:go-offline}\n"},
		"no toolchain":        {".factory/config.yaml": depsConfig, "go.mod": "module x\n"},
	}
	for name, files := range tests {
		t.Run(name, func(t *testing.T) {
			a, b := toolchainActivities(t, files)
			_, err := ensure(t, a)
			var appErr *temporal.ApplicationError
			if !errors.As(err, &appErr) || !appErr.NonRetryable() || len(b.builds) > 1 {
				t.Fatalf("err = %v, builds %d; want a non-retryable configuration error", err, len(b.builds))
			}
		})
	}
}
