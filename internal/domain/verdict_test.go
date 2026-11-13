package domain

import (
	"strings"
	"testing"
)

func TestParseReviewVerdict(t *testing.T) {
	v, err := ParseReviewVerdict([]byte(`{
		"verdict": "fail",
		"summary": "Handler ignores the error.",
		"findings": [
			{"severity": "blocker", "file": "internal/api/api.go", "line": 42, "message": "error from Decode is dropped"},
			{"severity": "info", "message": "consider a table test"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if v.Verdict != GateFail || len(v.Findings) != 2 || v.Findings[0].Line != 42 || v.Findings[1].File != "" {
		t.Fatalf("v = %+v", v)
	}
	if _, err := ParseReviewVerdict([]byte(`{"verdict":"pass","findings":[]}`)); err != nil {
		t.Fatalf("minimal pass: %v", err)
	}
}

func TestParseReviewVerdictRejects(t *testing.T) {
	tests := []struct{ name, json, want string }{
		{"not json", `verdict: pass`, "invalid character"},
		{"unknown field", `{"verdict":"pass","findings":[],"score":9}`, "unknown field"},
		{"trailing data", `{"verdict":"pass","findings":[]} {}`, "trailing data"},
		{"escalated is not a reviewer verdict", `{"verdict":"escalated","findings":[]}`, "must be pass or fail"},
		{"missing verdict", `{"findings":[]}`, "must be pass or fail"},
		{"missing findings", `{"verdict":"pass"}`, "findings is required"},
		{"unknown severity", `{"verdict":"fail","findings":[{"severity":"critical","message":"x"}]}`, `unknown severity "critical"`},
		{"empty message", `{"verdict":"fail","findings":[{"severity":"major","message":" "}]}`, "message is required"},
		{"line without file", `{"verdict":"fail","findings":[{"severity":"major","line":3,"message":"x"}]}`, "a line needs a file"},
		{"negative line", `{"verdict":"fail","findings":[{"severity":"major","file":"a.go","line":-1,"message":"x"}]}`, "must not be negative"},
		{"pass with blocker", `{"verdict":"pass","findings":[{"severity":"blocker","message":"x"}]}`, "cannot carry a blocker"},
		{"too many findings", `{"verdict":"fail","findings":[` + strings.Repeat(`{"severity":"info","message":"x"},`, 100) + `{"severity":"info","message":"x"}]}`, "at most 100"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseReviewVerdict([]byte(tt.json))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
