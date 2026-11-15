package activities

import (
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

// M6 (D89): an issue sent back by the queue is running again while its coder works.
func TestLeaveQueue(t *testing.T) {
	f := newQueueActivityFixture(t)
	id := uuid.MustParse(f.in.IssueID)
	if _, err := f.a.Store.TransitionIssue(t.Context(), id, domain.IssueRunning, domain.IssueQueued); err != nil {
		t.Fatal(err)
	}
	for range 2 { // a retry finds it running already
		if _, err := activityEnv(f.a).ExecuteActivity(f.a.LeaveQueue, f.in.IssueID); err != nil {
			t.Fatal(err)
		}
	}
	if is, _ := f.a.Store.GetIssue(t.Context(), id); is.Status != domain.IssueRunning {
		t.Errorf("issue %s", is.Status)
	}
}
