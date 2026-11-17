package config

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// DefaultE2EResults is where an e2e suite writes its JUnit XML results,
// relative to the repository, unless e2e.results says otherwise.
const DefaultE2EResults = "e2e-results.xml"

// E2E is how a project runs its end-to-end suite against a stage (verify:
// e2e-suite, D116). Command runs from the repository's root in its toolchain
// image with E2E_BASE_URL (the stage's url), E2E_RESULTS (where to write
// JUnit XML), E2E_STAGE and E2E_COMMIT set; it fails when a test fails. A
// test is tagged with the issues whose keys appear in its name or its suite's
// (FR-TA-3), e.g. a Bruno request "gc-65 search by tag".
type E2E struct {
	Command string `json:"command"`
	// Results is the JUnit XML file Command writes, relative to the
	// repository; DefaultE2EResults when empty.
	Results string `json:"results,omitempty"`
}

// ResultsPath is the results file, relative to the repository.
func (e E2E) ResultsPath() string {
	if e.Results == "" {
		return DefaultE2EResults
	}
	return e.Results
}

func (e E2E) validate() []error {
	var errs []error
	if strings.TrimSpace(e.Command) == "" {
		errs = append(errs, errors.New("e2e.command is required"))
	}
	if r := e.Results; r != "" {
		if path.IsAbs(r) || path.Clean(r) != r || r == "." || strings.HasPrefix(r, "../") || r == ".." {
			errs = append(errs, fmt.Errorf("e2e.results %q must be a clean path inside the repository", r))
		}
	}
	return errs
}
