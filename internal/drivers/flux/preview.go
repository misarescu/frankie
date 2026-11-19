package flux

import (
	"context"
	"encoding/json"
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
	// Commit set the change request's entry; empty when it already had it.
	Commit string `json:"commit,omitempty"`
	Tag    string `json:"tag,omitempty"`
	// Previous is the change request's entry before Commit; nil when it had
	// none.
	Previous *previewEntry `json:"previous,omitempty"`
	// Removed are the change requests, no longer open, whose entries Commit
	// dropped.
	Removed []int `json:"removed,omitempty"`
	// RevertCommit put Previous back.
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

// Publish implements drivers.Driver: it sets the change request's entry to
// its head unless the entry already has it. The entry keeps its slot, or gets
// the lowest one free (D131). A commit also drops the entries of change
// requests that are no longer open, and is refused with drivers.ErrNoRoom when
// MaxPreviews open ones would remain besides this one. A retry finds the
// commit an earlier attempt made by the deployment's marker.
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
	want.Slot = current.Slot
	if had && current == want {
		if st.Commit == "" {
			// Ours from an earlier attempt, or the preview already ran it.
			if st, err = d.findCommit(ctx, cfg, n, publishMarker(r.Deployment.ID)); err != nil {
				return nil, err
			}
		}
		st.Tag = want.Tag
		return encodePreview(st)
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
	if want.Slot == "" || slices.ContainsFunc(keep, func(e previewEntry) bool { return e.Slot == want.Slot }) {
		want.Slot = freeSlot(keep, cfg.MaxPreviews)
	}
	content, err := f.render(append(keep, want))
	if err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("Preview %s#%d at %s\n\nThe %s stage runs change request #%d as %s",
		r.Subject.Repo, n, short(r.Subject.SHA), r.Stage.Name, n, want.Tag)
	if had {
		msg += fmt.Sprintf(" (was %s)", current.Tag)
	}
	msg += "."
	if len(removed) > 0 {
		msg += fmt.Sprintf(" Removes the previews of %s, no longer open.", joinNumbers(removed))
	}
	msg += "\n\n" + publishMarker(r.Deployment.ID)
	commit, err := d.repo.WriteFile(ctx, cfg.Repo, cfg.Path, cfg.Branch, content, blob, msg)
	if err != nil {
		return nil, fmt.Errorf("flux-preview: commit the preview of #%d to %s: %w", n, cfg.Repo, err)
	}
	st = previewState{Commit: commit, Tag: want.Tag, Removed: removed}
	if had {
		st.Previous = &current
	}
	return encodePreview(st)
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

// Rollback implements drivers.Driver: while the change request's entry still
// has the deployment's head, it puts back the entry it had before, or removes
// it when it had none. Nothing to undo when the deployment changed nothing;
// done when the previous entry is back. Entries Publish dropped stay dropped:
// their change requests are no longer open.
func (d *PreviewDriver) Rollback(ctx context.Context, r drivers.Request) (drivers.State, error) {
	cfg, st, err := d.load(r)
	if err != nil {
		return nil, err
	}
	if st.Commit == "" {
		return encodePreview(st)
	}
	n := r.Subject.ChangeRequest
	f, blob, err := d.read(ctx, cfg, cfg.Branch)
	if err != nil {
		return nil, err
	}
	current, had := f.entry(n)
	switch {
	case !had && st.Previous == nil, had && st.Previous != nil && current == *st.Previous:
		return encodePreview(st)
	case !had || current.Tag != st.Tag:
		return nil, fmt.Errorf("%w: the preview of #%d no longer runs %s", ErrMovedOn, n, st.Tag)
	}
	entries := slices.DeleteFunc(slices.Clone(f.entries), func(e previewEntry) bool { return e.number() == n })
	what := "Remove"
	if st.Previous != nil {
		entries = append(entries, *st.Previous)
		what = "Put back " + st.Previous.Tag + " as"
	}
	content, err := f.render(entries)
	if err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("%s the preview of %s#%d\n\nReverts %s.\n\n%s", what, r.Subject.Repo, n, short(st.Commit), rollbackMarker(r.Deployment.ID))
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
// message carries marker: change request n's entry before it, and the entries
// it dropped. Empty when there is none.
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
			if e.number() == n {
				st.Previous = &e
			} else if _, kept := after.entry(e.number()); !kept {
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
