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
		{"negative issue budget", "budgets: {issueUSD: -1}\n", "budgets.issueUSD", 2},
		{"huge issue budget", "budgets: {issueUSD: 5000}\n", "budgets.issueUSD", 2},
		{"unknown budget", "budgets: {releaseUSD: 5}\n", "releaseUSD", 2},
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

func TestParseProjectDependencies(t *testing.T) {
	p, err := ParseProject([]byte(`
dependencies:
  files: [mise.toml, go.mod, go.sum, "web/**/package.json", web/bun.lock]
  run: mise run deps:warm
`), 0)
	if err != nil {
		t.Fatal(err)
	}
	d := p.Dependencies
	if d == nil || d.Run != "mise run deps:warm" || len(d.Files) != 5 {
		t.Fatalf("dependencies = %+v", d)
	}
	for path, want := range map[string]bool{"go.mod": true, "web/package.json": true, "web/app/package.json": true, "web/bun.lock": true,
		"main.go": false, "web/src/App.tsx": false, "sub/go.mod": false} {
		if got := d.Matches(path); got != want {
			t.Errorf("Matches(%q) = %v", path, got)
		}
	}

	for raw, want := range map[string]string{
		"dependencies: {run: x}\n":                            "at least one file",
		"dependencies: {files: [go.mod]}\n":                   "run: is required",
		"dependencies: {files: [../go.mod], run: x}\n":        ". or .. segment",
		"dependencies: {files: [go.mod], run: x, cache: y}\n": "unknown field",
	} {
		if _, err := ParseProject([]byte(raw), 0); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", raw, err, want)
		}
	}
}

// D53: a project may cap what each issue spends on models.
func TestParseProjectBudgets(t *testing.T) {
	p, err := ParseProject([]byte("budgets: {issueUSD: 12.5}\n"), 2)
	if err != nil || p.Budgets.IssueUSD != 12.5 {
		t.Fatalf("project = %+v, err = %v", p, err)
	}
	if p, err := ParseProject(nil, 2); err != nil || p.Budgets.IssueUSD != 0 {
		t.Fatalf("no budgets: %+v, %v", p, err)
	}
}
