package activities

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/drivers/driverstest"
	"github.com/misarescu/frankie/internal/store"
)

const (
	relSHA   = "cccccccccccccccccccccccccccccccccccccccc"
	relRepo  = "misarescu/Goodreads-Clone"
	stagesOK = `
e2e: {command: mise run e2e}
stages:
  - name: staging
    driver: flux
    config: {path: apps/goodreads/staging}
    url: https://goodreads-stg.example.ts.net
    verify: e2e-suite
  - name: production
    driver: plain
`
)

// releaseFixture is a project with one issue merged as relSHA, its
// configuration at relSHA, and fake release drivers: flux (rollback, health)
// and plain (no capabilities).
type releaseFixture struct {
	a       *Activities
	issue   domain.Issue
	flux    *driverstest.Fake
	plain   *driverstest.Fake
	verdict Verification
	asked   []VerifyRequest
}

func (f *releaseFixture) Verify(_ context.Context, req VerifyRequest) (Verification, error) {
	f.asked = append(f.asked, req)
	return f.verdict, nil
}

func newReleaseFixture(t *testing.T, cfg string) *releaseFixture {
	t.Helper()
	st, in := readyIssue(t)
	f := &releaseFixture{
		flux:    &driverstest.Fake{DriverName: "flux", Caps: drivers.Capabilities{Rollback: true, Health: true}},
		plain:   &driverstest.Fake{DriverName: "plain"},
		verdict: Verification{Passed: true, Summary: "12 tests passed"},
	}
	reg, err := drivers.NewRegistry(f.flux, f.plain)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	if cfg != "" {
		files[relSHA+":"+config.ProjectConfigPath] = cfg
	}
	f.a = &Activities{Store: st, GitHub: fakeRepo(t, files), Drivers: reg, Verifier: f, Log: quietLog(),
		Release: ReleaseConfig{StageTimeout: 200 * time.Millisecond, PollInterval: 10 * time.Millisecond}}
	f.issue = mergeIssue(t, st, uuid.MustParse(in.IssueID), relSHA)
	return f
}

// mergeIssue records the issue's change request merged as sha.
func mergeIssue(t *testing.T, st *store.Store, issueID uuid.UUID, sha string) domain.Issue {
	t.Helper()
	ctx := t.Context()
	for _, to := range []domain.IssueStatus{domain.IssueRunning, domain.IssueAwaitingHuman} {
		is, err := st.GetIssue(ctx, issueID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.TransitionIssue(ctx, issueID, is.Status, to); err != nil {
			t.Fatal(err)
		}
	}
	run, err := st.CreateRun(ctx, store.NewRun{IssueID: issueID, WorkflowSnapshot: json.RawMessage(`{}`), WorkflowVersion: "1", TemporalWorkflowID: "wf-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	cr, err := st.UpsertChangeRequest(ctx, store.NewChangeRequest{IssueID: issueID, RunID: run.ID, Repo: relRepo, Number: 7,
		ProviderRef: "https://github.com/" + relRepo + "/pull/7", Branch: "agent/gc-1/x", HeadSHA: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	is, err := st.RecordChangeRequestMerged(ctx, cr.ID, sha)
	if err != nil || is.Status != domain.IssueMerged {
		t.Fatalf("merge: %s, %v", is.Status, err)
	}
	return is
}

// deployCalls are a fake's calls other than the config checks preparing makes.
func deployCalls(fake *driverstest.Fake) []driverstest.Call {
	var out []driverstest.Call
	for _, c := range fake.Calls() {
		if c.Op != driverstest.OpValidate {
			out = append(out, c)
		}
	}
	return out
}

func (f *releaseFixture) prepare(t *testing.T) ReleasePlan {
	t.Helper()
	v, err := activityEnv(f.a).ExecuteActivity(f.a.PrepareRelease, PrepareReleaseRequest{Repo: relRepo, SHA: relSHA})
	if err != nil {
		t.Fatalf("PrepareRelease: %v", err)
	}
	var plan ReleasePlan
	_ = v.Get(&plan)
	return plan
}

func (f *releaseFixture) release(t *testing.T, id string) domain.Release {
	t.Helper()
	rel, err := f.a.Store.GetRelease(t.Context(), uuid.MustParse(id))
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

// M7, D114: a release is prepared from the stages at its commit.
func TestPrepareRelease(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	plan := f.prepare(t)
	if plan.Skip != "" || plan.Failed != "" || plan.Status != domain.ReleaseInProgress {
		t.Fatalf("plan = %+v, want a started release", plan)
	}
	want := []ReleaseStage{
		{Name: "staging", Driver: "flux", Config: json.RawMessage(`{"path":"apps/goodreads/staging"}`), URL: "https://goodreads-stg.example.ts.net", Verify: "e2e-suite", Rollback: true},
		{Name: "production", Driver: "plain", Config: json.RawMessage(`{}`)},
	}
	if len(plan.Stages) != 2 {
		t.Fatalf("stages = %+v", plan.Stages)
	}
	for i, s := range plan.Stages {
		w := want[i]
		if s.Name != w.Name || s.Driver != w.Driver || string(s.Config) != string(w.Config) || s.URL != w.URL || s.Verify != w.Verify || s.Rollback != w.Rollback {
			t.Errorf("stage %d = %+v (config %s), want %+v", i, s, s.Config, w)
		}
	}
	if !slices.Equal(plan.Issues, []string{f.issue.Key}) {
		t.Errorf("issues = %v, want %s", plan.Issues, f.issue.Key)
	}
	rel := f.release(t, plan.ReleaseID)
	if rel.Status != domain.ReleaseInProgress || rel.SHA != relSHA || rel.Repo != relRepo {
		t.Errorf("release = %+v", rel)
	}
	// A retry resumes the same release.
	if again := f.prepare(t); again.ReleaseID != plan.ReleaseID || len(again.Stages) != 2 || again.Skip != "" {
		t.Errorf("retry = %+v, want the same release", again)
	}
	// Once it ended, the commit is not released again.
	if _, err := f.a.Store.TransitionRelease(t.Context(), rel.ID, domain.ReleaseInProgress, domain.ReleaseSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	if again := f.prepare(t); !strings.Contains(again.Skip, "already succeeded") || len(again.Stages) != 0 {
		t.Errorf("after success = %+v, want skipped", again)
	}
}

func TestPrepareReleaseSkips(t *testing.T) {
	tests := []struct{ name, cfg, want string }{
		{"no configuration", "", "no .factory/config.yaml"},
		{"no stages", "workflow: default-build\n", "no release stages"},
		{"only a preview", "stages: [{name: pr, driver: flux-preview}]", "no release stages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newReleaseFixture(t, tt.cfg)
			if tt.name == "only a preview" {
				reg, _ := drivers.NewRegistry(&driverstest.Fake{DriverName: "flux-preview", Caps: drivers.Capabilities{Preview: true, Health: true}})
				f.a.Drivers = reg
			}
			plan := f.prepare(t)
			if !strings.Contains(plan.Skip, tt.want) || plan.ReleaseID != "" {
				t.Errorf("plan = %+v, want skipped: %s", plan, tt.want)
			}
			if rels, _ := f.a.Store.ListReleases(t.Context(), mustProjectID(t, f.a.Store), 5); len(rels) != 0 {
				t.Errorf("releases = %+v, want none", rels)
			}
		})
	}
	f := newReleaseFixture(t, stagesOK)
	v, err := activityEnv(f.a).ExecuteActivity(f.a.PrepareRelease, PrepareReleaseRequest{Repo: "misarescu/unknown", SHA: relSHA})
	var plan ReleasePlan
	if err != nil || v.Get(&plan) != nil || !strings.Contains(plan.Skip, "not a project") {
		t.Errorf("unknown repo = %+v, %v", plan, err)
	}
}

// Stages that cannot be used fail the release, with the reason, before any stage.
func TestPrepareReleaseFailsInvalidStages(t *testing.T) {
	tests := []struct{ name, cfg, want string }{
		{"unknown driver", "stages: [{name: staging, driver: argocd}]", `unknown release driver "argocd"`},
		{"rollback impossible", "stages: [{name: staging, driver: plain, rollback: true}]", "cannot roll back"},
		{"human gate", "stages: [{name: prod, driver: flux, gate: human}]", "gate human is not supported yet"},
		{"does not parse", "stages: [{name: Staging, driver: flux}]", `name "Staging"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newReleaseFixture(t, tt.cfg)
			plan := f.prepare(t)
			if !strings.Contains(plan.Failed, tt.want) || plan.Status != domain.ReleaseFailed || len(plan.Stages) != 0 {
				t.Fatalf("plan = %+v, want failed: %s", plan, tt.want)
			}
			rel := f.release(t, plan.ReleaseID)
			if rel.Status != domain.ReleaseFailed || !strings.Contains(rel.Reason, tt.want) {
				t.Errorf("release = %s %q", rel.Status, rel.Reason)
			}
			if is, _ := f.a.Store.GetIssue(t.Context(), f.issue.ID); is.Status != domain.IssueMerged {
				t.Errorf("issue = %s, want merged", is.Status)
			}
			if again := f.prepare(t); !strings.Contains(again.Skip, "already failed") {
				t.Errorf("retry = %+v, want skipped", again)
			}
		})
	}

	f := newReleaseFixture(t, stagesOK)
	f.a.Verifier = nil
	if plan := f.prepare(t); !strings.Contains(plan.Failed, "verify e2e-suite is not available") {
		t.Errorf("without a verifier = %+v", plan)
	}
}

func mustProjectID(t *testing.T, st *store.Store) uuid.UUID {
	t.Helper()
	p, err := st.GetProjectByName(t.Context(), relRepo)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}
