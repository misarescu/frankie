package flux

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PreviewConfig is a preview stage's `config` for the flux-preview driver
// (D124, D125).
type PreviewConfig struct {
	// Repo is the GitOps repository, "owner/name".
	Repo string `json:"repo"`
	// Branch is the branch Flux syncs; default main.
	Branch string `json:"branch,omitempty"`
	// Path is the inputs file: a ResourceSet document whose spec.inputs the
	// driver writes, one entry per change request with a preview.
	Path string `json:"path"`
	// Tag renders a change request head's image tag from {sha} and {short};
	// default "sha-{short}".
	Tag string `json:"tag,omitempty"`
	// VersionURL reports the version a preview runs, as JSON; {changeRequest}
	// is the change request's number. A preview runs the head once
	// VersionField matches its commit.
	VersionURL string `json:"versionURL"`
	// VersionField is the JSON field holding the version; default "version".
	VersionField string `json:"versionField,omitempty"`
	// MaxPreviews is how many previews run at once; default 2.
	MaxPreviews int `json:"maxPreviews,omitempty"`
}

// Preview limits.
const (
	defaultMaxPreviews = 2
	maxMaxPreviews     = 10
)

// parsePreviewConfig reads and checks a preview stage's config, filling the
// defaults.
func parsePreviewConfig(raw json.RawMessage) (PreviewConfig, error) {
	var c PreviewConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return PreviewConfig{}, fmt.Errorf("flux-preview config: %w", err)
	}
	if c.Branch == "" {
		c.Branch = "main"
	}
	if c.Tag == "" {
		c.Tag = "sha-{short}"
	}
	if c.VersionField == "" {
		c.VersionField = "version"
	}
	if c.MaxPreviews == 0 {
		c.MaxPreviews = defaultMaxPreviews
	}
	errs := checkShared(c.Repo, c.Branch, c.Path, c.Tag, c.VersionField)
	if !strings.Contains(c.VersionURL, "{changeRequest}") {
		errs = append(errs, fmt.Errorf("versionURL %q names no {changeRequest}", c.VersionURL))
	} else if err := checkURL("versionURL", c.versionURL(1)); err != nil {
		errs = append(errs, err)
	}
	if c.MaxPreviews < 1 || c.MaxPreviews > maxMaxPreviews {
		errs = append(errs, fmt.Errorf("maxPreviews %d is not between 1 and %d", c.MaxPreviews, maxMaxPreviews))
	}
	if err := errors.Join(errs...); err != nil {
		return PreviewConfig{}, fmt.Errorf("flux-preview config: %w", err)
	}
	return c, nil
}

// versionURL is the version URL of change request n's preview.
func (c PreviewConfig) versionURL(n int) string {
	return strings.ReplaceAll(c.VersionURL, "{changeRequest}", strconv.Itoa(n))
}
