package domain

import "testing"

func TestQuestionStatusValid(t *testing.T) {
	for _, s := range []QuestionStatus{QuestionPending, QuestionAnswered, QuestionExpired} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if QuestionStatus("maybe").Valid() {
		t.Error("unknown status accepted")
	}
}
