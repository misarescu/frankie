package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ReleaseNotes is what the release agent prepares for a release (the
// `release-notes` output contract, roles/schemas/release-notes.json; D129):
// the bump it proposes and why, and the notes.
type ReleaseNotes struct {
	Bump   Bump   `json:"bump"`
	Reason string `json:"reason"`
	// Notes are Markdown, one entry per issue key.
	Notes string `json:"notes"`
	// Breaking lists the changes that break existing clients, if any.
	Breaking []string `json:"breaking,omitempty"`
}

// ReleaseAnalysis is the release agent's reading of a failed verification
// (the `release-analysis` output contract; D129): the likely culprits.
type ReleaseAnalysis struct {
	Suspects []Suspect `json:"suspects"`
	Summary  string    `json:"summary"`
}

// Suspect is an issue of the release that may have broken it.
type Suspect struct {
	Issue      string     `json:"issue"` // its key, e.g. gc-74
	Likelihood Confidence `json:"likelihood"`
	Why        string     `json:"why"`
}

// Limits keep what the agent returns small enough to store, publish and put
// in an issue.
const (
	maxReleaseText  = 4000
	maxReleaseNotes = 60000
	maxBreaking     = 30
	maxSuspects     = 20
)

// ParseReleaseNotes decodes and validates release notes strictly.
func ParseReleaseNotes(raw []byte) (ReleaseNotes, error) {
	var n ReleaseNotes
	if err := decodeStrict(raw, &n); err != nil {
		return ReleaseNotes{}, fmt.Errorf("release notes: %w", err)
	}
	return n, n.Validate()
}

// Validate checks the notes against the contract.
func (n ReleaseNotes) Validate() error {
	var errs []error
	if !n.Bump.Valid() {
		errs = append(errs, fmt.Errorf("bump %q is not major, minor or patch", n.Bump))
	}
	errs = append(errs, text("reason", n.Reason, maxReleaseText), text("notes", n.Notes, maxReleaseNotes))
	if len(n.Breaking) > maxBreaking {
		errs = append(errs, fmt.Errorf("breaking: %d entries, at most %d", len(n.Breaking), maxBreaking))
	}
	for i, b := range n.Breaking {
		if err := text(fmt.Sprintf("breaking[%d]", i), b, maxReleaseText); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("release notes: %w", err)
	}
	return nil
}

// ParseReleaseAnalysis decodes and validates an analysis strictly.
func ParseReleaseAnalysis(raw []byte) (ReleaseAnalysis, error) {
	var a ReleaseAnalysis
	if err := decodeStrict(raw, &a); err != nil {
		return ReleaseAnalysis{}, fmt.Errorf("release analysis: %w", err)
	}
	return a, a.Validate()
}

// Validate checks the analysis against the contract.
func (a ReleaseAnalysis) Validate() error {
	errs := []error{text("summary", a.Summary, maxReleaseText)}
	if len(a.Suspects) > maxSuspects {
		errs = append(errs, fmt.Errorf("suspects: %d entries, at most %d", len(a.Suspects), maxSuspects))
	}
	for i, s := range a.Suspects {
		switch {
		case strings.TrimSpace(s.Issue) == "":
			errs = append(errs, fmt.Errorf("suspects[%d]: needs the issue's key", i))
		case s.Likelihood != ConfidenceHigh && s.Likelihood != ConfidenceMedium && s.Likelihood != ConfidenceLow:
			errs = append(errs, fmt.Errorf("suspects[%d]: likelihood %q is not high, medium or low", i, s.Likelihood))
		}
		if err := text(fmt.Sprintf("suspects[%d].why", i), s.Why, maxReleaseText); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("release analysis: %w", err)
	}
	return nil
}

// decodeStrict decodes one JSON object with no unknown fields or trailing data.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after the JSON object")
	}
	return nil
}

// text checks a required text field's length.
func text(name, s string, limit int) error {
	switch {
	case strings.TrimSpace(s) == "":
		return fmt.Errorf("%s is required", name)
	case len(s) > limit:
		return fmt.Errorf("%s is longer than %d bytes", name, limit)
	}
	return nil
}
