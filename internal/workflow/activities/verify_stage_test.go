package activities

import (
	"reflect"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

// M7, D114, D130.
func TestVerifyStage(t *testing.T) {
	f := newReleaseFixture(t, stagesOK)
	req := f.stageRequest(t, 0)
	f.verdict = Verification{Summary: "1 of 12 tests failed", Failures: []TestFailure{{Test: "Books/search", IssueKeys: []string{"gc-65"}, Message: "expected 200"}}}
	v, err := activityEnv(f.a).ExecuteActivity(f.a.VerifyStage, req)
	var got Verification
	if err != nil || v.Get(&got) != nil {
		t.Fatal(err)
	}
	if got.Passed || len(got.Failures) != 1 || got.Failures[0].IssueKeys[0] != "gc-65" {
		t.Errorf("verification = %+v", got)
	}
	// D130: the deployment keeps how its verification ended.
	rec := f.deployment(t, req).Verification
	if rec == nil || rec.Passed || rec.Summary != "1 of 12 tests failed" || !reflect.DeepEqual(rec.Failures,
		[]domain.TestFailure{{Test: "Books/search", IssueKeys: []string{"gc-65"}, Message: "expected 200"}}) {
		t.Errorf("recorded verification = %+v, want the failed one", rec)
	}
	want := VerifyRequest{ReleaseID: req.ReleaseID, DeploymentID: req.DeploymentID, Repo: relRepo, SHA: relSHA, Stage: "staging", Kind: "e2e-suite", URL: "https://goodreads-stg.example.ts.net"}
	if len(f.asked) != 1 || f.asked[0] != want {
		t.Errorf("verifier asked %+v, want %+v", f.asked, want)
	}

	unverified := f.stageRequest(t, 1)
	v, err = activityEnv(f.a).ExecuteActivity(f.a.VerifyStage, unverified)
	if err != nil || v.Get(&got) != nil || !got.Passed || len(f.asked) != 1 {
		t.Errorf("stage without verify = %+v, %v; want passed without asking", got, err)
	}
	if rec := f.deployment(t, unverified).Verification; rec == nil || !rec.Passed || rec.Summary != "not verified" {
		t.Errorf("recorded verification = %+v, want passed, not verified", rec)
	}

	f.a.Verifier = nil
	if _, err := activityEnv(f.a).ExecuteActivity(f.a.VerifyStage, req); err == nil {
		t.Error("verification without a verifier passed")
	}
}
