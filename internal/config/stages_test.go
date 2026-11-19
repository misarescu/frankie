package config

import (
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/drivers/driverstest"
)

// testDrivers registers fakes shaped like M7's drivers, plus one that can do nothing.
func testDrivers(t *testing.T) *drivers.Registry {
	t.Helper()
	r, err := drivers.NewRegistry(
		&driverstest.Fake{DriverName: "flux", Caps: drivers.Capabilities{Rollback: true, Health: true}, RejectKey: "token"},
		&driverstest.Fake{DriverName: "flux-preview", Caps: drivers.Capabilities{Health: true, Preview: true}},
		&driverstest.Fake{DriverName: "plain"},
	)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const goodreadsStages = `
e2e: {command: mise run e2e}
stages:
  - name: preview
    driver: flux-preview
    url: http://goodreads-previews.stages.svc.cluster.local/pr-{changeRequest}
    verify: e2e-suite
  - name: staging
    driver: flux
    url: https://goodreads-stg.example.ts.net/
    verify: e2e-suite
    rollback: true
    config: {repo: misarescu/Goodreads-CD, path: staging}
  - name: prod
    driver: flux
    gate: human
    url: https://goodreads.example.ts.net
    verify: e2e-suite
`

// M7, D113: a project declares its stages in .factory/config.yaml.
func TestParseProjectStages(t *testing.T) {
	p, err := ParseProject([]byte(goodreadsStages), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Stages) != 3 {
		t.Fatalf("stages = %+v", p.Stages)
	}
	preview, staging, prod := p.Stages[0], p.Stages[1], p.Stages[2]
	if string(preview.Config) != "{}" || preview.Rollback != nil {
		t.Errorf("preview = %+v, want an empty config and no rollback setting", preview)
	}
	if string(staging.Config) != `{"path":"staging","repo":"misarescu/Goodreads-CD"}` || staging.Rollback == nil || !*staging.Rollback {
		t.Errorf("staging = %+v (config %s)", staging, staging.Config)
	}
	if prod.Gate != GateHuman || prod.Verify != VerifyE2ESuite || prod.URL != "https://goodreads.example.ts.net" {
		t.Errorf("prod = %+v", prod)
	}
	// M7, D126: a preview stage's url names the change request.
	if got := preview.URLFor(152); got != "http://goodreads-previews.stages.svc.cluster.local/pr-152" {
		t.Errorf("preview.URLFor(152) = %q", got)
	}
	if got := staging.URLFor(152); got != staging.URL {
		t.Errorf("staging.URLFor(152) = %q, want its url", got)
	}
	if err := ValidateStages(t.Context(), p.Stages, testDrivers(t)); err != nil {
		t.Errorf("ValidateStages = %v", err)
	}

	ds := testDrivers(t)
	if s, ok := PreviewStage(p.Stages, ds); !ok || s.Name != "preview" {
		t.Errorf("PreviewStage = %+v, %v", s, ok)
	}
	var names []string
	for _, s := range ReleaseStages(p.Stages, ds) {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "staging,prod" {
		t.Errorf("ReleaseStages = %v, want staging, prod", names)
	}
	if _, ok := PreviewStage(p.Stages[1:], ds); ok {
		t.Error("PreviewStage found one among release stages")
	}
}

func TestParseProjectRejectsStages(t *testing.T) {
	tests := []struct{ name, yaml, want string }{
		{"bad name", "stages: [{name: Staging, driver: flux}]", `name "Staging"`},
		{"duplicate name", "stages: [{name: stg, driver: flux}, {name: stg, driver: flux}]", "duplicate stage name"},
		{"no driver", "stages: [{name: stg}]", `driver ""`},
		{"config not an object", "stages: [{name: stg, driver: flux, config: [a]}]", "config must be a JSON object"},
		{"unknown field", "stages: [{name: stg, driver: flux, channel: beta}]", "channel"},
		{"verify unknown", "stages: [{name: stg, driver: flux, verify: smoke-tests, url: 'https://x'}]", `verify "smoke-tests"`},
		{"verify without url", "stages: [{name: stg, driver: flux, verify: e2e-suite}]", "needs the stage's url"},
		{"gate unknown", "stages: [{name: stg, driver: flux, gate: agent}]", `gate "agent"`},
		{"url scheme", "stages: [{name: stg, driver: flux, url: 'ftp://x'}]", "http or https"},
		{"url relative", "stages: [{name: stg, driver: flux, url: '/stg'}]", "http or https"},
		{"url credentials", "stages: [{name: stg, driver: flux, url: 'https://u:p@x'}]", "credentials"},
		{"url query", "stages: [{name: stg, driver: flux, url: 'https://x/?a=1'}]", "query"},
		{"too many", "stages: [" + strings.Repeat("{name: s, driver: flux},", MaxStages) + "{name: last, driver: flux}]", "at most 10"},
		{"readyTimeout not a duration", "stages: [{name: pr, driver: flux-preview, readyTimeout: soon}]", `readyTimeout "soon"`},
		{"readyTimeout too short", "stages: [{name: pr, driver: flux-preview, readyTimeout: 30s}]", "from 1m0s to 25m0s"},
		{"readyTimeout too long", "stages: [{name: pr, driver: flux-preview, readyTimeout: 30m}]", "from 1m0s to 25m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProject([]byte(tt.yaml), 0)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParseProject = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

// FR-RL-3: capability mismatches are rejected when the config is checked.
func TestValidateStagesRejects(t *testing.T) {
	tests := []struct{ name, yaml, want string }{
		{"unknown driver", "stages: [{name: stg, driver: argocd}]", `unknown release driver "argocd"`},
		{"rollback required", "stages: [{name: stg, driver: plain, rollback: true}]", "cannot roll back"},
		{"verify needs health", "e2e: {command: x}\nstages: [{name: stg, driver: plain, verify: e2e-suite, url: 'https://x'}]", "health"},
		{"driver rejects config", "stages: [{name: stg, driver: flux, config: {token: x}}]", "token is not allowed"},
		{"two previews", "stages: [{name: a, driver: flux-preview}, {name: b, driver: flux-preview}]", "at most one preview stage"},
		{"preview after release", "stages: [{name: stg, driver: flux}, {name: pr, driver: flux-preview}]", "comes before the release stages"},
		{"preview gate", "stages: [{name: pr, driver: flux-preview, gate: human}]", "a preview stage has no gate"},
		{"change request in a release url", "stages: [{name: stg, driver: flux, url: 'https://x/pr-{changeRequest}'}]", "only a preview stage's url names {changeRequest}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParseProject([]byte(tt.yaml), 0)
			if err != nil {
				t.Fatalf("ParseProject: %v", err)
			}
			err = ValidateStages(t.Context(), p.Stages, testDrivers(t))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ValidateStages = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

// Rolling back stays optional: unset rolls back when the driver can, false never.
func TestValidateStagesRollbackOptional(t *testing.T) {
	p, err := ParseProject([]byte("stages: [{name: a, driver: plain}, {name: b, driver: plain, rollback: false}]"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateStages(t.Context(), p.Stages, testDrivers(t)); err != nil {
		t.Errorf("ValidateStages = %v", err)
	}
}

// M7.1, D131: a stage may wait longer than the platform's default for its
// deployment to run; without readyTimeout it sets nothing.
func TestStageReadyTimeout(t *testing.T) {
	p, err := ParseProject([]byte("stages: [{name: pr, driver: flux-preview, readyTimeout: 20m}, {name: stg, driver: flux}]"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Stages[0].ReadyWithin(); got != 20*time.Minute {
		t.Errorf("ReadyWithin = %s, want 20m", got)
	}
	if got := p.Stages[1].ReadyWithin(); got != 0 {
		t.Errorf("ReadyWithin without readyTimeout = %s, want 0", got)
	}
}

func TestParseProjectNoStages(t *testing.T) {
	p, err := ParseProject([]byte("workflow: default-build\n"), 0)
	if err != nil || p.Stages != nil {
		t.Fatalf("ParseProject = %+v, %v; want no stages", p.Stages, err)
	}
	if err := ValidateStages(t.Context(), nil, testDrivers(t)); err != nil {
		t.Errorf("ValidateStages(nil) = %v", err)
	}
}
