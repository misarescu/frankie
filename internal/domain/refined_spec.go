package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// RefinedSpec is what the refiner role returns: the issue rewritten into a spec
// a coder can implement and acceptance criteria tests can be written from (the
// `refined-spec` output contract, roles/schemas/refined-spec.json, D65). A person
// approves it before the issue runs.
type RefinedSpec struct {
	Spec               string   `json:"spec"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	// FileHints are the paths the work will likely touch, for overlap warnings (D63).
	FileHints []string `json:"file_hints,omitempty"`
	// Summary tells the approver what the refiner changed or assumed, and why.
	Summary string `json:"summary,omitempty"`
}

// Limits keep a refined spec small enough to store, review and prompt with.
const (
	maxRefinedSpec        = 20000
	maxCriteria           = 30
	maxCriterion          = 1000
	maxRefinedFileHints   = 50
	maxRefinedFileHint    = 300
	maxRefinedSpecSummary = 2000
)

// ParseRefinedSpec decodes and validates a refiner's result strictly: unknown
// fields, trailing data and anything outside the contract are rejected.
func ParseRefinedSpec(raw []byte) (RefinedSpec, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var r RefinedSpec
	if err := dec.Decode(&r); err != nil {
		return RefinedSpec{}, fmt.Errorf("refined spec: %w", err)
	}
	if dec.More() {
		return RefinedSpec{}, errors.New("refined spec: trailing data after the JSON object")
	}
	if err := r.Validate(); err != nil {
		return RefinedSpec{}, err
	}
	return r, nil
}

// Validate checks the result against the contract.
func (r RefinedSpec) Validate() error {
	var errs []error
	switch {
	case strings.TrimSpace(r.Spec) == "":
		errs = append(errs, errors.New("spec is required"))
	case len(r.Spec) > maxRefinedSpec:
		errs = append(errs, fmt.Errorf("spec is longer than %d bytes", maxRefinedSpec))
	}
	switch {
	case len(r.AcceptanceCriteria) == 0:
		errs = append(errs, errors.New("acceptance_criteria needs at least one criterion"))
	case len(r.AcceptanceCriteria) > maxCriteria:
		errs = append(errs, fmt.Errorf("%d acceptance criteria, at most %d", len(r.AcceptanceCriteria), maxCriteria))
	}
	for i, c := range r.AcceptanceCriteria {
		if strings.TrimSpace(c) == "" {
			errs = append(errs, fmt.Errorf("acceptance_criteria[%d] is empty", i))
		}
		if len(c) > maxCriterion {
			errs = append(errs, fmt.Errorf("acceptance_criteria[%d] is longer than %d bytes", i, maxCriterion))
		}
	}
	if len(r.FileHints) > maxRefinedFileHints {
		errs = append(errs, fmt.Errorf("%d file hints, at most %d", len(r.FileHints), maxRefinedFileHints))
	}
	for i, h := range r.FileHints {
		if h == "" || len(h) > maxRefinedFileHint || strings.HasPrefix(h, "/") || strings.Contains(h, "..") || strings.ContainsAny(h, " \t\n") {
			errs = append(errs, fmt.Errorf("file_hints[%d]: %q is not a path relative to the repository root", i, h))
		}
	}
	if len(r.Summary) > maxRefinedSpecSummary {
		errs = append(errs, fmt.Errorf("summary is longer than %d bytes", maxRefinedSpecSummary))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("refined spec: %w", err)
	}
	return nil
}
