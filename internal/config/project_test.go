package config

import (
	"strings"
	"testing"
)

func TestParseProjectServices(t *testing.T) {
	p, err := ParseProject([]byte(`
services:
  - postgres:17
  - name: cache
    image: docker.io/library/redis:7.4
    env: {REDIS_ARGS: "--save ''"}
  - image: ghcr.io/acme/fake-smtp:1.2
    port: 2525
`), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Services) != 3 {
		t.Fatalf("services = %+v", p.Services)
	}
	pg, cache, smtp := p.Services[0], p.Services[1], p.Services[2]

	t.Run("shorthand gets catalog defaults", func(t *testing.T) {
		if pg.Name != "postgres" || pg.Image != "postgres:17" || pg.Port != 5432 || pg.RunAsUser == nil || *pg.RunAsUser != 999 ||
			pg.Env["POSTGRES_HOST_AUTH_METHOD"] != "trust" || pg.DataDir == "" {
			t.Errorf("postgres = %+v", pg)
		}
	})
	t.Run("object keeps its name and env, catalog fills the rest", func(t *testing.T) {
		if cache.Name != "cache" || cache.Port != 6379 || cache.Env["REDIS_ARGS"] != "--save ''" || cache.DataDir != "/data" {
			t.Errorf("cache = %+v", cache)
		}
	})
	t.Run("unknown image runs as declared", func(t *testing.T) {
		if smtp.Name != "fake-smtp" || smtp.Port != 2525 || smtp.RunAsUser != nil || smtp.DataDir != "" {
			t.Errorf("smtp = %+v", smtp)
		}
	})
}

func TestParseProjectRepoEnvOverridesCatalog(t *testing.T) {
	p, err := ParseProject([]byte("services:\n  - image: postgres:17\n    env: {POSTGRES_DB: app}\n"), 1)
	if err != nil {
		t.Fatal(err)
	}
	env := p.Services[0].Env
	if env["POSTGRES_DB"] != "app" || env["POSTGRES_HOST_AUTH_METHOD"] != "trust" {
		t.Errorf("env = %v", env)
	}
	// The catalog map itself must not be modified.
	if _, leaked := catalog["postgres"].env["POSTGRES_DB"]; leaked {
		t.Error("repo env leaked into the catalog")
	}
}

func TestParseProjectEmpty(t *testing.T) {
	for _, raw := range []string{"", "services: []\n", "# nothing yet\n"} {
		p, err := ParseProject([]byte(raw), 2)
		if err != nil || len(p.Services) != 0 {
			t.Errorf("%q: %+v, %v", raw, p, err)
		}
	}
}

func TestParseProjectRejects(t *testing.T) {
	tests := []struct {
		name, raw, want string
		max             int
	}{
		{"too many services", "services: [postgres:17, redis:7]\n", "platform allows 1", 1},
		{"unknown top-level field", "servcies: [postgres:17]\n", "servcies", 2},
		{"unknown service field", "services:\n  - image: postgres:17\n    command: [sh]\n", "command", 2},
		{"bad image", "services: [\"postgres 17\"]\n", "not an image reference", 2},
		{"duplicate name", "services: [postgres:16, postgres:17]\n", "duplicate name", 2},
		{"bad name", "services:\n  - name: Main_DB\n    image: postgres:17\n", "DNS label", 2},
		{"bad port", "services:\n  - image: x:1\n    port: 70000\n", "out of range", 2},
		{"bad env name", "services:\n  - image: x:1\n    env: {\"A-B\": x}\n", "env name", 2},
		{"not yaml", "services: [\n", ".factory/config.yaml", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProject([]byte(tt.raw), tt.max)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestRepositoryName(t *testing.T) {
	for in, want := range map[string]string{
		"postgres:17":                          "postgres",
		"docker.io/library/postgres:17.2":      "postgres",
		"localhost:5000/team/redis:7":          "redis",
		"postgres@sha256:abc":                  "postgres",
		"registry.example.com:443/a/b/svc:1.0": "svc",
	} {
		if got := repositoryName(in); got != want {
			t.Errorf("repositoryName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseProjectWorkflow(t *testing.T) {
	p, err := ParseProject([]byte("workflow: default-build\n"), 0)
	if err != nil || p.Workflow != "default-build" {
		t.Fatalf("p = %+v, err = %v", p, err)
	}
	if _, err := ParseProject([]byte("workflow: ../evil\n"), 0); err == nil || !strings.Contains(err.Error(), "workflow:") {
		t.Fatalf("err = %v", err)
	}
}
