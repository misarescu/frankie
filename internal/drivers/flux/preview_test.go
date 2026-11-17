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

func previewRequest(n int, sha, config string) drivers.Request {
	return drivers.Request{
		Stage:      drivers.Stage{Name: "preview", Config: json.RawMessage(config)},
		Subject:    drivers.Subject{Repo: "misarescu/Goodreads-Clone", SHA: sha, ChangeRequest: n},
		Deployment: drivers.Deployment{ID: uuid.New(), State: drivers.EmptyState},
	}
}

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
	d := drivers.Checked(NewPreview(g, nil))
	r := previewRequest(152, headSHA, goodPreviewConfig)

	s, err := d.Publish(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if st := decodePreview(t, s); st.Commit != "c1" || st.Tag != "sha-abcdef1" || st.Previous != nil || len(st.Removed) != 0 {
		t.Errorf("state = %+v", st)
	}
	if got := g.entries(t); !slices.Equal(got, []previewEntry{entryFor(152, headSHA)}) {
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

// A new head replaces the change request's entry; rolling back puts the
// earlier head back.
func TestPreviewNewHead(t *testing.T) {
	earlier := entryFor(152, releaseSHA)
	g := newPreviewRepo(t, earlier)
	d := drivers.Checked(NewPreview(g, nil))
	r := previewRequest(152, headSHA, goodPreviewConfig)
	s, err := d.Publish(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if st := decodePreview(t, s); st.Previous == nil || *st.Previous != earlier || !strings.Contains(g.head().Message, "(was sha-1234567)") {
		t.Errorf("state = %+v, message %q", st, g.head().Message)
	}
	r.Deployment.State = s
	if _, err := d.Rollback(t.Context(), r); err != nil || !slices.Equal(g.entries(t), []previewEntry{earlier}) ||
		!strings.Contains(g.head().Message, "Put back sha-1234567 as the preview of misarescu/Goodreads-Clone#152") {
		t.Errorf("rollback = %v, entries %+v, message %q", err, g.entries(t), g.head().Message)
	}
}

// A commit drops the previews of change requests no longer open, and is
// refused when as many as allowed are open besides this one.
func TestPreviewRoom(t *testing.T) {
	merged, open, closed := entryFor(150, releaseSHA), entryFor(151, releaseSHA), entryFor(149, releaseSHA)
	g := newPreviewRepo(t, closed, merged, open)
	g.status[150], g.status[149] = domain.ChangeRequestMerged, domain.ChangeRequestClosed
	d := drivers.Checked(NewPreview(g, nil))
	s, err := d.Publish(t.Context(), previewRequest(152, headSHA, goodPreviewConfig))
	if err != nil {
		t.Fatal(err)
	}
	if st := decodePreview(t, s); !slices.Equal(st.Removed, []int{149, 150}) ||
		!slices.Equal(g.entries(t), []previewEntry{open, entryFor(152, headSHA)}) ||
		!strings.Contains(g.head().Message, "Removes the previews of #149, #150, no longer open.") {
		t.Errorf("state = %+v, entries %+v, message %q", st, g.entries(t), g.head().Message)
	}

	if _, err := d.Publish(t.Context(), previewRequest(153, headSHA, goodPreviewConfig)); !errors.Is(err, ErrNoRoom) ||
		!strings.Contains(err.Error(), "for change requests #151, #152, at most 2") || g.writes != 1 {
		t.Errorf("third preview = %v after %d writes, want ErrNoRoom", err, g.writes)
	}
	// Once one closes, there is room.
	g.status[151] = domain.ChangeRequestClosed
	if _, err := d.Publish(t.Context(), previewRequest(153, headSHA, goodPreviewConfig)); err != nil ||
		!slices.Equal(g.entries(t), []previewEntry{entryFor(152, headSHA), entryFor(153, headSHA)}) {
		t.Errorf("after #151 closed = %v, entries %+v", err, g.entries(t))
	}
	// A change request whose status cannot be read is not dropped: nothing is written.
	g.failFor = 152
	if _, err := d.Publish(t.Context(), previewRequest(154, headSHA, strings.Replace(goodPreviewConfig, `"repo"`, `"maxPreviews":5,"repo"`, 1))); err == nil || g.writes != 2 {
		t.Errorf("unknown status = %v after %d writes", err, g.writes)
	}
}

// An entry that already has the head writes nothing, not even to drop others.
func TestPreviewUnchanged(t *testing.T) {
	g := newPreviewRepo(t, entryFor(150, releaseSHA), entryFor(152, headSHA))
	g.status[150] = domain.ChangeRequestMerged
	d := drivers.Checked(NewPreview(g, nil))
	r := previewRequest(152, headSHA, goodPreviewConfig)
	s, err := d.Publish(t.Context(), r)
	if err != nil || g.writes != 0 || len(g.entries(t)) != 2 {
		t.Fatalf("publish = %v, %d writes, entries %+v", err, g.writes, g.entries(t))
	}
	if st := decodePreview(t, s); st.Commit != "" || st.Tag != "sha-abcdef1" {
		t.Errorf("state = %+v", st)
	}
	r.Deployment.State = s
	if _, err := d.Rollback(t.Context(), r); err != nil || g.writes != 0 {
		t.Errorf("rollback of nothing = %v, %d writes", err, g.writes)
	}
}

// A retry whose earlier attempt committed but lost its state finds the commit
// and what it replaced.
func TestPreviewRetryFindsItsCommit(t *testing.T) {
	earlier := entryFor(152, releaseSHA)
	g := newPreviewRepo(t, entryFor(150, releaseSHA), earlier)
	g.status[150] = domain.ChangeRequestClosed
	d := drivers.Checked(NewPreview(g, nil))
	r := previewRequest(152, headSHA, goodPreviewConfig)
	if _, err := d.Publish(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	s, err := d.Publish(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if st := decodePreview(t, s); st.Commit != "c1" || st.Previous == nil || *st.Previous != earlier || !slices.Equal(st.Removed, []int{150}) || g.writes != 1 {
		t.Errorf("state = %+v after %d writes", st, g.writes)
	}
}

// A rollback never undoes a later head.
func TestPreviewRollbackWhenMovedOn(t *testing.T) {
	g := newPreviewRepo(t)
	d := drivers.Checked(NewPreview(g, nil))
	first := previewRequest(152, releaseSHA, goodPreviewConfig)
	s, err := d.Publish(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Publish(t.Context(), previewRequest(152, headSHA, goodPreviewConfig)); err != nil {
		t.Fatal(err)
	}
	first.Deployment.State = s
	if _, err := d.Rollback(t.Context(), first); !errors.Is(err, ErrMovedOn) || !slices.Equal(g.entries(t), []previewEntry{entryFor(152, headSHA)}) {
		t.Errorf("rollback = %v, entries %+v; want ErrMovedOn and the later head kept", err, g.entries(t))
	}
}

func TestPreviewErrors(t *testing.T) {
	g := newPreviewRepo(t)
	d := drivers.Checked(NewPreview(g, nil))
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
	if _, err := NewPreview(g, nil).Promote(t.Context(), r, drivers.RolloutStep{Name: "x"}); !errors.Is(err, drivers.ErrUnsupported) {
		t.Errorf("Promote = %v, want ErrUnsupported", err)
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
