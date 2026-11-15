package activities

import (
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

// M6 (D87): the queue skips an issue that left the queue and sees a change
// request a person merged or closed.
func TestCheckQueueEntry(t *testing.T) {
	f := newQueueActivityFixture(t)
	check := func() QueueEntryState {
		t.Helper()
		v, err := activityEnv(f.a).ExecuteActivity(f.a.CheckQueueEntry, f.in, 9)
		if err != nil {
			t.Fatal(err)
		}
		var s QueueEntryState
		_ = v.Get(&s)
		return s
	}
	// StartRun moved the issue to running; it is not in the queue.
	if s := check(); s.Queued || s.ChangeRequest != domain.ChangeRequestOpen {
		t.Errorf("running issue = %+v", s)
	}
	if _, err := f.a.Store.TransitionIssue(t.Context(), uuid.MustParse(f.in.IssueID), domain.IssueRunning, domain.IssueQueued); err != nil {
		t.Fatal(err)
	}
	f.prov.pr = `{"state":"closed","merged":true}`
	if s := check(); !s.Queued || s.ChangeRequest != domain.ChangeRequestMerged {
		t.Errorf("queued issue merged by hand = %+v", s)
	}
}
