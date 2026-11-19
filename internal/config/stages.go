package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/misarescu/frankie/internal/drivers"
)

// MaxStages caps the stages a project declares.
const MaxStages = 10

// Stage verifications (D113). e2e-suite runs the project's end-to-end suite
// against the stage's URL.
const VerifyE2ESuite = "e2e-suite"

// ChangeRequestPlaceholder in a preview stage's URL is the number of the
// change request whose preview it is (D126).
const ChangeRequestPlaceholder = "{changeRequest}"

// Stage gates. human waits for a person's approval before the stage deploys.
const GateHuman = "human"

// A stage's readyTimeout is between these (D131): the wait for a stage to run
// a deployment is an Activity that Temporal stops after 30 minutes.
const (
	MinReadyTimeout = time.Minute
	MaxReadyTimeout = 25 * time.Minute
)

// Stage is one stage a release (or, with a preview driver, a change request)
// moves through (docs/spec/06-delivery.md, D113). Stages are the project's
// configuration, in order.
type Stage struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
	// Config is the driver's configuration, a JSON object the driver checks
	// (ValidateStages); `{}` when the stage gives none.
	Config json.RawMessage `json:"config,omitempty"`
	// URL is where the stage serves the project, which verification runs
	// against, e.g. https://goodreads-stg.example.ts.net.
	URL string `json:"url,omitempty"`
	// Verify is how a deployment is checked once the stage runs it: "" (not
	// checked) or VerifyE2ESuite.
	Verify string `json:"verify,omitempty"`
	// Gate is "" or GateHuman: a person approves before the stage deploys.
	Gate string `json:"gate,omitempty"`
	// Rollback: true requires rolling a failed deployment back, so the
	// driver must support it; false never rolls back; unset rolls back when
	// the driver can (FR-RL-2).
	Rollback *bool `json:"rollback,omitempty"`
	// ReadyTimeout bounds waiting for the stage to run a deployment, as a Go
	// duration ("20m"), from MinReadyTimeout to MaxReadyTimeout; empty: the
	// platform's default (D131). A preview of the factory needs longer than an
	// app's, since it may wait for a node.
	ReadyTimeout string `json:"readyTimeout,omitempty"`
}

// ReadyWithin is the stage's readyTimeout, zero when it sets none.
func (s Stage) ReadyWithin() time.Duration {
	d, err := time.ParseDuration(s.ReadyTimeout)
	if err != nil {
		return 0
	}
	return d
}

// URLFor is the stage's URL for change request n's preview: the URL with
// ChangeRequestPlaceholder filled in.
func (s Stage) URLFor(n int) string {
	return strings.ReplaceAll(s.URL, ChangeRequestPlaceholder, strconv.Itoa(n))
}

// validateStages checks what needs no driver; it normalizes an absent config
// to `{}`.
func validateStages(stages []Stage) []error {
	var errs []error
	if len(stages) > MaxStages {
		errs = append(errs, fmt.Errorf("stages: %d declared, at most %d", len(stages), MaxStages))
	}
	seen := map[string]bool{}
	for i := range stages {
		s := &stages[i]
		at := fmt.Sprintf("stages[%d]", i)
		if !identifierRE.MatchString(s.Name) || len(s.Name) > maxIdentifierLength {
			errs = append(errs, fmt.Errorf("%s: name %q must be lowercase letters, digits and dashes, at most %d characters", at, s.Name, maxIdentifierLength))
		} else {
			at = fmt.Sprintf("stages.%s", s.Name)
		}
		if seen[s.Name] {
			errs = append(errs, fmt.Errorf("%s: duplicate stage name", at))
		}
		seen[s.Name] = true
		if !identifierRE.MatchString(s.Driver) || len(s.Driver) > maxIdentifierLength {
			errs = append(errs, fmt.Errorf("%s: driver %q is not a driver name", at, s.Driver))
		}
		if len(bytes.TrimSpace(s.Config)) == 0 || bytes.Equal(bytes.TrimSpace(s.Config), []byte("null")) {
			s.Config = json.RawMessage(`{}`)
		} else if err := drivers.ValidateObject(s.Config); err != nil {
			errs = append(errs, fmt.Errorf("%s: config %w", at, err))
		}
		if s.URL != "" {
			if err := validateStageURL(s.URLFor(1)); err != nil {
				errs = append(errs, fmt.Errorf("%s: url: %w", at, err))
			}
		}
		switch s.Verify {
		case "":
		case VerifyE2ESuite:
			if s.URL == "" {
				errs = append(errs, fmt.Errorf("%s: verify %s needs the stage's url", at, VerifyE2ESuite))
			}
		default:
			errs = append(errs, fmt.Errorf("%s: verify %q must be %q or empty", at, s.Verify, VerifyE2ESuite))
		}
		if s.Gate != "" && s.Gate != GateHuman {
			errs = append(errs, fmt.Errorf("%s: gate %q must be %q or empty", at, s.Gate, GateHuman))
		}
		if s.ReadyTimeout != "" {
			if d, err := time.ParseDuration(s.ReadyTimeout); err != nil || d < MinReadyTimeout || d > MaxReadyTimeout {
				errs = append(errs, fmt.Errorf("%s: readyTimeout %q must be a duration from %s to %s", at, s.ReadyTimeout, MinReadyTimeout, MaxReadyTimeout))
			}
		}
	}
	return errs
}

// validateStageURL accepts an absolute http(s) URL without credentials, query
// or fragment: verification appends its own paths.
func validateStageURL(raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return err
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("%q must be http or https", raw)
	case u.Host == "":
		return fmt.Errorf("%q has no host", raw)
	case u.User != nil:
		return fmt.Errorf("%q must not carry credentials", raw)
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("%q must not have a query or fragment", raw)
	}
	return nil
}

// StageDrivers looks up release drivers by name; *drivers.Registry is one.
type StageDrivers interface {
	Get(name string) (drivers.Driver, error)
}

// ValidateStages checks a project's stages against their drivers (FR-RL-3,
// D113): each driver exists and accepts the stage's config, a stage that
// requires rollback has a driver that can, a verified stage has a driver whose
// Status reflects the running target (else verification could run against
// the previous version), and a preview stage, at most one, comes before the
// release stages and has no gate. Stages come from a parsed Project.
func ValidateStages(ctx context.Context, stages []Stage, ds StageDrivers) error {
	var errs []error
	previews, releaseSeen := 0, false
	for _, s := range stages {
		at := "stages." + s.Name
		d, err := ds.Get(s.Driver)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", at, err))
			continue
		}
		caps := d.Capabilities()
		need := drivers.Capabilities{Health: s.Verify != ""}
		if s.Rollback != nil && *s.Rollback {
			need.Rollback = true
		}
		if missing := caps.Missing(need); len(missing) > 0 {
			errs = append(errs, fmt.Errorf("%s: driver %s cannot %s, which the stage needs", at, s.Driver, describeMissing(missing)))
		}
		if caps.Preview {
			previews++
			switch {
			case previews > 1:
				errs = append(errs, fmt.Errorf("%s: a project has at most one preview stage", at))
			case releaseSeen:
				errs = append(errs, fmt.Errorf("%s: the preview stage comes before the release stages", at))
			}
			if s.Gate != "" {
				errs = append(errs, fmt.Errorf("%s: a preview stage has no gate", at))
			}
		} else {
			releaseSeen = true
			if strings.Contains(s.URL, ChangeRequestPlaceholder) {
				errs = append(errs, fmt.Errorf("%s: url: only a preview stage's url names %s", at, ChangeRequestPlaceholder))
			}
		}
		if err := d.ValidateConfig(ctx, s.Config); err != nil {
			errs = append(errs, fmt.Errorf("%s: config: %w", at, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%s: %w", ProjectConfigPath, err)
	}
	return nil
}

func describeMissing(missing []string) string {
	words := map[string]string{
		"rollback":    "roll back",
		"progressive": "roll out progressively",
		"health":      "report whether the stage runs a deployment (health)",
		"preview":     "deploy previews",
	}
	out := make([]string, 0, len(missing))
	for _, m := range missing {
		out = append(out, words[m])
	}
	return strings.Join(out, " or ")
}

// PreviewStage returns the stage whose driver deploys previews, if any.
func PreviewStage(stages []Stage, ds StageDrivers) (Stage, bool) {
	for _, s := range stages {
		if d, err := ds.Get(s.Driver); err == nil && d.Capabilities().Preview {
			return s, true
		}
	}
	return Stage{}, false
}

// ReleaseStages returns the stages a release moves through, in order: all
// but the preview stage.
func ReleaseStages(stages []Stage, ds StageDrivers) []Stage {
	var out []Stage
	for _, s := range stages {
		if d, err := ds.Get(s.Driver); err == nil && d.Capabilities().Preview {
			continue
		}
		out = append(out, s)
	}
	return out
}
