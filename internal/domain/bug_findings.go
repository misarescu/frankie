package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Confidence is how sure an investigation is of its root cause.
type Confidence string

// Confidence levels.
const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Evidence is one place in the code that shows the root cause.
type Evidence struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
	Note string `json:"note"`
}

// BugFindings is what a bug investigation returns (the `bug-findings` output
// contract, roles/schemas/bug-findings.json; D94): the root cause with the
// code that shows it, how to reproduce the bug, and whether a test can.
type BugFindings struct {
	RootCause string     `json:"root_cause"`
	Evidence  []Evidence `json:"evidence"`
	Reproduce string     `json:"reproduce"`
	// Testable says an automated test can show the bug; TestPlan then says
	// which test, and WhyUntestable otherwise says why none can.
	Testable      bool       `json:"testable"`
	TestPlan      string     `json:"test_plan,omitempty"`
	WhyUntestable string     `json:"why_untestable,omitempty"`
	Confidence    Confidence `json:"confidence"`
}

// Limits keep findings small enough to store and to put in prompts.
const (
	maxFindingsText = 4000
	maxEvidence     = 30
)

// ParseBugFindings decodes and validates findings strictly.
func ParseBugFindings(raw []byte) (BugFindings, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f BugFindings
	if err := dec.Decode(&f); err != nil {
		return BugFindings{}, fmt.Errorf("bug findings: %w", err)
	}
	if dec.More() {
		return BugFindings{}, errors.New("bug findings: trailing data after the JSON object")
	}
	return f, f.Validate()
}

// Validate checks the findings against the contract.
func (f BugFindings) Validate() error {
	var errs []error
	text := func(name, s string, required bool) {
		switch {
		case required && strings.TrimSpace(s) == "":
			errs = append(errs, fmt.Errorf("%s is required", name))
		case len(s) > maxFindingsText:
			errs = append(errs, fmt.Errorf("%s is longer than %d bytes", name, maxFindingsText))
		}
	}
	text("root_cause", f.RootCause, true)
	text("reproduce", f.Reproduce, true)
	text("test_plan", f.TestPlan, f.Testable)
	text("why_untestable", f.WhyUntestable, !f.Testable)
	switch {
	case len(f.Evidence) == 0:
		errs = append(errs, errors.New("evidence: at least one place in the code is required"))
	case len(f.Evidence) > maxEvidence:
		errs = append(errs, fmt.Errorf("evidence: %d entries, at most %d", len(f.Evidence), maxEvidence))
	}
	for i, e := range f.Evidence {
		if strings.TrimSpace(e.File) == "" || strings.TrimSpace(e.Note) == "" || e.Line < 0 || len(e.Note) > maxFindingsText {
			errs = append(errs, fmt.Errorf("evidence[%d]: needs a file and a note (and a line of 1 or more, if any)", i))
		}
	}
	switch f.Confidence {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
	default:
		errs = append(errs, fmt.Errorf("confidence %q must be high, medium or low", f.Confidence))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("bug findings: %w", err)
	}
	return nil
}

// BugFindingsFields are the findings a workflow step can be conditional on
// (`when`, D94), with the kind of value each holds.
var BugFindingsFields = map[string]string{"testable": "bool", "confidence": "string"}

// Field returns a findings field a `when` condition reads.
func (f BugFindings) Field(name string) (any, bool) {
	switch name {
	case "testable":
		return f.Testable, true
	case "confidence":
		return string(f.Confidence), true
	}
	return nil, false
}

// Markdown renders the findings for a later step's prompt and the change
// request's description.
func (f BugFindings) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Root cause** (%s confidence): %s\n\n", f.Confidence, strings.TrimSpace(f.RootCause))
	b.WriteString("**Evidence:**\n")
	for _, e := range f.Evidence {
		if e.Line > 0 {
			fmt.Fprintf(&b, "- `%s:%d`: %s\n", e.File, e.Line, e.Note)
		} else {
			fmt.Fprintf(&b, "- `%s`: %s\n", e.File, e.Note)
		}
	}
	fmt.Fprintf(&b, "\n**How to reproduce:** %s\n", strings.TrimSpace(f.Reproduce))
	if f.Testable {
		fmt.Fprintf(&b, "\n**Test that shows it:** %s\n", strings.TrimSpace(f.TestPlan))
	} else {
		fmt.Fprintf(&b, "\n**No test can show it:** %s\n", strings.TrimSpace(f.WhyUntestable))
	}
	return b.String()
}
