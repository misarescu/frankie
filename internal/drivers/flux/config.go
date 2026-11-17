package flux

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Config is a stage's `config` for the flux driver (D117).
type Config struct {
	// Repo is the GitOps repository, "owner/name".
	Repo string `json:"repo"`
	// Branch is the branch Flux syncs; default main.
	Branch string `json:"branch,omitempty"`
	// Path is the kustomization.yaml that pins the stage's image.
	Path string `json:"path"`
	// Image is the images entry whose newTag a release sets.
	Image string `json:"image"`
	// Tag renders a release's image tag from {sha} and {short} (7
	// characters); default "sha-{short}".
	Tag string `json:"tag,omitempty"`
	// VersionURL reports the version the stage runs, as JSON; the stage runs
	// a release once VersionField matches its commit.
	VersionURL string `json:"versionURL"`
	// VersionField is the JSON field holding the version; default "version".
	VersionField string `json:"versionField,omitempty"`
}

var (
	repoRE  = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	fieldRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tagRE   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

// parseConfig reads and checks a stage's config, filling the defaults.
func parseConfig(raw json.RawMessage) (Config, error) {
	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("flux config: %w", err)
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
	var errs []error
	if !repoRE.MatchString(c.Repo) {
		errs = append(errs, fmt.Errorf("repo %q is not owner/name", c.Repo))
	}
	if strings.ContainsAny(c.Branch, " ~^:?*[\\") || strings.HasPrefix(c.Branch, "-") || strings.Contains(c.Branch, "..") {
		errs = append(errs, fmt.Errorf("branch %q is not a branch name", c.Branch))
	}
	yamlFile := strings.HasSuffix(c.Path, ".yaml") || strings.HasSuffix(c.Path, ".yml")
	if c.Path == "" || path.IsAbs(c.Path) || path.Clean(c.Path) != c.Path || strings.HasPrefix(c.Path, "../") || !yamlFile {
		errs = append(errs, fmt.Errorf("path %q must be a clean relative path to a .yaml file", c.Path))
	}
	if c.Image == "" || strings.ContainsAny(c.Image, " \t\"'#") {
		errs = append(errs, fmt.Errorf("image %q is not an image name", c.Image))
	}
	if !strings.Contains(c.Tag, "{sha}") && !strings.Contains(c.Tag, "{short}") {
		errs = append(errs, fmt.Errorf("tag %q names neither {sha} nor {short}", c.Tag))
	} else if !tagRE.MatchString(renderTag(c.Tag, strings.Repeat("a", 40))) {
		errs = append(errs, fmt.Errorf("tag %q does not render an image tag", c.Tag))
	}
	if u, err := url.Parse(c.VersionURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		errs = append(errs, fmt.Errorf("versionURL %q must be an http(s) URL without credentials", c.VersionURL))
	}
	if !fieldRE.MatchString(c.VersionField) {
		errs = append(errs, fmt.Errorf("versionField %q is not a field name", c.VersionField))
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("flux config: %w", err)
	}
	return c, nil
}

// renderTag is a release's image tag.
func renderTag(tmpl, sha string) string {
	return strings.NewReplacer("{sha}", sha, "{short}", short(sha)).Replace(tmpl)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
