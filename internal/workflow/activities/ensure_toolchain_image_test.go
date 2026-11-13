package activities

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/images"
)

const testBaseSHA = "0123456789abcdef0123456789abcdef01234567"

// fakeRepo serves the GitHub endpoints the activity reads; files maps path to content.
func fakeRepo(t *testing.T, files map[string]string) *github.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/misarescu/Goodreads-Clone/commits/main":
			_, _ = w.Write([]byte(testBaseSHA))
		case strings.HasPrefix(r.URL.Path, "/repos/misarescu/Goodreads-Clone/contents/"):
			// Files are read at a commit: the resolved base, or the pushed one.
			if ref := r.URL.Query().Get("ref"); ref != testBaseSHA && ref != pushedSHA {
				http.Error(w, "files must be read at a resolved commit, got ref "+ref, http.StatusBadRequest)
				return
			}
			content, ok := files[strings.TrimPrefix(r.URL.Path, "/repos/misarescu/Goodreads-Clone/contents/")]
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
	return github.NewClient("pat", srv.URL)
}

type memRegistry map[string]bool

func (m memRegistry) Has(_ context.Context, repository, tag string) (bool, error) {
	return m[repository+":"+tag], nil
}

type memBuilder struct {
	reg    memRegistry
	builds []images.BuildRequest
	err    error
}

func (b *memBuilder) Build(_ context.Context, r images.BuildRequest) error {
	b.builds = append(b.builds, r)
	if b.err != nil {
		return b.err
	}
	b.reg[r.Repository+":"+r.Inputs.Tag()] = true
	return nil
}

func toolchainActivities(t *testing.T, files map[string]string) (*Activities, *memBuilder) {
	t.Helper()
	reg := memRegistry{}
	b := &memBuilder{reg: reg}
	return &Activities{
		GitHub:    fakeRepo(t, files),
		Toolchain: &images.Ensurer{Registry: reg, Builder: b, PullHost: "localhost:30500"},
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
	if !strings.HasPrefix(first, "localhost:30500/toolchains/misarescu/goodreads-clone:tc-") {
		t.Errorf("ref = %q", first)
	}
	second, err := ensure(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if second != first || len(b.builds) != 1 {
		t.Errorf("second ref %q, builds %d; want the first image reused", second, len(b.builds))
	}
	got := b.builds[0].Inputs
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
