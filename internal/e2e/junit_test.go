package e2e

import (
	"slices"
	"testing"
)

// Bruno's JUnit reporter: one suite per request, one case per assertion.
const brunoJUnit = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="Books/gc-65 search by tag" tests="2" failures="1" errors="0">
    <testcase name="res.status eq 200" classname="api/Books/search.bru" time="0.01"/>
    <testcase name="res.body.books length gt 0" classname="api/Books/search.bru" time="0.01">
      <failure type="failure" message="expected 0 to be above 0"/>
    </testcase>
  </testsuite>
  <testsuite name="Users/create" tests="1" errors="1">
    <testcase name="request" classname="api/Users/create.bru">
      <error type="error">connect ECONNREFUSED 10.0.0.1:443</error>
    </testcase>
  </testsuite>
  <testsuite name="outer">
    <testsuite name="inner">
      <testcase name="skipped one"><skipped/></testcase>
    </testsuite>
  </testsuite>
</testsuites>`

// M7, D116.
func TestParseJUnit(t *testing.T) {
	cases, err := ParseJUnit([]byte(brunoJUnit))
	if err != nil {
		t.Fatal(err)
	}
	want := []Case{
		{Suite: "Books/gc-65 search by tag", Name: "res.status eq 200", Outcome: Passed},
		{Suite: "Books/gc-65 search by tag", Name: "res.body.books length gt 0", Outcome: Failed, Message: "expected 0 to be above 0"},
		{Suite: "Users/create", Name: "request", Outcome: Errored, Message: "connect ECONNREFUSED 10.0.0.1:443"},
		{Suite: "outer > inner", Name: "skipped one", Outcome: Skipped},
	}
	if !slices.Equal(cases, want) {
		t.Errorf("cases =\n%+v\nwant\n%+v", cases, want)
	}
	if got := cases[3].Title(); got != "outer > inner > skipped one" {
		t.Errorf("title = %q", got)
	}
}

func TestParseJUnitSingleSuiteAndClassname(t *testing.T) {
	cases, err := ParseJUnit([]byte(`<testsuite><testcase name="a" classname="pkg.A"/></testsuite>`))
	if err != nil || len(cases) != 1 || cases[0].Suite != "pkg.A" || cases[0].Title() != "pkg.A > a" {
		t.Errorf("cases = %+v, %v", cases, err)
	}
	if got := (Case{Name: "a"}).Title(); got != "a" {
		t.Errorf("title = %q", got)
	}
}

func TestParseJUnitRejects(t *testing.T) {
	for _, in := range []string{"", "  ", "<html/>", "<testsuites>"} {
		if _, err := ParseJUnit([]byte(in)); err == nil {
			t.Errorf("ParseJUnit(%q) accepted", in)
		}
	}
}
