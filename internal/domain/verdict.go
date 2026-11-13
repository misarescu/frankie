package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Severity ranks a review finding.
type Severity string

// Finding severities, most severe first.
const (
	SeverityBlocker Severity = "blocker"
	SeverityMajor   Severity = "major"
	SeverityMinor   Severity = "minor"
	SeverityInfo    Severity = "info"
)

// Severities lists the known severities, most severe first.
var Severities = []Severity{SeverityBlocker, SeverityMajor, SeverityMinor, SeverityInfo}

// Valid reports whether s is a known severity.
func (s Severity) Valid() bool {
	for _, k := range Severities {
		if s == k {
			return true
		}
	}
	return false
}

// Finding is one problem a reviewer reports.
type Finding struct {
	Severity Severity `json:"severity"`
	File     string   `json:"file,omitempty"`
	Line     int      `json:"line,omitempty"`
	Message  string   `json:"message"`
}

// ReviewVerdict is what a reviewer role returns for an agent-verdict gate (the
// `review-verdict` output contract; its JSON Schema is roles/schemas/review-verdict.json).
type ReviewVerdict struct {
	Verdict  GateVerdict `json:"verdict"` // pass or fail
	Summary  string      `json:"summary,omitempty"`
	Findings []Finding   `json:"findings"`
}

// Limits keep a verdict small enough to store and to feed back to the coder.
const (
	maxFindings       = 100
	maxFindingMessage = 4000
	maxVerdictSummary = 4000
)

// ParseReviewVerdict decodes and validates a reviewer's verdict strictly: unknown
// fields, a verdict other than pass or fail, and a pass that carries a blocker
// are all rejected.
func ParseReviewVerdict(raw []byte) (ReviewVerdict, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var v ReviewVerdict
	if err := dec.Decode(&v); err != nil {
		return ReviewVerdict{}, fmt.Errorf("review verdict: %w", err)
	}
	if dec.More() {
		return ReviewVerdict{}, errors.New("review verdict: trailing data after the JSON object")
	}
	if err := v.Validate(); err != nil {
		return ReviewVerdict{}, err
	}
	return v, nil
}

// Validate checks the verdict against the contract.
func (v ReviewVerdict) Validate() error {
	var errs []error
	if v.Verdict != GatePass && v.Verdict != GateFail {
		errs = append(errs, fmt.Errorf("verdict %q must be pass or fail", v.Verdict))
	}
	if v.Findings == nil {
		errs = append(errs, errors.New("findings is required (use [] for none)"))
	}
	if len(v.Findings) > maxFindings {
		errs = append(errs, fmt.Errorf("%d findings, at most %d", len(v.Findings), maxFindings))
	}
	if len(v.Summary) > maxVerdictSummary {
		errs = append(errs, fmt.Errorf("summary is longer than %d bytes", maxVerdictSummary))
	}
	for i, f := range v.Findings {
		if !f.Severity.Valid() {
			errs = append(errs, fmt.Errorf("findings[%d]: unknown severity %q", i, f.Severity))
		}
		if strings.TrimSpace(f.Message) == "" {
			errs = append(errs, fmt.Errorf("findings[%d]: message is required", i))
		}
		if len(f.Message) > maxFindingMessage {
			errs = append(errs, fmt.Errorf("findings[%d]: message is longer than %d bytes", i, maxFindingMessage))
		}
		if f.Line < 0 {
			errs = append(errs, fmt.Errorf("findings[%d]: line must not be negative", i))
		}
		if f.Line > 0 && f.File == "" {
			errs = append(errs, fmt.Errorf("findings[%d]: a line needs a file", i))
		}
		if v.Verdict == GatePass && f.Severity == SeverityBlocker {
			errs = append(errs, fmt.Errorf("findings[%d]: a passing verdict cannot carry a blocker", i))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("review verdict: %w", err)
	}
	return nil
}
