package activities

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

const agentOn = stagesOK + "release: {agent: true}\n"

// M7 (D129): a project's release.agent at the commit turns the agent on.
func TestPrepareReleaseAgentFlag(t *testing.T) {
	if plan := newReleaseFixture(t, agentOn).prepare(t); !plan.Agent {
		t.Errorf("plan = %+v, want the agent on", plan)
	}
	if plan := newReleaseFixture(t, stagesOK).prepare(t); plan.Agent {
		t.Errorf("plan = %+v, want the agent off by default", plan)
	}
}

func (f *releaseFixture) session(t *testing.T, req ReleaseAgentRequest) ReleaseSession {
	t.Helper()
	v, err := activityEnv(f.a).ExecuteActivity(f.a.PrepareReleaseSession, req)
	if err != nil {
		t.Fatalf("PrepareReleaseSession: %v", err)
	}
	var s ReleaseSession
	_ = v.Get(&s)
	return s
}

// M7 (D129): a release agent session is a step of the release's run, with the
// release-agent role, the step's contract and a task naming the release.
func TestPrepareReleaseSession(t *testing.T) {
	f := newReleaseFixture(t, agentOn)
	plan := f.prepare(t)
	req := ReleaseAgentRequest{ReleaseID: plan.ReleaseID, Repo: relRepo, SHA: relSHA, BaseBranch: "main", Step: ReleaseStepNotes}
	s := f.session(t, req)

	if s.Run.ReleaseID != plan.ReleaseID || s.Run.Branch != "agent/release-ccccccc/notes" || s.Issue.IssueID != "" ||
		s.Issue.IssueKey != "release-ccccccc" || s.Issue.Repo != relRepo || s.Issue.BaseBranch != "main" {
		t.Errorf("session = %+v", s)
	}
	st := s.Run.Step
	if st == nil || st.Role != "release-agent" || st.Contract != config.ContractReleaseNotes || st.BudgetUSD != 1 ||
		!strings.Contains(st.SystemPrompt, "You are the release agent") || !strings.Contains(st.SystemPrompt, `"title": "Release notes"`) {
		t.Fatalf("step = %+v", st)
	}
	for _, want := range []string{"Write the release notes for commit " + relSHA, "first versioned release; it will be v0.1.0",
		"### " + f.issue.Key + " (task): Add search", "https://github.com/" + relRepo + "/pull/7", "git show " + relSHA} {
		if !strings.Contains(s.Issue.Spec, want) {
			t.Errorf("task lacks %q:\n%s", want, s.Issue.Spec)
		}
	}
	if p := releasePrompt(s.Issue); !strings.HasPrefix(p, s.Issue.Spec[:40]) || !strings.HasSuffix(p, "the JSON result your instructions describe.") {
		t.Errorf("prompt = %q", p)
	}

	// The run belongs to the release and runs; a retry gets the same step.
	runs, err := f.a.Store.ListReleaseRuns(t.Context(), uuid.MustParse(plan.ReleaseID))
	if err != nil || len(runs) != 1 || runs[0].Status != domain.ExecRunning || runs[0].ID.String() != s.Run.RunID {
		t.Fatalf("release runs = %+v, %v", runs, err)
	}
	if again := f.session(t, req); again.Run.StepRunID != s.Run.StepRunID {
		t.Errorf("a retry made step %s, want %s", again.Run.StepRunID, s.Run.StepRunID)
	}

	t.Run("analysis", func(t *testing.T) {
		a := f.session(t, ReleaseAgentRequest{ReleaseID: plan.ReleaseID, Repo: relRepo, SHA: relSHA, BaseBranch: "main", Step: ReleaseStepAnalysis,
			Failure: &ReleaseFailureReport{Stage: "staging", RolledBack: true, Verification: Verification{Summary: "1 of 12 tests failed",
				Failures: []TestFailure{{Test: "Books/search", IssueKeys: []string{f.issue.Key}, Message: "expected 200, got 500"}}}}})
		if a.Run.RunID != s.Run.RunID || a.Run.Step.Contract != config.ContractReleaseAnalysis {
			t.Errorf("analysis session = %+v", a.Run)
		}
		for _, want := range []string{"failed verification on the staging stage and was rolled back", "- Books/search [" + f.issue.Key + "]: expected 200, got 500"} {
			if !strings.Contains(a.Issue.Spec, want) {
				t.Errorf("task lacks %q:\n%s", want, a.Issue.Spec)
			}
		}
	})
	t.Run("the repo's own role", func(t *testing.T) {
		g := newReleaseFixture(t, agentOn)
		g.a.GitHub = fakeRepo(t, map[string]string{
			relSHA + ":" + config.ProjectConfigPath:       agentOn,
			relSHA + ":.factory/roles/release-agent.yaml": "name: release-agent\nversion: 4\nsystem_prompt_file: notes.md\n",
			relSHA + ":.factory/roles/notes.md":           "Write notes for librarians.",
		})
		p := g.prepare(t)
		got := g.session(t, ReleaseAgentRequest{ReleaseID: p.ReleaseID, Repo: relRepo, SHA: relSHA, BaseBranch: "main", Step: ReleaseStepNotes})
		if got.Run.Step.RoleVersion != 4 || !strings.HasPrefix(got.Run.Step.SystemPrompt, "Write notes for librarians.") {
			t.Errorf("step = %+v", got.Run.Step)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		for _, bad := range []ReleaseAgentRequest{
			{ReleaseID: plan.ReleaseID, Repo: relRepo, SHA: relSHA, Step: "deploy"},
			{ReleaseID: plan.ReleaseID, Repo: relRepo, SHA: relSHA, Step: ReleaseStepAnalysis},
			{ReleaseID: "nope", Repo: relRepo, SHA: relSHA, Step: ReleaseStepNotes},
		} {
			if _, err := activityEnv(f.a).ExecuteActivity(f.a.PrepareReleaseSession, bad); err == nil {
				t.Errorf("%+v was prepared", bad)
			}
		}
	})
}

// The task of a release after a versioned one names the range it covers.
func TestReleaseNotesTaskAfterAVersion(t *testing.T) {
	f := newReleaseFixture(t, agentOn)
	prev := succeededRelease(t, f, strings.Repeat("b", 40))
	if _, err := f.a.Store.SetReleaseVersion(t.Context(), prev.ID, domain.Version{Minor: 3}, "", ""); err != nil {
		t.Fatal(err)
	}
	plan := f.prepare(t)
	s := f.session(t, ReleaseAgentRequest{ReleaseID: plan.ReleaseID, Repo: relRepo, SHA: relSHA, BaseBranch: "main", Step: ReleaseStepNotes})
	if want := "The previous version is v0.3.0, at commit " + prev.SHA + ": this release is everything in `" + prev.SHA + ".." + relSHA + "`."; !strings.Contains(s.Issue.Spec, want) {
		t.Errorf("task lacks %q:\n%s", want, s.Issue.Spec)
	}
}

// succeededRelease records a release of sha that passed its stages.
func succeededRelease(t *testing.T, f *releaseFixture, sha string) domain.Release {
	t.Helper()
	ctx := t.Context()
	p, err := f.a.Store.GetProjectByName(ctx, relRepo)
	if err != nil {
		t.Fatal(err)
	}
	rel, _, err := f.a.Store.CreateRelease(ctx, store.NewRelease{ProjectID: p.ID, Repo: relRepo, SHA: sha})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]domain.ReleaseStatus{{domain.ReleasePending, domain.ReleaseInProgress}, {domain.ReleaseInProgress, domain.ReleaseSucceeded}} {
		if rel, err = f.a.Store.TransitionRelease(ctx, rel.ID, step[0], step[1], ""); err != nil {
			t.Fatal(err)
		}
	}
	return rel
}

func (f *releaseFixture) recordNotes(t *testing.T, req ReleaseNotesRequest) domain.NotesSource {
	t.Helper()
	v, err := activityEnv(f.a).ExecuteActivity(f.a.RecordReleaseNotes, req)
	if err != nil {
		t.Fatalf("RecordReleaseNotes: %v", err)
	}
	var src domain.NotesSource
	_ = v.Get(&src)
	return src
}

// M7 (D129): the agent's notes are recorded when they meet the contract;
// otherwise the fallback bumps from the issue types and lists the issues.
func TestRecordReleaseNotes(t *testing.T) {
	f := newReleaseFixture(t, agentOn)
	plan := f.prepare(t)
	src := f.recordNotes(t, ReleaseNotesRequest{ReleaseID: plan.ReleaseID,
		Result: `{"bump":"major","reason":"search answers a page","notes":"### Changed\n\n- gc-1: search pages","breaking":["search returns {books, total}"]}`})
	rel := f.release(t, plan.ReleaseID)
	if src != domain.NotesByAgent || rel.Bump != domain.BumpMajor || rel.NotesSource != domain.NotesByAgent ||
		!strings.Contains(rel.Notes, "- gc-1: search pages") || !strings.Contains(rel.Notes, "### Breaking changes\n\n- search returns {books, total}") {
		t.Errorf("release = %+v", rel)
	}
	for name, req := range map[string]ReleaseNotesRequest{
		"an answer off the contract": {ReleaseID: plan.ReleaseID, Result: "Here are the notes!"},
		"a failed session":           {ReleaseID: plan.ReleaseID, Failed: "agent session failed: budget"},
	} {
		t.Run(name, func(t *testing.T) {
			if src := f.recordNotes(t, req); src != domain.NotesByFallback {
				t.Errorf("source = %s", src)
			}
			rel := f.release(t, plan.ReleaseID)
			if rel.Bump != domain.BumpMinor || rel.NotesSource != domain.NotesByFallback ||
				rel.Notes != "- "+f.issue.Key+": Add search (https://github.com/"+relRepo+"/pull/7)\n" {
				t.Errorf("release = %+v", rel)
			}
		})
	}
}

// providerReleases serves GitHub's tag and release endpoints for
// PublishVersion; tags maps a tag to its commit.
type providerReleases struct {
	tags    map[string]string
	created []map[string]any
	fail    int // answer POST with this status
}

func (p *providerReleases) client(t *testing.T) *github.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/repos/"+relRepo)
		switch {
		case strings.HasPrefix(path, "/git/ref/tags/"):
			sha, ok := p.tags[strings.TrimPrefix(path, "/git/ref/tags/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = fmt.Fprintf(w, `{"object":{"sha":%q,"type":"commit"}}`, sha)
		case strings.HasPrefix(path, "/releases/tags/"):
			http.NotFound(w, r)
		case r.Method == http.MethodPost && path == "/releases":
			if p.fail != 0 {
				http.Error(w, `{"message":"nope"}`, p.fail)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			p.created = append(p.created, body)
			tag, _ := body["tag_name"].(string)
			p.tags[tag], _ = body["target_commitish"].(string)
			_, _ = fmt.Fprintf(w, `{"html_url":"https://github.com/%s/releases/tag/%s"}`, relRepo, tag)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	return github.NewClient("pat", srv.URL)
}

func (f *releaseFixture) publish(t *testing.T, id string) (PublishedVersion, error) {
	t.Helper()
	v, err := activityEnv(f.a).ExecuteActivity(f.a.PublishVersion, id)
	if err != nil {
		return PublishedVersion{}, err
	}
	var out PublishedVersion
	_ = v.Get(&out)
	return out, nil
}

// M7 (D129): a succeeded release gets the next version and a GitHub Release
// with its notes; the first is v0.1.0.
func TestPublishVersion(t *testing.T) {
	f := newReleaseFixture(t, agentOn)
	host := &providerReleases{tags: map[string]string{}}
	f.a.GitHub = host.client(t)

	first := succeededRelease(t, f, strings.Repeat("1", 40))
	if _, err := f.a.Store.SetReleaseNotes(t.Context(), first.ID, domain.BumpMajor, "- gc-1: search", domain.NotesByAgent); err != nil {
		t.Fatal(err)
	}
	got, err := f.publish(t, first.ID.String())
	if err != nil || got.Version != "v0.1.0" || got.URL != "https://github.com/"+relRepo+"/releases/tag/v0.1.0" || got.Error != "" {
		t.Fatalf("first = %+v, %v", got, err)
	}
	if len(host.created) != 1 || host.created[0]["target_commitish"] != first.SHA || host.created[0]["body"] != "- gc-1: search" {
		t.Errorf("created = %+v", host.created)
	}
	if again, err := f.publish(t, first.ID.String()); err != nil || again != got || len(host.created) != 1 {
		t.Errorf("a retry = %+v, %v, created %d; want the same version, published once", again, err, len(host.created))
	}

	// Without notes, the fallback's: the first release took the only issue,
	// so this one has none and is a patch.
	second := succeededRelease(t, f, strings.Repeat("2", 40))
	if got, err := f.publish(t, second.ID.String()); err != nil || got.Version != "v0.1.1" {
		t.Errorf("second = %+v, %v", got, err)
	}
	if rel := f.release(t, second.ID.String()); rel.NotesSource != domain.NotesByFallback || rel.Version != "v0.1.1" || !strings.HasPrefix(rel.Notes, "Commit 2222") {
		t.Errorf("second release = %+v", rel)
	}

	t.Run("the tag names another commit", func(t *testing.T) {
		third := succeededRelease(t, f, strings.Repeat("3", 40))
		host.tags["v0.1.2"] = strings.Repeat("9", 40)
		if _, err := f.a.Store.SetReleaseNotes(t.Context(), third.ID, domain.BumpPatch, "- fix", domain.NotesByAgent); err != nil {
			t.Fatal(err)
		}
		got, err := f.publish(t, third.ID.String())
		if err != nil || got.Version != "v0.1.2" || got.URL != "" || !strings.Contains(got.Error, "already names another commit") {
			t.Errorf("third = %+v, %v", got, err)
		}
		if rel := f.release(t, third.ID.String()); rel.Status != domain.ReleaseSucceeded || rel.PublishError == "" {
			t.Errorf("third release = %+v", rel)
		}
	})
	t.Run("a refusal", func(t *testing.T) {
		host.fail = http.StatusForbidden
		defer func() { host.fail = 0 }()
		fourth := succeededRelease(t, f, strings.Repeat("4", 40))
		got, err := f.publish(t, fourth.ID.String())
		if err != nil || got.Version == "" || !strings.Contains(got.Error, "403") {
			t.Errorf("fourth = %+v, %v", got, err)
		}
	})
	t.Run("a release that did not succeed", func(t *testing.T) {
		p, err := f.a.Store.GetProjectByName(t.Context(), relRepo)
		if err != nil {
			t.Fatal(err)
		}
		rel, _, err := f.a.Store.CreateRelease(t.Context(), store.NewRelease{ProjectID: p.ID, Repo: relRepo, SHA: strings.Repeat("6", 40)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.publish(t, rel.ID.String()); err == nil {
			t.Error("a pending release was published")
		}
	})
}

// M7 (D129): a release's sessions share its budget, and its run ends with
// them.
func TestReleaseAgentBudgetAndRun(t *testing.T) {
	f := newReleaseFixture(t, agentOn)
	f.a.Release.AgentBudgetUSD = 0.5
	plan := f.prepare(t)
	s := f.session(t, ReleaseAgentRequest{ReleaseID: plan.ReleaseID, Repo: relRepo, SHA: relSHA, BaseBranch: "main", Step: ReleaseStepNotes})
	set := f.a.sessionSettings(s.Run)
	if budget, exhausted, err := f.a.sessionBudget(t.Context(), s.Issue, s.Run, set); err != nil || budget != 0.5 || exhausted != "" {
		t.Errorf("budget = %v, %q, %v", budget, exhausted, err)
	}
	step := uuid.MustParse(s.Run.StepRunID)
	if _, err := f.a.Store.AddStepRunUsage(t.Context(), step, domain.Usage{CostUSD: 0.5}); err != nil {
		t.Fatal(err)
	}
	if _, exhausted, err := f.a.sessionBudget(t.Context(), s.Issue, s.Run, set); err != nil || !strings.Contains(exhausted, "the release's model budget of $0.50 is used up") {
		t.Errorf("exhausted = %q, %v", exhausted, err)
	}

	for _, to := range [][2]domain.ExecStatus{{domain.ExecPending, domain.ExecRunning}, {domain.ExecRunning, domain.ExecSucceeded}} {
		if _, err := f.a.Store.TransitionStepRun(t.Context(), step, to[0], to[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.FinishReleaseRun, plan.ReleaseID); err != nil {
		t.Fatal(err)
	}
	runs, err := f.a.Store.ListReleaseRuns(t.Context(), uuid.MustParse(plan.ReleaseID))
	if err != nil || len(runs) != 1 || runs[0].Status != domain.ExecSucceeded {
		t.Errorf("runs = %+v, %v", runs, err)
	}
	// A release without sessions has no run to finish.
	other := succeededRelease(t, f, strings.Repeat("5", 40))
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.FinishReleaseRun, other.ID.String()); err != nil {
		t.Errorf("FinishReleaseRun without a run: %v", err)
	}
}
