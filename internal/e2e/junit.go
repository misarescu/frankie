// Package e2e reads the results of a project's end-to-end suite (D116): JUnit
// XML, which Bruno and Playwright both write, and the issue keys its tests are
// tagged with (FR-TA-3).
package e2e

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
)

// Outcome is how one test case ended.
type Outcome string

// Outcomes. An error is a test that could not run as written (e.g. its request
// failed to connect); a failure is an assertion that did not hold.
const (
	Passed  Outcome = "passed"
	Failed  Outcome = "failed"
	Errored Outcome = "errored"
	Skipped Outcome = "skipped"
)

// Case is one test case of a suite.
type Case struct {
	Suite   string // the enclosing suites' names, outermost first, joined by " > "
	Name    string
	Outcome Outcome
	Message string // why it failed or errored
}

// Title is the case's suite and name.
func (c Case) Title() string {
	if c.Suite == "" {
		return c.Name
	}
	return c.Suite + " > " + c.Name
}

type junitSuite struct {
	Name   string       `xml:"name,attr"`
	Suites []junitSuite `xml:"testsuite"`
	Cases  []junitCase  `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Failures  []junitResult `xml:"failure"`
	Errors    []junitResult `xml:"error"`
	Skipped   *struct{}     `xml:"skipped"`
}

type junitResult struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// ParseJUnit reads JUnit XML: a <testsuites> root or a single <testsuite>,
// suites nested in suites.
func ParseJUnit(data []byte) ([]Case, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("e2e: empty JUnit results")
	}
	var root struct {
		XMLName xml.Name
		junitSuite
	}
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("e2e: JUnit results: %w", err)
	}
	var out []Case
	switch root.XMLName.Local {
	case "testsuites":
		for _, s := range root.Suites {
			out = appendSuite(out, nil, s)
		}
		out = appendSuite(out, nil, junitSuite{Cases: root.Cases})
	case "testsuite":
		out = appendSuite(out, nil, root.junitSuite)
	default:
		return nil, fmt.Errorf("e2e: JUnit results start with <%s>, not <testsuites> or <testsuite>", root.XMLName.Local)
	}
	return out, nil
}

func appendSuite(out []Case, parents []string, s junitSuite) []Case {
	names := parents
	if s.Name != "" {
		names = append(append([]string{}, parents...), s.Name)
	}
	for _, c := range s.Cases {
		suite := strings.Join(names, " > ")
		if suite == "" {
			suite = c.Classname
		}
		k := Case{Suite: suite, Name: c.Name, Outcome: Passed}
		switch {
		case len(c.Failures) > 0:
			k.Outcome, k.Message = Failed, message(c.Failures[0])
		case len(c.Errors) > 0:
			k.Outcome, k.Message = Errored, message(c.Errors[0])
		case c.Skipped != nil:
			k.Outcome = Skipped
		}
		out = append(out, k)
	}
	for _, child := range s.Suites {
		out = appendSuite(out, names, child)
	}
	return out
}

func message(r junitResult) string {
	if m := strings.TrimSpace(r.Message); m != "" {
		return m
	}
	return strings.TrimSpace(r.Text)
}
