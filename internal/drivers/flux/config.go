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
	errs := checkShared(c.Repo, c.Branch, c.Path, c.Tag, c.VersionField)
	if c.Image == "" || strings.ContainsAny(c.Image, " \t\"'#") {
		errs = append(errs, fmt.Errorf("image %q is not an image name", c.Image))
	}
	if err := checkURL("versionURL", c.VersionURL); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("flux config: %w", err)
	}
	return c, nil
}

// checkShared checks the fields the flux and flux-preview drivers share: the
// GitOps repository, its branch, the file the driver writes, the image tag
// template and the version field.
func checkShared(repo, branch, file, tag, versionField string) []error {
	var errs []error
	if !repoRE.MatchString(repo) {
		errs = append(errs, fmt.Errorf("repo %q is not owner/name", repo))
	}
	if strings.ContainsAny(branch, " ~^:?*[\\") || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") {
		errs = append(errs, fmt.Errorf("branch %q is not a branch name", branch))
	}
	yamlFile := strings.HasSuffix(file, ".yaml") || strings.HasSuffix(file, ".yml")
	if file == "" || path.IsAbs(file) || path.Clean(file) != file || strings.HasPrefix(file, "../") || !yamlFile {
		errs = append(errs, fmt.Errorf("path %q must be a clean relative path to a .yaml file", file))
	}
	if !strings.Contains(tag, "{sha}") && !strings.Contains(tag, "{short}") {
		errs = append(errs, fmt.Errorf("tag %q names neither {sha} nor {short}", tag))
	} else if !tagRE.MatchString(renderTag(tag, strings.Repeat("a", 40))) {
		errs = append(errs, fmt.Errorf("tag %q does not render an image tag", tag))
	}
	if !fieldRE.MatchString(versionField) {
		errs = append(errs, fmt.Errorf("versionField %q is not a field name", versionField))
	}
	return errs
}

// checkURL checks raw is an http(s) URL without credentials.
func checkURL(name, raw string) error {
	if u, err := url.Parse(raw); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return fmt.Errorf("%s %q must be an http(s) URL without credentials", name, raw)
	}
	return nil
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
