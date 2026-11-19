package factorye2e

import (
	"encoding/xml"
	"fmt"
)

type junitSuites struct {
	XMLName xml.Name     `xml:"testsuites"`
	Suites  []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Classname string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Failure   *junitMessage `xml:"failure,omitempty"`
	Error     *junitMessage `xml:"error,omitempty"`
}

type junitMessage struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// JUnit writes the report as JUnit XML, one suite per issue in the order
// found, which the verification reads like any project's suite (D116).
func (r Report) JUnit() ([]byte, error) {
	var out junitSuites
	index := map[string]int{}
	for _, c := range r.Cases {
		i, ok := index[c.Suite]
		if !ok {
			i = len(out.Suites)
			index[c.Suite] = i
			out.Suites = append(out.Suites, junitSuite{Name: c.Suite})
		}
		s := &out.Suites[i]
		jc := junitCase{Classname: c.Suite, Name: c.Name}
		switch {
		case c.Failure != "":
			jc.Failure = &junitMessage{Message: c.Failure, Text: c.Failure}
			s.Failures++
		case c.Error != "":
			jc.Error = &junitMessage{Message: c.Error, Text: c.Error}
			s.Errors++
		}
		s.Tests++
		s.Cases = append(s.Cases, jc)
	}
	b, err := xml.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("factorye2e: JUnit: %w", err)
	}
	return append([]byte(xml.Header), append(b, '\n')...), nil
}
