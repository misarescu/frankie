package activities

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/temporal"
)

// Verifier checks a stage that runs a release, e.g. by running the project's
// end-to-end suite against the stage's URL (verify: e2e-suite).
type Verifier interface {
	Verify(ctx context.Context, req VerifyRequest) (Verification, error)
}

// VerifyRequest is what a verifier checks.
type VerifyRequest struct {
	ReleaseID string
	Repo      string
	SHA       string
	Stage     string
	Kind      string // the stage's verify, e.g. e2e-suite
	URL       string
}

// Verification is a verifier's result. Failures name the failed tests and the
// issues they are tagged with (FR-TA-3), the release's suspects.
type Verification struct {
	Passed   bool
	Summary  string
	Failures []TestFailure `json:",omitempty"`
}

// TestFailure is one failed test.
type TestFailure struct {
	Test      string
	IssueKeys []string `json:",omitempty"`
	Message   string   `json:",omitempty"`
}

// VerifyStage checks the stage once it runs the release (D114). A stage
// without verify passes.
func (a *Activities) VerifyStage(ctx context.Context, req StageRequest) (Verification, error) {
	if req.Stage.Verify == "" {
		return Verification{Passed: true, Summary: "not verified"}, nil
	}
	if a.Verifier == nil {
		return Verification{}, temporal.NewNonRetryableApplicationError("no verifier for "+req.Stage.Verify, "config", nil)
	}
	v, err := a.Verifier.Verify(ctx, VerifyRequest{ReleaseID: req.ReleaseID, Repo: req.Repo, SHA: req.SHA,
		Stage: req.Stage.Name, Kind: req.Stage.Verify, URL: req.Stage.URL})
	if err != nil {
		return Verification{}, fmt.Errorf("verify %s: %w", req.Stage.Name, err)
	}
	return v, nil
}
