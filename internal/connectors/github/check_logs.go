package github

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/misarescu/frankie/internal/domain"
)

// The prefixes of a Check's ID: a check run (for GitHub Actions, the job) or
// a GitHub Actions workflow run (D86).
const (
	checkRunPrefix    = "check-run/"
	workflowRunPrefix = "workflow-run/"
)

// CheckLog returns the end of a failed check's log (D120): the output of the
// job's first failed step, up to its error, at most limits.Lines lines and
// limits.Bytes bytes (zero limits are the defaults). For a workflow run (D86)
// it reads up to limits.Jobs of the run's failed jobs, each under a heading
// that names the job and its failed step. A check whose log GitHub does not
// keep (another app's check, a commit status) returns "".
func (c *Client) CheckLog(ctx context.Context, repo string, check domain.Check, limits domain.CILogLimits) (string, error) {
	limits = limits.WithDefaults()
	switch {
	case strings.HasPrefix(check.ID, checkRunPrefix):
		out, err := c.jobLog(ctx, repo, strings.TrimPrefix(check.ID, checkRunPrefix), limits)
		var se *StatusError
		if errors.As(err, &se) && se.Code == http.StatusNotFound {
			return "", nil // not a GitHub Actions job
		}
		return out, err
	case strings.HasPrefix(check.ID, workflowRunPrefix):
		return c.workflowRunLog(ctx, repo, strings.TrimPrefix(check.ID, workflowRunPrefix), limits)
	}
	return "", nil
}

// workflowRunLog reads the logs of a workflow run's failed jobs.
func (c *Client) workflowRunLog(ctx context.Context, repo, runID string, limits domain.CILogLimits) (string, error) {
	if _, err := strconv.ParseInt(runID, 10, 64); err != nil {
		return "", fmt.Errorf("github: bad workflow run id %q", runID)
	}
	var resp struct {
		Jobs []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
			Steps      []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
			} `json:"steps"`
		} `json:"jobs"`
	}
	q := url.Values{"filter": {"latest"}, "per_page": {"100"}}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo+"/actions/runs/"+runID+"/jobs?"+q.Encode(), nil, &resp); err != nil {
		return "", err
	}
	var parts []string
	for _, j := range resp.Jobs {
		if j.Conclusion != "failure" && j.Conclusion != "timed_out" {
			continue
		}
		if len(parts) == limits.Jobs {
			break
		}
		heading := "Job " + j.Name
		for _, s := range j.Steps {
			if s.Conclusion == "failure" || s.Conclusion == "timed_out" {
				heading += ", step " + s.Name
				break
			}
		}
		log, err := c.jobLog(ctx, repo, strconv.FormatInt(j.ID, 10), limits)
		if err != nil {
			return "", err
		}
		if log != "" {
			parts = append(parts, heading+":\n"+log)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// jobLog reads a GitHub Actions job's log, which GitHub serves as a redirect
// to plain text, and returns its excerpt.
func (c *Client) jobLog(ctx context.Context, repo, jobID string, limits domain.CILogLimits) (string, error) {
	if _, err := strconv.ParseInt(jobID, 10, 64); err != nil {
		return "", fmt.Errorf("github: bad job id %q", jobID)
	}
	var out string
	err := c.stream(ctx, "/repos/"+repo+"/actions/jobs/"+jobID+"/logs", func(r io.Reader) error {
		var err error
		out, err = logExcerpt(r, limits.Lines, limits.Bytes)
		return err
	})
	return out, err
}

var (
	logTimestamp = regexp.MustCompile(`^\x{feff}?\d{4}-\d\d-\d\dT[\d:.]+Z ?`)
	ansiEscape   = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
)

// logStep is the output of one step of a job's log: the line that started
// it and its last lines.
type logStep struct {
	header   string
	lines    []string
	dropped  int
	hasError bool
}

func (s *logStep) add(line string, keep int) {
	s.lines = append(s.lines, line)
	if len(s.lines) > keep {
		s.lines = s.lines[1:]
		s.dropped++
	}
}

// logExcerpt reads a GitHub Actions job log and returns the output of its
// first step that reported an error (`##[error]`), from the line that ran it
// to the end of that step, keeping the last maxLines lines and maxBytes bytes;
// without an error, the last maxLines lines of the log. Timestamps and
// terminal colours are removed. It stops reading once the failed step ended.
func logExcerpt(r io.Reader, maxLines, maxBytes int) (string, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var step, tail logStep
	for {
		raw, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// A very long line: keep its start, skip the rest.
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
		}
		if len(raw) > 0 {
			line := strings.TrimRight(string(raw), "\r\n")
			line = ansiEscape.ReplaceAllString(logTimestamp.ReplaceAllString(line, ""), "")
			// The next step starts, or the job's post steps do.
			if strings.HasPrefix(line, "##[group]Run ") || line == "Post job cleanup." {
				if step.hasError {
					return step.render(maxBytes), nil
				}
				step = logStep{}
				if line != "Post job cleanup." {
					step.header = strings.TrimPrefix(line, "##[group]")
				}
				tail.add(strings.TrimPrefix(line, "##[group]"), maxLines)
				continue
			}
			switch {
			case line == "##[endgroup]":
				continue
			case strings.HasPrefix(line, "##[group]"):
				line = strings.TrimPrefix(line, "##[group]")
			case strings.HasPrefix(line, "##[error]"):
				line, step.hasError = "Error: "+strings.TrimPrefix(line, "##[error]"), true
			case strings.HasPrefix(line, "##[warning]"):
				line = "Warning: " + strings.TrimPrefix(line, "##[warning]")
			}
			step.add(line, maxLines)
			tail.add(line, maxLines)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("github: read log: %w", err)
		}
	}
	if step.hasError {
		return step.render(maxBytes), nil
	}
	return tail.render(maxBytes), nil
}

// render writes the step's header, how many lines were left out and its last
// lines, dropping more of the earliest ones until it fits in maxBytes.
func (s logStep) render(maxBytes int) string {
	lines, dropped := s.lines, s.dropped
	for {
		var b strings.Builder
		if s.header != "" {
			b.WriteString(s.header + "\n")
		}
		switch {
		case dropped == 1:
			b.WriteString("... (1 line left out)\n")
		case dropped > 1:
			fmt.Fprintf(&b, "... (%d lines left out)\n", dropped)
		}
		b.WriteString(strings.Join(lines, "\n"))
		if b.Len() <= maxBytes || len(lines) <= 1 {
			out := b.String()
			if len(out) > maxBytes {
				out = out[len(out)-maxBytes:]
			}
			return strings.TrimRight(out, "\n")
		}
		lines, dropped = lines[1:], dropped+1
	}
}
