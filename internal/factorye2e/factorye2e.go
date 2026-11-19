// Package factorye2e is the factory's own end-to-end check (D132). It runs in
// a verification pod against a preview of the factory, a whole factory built
// from a change request (D131), whose setup Job opened and started one issue
// in misarescu/frankie-dummy. It waits for that issue to end and reports each
// of its runs' steps as a JUnit test, so a failed check names the step where
// the factory broke. It only reads the preview's API, which needs no token.
package factorye2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config is what a check needs.
type Config struct {
	// BaseURL is the preview's API (E2E_BASE_URL).
	BaseURL string
	// Project is the repo whose issue the preview builds.
	Project string
	// Timeout bounds waiting for the issue to end; Poll is how often it asks.
	Timeout, Poll time.Duration
	HTTP          *http.Client
	// Log gets a line whenever what the check sees changes.
	Log io.Writer
}

// Case is one test of the report: a step, or whether the issue merged.
// Failure is a factory fault; Error is a check that could not judge it (the
// preview's API unreachable), which verification treats as inconclusive.
type Case struct {
	Suite, Name    string
	Failure, Error string
}

// Report is what a check found.
type Report struct {
	Cases []Case
}

// Passed reports whether every case passed.
func (r Report) Passed() bool {
	for _, c := range r.Cases {
		if c.Failure != "" || c.Error != "" {
			return false
		}
	}
	return len(r.Cases) > 0
}

// issue is an issue as GET /api/issues lists it.
type issue struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}

// issueDetail is what the check reads of GET /api/issues/{key}.
type issueDetail struct {
	Key         string `json:"key"`
	Status      string `json:"status"`
	CloseReason string `json:"close_reason"`
	Runs        []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"runs"`
}

// step is what the check reads of GET /api/runs/{id}/steps.
type step struct {
	StepID   string  `json:"step_id"`
	Attempt  int     `json:"attempt"`
	Kind     string  `json:"kind"`
	Status   string  `json:"status"`
	CostUSD  float64 `json:"cost_usd"`
	WaitsFor string  `json:"waits_for"`
	Role     string  `json:"role"`
	Gate     string  `json:"gate"`
}

// ended reports whether an issue's status is final for the check: merged,
// released, failed or canceled, or waiting for a person, whom a preview
// never has.
func ended(status string) bool {
	switch status {
	case "merged", "released", "failed", "canceled", "awaiting_human":
		return true
	}
	return false
}

// Run waits until every issue of the project has ended, or the timeout
// passes, and reports what it found.
func Run(ctx context.Context, cfg Config) Report {
	c := client{cfg: cfg}
	deadline := time.Now().Add(cfg.Timeout)
	var issues []issue
	var lastErr error
	reached, last := false, ""
	for {
		got, err := c.issues(ctx)
		if err != nil {
			lastErr = err
		} else {
			reached, issues = true, got
		}
		if seen := describe(issues, err); seen != last {
			c.logf("%s", seen)
			last = seen
		}
		if reached && len(issues) > 0 && allEnded(issues) {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(cfg.Poll):
		}
	}

	const suite = "factory e2e"
	switch {
	case !reached:
		return Report{Cases: []Case{{Suite: suite, Name: "the preview's API answers",
			Error: fmt.Sprintf("could not read %s's issues from %s within %s: %v", cfg.Project, cfg.BaseURL, cfg.Timeout, lastErr)}}}
	case len(issues) == 0:
		return Report{Cases: []Case{{Suite: suite, Name: "an issue is taken in",
			Failure: fmt.Sprintf("no issue of %s appeared within %s: intake took nothing in", cfg.Project, cfg.Timeout)}}}
	}
	var r Report
	for _, is := range issues {
		r.Cases = append(r.Cases, c.issueCases(ctx, suite+" > "+is.Key, is)...)
	}
	return r
}

// issueCases reports one issue: a case per step of its runs, then whether it
// merged.
func (c client) issueCases(ctx context.Context, suite string, is issue) []Case {
	d, err := c.issue(ctx, is.Key)
	if err != nil {
		return []Case{{Suite: suite, Name: "the issue can be read", Error: err.Error()}}
	}
	var out []Case
	for i, run := range d.Runs {
		steps, err := c.steps(ctx, run.ID)
		if err != nil {
			out = append(out, Case{Suite: suite, Name: fmt.Sprintf("run %d: its steps can be read", i+1), Error: err.Error()})
			continue
		}
		for _, s := range steps {
			out = append(out, stepCase(suite, i+1, len(d.Runs), d.Status, s))
		}
	}
	merged := Case{Suite: suite, Name: "the issue merged"}
	switch {
	case d.Status == "merged" || d.Status == "released":
	case d.Status == "awaiting_human":
		merged.Failure = "the issue waits for a person, whom a preview never has: a gate or question needs a decision"
	case ended(d.Status):
		merged.Failure = "the issue ended " + d.Status
		if d.CloseReason != "" {
			merged.Failure += ": " + d.CloseReason
		}
	default:
		merged.Failure = fmt.Sprintf("the issue is still %s after %s", d.Status, c.cfg.Timeout)
	}
	return append(out, merged)
}

// stepCase is one step. Once the issue merged every step passed: a gate that
// failed and sent the coder back is the factory working. Otherwise a failed or
// canceled step is a failure, as is one waiting for a person or still going.
func stepCase(suite string, run, runs int, issueStatus string, s step) Case {
	name := fmt.Sprintf("%s #%d", s.StepID, s.Attempt)
	if runs > 1 {
		name = fmt.Sprintf("run %d: %s", run, name)
	}
	what := s.Kind
	switch {
	case s.Gate != "":
		what = s.Gate + " gate"
	case s.Role != "":
		what = s.Role
	}
	c := Case{Suite: suite, Name: name}
	switch {
	case issueStatus == "merged" || issueStatus == "released":
	case s.Status == "failed":
		c.Failure = fmt.Sprintf("%s %s failed on attempt %d", what, s.StepID, s.Attempt)
	case s.Status == "canceled":
		c.Failure = fmt.Sprintf("%s %s was canceled on attempt %d", what, s.StepID, s.Attempt)
	case s.WaitsFor == "approval" || s.WaitsFor == "answer":
		c.Failure = fmt.Sprintf("%s %s waits for a person's %s", what, s.StepID, s.WaitsFor)
	case s.Status == "pending" || s.Status == "running":
		c.Failure = fmt.Sprintf("%s %s is still %s", what, s.StepID, s.Status)
	}
	return c
}

func allEnded(issues []issue) bool {
	for _, is := range issues {
		if !ended(is.Status) {
			return false
		}
	}
	return true
}

func describe(issues []issue, err error) string {
	if err != nil {
		return "the preview's API: " + err.Error()
	}
	if len(issues) == 0 {
		return "no issue yet"
	}
	s := make([]string, len(issues))
	for i, is := range issues {
		s[i] = is.Key + " " + is.Status
	}
	return strings.Join(s, ", ")
}

// client reads the preview's API.
type client struct{ cfg Config }

func (c client) logf(format string, args ...any) {
	if c.cfg.Log != nil {
		_, _ = fmt.Fprintf(c.cfg.Log, format+"\n", args...)
	}
}

func (c client) issues(ctx context.Context) ([]issue, error) {
	var out []issue
	err := c.get(ctx, "/api/issues?project="+url.QueryEscape(c.cfg.Project), &out)
	return out, err
}

func (c client) issue(ctx context.Context, key string) (issueDetail, error) {
	var out issueDetail
	err := c.get(ctx, "/api/issues/"+url.PathEscape(key), &out)
	return out, err
}

func (c client) steps(ctx context.Context, runID string) ([]step, error) {
	var out []step
	err := c.get(ctx, "/api/runs/"+url.PathEscape(runID)+"/steps", &out)
	return out, err
}

func (c client) get(ctx context.Context, path string, v any) error {
	u := strings.TrimSuffix(c.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	return nil
}
