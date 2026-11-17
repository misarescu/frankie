package activities

import (
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

// M7, D110, D114: a release that succeeds releases its issues.
func TestFinishRelease(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	plan := f.prepare(t)
	for range 2 {
		if _, err := activityEnv(f.a).ExecuteActivity(f.a.FinishRelease, FinishReleaseRequest{ReleaseID: plan.ReleaseID, Status: domain.ReleaseSucceeded}); err != nil {
			t.Fatal(err)
		}
	}
	if rel := f.release(t, plan.ReleaseID); rel.Status != domain.ReleaseSucceeded {
		t.Errorf("release = %s", rel.Status)
	}
	if is, _ := f.a.Store.GetIssue(t.Context(), f.issue.ID); is.Status != domain.IssueReleased {
		t.Errorf("issue = %s, want released", is.Status)
	}
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.FinishRelease, FinishReleaseRequest{ReleaseID: plan.ReleaseID, Status: domain.ReleaseInProgress}); err == nil {
		t.Error("finishing as in_progress accepted")
	}
}

func TestFinishReleaseFailed(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	plan := f.prepare(t)
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.FinishRelease, FinishReleaseRequest{ReleaseID: plan.ReleaseID,
		Status: domain.ReleaseRolledBack, Reason: "staging: verification failed"}); err != nil {
		t.Fatal(err)
	}
	if rel := f.release(t, plan.ReleaseID); rel.Status != domain.ReleaseRolledBack || rel.Reason != "staging: verification failed" {
		t.Errorf("release = %s %q", rel.Status, rel.Reason)
	}
	if is, _ := f.a.Store.GetIssue(t.Context(), f.issue.ID); is.Status != domain.IssueMerged {
		t.Errorf("issue = %s, want merged", is.Status)
	}
}
