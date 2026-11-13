package domain

import "testing"

func TestApprovalStatusValid(t *testing.T) {
	for _, s := range []ApprovalStatus{ApprovalPending, ApprovalApproved, ApprovalRejected, ApprovalTimedOut} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if ApprovalStatus("maybe").Valid() {
		t.Error("unknown status accepted")
	}
}
