package store

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

const releaseRepo = "misarescu/toy"

// mergedIssue records an issue of the project whose change request in repo
// merged as mergeSHA, in the order of the calls: a person's merge (D61).
func mergedIssue(t *testing.T, s *Store, projectID uuid.UUID, repo string, number int, mergeSHA string) domain.Issue {
	t.Helper()
	return mergedIssueFrom(t, s, projectID, domain.IssueAwaitingHuman, repo, number, mergeSHA)
}

// mergedIssueFrom is mergedIssue for an issue that waited in status from,
// e.g. queued for the merge queue's merge.
func mergedIssueFrom(t *testing.T, s *Store, projectID uuid.UUID, from domain.IssueStatus, repo string, number int, mergeSHA string) domain.Issue {
	t.Helper()
	is := mustIssue(t, s, projectID, from)
	r := mustRun(t, s, is.ID)
	cr, err := s.UpsertChangeRequest(t.Context(), NewChangeRequest{IssueID: is.ID, RunID: r.ID, Repo: repo, Number: number,
		ProviderRef: "https://github.com/" + repo + "/pull/1", Branch: "agent/p-1/x", HeadSHA: "h" + mergeSHA})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := s.RecordChangeRequestMerged(t.Context(), cr.ID, mergeSHA)
	if err != nil || merged.Status != domain.IssueMerged {
		t.Fatalf("merge %s: %s, %v", mergeSHA, merged.Status, err)
	}
	return merged
}

func mustRelease(t *testing.T, s *Store, projectID uuid.UUID, sha string) domain.Release {
	t.Helper()
	rel, _, err := s.CreateRelease(t.Context(), NewRelease{ProjectID: projectID, Repo: releaseRepo, SHA: sha})
	if err != nil {
		t.Fatalf("CreateRelease(%s): %v", sha, err)
	}
	return rel
}

func ids(issues ...domain.Issue) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(issues))
	for _, is := range issues {
		out = append(out, is.ID)
	}
	return out
}

func mustTransitionRelease(t *testing.T, s *Store, id uuid.UUID, path ...domain.ReleaseStatus) domain.Release {
	t.Helper()
	var rel domain.Release
	for i := 1; i < len(path); i++ {
		var err error
		if rel, err = s.TransitionRelease(t.Context(), id, path[i-1], path[i], ""); err != nil {
			t.Fatalf("TransitionRelease %s -> %s: %v", path[i-1], path[i], err)
		}
	}
	return rel
}

// M7, D110: a release contains the merged issues up to its commit, in merge order.
func TestCreateReleaseContainsMergedIssuesUpToItsCommit(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	a := mergedIssue(t, s, p.ID, releaseRepo, 1, "m1")
	b := mergedIssue(t, s, p.ID, releaseRepo, 2, "m2")
	c := mergedIssue(t, s, p.ID, releaseRepo, 3, "m3")
	mergedIssue(t, s, p.ID, "misarescu/other", 4, "o1") // another repo of the project
	mustIssue(t, s, p.ID, domain.IssueRunning)          // not merged yet
	other := mustProject(t, s)
	mergedIssue(t, s, other.ID, releaseRepo, 5, "x1") // another project

	rel, created, err := s.CreateRelease(t.Context(), NewRelease{ProjectID: p.ID, Repo: releaseRepo, SHA: "m2"})
	if err != nil || !created {
		t.Fatalf("CreateRelease = %v, created %v", err, created)
	}
	if rel.Status != domain.ReleasePending || rel.SHA != "m2" || rel.Repo != releaseRepo || rel.ProjectID != p.ID || rel.FinishedAt != nil {
		t.Errorf("release = %+v, want a pending release of m2", rel)
	}
	if want := ids(a, b); !slices.Equal(rel.IssueIDs, want) {
		t.Errorf("issues = %v, want %v (up to the merge of m2, in merge order)", rel.IssueIDs, want)
	}

	again, created, err := s.CreateRelease(t.Context(), NewRelease{ProjectID: p.ID, Repo: releaseRepo, SHA: "m2"})
	if err != nil || created || again.ID != rel.ID || !slices.Equal(again.IssueIDs, rel.IssueIDs) {
		t.Errorf("second create = %+v, created %v, %v; want the first release", again, created, err)
	}

	// A commit no recorded merge made contains every merged issue.
	pushed := mustRelease(t, s, p.ID, "pushed-by-hand")
	if want := ids(a, b, c); !slices.Equal(pushed.IssueIDs, want) {
		t.Errorf("issues of an unknown commit = %v, want %v", pushed.IssueIDs, want)
	}

	list, err := s.ListReleases(t.Context(), p.ID, 10)
	if err != nil || len(list) != 2 || list[0].ID != pushed.ID || list[1].ID != rel.ID {
		t.Errorf("ListReleases = %+v, %v; want both, newest first", list, err)
	}
	if !slices.Equal(list[1].IssueIDs, rel.IssueIDs) {
		t.Errorf("listed issues = %v, want %v", list[1].IssueIDs, rel.IssueIDs)
	}

	for _, in := range []NewRelease{{Repo: releaseRepo, SHA: "m1"}, {ProjectID: p.ID, SHA: "m1"}, {ProjectID: p.ID, Repo: releaseRepo}} {
		if _, _, err := s.CreateRelease(t.Context(), in); err == nil {
			t.Errorf("CreateRelease(%+v) accepted", in)
		}
	}
	if _, err := s.ListReleases(t.Context(), p.ID, 0); err == nil {
		t.Error("ListReleases with limit 0 accepted")
	}
}

// M7 (D121): the merge queue's merge asks for its release right away, before
// the issue's run ends, so the release still contains the issue it merged
// (gc-71's release contained none, and gc-72's contained gc-71).
func TestCreateReleaseContainsTheQueuesMerge(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	a := mergedIssueFrom(t, s, p.ID, domain.IssueQueued, releaseRepo, 1, "m1")
	first := mustRelease(t, s, p.ID, "m1")
	mustTransitionRelease(t, s, first.ID, domain.ReleasePending, domain.ReleaseInProgress, domain.ReleaseSucceeded)
	b := mergedIssueFrom(t, s, p.ID, domain.IssueQueued, releaseRepo, 2, "m2")
	second := mustRelease(t, s, p.ID, "m2")
	if !slices.Equal(first.IssueIDs, ids(a)) || !slices.Equal(second.IssueIDs, ids(b)) {
		t.Errorf("releases contain %v and %v, want %v and %v", first.IssueIDs, second.IssueIDs, ids(a), ids(b))
	}
}

// Change requests merged before merges were timed count from their last update.
func TestCreateReleaseCountsMergesRecordedBeforeD110(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	old := mergedIssue(t, s, p.ID, releaseRepo, 1, "")
	if _, err := s.Pool().Exec(t.Context(), `UPDATE change_requests SET merged_at = NULL WHERE issue_id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}
	b := mergedIssue(t, s, p.ID, releaseRepo, 2, "m2")
	if rel := mustRelease(t, s, p.ID, "m2"); !slices.Equal(rel.IssueIDs, ids(old, b)) {
		t.Errorf("issues = %v, want %v", rel.IssueIDs, ids(old, b))
	}
}

// A release that succeeds releases its issues; later releases no longer contain them.
func TestTransitionReleaseReleasesItsIssues(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	a := mergedIssue(t, s, p.ID, releaseRepo, 1, "m1")
	b := mergedIssue(t, s, p.ID, releaseRepo, 2, "m2")
	rel := mustRelease(t, s, p.ID, "m2")

	got := mustTransitionRelease(t, s, rel.ID, domain.ReleasePending, domain.ReleaseInProgress)
	if got.Status != domain.ReleaseInProgress || got.FinishedAt != nil {
		t.Errorf("release = %s finished %v, want in_progress", got.Status, got.FinishedAt)
	}
	if is, _ := s.GetIssue(t.Context(), a.ID); is.Status != domain.IssueMerged {
		t.Errorf("issue during the release = %s, want merged", is.Status)
	}
	got = mustTransitionRelease(t, s, rel.ID, domain.ReleaseInProgress, domain.ReleaseSucceeded)
	if got.Status != domain.ReleaseSucceeded || got.FinishedAt == nil {
		t.Errorf("release = %s finished %v, want succeeded with a time", got.Status, got.FinishedAt)
	}
	for _, is := range []domain.Issue{a, b} {
		if is, _ := s.GetIssue(t.Context(), is.ID); is.Status != domain.IssueReleased {
			t.Errorf("issue %s = %s, want released", is.Key, is.Status)
		}
	}

	// Repeating the transition returns the release as it is.
	if again, err := s.TransitionRelease(t.Context(), rel.ID, domain.ReleaseInProgress, domain.ReleaseSucceeded, ""); err != nil || again.Status != domain.ReleaseSucceeded {
		t.Errorf("repeat = %s, %v; want succeeded", again.Status, err)
	}
	if _, err := s.TransitionRelease(t.Context(), rel.ID, domain.ReleaseInProgress, domain.ReleaseFailed, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("failing a succeeded release = %v, want ErrConflict", err)
	}
	if _, err := s.TransitionRelease(t.Context(), rel.ID, domain.ReleaseSucceeded, domain.ReleaseRolledBack, ""); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("succeeded -> rolled_back = %v, want ErrInvalidTransition", err)
	}

	c := mergedIssue(t, s, p.ID, releaseRepo, 3, "m3")
	if next := mustRelease(t, s, p.ID, "m3"); !slices.Equal(next.IssueIDs, ids(c)) {
		t.Errorf("next release = %v, want only %s", next.IssueIDs, c.Key)
	}
}

// A failed release's issues stay merged and are in the next release.
func TestFailedReleaseIssuesAreInTheNext(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	a := mergedIssue(t, s, p.ID, releaseRepo, 1, "m1")
	rel := mustRelease(t, s, p.ID, "m1")
	mustTransitionRelease(t, s, rel.ID, domain.ReleasePending, domain.ReleaseInProgress)
	if _, err := s.TransitionRelease(t.Context(), rel.ID, domain.ReleaseInProgress, domain.ReleaseFailed, "staging: e2e suite failed"); err != nil {
		t.Fatal(err)
	}
	// M7, D114: a reason is kept unless a later transition gives another.
	got, err := s.TransitionRelease(t.Context(), rel.ID, domain.ReleaseFailed, domain.ReleaseRolledBack, "")
	if err != nil || got.Status != domain.ReleaseRolledBack || got.FinishedAt == nil || got.Reason != "staging: e2e suite failed" {
		t.Errorf("release = %+v, %v; want rolled_back with a time and the reason", got, err)
	}
	if is, _ := s.GetIssue(t.Context(), a.ID); is.Status != domain.IssueMerged {
		t.Errorf("issue = %s, want merged", is.Status)
	}
	b := mergedIssue(t, s, p.ID, releaseRepo, 2, "m2")
	if next := mustRelease(t, s, p.ID, "m2"); !slices.Equal(next.IssueIDs, ids(a, b)) {
		t.Errorf("next release = %v, want %v", next.IssueIDs, ids(a, b))
	}
}

func TestDeployments(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	p := mustProject(t, s)
	rel := mustRelease(t, s, p.ID, "m1")

	staging, err := s.CreateDeployment(ctx, NewDeployment{ReleaseID: rel.ID, Stage: "staging", Driver: "flux"})
	if err != nil {
		t.Fatal(err)
	}
	if staging.Status != domain.ReleasePending || staging.StartedAt != nil || string(staging.DriverState) != "{}" {
		t.Errorf("deployment = %+v, want pending with empty driver state", staging)
	}
	if again, err := s.CreateDeployment(ctx, NewDeployment{ReleaseID: rel.ID, Stage: "staging", Driver: "flux"}); err != nil || again.ID != staging.ID {
		t.Errorf("second create = %+v, %v; want the first", again, err)
	}
	if _, err := s.CreateDeployment(ctx, NewDeployment{ReleaseID: rel.ID, Stage: "staging", Driver: "flagd"}); !errors.Is(err, ErrConflict) {
		t.Errorf("same stage, other driver = %v, want ErrConflict", err)
	}
	for _, in := range []NewDeployment{{Stage: "staging", Driver: "flux"}, {ReleaseID: rel.ID, Driver: "flux"}, {ReleaseID: rel.ID, Stage: "staging"}} {
		if _, err := s.CreateDeployment(ctx, in); err == nil {
			t.Errorf("CreateDeployment(%+v) accepted", in)
		}
	}

	started, err := s.TransitionDeployment(ctx, staging.ID, domain.ReleasePending, domain.ReleaseInProgress, "")
	if err != nil || started.StartedAt == nil || started.FinishedAt != nil {
		t.Fatalf("start = %+v, %v; want started", started, err)
	}
	state := json.RawMessage(`{"commit":"c1"}`)
	withState, err := s.SetDeploymentDriverState(ctx, staging.ID, state)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(withState.DriverState, &got); err != nil || got["commit"] != "c1" {
		t.Errorf("driver state = %s, %v; want the commit", withState.DriverState, err)
	}
	for _, bad := range []string{`[]`, `"x"`, `null`, `{`} {
		if _, err := s.SetDeploymentDriverState(ctx, staging.ID, json.RawMessage(bad)); err == nil {
			t.Errorf("driver state %s accepted", bad)
		}
	}

	failed, err := s.TransitionDeployment(ctx, staging.ID, domain.ReleaseInProgress, domain.ReleaseFailed, "e2e suite failed")
	if err != nil || failed.Reason != "e2e suite failed" || failed.FinishedAt == nil {
		t.Fatalf("fail = %+v, %v; want failed with the reason", failed, err)
	}
	rolledBack, err := s.TransitionDeployment(ctx, staging.ID, domain.ReleaseFailed, domain.ReleaseRolledBack, "")
	if err != nil || rolledBack.Reason != "e2e suite failed" || !rolledBack.FinishedAt.Equal(*failed.FinishedAt) {
		t.Errorf("roll back = %+v, %v; want the reason and finish time kept", rolledBack, err)
	}
	if again, err := s.TransitionDeployment(ctx, staging.ID, domain.ReleaseFailed, domain.ReleaseRolledBack, ""); err != nil || again.Status != domain.ReleaseRolledBack {
		t.Errorf("repeat = %s, %v; want rolled_back", again.Status, err)
	}
	if _, err := s.TransitionDeployment(ctx, staging.ID, domain.ReleasePending, domain.ReleaseInProgress, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("restart = %v, want ErrConflict", err)
	}
	if _, err := s.TransitionDeployment(ctx, staging.ID, domain.ReleasePending, domain.ReleaseSucceeded, ""); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Errorf("pending -> succeeded = %v, want ErrInvalidTransition", err)
	}

	prod, err := s.CreateDeployment(ctx, NewDeployment{ReleaseID: rel.ID, Stage: "prod", Driver: "flux"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.ListDeployments(ctx, rel.ID)
	if err != nil || len(list) != 2 || list[0].ID != staging.ID || list[1].ID != prod.ID {
		t.Errorf("ListDeployments = %+v, %v; want staging then prod", list, err)
	}
}

// M7, D130: a deployment keeps how its verification ended; a later record
// replaces it.
func TestSetDeploymentVerification(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	p := mustProject(t, s)
	rel := mustRelease(t, s, p.ID, "m1")
	dep, err := s.CreateDeployment(ctx, NewDeployment{ReleaseID: rel.ID, Stage: "staging", Driver: "flux"})
	if err != nil {
		t.Fatal(err)
	}
	if dep.Verification != nil {
		t.Errorf("new deployment's verification = %+v, want none", dep.Verification)
	}
	failed := domain.Verification{Summary: "1 of 280 tests failed",
		Failures: []domain.TestFailure{{Test: "Books/search", IssueKeys: []string{"gc-65"}, Message: "expected 200"}}}
	if _, err := s.SetDeploymentVerification(ctx, dep.ID, failed); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Verification == nil || got.Verification.Passed || got.Verification.Summary != failed.Summary ||
		len(got.Verification.Failures) != 1 || got.Verification.Failures[0].IssueKeys[0] != "gc-65" {
		t.Errorf("verification = %+v, want the failed one", got.Verification)
	}
	passed, err := s.SetDeploymentVerification(ctx, dep.ID, domain.Verification{Passed: true, Summary: "280 tests passed"})
	if err != nil || passed.Verification == nil || !passed.Verification.Passed || len(passed.Verification.Failures) != 0 {
		t.Errorf("replaced verification = %+v, %v; want the passed one", passed.Verification, err)
	}
	if _, err := s.SetDeploymentVerification(ctx, domain.NewID(), failed); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown deployment = %v, want ErrNotFound", err)
	}
}

// M7, D115: a release records the bug issue its failed verification opened, once.
func TestSetReleaseBugIssue(t *testing.T) {
	s := newTestStore(t)
	p := mustProject(t, s)
	rel := mustRelease(t, s, p.ID, "m1")
	if rel.BugIssueRef != "" {
		t.Fatalf("new release has bug issue %q", rel.BugIssueRef)
	}
	got, err := s.SetReleaseBugIssue(t.Context(), rel.ID, "https://github.com/o/r/issues/131")
	if err != nil || got.BugIssueRef != "https://github.com/o/r/issues/131" {
		t.Fatalf("SetReleaseBugIssue = %+v, %v", got, err)
	}
	if again, err := s.SetReleaseBugIssue(t.Context(), rel.ID, "https://github.com/o/r/issues/132"); err != nil || again.BugIssueRef != got.BugIssueRef {
		t.Errorf("second bug issue = %q, %v; want the first kept", again.BugIssueRef, err)
	}
	if _, err := s.SetReleaseBugIssue(t.Context(), rel.ID, " "); err == nil {
		t.Error("an empty reference accepted")
	}
	if _, err := s.SetReleaseBugIssue(t.Context(), uuid.New(), "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown release = %v, want ErrNotFound", err)
	}
}

// M7 (D129): a release records what its agent prepared, and the version it
// got once it succeeded; the newest version is the next one's base.
func TestReleaseNotesAndVersion(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	p := mustProject(t, s)
	if _, ok, err := s.LatestReleaseVersion(ctx, p.ID, releaseRepo); err != nil || ok {
		t.Fatalf("latest version of a new repo = %v, %v", ok, err)
	}
	rel := mustRelease(t, s, p.ID, "v1")
	got, err := s.SetReleaseNotes(ctx, rel.ID, domain.BumpMinor, "- gc-77: similar books", domain.NotesByAgent)
	if err != nil || got.Bump != domain.BumpMinor || got.Notes != "- gc-77: similar books" || got.NotesSource != domain.NotesByAgent {
		t.Fatalf("SetReleaseNotes = %+v, %v", got, err)
	}
	if _, err := s.SetReleaseNotes(ctx, rel.ID, "huge", "", domain.NotesByAgent); err == nil {
		t.Error("an unknown bump was accepted")
	}
	if _, err := s.SetReleaseNotes(ctx, rel.ID, domain.BumpPatch, "", "someone"); err == nil {
		t.Error("an unknown notes source was accepted")
	}
	// Only a succeeded release gets a version.
	if _, err := s.SetReleaseVersion(ctx, rel.ID, domain.FirstVersion, "", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("version of a pending release: %v, want ErrNotFound", err)
	}
	mustTransitionRelease(t, s, rel.ID, domain.ReleasePending, domain.ReleaseInProgress, domain.ReleaseSucceeded)
	got, err = s.SetReleaseVersion(ctx, rel.ID, domain.FirstVersion, "https://github.com/o/r/releases/tag/v0.1.0", "")
	if err != nil || got.Version != "v0.1.0" || got.ProviderReleaseURL == "" || got.PublishError != "" {
		t.Fatalf("SetReleaseVersion = %+v, %v", got, err)
	}

	next := mustRelease(t, s, p.ID, "v2")
	mustTransitionRelease(t, s, next.ID, domain.ReleasePending, domain.ReleaseInProgress, domain.ReleaseSucceeded)
	if got, err := s.SetReleaseVersion(ctx, next.ID, domain.Version{Minor: 2}, "", "tag v0.2.0 names another commit"); err != nil || got.PublishError == "" {
		t.Fatalf("SetReleaseVersion with an error = %+v, %v", got, err)
	}
	if v, ok, err := s.LatestReleaseVersion(ctx, p.ID, releaseRepo); err != nil || !ok || v != (domain.Version{Minor: 2}) {
		t.Errorf("latest version = %v, %v, %v; want v0.2.0", v, ok, err)
	}
	if _, ok, err := s.LatestReleaseVersion(ctx, p.ID, "misarescu/other"); err != nil || ok {
		t.Errorf("another repo's latest version = %v, %v", ok, err)
	}
}
