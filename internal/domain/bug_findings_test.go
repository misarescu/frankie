package domain

import (
	"strings"
	"testing"
)

const goodFindings = `{"root_cause":"GetUserReadingStats sums shelf_books rows, so a book on two shelves counts twice",
"evidence":[{"file":"db/queries/shelf_books.sql","line":40,"note":"SUM over rows, not distinct books"}],
"reproduce":"put a finished book on two shelves and GET /api/users/1/stats","testable":true,
"test_plan":"storage test in internal/storage/shelf_books_test.go asserting books_finished 1","confidence":"high"}`

// M6 (D94): an investigation's findings are parsed strictly.
func TestParseBugFindings(t *testing.T) {
	f, err := ParseBugFindings([]byte(goodFindings))
	if err != nil || !f.Testable || f.Confidence != ConfidenceHigh || len(f.Evidence) != 1 {
		t.Fatalf("findings = %+v, %v", f, err)
	}
	for name, raw := range map[string]string{
		"unknown field":           strings.Replace(goodFindings, `"confidence"`, `"severity":"x","confidence"`, 1),
		"no root cause":           strings.Replace(goodFindings, `GetUserReadingStats sums shelf_books rows, so a book on two shelves counts twice`, ` `, 1),
		"no evidence":             strings.Replace(goodFindings, `[{"file":"db/queries/shelf_books.sql","line":40,"note":"SUM over rows, not distinct books"}]`, `[]`, 1),
		"testable without a plan": strings.Replace(goodFindings, `"test_plan":"storage test in internal/storage/shelf_books_test.go asserting books_finished 1",`, ``, 1),
		"untestable without why":  strings.Replace(goodFindings, `"testable":true`, `"testable":false`, 1),
		"bad confidence":          strings.Replace(goodFindings, `"high"`, `"sure"`, 1),
		"trailing data":           goodFindings + `{}`,
	} {
		if _, err := ParseBugFindings([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestBugFindingsFieldsAndMarkdown(t *testing.T) {
	f, _ := ParseBugFindings([]byte(goodFindings))
	if v, ok := f.Field("testable"); !ok || v != true {
		t.Errorf("testable = %v, %v", v, ok)
	}
	if v, ok := f.Field("confidence"); !ok || v != "high" {
		t.Errorf("confidence = %v, %v", v, ok)
	}
	if _, ok := f.Field("root_cause"); ok {
		t.Error("a free-text field is not a condition")
	}
	for field := range BugFindingsFields {
		if _, ok := f.Field(field); !ok {
			t.Errorf("listed field %q has no value", field)
		}
	}
	md := f.Markdown()
	for _, want := range []string{"**Root cause** (high confidence): GetUserReadingStats", "- `db/queries/shelf_books.sql:40`: SUM", "**Test that shows it:** storage test"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
}
