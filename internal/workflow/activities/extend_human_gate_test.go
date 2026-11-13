package activities

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestExtendHumanGate(t *testing.T) {
	a, req := humanGate(t)
	later := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	if _, err := activityEnv(a).ExecuteActivity(a.ExtendHumanGate, req.StepRunID, later); err != nil {
		t.Fatal(err)
	}
	got, _ := a.Store.GetApproval(t.Context(), uuid.MustParse(req.StepRunID))
	if !got.ExpiresAt.Equal(later) {
		t.Errorf("expires at %v, want %v", got.ExpiresAt, later)
	}
}
