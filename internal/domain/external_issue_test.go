package domain

import "testing"

// D60: a label or the source's issue type decides the type.
func TestSourceIssueType(t *testing.T) {
	tests := []struct {
		name   string
		in     ExternalIssue
		want   IssueType
		wantOK bool
	}{
		{"label", ExternalIssue{Labels: []string{"factory", "epic"}}, IssueEpic, true},
		{"label any case", ExternalIssue{Labels: []string{"Story"}}, IssueStory, true},
		{"source type", ExternalIssue{SourceType: "Bug"}, IssueBug, true},
		{"source type wins over label", ExternalIssue{SourceType: "Task", Labels: []string{"epic"}}, IssueTask, true},
		{"unknown source type falls back to labels", ExternalIssue{SourceType: "Feature", Labels: []string{"bug"}}, IssueBug, true},
		{"none", ExternalIssue{Labels: []string{"factory", "frontend"}}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SourceIssueType(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("SourceIssueType = %q, %v; want %q, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestInferIssueType(t *testing.T) {
	if got := InferIssueType(false, false); got != IssueTask {
		t.Errorf("leaf = %s, want task", got)
	}
	if got := InferIssueType(true, false); got != IssueStory {
		t.Errorf("parent of leaves = %s, want story", got)
	}
	if got := InferIssueType(true, true); got != IssueEpic {
		t.Errorf("grandparent = %s, want epic", got)
	}
}
