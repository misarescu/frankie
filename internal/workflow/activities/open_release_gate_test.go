package activities

import (
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

// M8, D139: a release's gate is a step of the release's run, in the approvals
// inbox under the release's name, and its decision goes to the release
// workflow.
func TestOpenReleaseGate(t *testing.T) {
	f := newReleaseFixture(t, "stages: [{name: staging, driver: flux}, {name: production, driver: flux, gate: human}]")
	plan := f.prepare(t)
	req := ReleaseGateRequest{ReleaseID: plan.ReleaseID, Repo: relRepo, SHA: relSHA, Stage: "production", ExpiresAt: time.Now().Add(time.Hour)}
	open := func() string {
		t.Helper()
		v, err := activityEnv(f.a).ExecuteActivity(f.a.OpenReleaseGate, req)
		var id string
		if err != nil || v.Get(&id) != nil {
			t.Fatalf("OpenReleaseGate: %v", err)
		}
		return id
	}
	id := open()
	if again := open(); again != id {
		t.Errorf("retry opened %s, want %s", again, id)
	}

	pending, err := f.a.Store.ListPendingApprovals(t.Context())
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	p := pending[0]
	if p.StepRunID.String() != id || p.StepID != "production" || p.IssueKey != "release-ccccccc" ||
		p.IssueTitle != "Release ccccccc of "+relRepo || p.CommitSHA != relSHA || p.TemporalWorkflowID != ReleaseWorkflowID(relRepo) {
		t.Errorf("pending approval = %+v", p)
	}
	if got, err := f.a.Store.GetPendingApproval(t.Context(), p.StepRunID); err != nil || got.TemporalWorkflowID != ReleaseWorkflowID(relRepo) {
		t.Errorf("the API's lookup = %+v, %v", got, err)
	}

	if _, err := activityEnv(f.a).ExecuteActivity(f.a.CloseHumanGate, HumanGateClose{StepRunID: id, Status: domain.ApprovalApproved, By: "mihai"}); err != nil {
		t.Fatalf("CloseHumanGate: %v", err)
	}
	if pending, _ := f.a.Store.ListPendingApprovals(t.Context()); len(pending) != 0 {
		t.Errorf("pending after the decision = %+v", pending)
	}
	if err := f.a.FinishReleaseRun(t.Context(), plan.ReleaseID); err != nil {
		t.Fatal(err)
	}
	runs, err := f.a.Store.ListReleaseRuns(t.Context(), f.release(t, plan.ReleaseID).ID)
	if err != nil || len(runs) != 1 || runs[0].Status != domain.ExecSucceeded {
		t.Errorf("release runs = %+v, %v", runs, err)
	}
}
