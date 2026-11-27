package flux

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/drivers"
)

// PreviewName is how a preview stage names the flux-preview driver.
const PreviewName = "flux-preview"

// PreviewRepo is what the flux-preview driver needs of the git provider: the
// GitOps repository's file, and whether a change request is still open.
// *github.Client is one.
type PreviewRepo interface {
	Repo
	ChangeRequestStatus(ctx context.Context, repo string, number int) (domain.ChangeRequestStatus, error)
}

// PreviewDriver is the flux-preview driver (D124, D125): it deploys a change
// request's head as a preview by committing an entry {changeRequest, tag,
// sha} to the inputs of a Flux Operator ResourceSet in a GitOps repository,
// which renders one namespace per entry. Like the flux driver it holds no
// credential for the previews' cluster and learns that a preview runs a head
// only from the version the app reports.
type PreviewDriver struct {
	repo PreviewRepo
	http *http.Client
}

// NewPreview returns the driver; client fetches the previews' version URLs
// (default: 10-second timeout).
func NewPreview(repo PreviewRepo, client *http.Client) *PreviewDriver {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &PreviewDriver{repo: repo, http: client}
}

var _ drivers.Driver = (*PreviewDriver)(nil)

// previewState is the driver's record of a deployment.
type previewState struct {
	// Commit added the change request's entry. Empty in states written
	// before D137 when the entry already had the head.
	Commit string `json:"commit,omitempty"`
	Tag    string `json:"tag,omitempty"`
	// Removed are the change requests, no longer open, whose entries Commit
	// dropped.
	Removed []int `json:"removed,omitempty"`
	// RevertCommit removed the entry.
	RevertCommit string `json:"revertCommit,omitempty"`
}

// Name implements drivers.Driver.
func (d *PreviewDriver) Name() string { return PreviewName }

// Capabilities implements drivers.Driver: previews, rolled back by a commit,
// with health from the version the app reports.
func (d *PreviewDriver) Capabilities() drivers.Capabilities {
	return drivers.Capabilities{Preview: true, Rollback: true, Health: true}
}

// ValidateConfig implements drivers.Driver.
func (d *PreviewDriver) ValidateConfig(_ context.Context, config json.RawMessage) error {
	if _, err := parsePreviewConfig(config); err != nil {
		return fmt.Errorf("%w: %w", drivers.ErrInvalidConfig, err)
	}
	return nil
}

// Publish implements drivers.Driver: it adds the change request's entry with
// its head, in the lowest slot free (D131). An entry the deployment did not
// write is left from an earlier check and its preview holds that check's
// state, so Publish first removes it and then refuses with drivers.ErrNoRoom
// while the change request's version URL still reports a version (D137). A
// commit also drops the entries of change requests that are no longer open,
// and is refused with drivers.ErrNoRoom when MaxPreviews open ones would
// remain besides this one. A retry finds the commit an earlier attempt made
// by the deployment's marker.
func (d *PreviewDriver) Publish(ctx context.Context, r drivers.Request) (drivers.State, error) {
	cfg, st, err := d.load(r)
	if err != nil {
		return nil, err
	}
	n := r.Subject.ChangeRequest
	want := previewEntry{ChangeRequest: strconv.Itoa(n), Tag: renderTag(cfg.Tag, r.Subject.SHA), SHA: r.Subject.SHA}
	if err := want.check(); err != nil {
		return nil, fmt.Errorf("%w: flux-preview: %w", drivers.ErrInvalidRequest, err)
	}
	f, blob, err := d.read(ctx, cfg, cfg.Branch)
	if err != nil {
		return nil, err
	}
	current, had := f.entry(n)
	if had && current.Tag == want.Tag && current.SHA == want.SHA {
		if st.Commit == "" {
			if st, err = d.findCommit(ctx, cfg, n, publishMarker(r.Deployment.ID)); err != nil {
				return nil, err
			}
		}
		if st.Commit != "" {
			st.Tag = want.Tag
			return encodePreview(st)
		}
	}
	if had {
		return nil, d.removeLeftover(ctx, cfg, r, f, blob, current)
	}
	// An unreachable version URL counts as gone: a removed preview's Service
	// name stops resolving.
	switch v, _, err := reportedVersion(ctx, d.http, cfg.versionURL(n), cfg.VersionField); {
	case errors.Is(err, drivers.ErrInvalidConfig), ctx.Err() != nil:
		return nil, cmp.Or(ctx.Err(), err)
	case v != "":
		return nil, fmt.Errorf("flux-preview: %w: the preview of #%d removed before still runs %s", drivers.ErrNoRoom, n, quoted(v))
	}

	var keep []previewEntry
	var removed []int
	for _, e := range f.entries {
		if e.number() == n {
			continue
		}
		status, err := d.repo.ChangeRequestStatus(ctx, r.Subject.Repo, e.number())
		if err != nil {
			return nil, fmt.Errorf("flux-preview: status of change request %d: %w", e.number(), err)
		}
		if status != domain.ChangeRequestOpen {
			removed = append(removed, e.number())
			continue
		}
		keep = append(keep, e)
	}
	if len(keep) >= cfg.MaxPreviews {
		return nil, fmt.Errorf("flux-preview: %w: %d previews run, for change requests %s, at most %d", drivers.ErrNoRoom, len(keep), numbers(keep), cfg.MaxPreviews)
	}
	want.Slot = freeSlot(keep, cfg.MaxPreviews)
	content, err := f.render(append(keep, want))
	if err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("Preview %s#%d at %s\n\nThe %s stage runs change request #%d as %s.",
		r.Subject.Repo, n, short(r.Subject.SHA), r.Stage.Name, n, want.Tag)
	if len(removed) > 0 {
		msg += fmt.Sprintf(" Removes the previews of %s, no longer open.", joinNumbers(removed))
	}
	msg += "\n\n" + publishMarker(r.Deployment.ID)
	commit, err := d.repo.WriteFile(ctx, cfg.Repo, cfg.Path, cfg.Branch, content, blob, msg)
	if err != nil {
		return nil, fmt.Errorf("flux-preview: commit the preview of #%d to %s: %w", n, cfg.Repo, err)
	}
	return encodePreview(previewState{Commit: commit, Tag: want.Tag, Removed: removed})
}

// removeLeftover commits the removal of left, change request n's entry from
// an earlier check, and returns drivers.ErrNoRoom: the preview it rendered is
// still going away.
func (d *PreviewDriver) removeLeftover(ctx context.Context, cfg PreviewConfig, r drivers.Request, f inputsFile, blob string, left previewEntry) error {
	n := r.Subject.ChangeRequest
	content, err := f.render(slices.DeleteFunc(slices.Clone(f.entries), func(e previewEntry) bool { return e.number() == n }))
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("Remove the preview of %s#%d left from before\n\nIt runs %s from an earlier check; the %s stage checks %s in a new one.",
		r.Subject.Repo, n, left.Tag, r.Stage.Name, short(r.Subject.SHA))
	if _, err := d.repo.WriteFile(ctx, cfg.Repo, cfg.Path, cfg.Branch, content, blob, msg); err != nil {
		return fmt.Errorf("flux-preview: remove the preview of #%d left from before: %w", n, err)
	}
	return fmt.Errorf("flux-preview: %w: removed the preview of #%d left from before, running %s", drivers.ErrNoRoom, n, left.Tag)
}

// Status implements drivers.Driver: ready once the preview's version URL
// reports the change request's head.
func (d *PreviewDriver) Status(ctx context.Context, r drivers.Request) (drivers.Status, error) {
	cfg, _, err := d.load(r)
	if err != nil {
		return drivers.Status{}, err
	}
	return versionStatus(ctx, d.http, cfg.versionURL(r.Subject.ChangeRequest), cfg.VersionField, r.Subject.SHA)
}

// Promote implements drivers.Driver: the driver is not progressive.
func (d *PreviewDriver) Promote(context.Context, drivers.Request, drivers.RolloutStep) (drivers.State, error) {
	return nil, fmt.Errorf("flux-preview: promote: %w", drivers.ErrUnsupported)
}

// Rollback implements drivers.Driver: it removes the change request's entry
// while the entry still has the deployment's head; a later head is never
// undone. A preview lives only for its check, so there is no earlier entry to
// put back (D137). Entries Publish dropped stay dropped: their change requests
// are no longer open.
func (d *PreviewDriver) Rollback(ctx context.Context, r drivers.Request) (drivers.State, error) {
	cfg, st, err := d.load(r)
	if err != nil {
		return nil, err
	}
	if st.Tag == "" {
		return encodePreview(st)
	}
	n := r.Subject.ChangeRequest
	f, blob, err := d.read(ctx, cfg, cfg.Branch)
	if err != nil {
		return nil, err
	}
	current, had := f.entry(n)
	switch {
	case !had:
		return encodePreview(st)
	case current.Tag != st.Tag:
		return nil, fmt.Errorf("%w: the preview of #%d no longer runs %s", ErrMovedOn, n, st.Tag)
	}
	content, err := f.render(slices.DeleteFunc(slices.Clone(f.entries), func(e previewEntry) bool { return e.number() == n }))
	if err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("Remove the preview of %s#%d\n\n", r.Subject.Repo, n)
	if st.Commit != "" {
		msg += fmt.Sprintf("Reverts %s.\n\n", short(st.Commit))
	}
	msg += rollbackMarker(r.Deployment.ID)
	commit, err := d.repo.WriteFile(ctx, cfg.Repo, cfg.Path, cfg.Branch, content, blob, msg)
	if err != nil {
		return nil, fmt.Errorf("flux-preview: roll back the preview of #%d: %w", n, err)
	}
	st.RevertCommit = commit
	return encodePreview(st)
}

func (d *PreviewDriver) load(r drivers.Request) (PreviewConfig, previewState, error) {
	cfg, err := parsePreviewConfig(r.Stage.Config)
	if err != nil {
		return PreviewConfig{}, previewState{}, fmt.Errorf("%w: %w", drivers.ErrInvalidConfig, err)
	}
	if r.Subject.ChangeRequest < 1 {
		return PreviewConfig{}, previewState{}, fmt.Errorf("%w: flux-preview deploys change requests", drivers.ErrInvalidRequest)
	}
	var st previewState
	if err := json.Unmarshal(r.Deployment.State, &st); err != nil {
		return PreviewConfig{}, previewState{}, fmt.Errorf("%w: flux-preview state: %w", drivers.ErrInvalidRequest, err)
	}
	return cfg, st, nil
}

// read returns the inputs file at ref and its blob.
func (d *PreviewDriver) read(ctx context.Context, cfg PreviewConfig, ref string) (inputsFile, string, error) {
	rf, found, err := d.repo.ReadFile(ctx, cfg.Repo, cfg.Path, ref)
	if err != nil {
		return inputsFile{}, "", fmt.Errorf("flux-preview: read %s: %w", cfg.Path, err)
	}
	if !found {
		return inputsFile{}, "", fmt.Errorf("%w: flux-preview: no %s in %s at %s", drivers.ErrInvalidConfig, cfg.Path, cfg.Repo, ref)
	}
	f, err := parseInputs(rf.Content)
	if err != nil {
		return inputsFile{}, "", fmt.Errorf("%w: %w", drivers.ErrInvalidConfig, err)
	}
	return f, rf.BlobSHA, nil
}

// findCommit rebuilds the state of the latest commit of the inputs file whose
// message carries marker: the entries of change requests other than n it
// dropped. Empty when there is none.
func (d *PreviewDriver) findCommit(ctx context.Context, cfg PreviewConfig, n int, marker string) (previewState, error) {
	commits, err := d.repo.FileCommits(ctx, cfg.Repo, cfg.Path, cfg.Branch, 30)
	if err != nil {
		return previewState{}, fmt.Errorf("flux-preview: history of %s: %w", cfg.Path, err)
	}
	for _, c := range commits {
		if !strings.Contains(c.Message, marker) {
			continue
		}
		st := previewState{Commit: c.SHA}
		if c.ParentSHA == "" {
			return st, nil
		}
		before, _, err := d.read(ctx, cfg, c.ParentSHA)
		if err != nil {
			return previewState{}, err
		}
		after, _, err := d.read(ctx, cfg, c.SHA)
		if err != nil {
			return previewState{}, err
		}
		for _, e := range before.entries {
			if _, kept := after.entry(e.number()); !kept && e.number() != n {
				st.Removed = append(st.Removed, e.number())
			}
		}
		return st, nil
	}
	return previewState{}, nil
}

func numbers(es []previewEntry) string {
	ns := make([]int, len(es))
	for i, e := range es {
		ns[i] = e.number()
	}
	return joinNumbers(ns)
}

func joinNumbers(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = "#" + strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

func encodePreview(st previewState) (drivers.State, error) {
	b, err := json.Marshal(st)
	if err != nil {
		return nil, fmt.Errorf("flux-preview: encode state: %w", err)
	}
	return b, nil
}
