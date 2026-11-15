package activities

import (
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

// M6 (D92): rejecting closes the change request on the provider with the
// reason, and records it closed; a retry does it once.
func TestCloseChangeRequest(t *testing.T) {
	f := newQueueActivityFixture(t)
	var comments int
	f.prov.onComment = func(body string) {
		comments++
		if body != "Rejected in Frankie by ana: wrong approach" {
			t.Errorf("comment = %q", body)
		}
	}
	for range 2 {
		if _, err := activityEnv(f.a).ExecuteActivity(f.a.CloseChangeRequest, f.in, f.run.Branch, "Rejected in Frankie by ana: wrong approach"); err != nil {
			t.Fatal(err)
		}
	}
	crs, _ := f.a.Store.ListChangeRequestsForIssue(t.Context(), f.cr.IssueID)
	if crs[0].Status != string(domain.ChangeRequestClosed) || comments != 1 || f.prov.closed != 1 {
		t.Errorf("change request %s, %d comments, closed %d times", crs[0].Status, comments, f.prov.closed)
	}
}
