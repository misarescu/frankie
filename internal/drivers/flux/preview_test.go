package flux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/drivers"
)

const (
	previewPath = "apps/goodreads/previews/inputs.yaml"
	headSHA     = "abcdef1234567890abcdef1234567890abcdef12"
)

// previewRepo is the GitOps repository's inputs file on main, and the
// project's change requests.
type previewRepo struct {
	history []domain.Commit // oldest first
	content map[string]string
	writes  int
	status  map[int]domain.ChangeRequestStatus // default open
	failFor int                                // ChangeRequestStatus of this one fails
}

func newPreviewRepo(t *testing.T, entries ...previewEntry) *previewRepo {
	t.Helper()
	f, err := parseInputs([]byte(emptyInputs))
	if err != nil {
		t.Fatal(err)
	}
	content, err := f.render(entries)
	if err != nil {
		t.Fatal(err)
	}
	return &previewRepo{history: []domain.Commit{{SHA: "c0", Message: "initial"}}, content: map[string]string{"c0": string(content)}, status: map[int]domain.ChangeRequestStatus{}}
}

func (g *previewRepo) head() domain.Commit { return g.history[len(g.history)-1] }

func (g *previewRepo) ReadFile(_ context.Context, repo, path, ref string) (domain.RepoFile, bool, error) {
	if repo != "misarescu/Goodreads-CD" || path != previewPath {
		return domain.RepoFile{}, false, nil
	}
	sha := ref
	if ref == "main" {
		sha = g.head().SHA
	}
	c, ok := g.content[sha]
	return domain.RepoFile{Content: []byte(c), BlobSHA: "blob-" + sha}, ok, nil
}

func (g *previewRepo) WriteFile(_ context.Context, repo, path, branch string, content []byte, blobSHA, message string) (string, error) {
	if repo != "misarescu/Goodreads-CD" || path != previewPath || branch != "main" {
		return "", fmt.Errorf("write to %s %s %s", repo, path, branch)
	}
	if blobSHA != "blob-"+g.head().SHA {
		return "", github.ErrFileChanged
	}
	g.writes++
	sha := fmt.Sprintf("c%d", len(g.history))
	g.history = append(g.history, domain.Commit{SHA: sha, Message: message, ParentSHA: g.head().SHA})
	g.content[sha] = string(content)
	return sha, nil
}

func (g *previewRepo) FileCommits(_ context.Context, _, _, _ string, limit int) ([]domain.Commit, error) {
	var out []domain.Commit
	for i := len(g.history) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, g.history[i])
	}
	return out, nil
}

func (g *previewRepo) ChangeRequestStatus(_ context.Context, repo string, n int) (domain.ChangeRequestStatus, error) {
	if repo != "misarescu/Goodreads-Clone" || n == g.failFor {
		return "", fmt.Errorf("no change request %s#%d", repo, n)
	}
	if s, ok := g.status[n]; ok {
		return s, nil
	}
	return domain.ChangeRequestOpen, nil
}

// entries are the inputs file's entries on main.
func (g *previewRepo) entries(t *testing.T) []previewEntry {
	t.Helper()
	f, err := parseInputs([]byte(g.content[g.head().SHA]))
	if err != nil {
		t.Fatal(err)
	}
	return f.entries
}

func entryFor(n int, sha string) previewEntry {
	return previewEntry{ChangeRequest: fmt.Sprint(n), Tag: "sha-" + sha[:7], SHA: sha}
}

// inSlot is e in slot s (D131).
func inSlot(e previewEntry, s string) previewEntry {
	e.Slot = s
	return e
}

func previewRequest(n int, sha, config string) drivers.Request {
	return drivers.Request{
		Stage:      drivers.Stage{Name: "preview", Config: json.RawMessage(config)},
		Subject:    drivers.Subject{Repo: "misarescu/Goodreads-Clone", SHA: sha, ChangeRequest: n},
		Deployment: drivers.Deployment{ID: uuid.New(), State: drivers.EmptyState},
	}
}

// gone is the client of a driver whose previews' version URLs all answer 404.
var gone = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Request: r}, nil
})}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func decodePreview(t *testing.T, s drivers.State) previewState {
	t.Helper()
	var st previewState
	if err := json.Unmarshal(s, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// M7, D125: publishing adds the change request's entry; rolling back removes
// it again, since it had none.
func TestPreviewPublishAndRollback(t *testing.T) {
	g := newPreviewRepo(t)
	d := drivers.Checked(NewPreview(g, gone))
	r := previewRequest(152, headSHA, goodPreviewConfig)

	s, err := d.Publish(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if st := decodePreview(t, s); st.Commit != "c1" || st.Tag != "sha-abcdef1" || len(st.Removed) != 0 {
		t.Errorf("state = %+v", st)
	}
	if got := g.entries(t); !slices.Equal(got, []previewEntry{inSlot(entryFor(152, headSHA), "0")}) {
		t.Errorf("entries = %+v", got)
	}
	for _, want := range []string{"Preview misarescu/Goodreads-Clone#152 at abcdef1", "runs change request #152 as sha-abcdef1.", "Frankie-Deployment: " + r.Deployment.ID.String()} {
		if !strings.Contains(g.head().Message, want) {
			t.Errorf("commit message lacks %q:\n%s", want, g.head().Message)
		}
	}
	if !strings.HasPrefix(g.content["c1"], "# Written by Frankie's flux-preview driver") {
		t.Errorf("header lost:\n%s", g.content["c1"])
	}

	// Publishing again with the state changes nothing.
	r.Deployment.State = s
	if again, err := d.Publish(t.Context(), r); err != nil || decodePreview(t, again).Commit != "c1" || g.writes != 1 {
		t.Errorf("republish = %s, %v, %d writes", again, err, g.writes)
	}

	s, err = d.Rollback(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if st := decodePreview(t, s); st.RevertCommit != "c2" || len(g.entries(t)) != 0 ||
		!strings.Contains(g.head().Message, "Remove the preview of misarescu/Goodreads-Clone#152") ||
		!strings.Contains(g.head().Message, "Reverts c1") || !strings.Contains(g.head().Message, "Frankie-Rollback: "+r.Deployment.ID.String()) {
		t.Errorf("rollback state = %+v, entries %+v, message %q", st, g.entries(t), g.head().Message)
	}
	r.Deployment.State = s
	if _, err := d.Rollback(t.Context(), r); err != nil || g.writes != 2 {
		t.Errorf("second rollback = %v, %d writes", err, g.writes)
	}
}

// A commit drops the previews of change requests no longer open, and is
// refused when as many as allowed are open besides this one.
func TestPreviewRoom(t *testing.T) {
	merged, open, closed := entryFor(150, releaseSHA), entryFor(151, releaseSHA), entryFor(149, releaseSHA)
	g := newPreviewRepo(t, closed, merged, open)
	g.status[150], g.status[149] = domain.ChangeRequestMerged, domain.ChangeRequestClosed
	d := drivers.Checked(NewPreview(g, gone))
	s, err := d.Publish(t.Context(), previewRequest(152, headSHA, goodPreviewConfig))
	if err != nil {
		t.Fatal(err)
	}
	if st := decodePreview(t, s); !slices.Equal(st.Removed, []int{149, 150}) ||
		!slices.Equal(g.entries(t), []previewEntry{open, inSlot(entryFor(152, headSHA), "0")}) ||
		!strings.Contains(g.head().Message, "Removes the previews of #149, #150, no longer open.") {
		t.Errorf("state = %+v, entries %+v, message %q", st, g.entries(t), g.head().Message)
	}

	if _, err := d.Publish(t.Context(), previewRequest(153, headSHA, goodPreviewConfig)); !errors.Is(err, drivers.ErrNoRoom) ||
		!strings.Contains(err.Error(), "for change requests #151, #152, at most 2") || g.writes != 1 {
		t.Errorf("third preview = %v after %d writes, want ErrNoRoom", err, g.writes)
	}
	// Once one closes, there is room.
	g.status[151] = domain.ChangeRequestClosed
	if _, err := d.Publish(t.Context(), previewRequest(153, headSHA, goodPreviewConfig)); err != nil ||
		!slices.Equal(g.entries(t), []previewEntry{inSlot(entryFor(152, headSHA), "0"), inSlot(entryFor(153, headSHA), "1")}) {
		t.Errorf("after #151 closed = %v, entries %+v", err, g.entries(t))
	}
	// A change request whose status cannot be read is not dropped: nothing is written.
	g.failFor = 152
	if _, err := d.Publish(t.Context(), previewRequest(154, headSHA, strings.Replace(goodPreviewConfig, `"repo"`, `"maxPreviews":5,"repo"`, 1))); err == nil || g.writes != 2 {
		t.Errorf("unknown status = %v after %d writes", err, g.writes)
	}
}

// M7.1, D137: a state written before D137 for an entry that already had the
// head holds no commit; rolling it back removes the entry all the same.
func TestPreviewRollbackOfUnchangedEntry(t *testing.T) {
	g := newPreviewRepo(t, entryFor(150, releaseSHA), entryFor(152, headSHA))
	d := drivers.Checked(NewPreview(g, gone))
	r := previewRequest(152, headSHA, goodPreviewConfig)
	r.Deployment.State = json.RawMessage(`{"tag":"sha-abcdef1"}`)
	if _, err := d.Rollback(t.Context(), r); err != nil || g.writes != 1 || !slices.Equal(g.entries(t), []previewEntry{entryFor(150, releaseSHA)}) ||
		strings.Contains(g.head().Message, "Reverts") {
		t.Errorf("rollback = %v, %d writes, entries %+v, message %q", err, g.writes, g.entries(t), g.head().Message)
	}
}

// A retry whose earlier attempt committed but lost its state finds the commit
// and what it dropped.
func TestPreviewRetryFindsItsCommit(t *testing.T) {
	g := newPreviewRepo(t, entryFor(150, releaseSHA))
	g.status[150] = domain.ChangeRequestClosed
	d := drivers.Checked(NewPreview(g, gone))
	r := previewRequest(152, headSHA, goodPreviewConfig)
	if _, err := d.Publish(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	s, err := d.Publish(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if st := decodePreview(t, s); st.Commit != "c1" || st.Tag != "sha-abcdef1" || !slices.Equal(st.Removed, []int{150}) || g.writes != 1 {
		t.Errorf("state = %+v after %d writes", st, g.writes)
	}
}

// A rollback never undoes a later head.
func TestPreviewRollbackWhenMovedOn(t *testing.T) {
	g := newPreviewRepo(t)
	d := drivers.Checked(NewPreview(g, gone))
	first := previewRequest(152, releaseSHA, goodPreviewConfig)
	s, err := d.Publish(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	later := previewRequest(152, headSHA, goodPreviewConfig)
	if _, err := d.Publish(t.Context(), later); !errors.Is(err, drivers.ErrNoRoom) {
		t.Fatalf("publish over the first = %v, want it removed and ErrNoRoom", err)
	}
	if _, err := d.Publish(t.Context(), later); err != nil {
		t.Fatal(err)
	}
	first.Deployment.State = s
	if _, err := d.Rollback(t.Context(), first); !errors.Is(err, ErrMovedOn) || !slices.Equal(g.entries(t), []previewEntry{inSlot(entryFor(152, headSHA), "0")}) {
		t.Errorf("rollback = %v, entries %+v; want ErrMovedOn and the later head kept", err, g.entries(t))
	}
}

func TestPreviewErrors(t *testing.T) {
	g := newPreviewRepo(t)
	d := drivers.Checked(NewPreview(g, gone))
	if c := d.Capabilities(); !c.Preview || !c.Rollback || !c.Health || c.Progressive || d.Name() != "flux-preview" {
		t.Errorf("capabilities = %+v, name %s", c, d.Name())
	}
	r := previewRequest(152, headSHA, strings.Replace(goodPreviewConfig, previewPath, "apps/other/inputs.yaml", 1))
	if _, err := d.Publish(t.Context(), r); !errors.Is(err, drivers.ErrInvalidConfig) {
		t.Errorf("missing file = %v, want ErrInvalidConfig", err)
	}
	g.content["c0"] = strings.Replace(emptyInputs, "kind: ResourceSet", "kind: ConfigMap", 1)
	if _, err := d.Publish(t.Context(), previewRequest(152, headSHA, goodPreviewConfig)); !errors.Is(err, drivers.ErrInvalidConfig) {
		t.Errorf("not an inputs file = %v, want ErrInvalidConfig", err)
	}
	release := previewRequest(152, headSHA, goodPreviewConfig)
	release.Subject.ChangeRequest, release.Subject.ReleaseID = 0, uuid.New()
	if _, err := d.Publish(t.Context(), release); !errors.Is(err, drivers.ErrUnsupported) {
		t.Errorf("a release = %v, want ErrUnsupported", err)
	}
	if err := d.ValidateConfig(t.Context(), json.RawMessage(`{"repo":"x"}`)); !errors.Is(err, drivers.ErrInvalidConfig) {
		t.Errorf("ValidateConfig = %v", err)
	}
	if err := d.ValidateConfig(t.Context(), json.RawMessage(goodPreviewConfig)); err != nil {
		t.Errorf("ValidateConfig(good) = %v", err)
	}
	if _, err := NewPreview(g, gone).Promote(t.Context(), r, drivers.RolloutStep{Name: "x"}); !errors.Is(err, drivers.ErrUnsupported) {
		t.Errorf("Promote = %v, want ErrUnsupported", err)
	}
}

// M7.1, D131, D137: each preview holds a slot below maxPreviews, the lowest
// free when it starts, given back when it goes; entries from before slots
// hold none.
func TestPreviewSlots(t *testing.T) {
	legacy := entryFor(150, releaseSHA)
	g := newPreviewRepo(t, legacy)
	d := drivers.Checked(NewPreview(g, gone))
	three := strings.Replace(goodPreviewConfig, `"repo"`, `"maxPreviews":3,"repo"`, 1)
	publish := func(n int, sha string) drivers.State {
		t.Helper()
		s, err := d.Publish(t.Context(), previewRequest(n, sha, three))
		if err != nil {
			t.Fatalf("publish #%d: %v", n, err)
		}
		return s
	}
	publish(152, headSHA)
	publish(153, headSHA)
	if got := g.entries(t); !slices.Equal(got, []previewEntry{legacy, inSlot(entryFor(152, headSHA), "0"), inSlot(entryFor(153, headSHA), "1")}) {
		t.Fatalf("entries = %+v", got)
	}

	// #152 closes: its slot goes to the next preview, once #152's entry is dropped.
	g.status[152], g.status[150] = domain.ChangeRequestClosed, domain.ChangeRequestMerged
	s154 := publish(154, headSHA)
	if got := g.entries(t); !slices.Equal(got, []previewEntry{inSlot(entryFor(153, headSHA), "1"), inSlot(entryFor(154, headSHA), "0")}) {
		t.Fatalf("after #152 closed, entries = %+v", got)
	}

	// Rolling a preview back gives its slot to the next one.
	r := previewRequest(154, headSHA, three)
	r.Deployment.State = s154
	if _, err := d.Rollback(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	publish(155, headSHA)
	if got := g.entries(t); !slices.Equal(got, []previewEntry{inSlot(entryFor(153, headSHA), "1"), inSlot(entryFor(155, headSHA), "0")}) {
		t.Errorf("after #154 rolled back, entries = %+v", got)
	}
}

// versionServer answers the version URL of every preview with the version
// running holds, or 404 while it is empty, and returns the config pointing
// at it.
func versionServer(t *testing.T, running *atomic.Value) (string, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, _ := running.Load().(string)
		if v == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"version":%q}`, v)
	}))
	t.Cleanup(srv.Close)
	return strings.Replace(goodPreviewConfig, "http://goodreads-previews.stages.svc.cluster.local", srv.URL, 1), srv.Client()
}

// M7.1, D137: an entry the deployment did not write was left
// by an earlier check whose removal failed. Its preview holds the earlier
// check's state, so Publish removes it and waits until that preview is gone
// before adding the head, and Rollback removes the entry rather than putting
// the leftover back.
func TestPreviewLeftoverEntry(t *testing.T) {
	for name, leftover := range map[string]string{"earlier head": releaseSHA, "same head": headSHA} {
		t.Run(name, func(t *testing.T) {
			var running atomic.Value
			running.Store(short(leftover))
			cfg, client := versionServer(t, &running)
			g := newPreviewRepo(t, inSlot(entryFor(168, leftover), "1"))
			d := drivers.Checked(NewPreview(g, client))
			r := previewRequest(168, headSHA, cfg)

			if _, err := d.Publish(t.Context(), r); !errors.Is(err, drivers.ErrNoRoom) || g.writes != 1 || len(g.entries(t)) != 0 ||
				!strings.Contains(g.head().Message, "Remove the preview of misarescu/Goodreads-Clone#168 left from before") {
				t.Fatalf("publish over the leftover = %v, %d writes, entries %+v, message %q; want it removed and ErrNoRoom", err, g.writes, g.entries(t), g.head().Message)
			}
			if _, err := d.Publish(t.Context(), r); !errors.Is(err, drivers.ErrNoRoom) || !strings.Contains(err.Error(), "still runs "+short(leftover)) || g.writes != 1 {
				t.Fatalf("publish while the leftover still runs = %v, %d writes; want ErrNoRoom and nothing written", err, g.writes)
			}

			running.Store("")
			s, err := d.Publish(t.Context(), r)
			if err != nil || g.writes != 2 || !slices.Equal(g.entries(t), []previewEntry{inSlot(entryFor(168, headSHA), "0")}) {
				t.Fatalf("publish once the leftover is gone = %v, %d writes, entries %+v", err, g.writes, g.entries(t))
			}
			r.Deployment.State = s
			if _, err := d.Rollback(t.Context(), r); err != nil || g.writes != 3 || len(g.entries(t)) != 0 {
				t.Errorf("rollback = %v, %d writes, entries %+v; want the entry removed", err, g.writes, g.entries(t))
			}
		})
	}
}

// A preview runs the head once its own version URL reports the commit.
func TestPreviewStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pr-152/api/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"version":"abcdef1"}`))
	}))
	defer srv.Close()
	d := drivers.Checked(NewPreview(newPreviewRepo(t), srv.Client()))
	cfg := strings.Replace(goodPreviewConfig, "http://goodreads-previews.stages.svc.cluster.local", srv.URL, 1)
	if st, err := d.Status(t.Context(), previewRequest(152, headSHA, cfg)); err != nil || st.Phase != drivers.PhaseReady {
		t.Errorf("status = %+v, %v", st, err)
	}
	if st, err := d.Status(t.Context(), previewRequest(153, headSHA, cfg)); err != nil || st.Phase != drivers.PhaseProgressing || !strings.Contains(st.Message, "answered 404") {
		t.Errorf("another preview's status = %+v, %v", st, err)
	}
}
