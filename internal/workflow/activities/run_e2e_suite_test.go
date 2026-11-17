package activities

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/egress"
	"github.com/misarescu/frankie/internal/sandbox"
)

const e2eConfig = `
e2e: {command: mise run e2e, results: api/out/junit.xml}
egress: {allow: [registry.npmjs.org]}
`

func newSuiteFixture(t *testing.T, exits ...*int32) (*gateFixture, VerifyRequest) {
	t.Helper()
	f := newGateFixtureWithFiles(t, map[string]string{relSHA + ":" + config.ProjectConfigPath: e2eConfig}, exits...)
	signer, err := egress.NewSigner([]byte(strings.Repeat("e", 32)))
	if err != nil {
		t.Fatal(err)
	}
	f.a.Egress = signer
	return f, VerifyRequest{ReleaseID: uuid.NewString(), DeploymentID: uuid.NewString(), Repo: relRepo, BaseBranch: "main",
		SHA: relSHA, Stage: "staging", Kind: config.VerifyE2ESuite, URL: "https://goodreads-stg.example.ts.net/"}
}

// M7, D116: the suite runs in a sandboxed gate pod at the release's commit,
// checked out from the base branch, against the stage's URL.
func TestSuiteVerifierPod(t *testing.T) {
	f, req := newSuiteFixture(t, code(0), code(0))
	v, err := SuiteVerifier{A: f.a}.Verify(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Passed || v.Inconclusive {
		t.Errorf("verification = %+v, want passed on exit 0", v)
	}
	if len(f.created) != 1 {
		t.Fatalf("created %d pods", len(f.created))
	}
	p := f.created[0]
	if !strings.HasPrefix(p.Name, "e2e-staging-") || p.Labels[sandbox.RunIDLabel] != req.ReleaseID ||
		p.Labels[sandbox.StepRunIDLabel] != req.DeploymentID || p.Spec.RuntimeClassName == nil || *p.Spec.RuntimeClassName != "kata" {
		t.Errorf("pod = %s %v", p.Name, p.Labels)
	}
	var clone, suite corev1.Container
	for _, c := range p.Spec.InitContainers {
		switch c.Name {
		case sandbox.GateCloneContainer:
			clone = c
		case sandbox.GateCommandContainer(0):
			suite = c
		}
	}
	if containerEnv(clone.Env, "GATE_BRANCH") != "main" || containerEnv(clone.Env, "GATE_SHA") != relSHA {
		t.Errorf("clone checks out %s@%s", containerEnv(clone.Env, "GATE_BRANCH"), containerEnv(clone.Env, "GATE_SHA"))
	}
	for k, want := range map[string]string{
		"E2E_BASE_URL": "https://goodreads-stg.example.ts.net", "E2E_RESULTS": "/workspace/repo/api/out/junit.xml",
		"E2E_STAGE": "staging", "E2E_COMMIT": relSHA,
	} {
		if got := containerEnv(suite.Env, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.Contains(suite.Command[2], "mise run e2e") {
		t.Errorf("suite command = %q", suite.Command)
	}

	// The checkout is read-only, and the stage joins the project's egress hosts.
	cred := containerEnv(clone.Env, "GIT_CONFIG_VALUE_0")
	if cred == "" {
		t.Fatal("clone has no git credential")
	}
	proxy, err := url.Parse(containerEnv(suite.Env, "HTTPS_PROXY"))
	if err != nil || proxy.User == nil {
		t.Fatalf("proxy = %q", containerEnv(suite.Env, "HTTPS_PROXY"))
	}
	token, _ := proxy.User.Password()
	g, err := f.a.Egress.Verify(token)
	if err != nil || !slices.Equal(g.Hosts, []string{"registry.npmjs.org", "goodreads-stg.example.ts.net"}) {
		t.Errorf("egress grant = %+v, %v", g, err)
	}
}

// M7, D126: a preview's suite checks out the head from its agent branch, as
// the run's issue, yet runs as a verification pod, which reaches the stages.
func TestSuiteVerifierPreview(t *testing.T) {
	f, req := newSuiteFixture(t, code(0), code(0))
	runID := uuid.NewString()
	req.ReleaseID, req.RunID, req.IssueKey, req.Branch = "", runID, "gc-9", "agent/gc-9/add-tags"
	req.Stage, req.URL = "preview", "http://goodreads-previews.stages.svc.cluster.local/pr-152"
	if v, err := (SuiteVerifier{A: f.a}).Verify(t.Context(), req); err != nil || !v.Passed {
		t.Fatalf("Verify = %+v, %v", v, err)
	}
	p := f.created[0]
	if p.Labels[sandbox.RoleLabel] != sandbox.RoleVerify || p.Labels[sandbox.RunIDLabel] != runID || p.Labels[sandbox.IssueKeyLabel] != "gc-9" {
		t.Errorf("pod labels = %v", p.Labels)
	}
	for _, c := range p.Spec.InitContainers {
		switch c.Name {
		case sandbox.GateCloneContainer:
			if containerEnv(c.Env, "GATE_BRANCH") != req.Branch {
				t.Errorf("clone checks out %s, want the agent branch", containerEnv(c.Env, "GATE_BRANCH"))
			}
		case sandbox.GateCommandContainer(0):
			if got := containerEnv(c.Env, "E2E_BASE_URL"); got != req.URL {
				t.Errorf("E2E_BASE_URL = %q", got)
			}
		}
	}
}

// A suite that exits non-zero without writing results cannot judge the release.
func TestSuiteVerifierWithoutResults(t *testing.T) {
	f, req := newSuiteFixture(t, code(0), code(1))
	v, err := SuiteVerifier{A: f.a}.Verify(t.Context(), req)
	if err != nil || v.Passed || !v.Inconclusive || !strings.Contains(v.Summary, "exited 1 without writing its results") {
		t.Errorf("verification = %+v, %v", v, err)
	}

	f, req = newSuiteFixture(t, code(1))
	v, err = SuiteVerifier{A: f.a}.Verify(t.Context(), req)
	if err != nil || !v.Inconclusive || !strings.Contains(v.Summary, "checking out the release failed") {
		t.Errorf("failed checkout = %+v, %v", v, err)
	}
}

func TestSuiteVerifierRejects(t *testing.T) {
	f, req := newSuiteFixture(t, code(0), code(0))
	for name, mutate := range map[string]func(*VerifyRequest){
		"unknown kind":  func(r *VerifyRequest) { r.Kind = "smoke-tests" },
		"no url":        func(r *VerifyRequest) { r.URL = "" },
		"bad id":        func(r *VerifyRequest) { r.DeploymentID = "x" },
		"no e2e config": func(r *VerifyRequest) { r.SHA = strings.Repeat("d", 40) },
	} {
		r := req
		mutate(&r)
		v, err := SuiteVerifier{A: f.a}.Verify(t.Context(), r)
		if name == "no e2e config" {
			if err != nil || !v.Inconclusive || !strings.Contains(v.Summary, "no e2e.command") {
				t.Errorf("%s = %+v, %v", name, v, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// M7, D116: the exit code decides; the results name failures and their issues.
func TestVerdict(t *testing.T) {
	results := func(xml string) []byte {
		return []byte("running\n<<<frankie-e2e-results>>>\n" + xml + "\n<<<frankie-e2e-results-end>>>\n")
	}
	const failing = `<testsuites><testsuite name="Books/gc-65 search">
<testcase name="status 200"/><testcase name="has books"><failure message="expected 0 to be above 0"/></testcase>
</testsuite><testsuite name="Users/create"><testcase name="request"><error>ECONNREFUSED</error></testcase></testsuite></testsuites>`
	tests := []struct {
		name               string
		code               int32
		log                []byte
		passed, inconclude bool
		summary            string
		failures           int
	}{
		{"passed with results", 0, results(`<testsuite><testcase name="a"/><testcase name="b"/></testsuite>`), true, false, "2 tests passed", 0},
		{"passed without results", 0, []byte("ok"), true, false, "the e2e suite passed", 0},
		{"exit 0 wins over failures", 0, results(failing), true, false, "3 tests passed", 0},
		{"failed", 1, results(failing), false, false, "2 of 3 tests failed", 2},
		{"only errors", 1, results(`<testsuite name="s"><testcase name="r"><error>ECONNREFUSED 10.0.0.1</error></testcase></testsuite>`), false, true, "1 of 1 tests could not run", 1},
		{"no results", 2, []byte("bru: command not found\n<<<frankie-e2e-no-results>>>\n"), false, true, "exited 2 without writing its results: bru: command not found", 0},
		{"unreadable results", 1, results("<html/>"), false, true, "could not be read", 0},
		{"no failing test", 1, results(`<testsuite><testcase name="a"/></testsuite>`), false, false, "exited 1 with no failing test", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := verdict(tt.code, tt.log, "gc", "mise run e2e")
			if v.Passed != tt.passed || v.Inconclusive != tt.inconclude || !strings.Contains(v.Summary, tt.summary) || len(v.Failures) != tt.failures {
				t.Errorf("verdict = %+v", v)
			}
		})
	}
	v := verdict(1, results(failing), "gc", "x")
	if f := v.Failures[0]; f.Test != "Books/gc-65 search > has books" || !slices.Equal(f.IssueKeys, []string{"gc-65"}) || f.Message != "expected 0 to be above 0" {
		t.Errorf("failure = %+v", f)
	}
	if f := v.Failures[1]; len(f.IssueKeys) != 0 || f.Message != "ECONNREFUSED" {
		t.Errorf("untagged failure = %+v", f)
	}
}

func containerEnv(env []corev1.EnvVar, name string) string {
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}
