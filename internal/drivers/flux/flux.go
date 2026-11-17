// Package flux is the `flux` release driver (D117): it deploys a release to a
// stage that a Flux instance syncs from a GitOps repository, by committing the
// stage's image tag there, and rolls it back by committing the previous tag.
// It holds no credential for the stage's cluster (D108): it writes to the
// repository through the git provider and learns whether the stage runs a
// release only from the version the app reports.
package flux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/drivers"
)

// Name is how stages name the driver.
const Name = "flux"

// Repo is what the driver needs of the git provider; *github.Client is one.
type Repo interface {
	ReadFile(ctx context.Context, repo, path, ref string) (domain.RepoFile, bool, error)
	WriteFile(ctx context.Context, repo, path, branch string, content []byte, blobSHA, message string) (string, error)
	FileCommits(ctx context.Context, repo, path, branch string, limit int) ([]domain.Commit, error)
}

// Driver implements drivers.Driver.
type Driver struct {
	repo Repo
	http *http.Client
}

// New returns the driver; client fetches the stages' version URLs (default:
// 10-second timeout).
func New(repo Repo, client *http.Client) *Driver {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Driver{repo: repo, http: client}
}

// state is the driver's record of a deployment.
type state struct {
	// Commit set the release's tag; empty when the stage already ran it.
	Commit      string `json:"commit,omitempty"`
	Tag         string `json:"tag,omitempty"`
	PreviousTag string `json:"previousTag,omitempty"`
	// RevertCommit put PreviousTag back.
	RevertCommit string `json:"revertCommit,omitempty"`
}

// Name implements drivers.Driver.
func (d *Driver) Name() string { return Name }

// Capabilities implements drivers.Driver: rollback by a commit, health from the
// version the app reports.
func (d *Driver) Capabilities() drivers.Capabilities {
	return drivers.Capabilities{Rollback: true, Health: true}
}

// ValidateConfig implements drivers.Driver.
func (d *Driver) ValidateConfig(_ context.Context, config json.RawMessage) error {
	if _, err := parseConfig(config); err != nil {
		return fmt.Errorf("%w: %w", drivers.ErrInvalidConfig, err)
	}
	return nil
}

func publishMarker(id fmt.Stringer) string  { return "Frankie-Deployment: " + id.String() }
func rollbackMarker(id fmt.Stringer) string { return "Frankie-Rollback: " + id.String() }

// Publish implements drivers.Driver: it commits the release's tag to the
// stage's kustomization unless the stage already pins it. A retry finds the
// commit an earlier attempt made by the deployment's marker in its message.
func (d *Driver) Publish(ctx context.Context, r drivers.Request) (drivers.State, error) {
	cfg, st, err := d.load(r)
	if err != nil {
		return nil, err
	}
	f, current, err := d.read(ctx, cfg, cfg.Branch)
	if err != nil {
		return nil, err
	}
	target := renderTag(cfg.Tag, r.Subject.SHA)
	if current == target {
		if st.Commit == "" {
			// Ours from an earlier attempt, or the stage already ran it.
			if c, prev, err := d.findCommit(ctx, cfg, publishMarker(r.Deployment.ID)); err != nil {
				return nil, err
			} else if c != "" {
				st.Commit, st.PreviousTag = c, prev
			}
		}
		st.Tag = target
		return encode(st)
	}
	content, err := setImageTag(f.Content, cfg.Image, target)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", drivers.ErrInvalidConfig, err)
	}
	msg := fmt.Sprintf("Release %s to %s\n\n%s now runs %s:%s (was %s), for Frankie release %s.\n\n%s",
		short(r.Subject.SHA), r.Stage.Name, r.Stage.Name, cfg.Image, target, current, r.Subject.ReleaseID, publishMarker(r.Deployment.ID))
	commit, err := d.repo.WriteFile(ctx, cfg.Repo, cfg.Path, cfg.Branch, content, f.BlobSHA, msg)
	if err != nil {
		return nil, fmt.Errorf("flux: commit %s to %s: %w", target, cfg.Repo, err)
	}
	return encode(state{Commit: commit, Tag: target, PreviousTag: current})
}

// Status implements drivers.Driver: ready once the stage's version URL reports
// the release's commit. Only the version is ever quoted back.
func (d *Driver) Status(ctx context.Context, r drivers.Request) (drivers.Status, error) {
	cfg, _, err := d.load(r)
	if err != nil {
		return drivers.Status{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.VersionURL, nil)
	if err != nil {
		return drivers.Status{}, fmt.Errorf("%w: %w", drivers.ErrInvalidConfig, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := d.http.Do(req)
	if err != nil {
		return drivers.Status{}, fmt.Errorf("flux: %s: %w", cfg.VersionURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return drivers.Status{Phase: drivers.PhaseProgressing, Message: fmt.Sprintf("the version URL answered %d", resp.StatusCode)}, nil
	}
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return drivers.Status{Phase: drivers.PhaseProgressing, Message: "the version URL did not answer JSON"}, nil
	}
	v, _ := body[cfg.VersionField].(string)
	switch {
	case v == "":
		return drivers.Status{Phase: drivers.PhaseProgressing, Message: "the version URL reports no " + cfg.VersionField}, nil
	case versionMatches(v, r.Subject.SHA):
		return drivers.Status{Phase: drivers.PhaseReady, Message: "version is " + v}, nil
	}
	if len(v) > 64 {
		v = v[:64] + "..."
	}
	return drivers.Status{Phase: drivers.PhaseProgressing, Message: "version is " + v}, nil
}

// versionMatches reports whether a reported version names commit sha: the
// full SHA or a prefix of at least 7 characters.
func versionMatches(version, sha string) bool {
	v := strings.ToLower(strings.TrimSpace(version))
	return len(v) >= 7 && strings.HasPrefix(sha, v)
}

// Promote implements drivers.Driver: the driver is not progressive.
func (d *Driver) Promote(context.Context, drivers.Request, drivers.RolloutStep) (drivers.State, error) {
	return nil, fmt.Errorf("flux: promote: %w", drivers.ErrUnsupported)
}

var _ drivers.Driver = (*Driver)(nil)

// ErrMovedOn means the stage pins another tag than the deployment set: a
// later change, which a rollback must not undo.
var ErrMovedOn = errors.New("flux: the stage moved on since the deployment")

// Rollback implements drivers.Driver: it commits the previous tag back while
// the stage still pins the deployment's. Nothing to undo when the deployment
// changed nothing; done when the previous tag is back.
func (d *Driver) Rollback(ctx context.Context, r drivers.Request) (drivers.State, error) {
	cfg, st, err := d.load(r)
	if err != nil {
		return nil, err
	}
	if st.Commit == "" {
		return encode(st)
	}
	if st.PreviousTag == "" {
		return nil, fmt.Errorf("%w: flux: the tag before %s is unknown", drivers.ErrInvalidRequest, st.Commit)
	}
	f, current, err := d.read(ctx, cfg, cfg.Branch)
	if err != nil {
		return nil, err
	}
	switch current {
	case st.PreviousTag:
		return encode(st)
	case st.Tag:
	default:
		return nil, fmt.Errorf("%w: %s pins %s, not %s", ErrMovedOn, cfg.Path, current, st.Tag)
	}
	content, err := setImageTag(f.Content, cfg.Image, st.PreviousTag)
	if err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("Roll back %s to %s\n\nReverts %s: Frankie release %s failed on %s.\n\n%s",
		r.Stage.Name, st.PreviousTag, short(st.Commit), r.Subject.ReleaseID, r.Stage.Name, rollbackMarker(r.Deployment.ID))
	commit, err := d.repo.WriteFile(ctx, cfg.Repo, cfg.Path, cfg.Branch, content, f.BlobSHA, msg)
	if err != nil {
		return nil, fmt.Errorf("flux: roll back %s: %w", cfg.Repo, err)
	}
	st.RevertCommit = commit
	return encode(st)
}

func (d *Driver) load(r drivers.Request) (Config, state, error) {
	cfg, err := parseConfig(r.Stage.Config)
	if err != nil {
		return Config{}, state{}, fmt.Errorf("%w: %w", drivers.ErrInvalidConfig, err)
	}
	var st state
	if err := json.Unmarshal(r.Deployment.State, &st); err != nil {
		return Config{}, state{}, fmt.Errorf("%w: flux state: %w", drivers.ErrInvalidRequest, err)
	}
	return cfg, st, nil
}

// read returns the kustomization at ref and the tag it pins.
func (d *Driver) read(ctx context.Context, cfg Config, ref string) (domain.RepoFile, string, error) {
	f, found, err := d.repo.ReadFile(ctx, cfg.Repo, cfg.Path, ref)
	if err != nil {
		return domain.RepoFile{}, "", fmt.Errorf("flux: read %s: %w", cfg.Path, err)
	}
	if !found {
		return domain.RepoFile{}, "", fmt.Errorf("%w: flux: no %s in %s at %s", drivers.ErrInvalidConfig, cfg.Path, cfg.Repo, ref)
	}
	tag, err := imageTag(f.Content, cfg.Image)
	if err != nil {
		return domain.RepoFile{}, "", fmt.Errorf("%w: %w", drivers.ErrInvalidConfig, err)
	}
	return f, tag, nil
}

// findCommit finds the latest commit of the kustomization whose message
// carries marker, and the tag its parent pinned.
func (d *Driver) findCommit(ctx context.Context, cfg Config, marker string) (commit, previousTag string, err error) {
	commits, err := d.repo.FileCommits(ctx, cfg.Repo, cfg.Path, cfg.Branch, 30)
	if err != nil {
		return "", "", fmt.Errorf("flux: history of %s: %w", cfg.Path, err)
	}
	for _, c := range commits {
		if !strings.Contains(c.Message, marker) {
			continue
		}
		if c.ParentSHA == "" {
			return c.SHA, "", nil
		}
		_, prev, err := d.read(ctx, cfg, c.ParentSHA)
		if err != nil {
			return "", "", err
		}
		return c.SHA, prev, nil
	}
	return "", "", nil
}

func encode(st state) (drivers.State, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return nil, fmt.Errorf("flux: encode state: %w", err)
	}
	return b, nil
}
