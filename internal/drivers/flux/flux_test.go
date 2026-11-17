package flux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/drivers"
)

const (
	releaseSHA = "1234567890abcdef1234567890abcdef12345678"
	stagePath  = "apps/goodreads/staging/kustomization.yaml"
)

// gitopsRepo is a GitOps repository with one file's history on main.
type gitopsRepo struct {
	history []domain.Commit // oldest first; ParentSHA set
	content map[string]string
	writes  int
	reads   []string
}

func newGitopsRepo() *gitopsRepo {
	return &gitopsRepo{history: []domain.Commit{{SHA: "c0", Message: "initial"}}, content: map[string]string{"c0": stagingKustomization}}
}

func (g *gitopsRepo) head() domain.Commit { return g.history[len(g.history)-1] }

func (g *gitopsRepo) ReadFile(_ context.Context, repo, path, ref string) (domain.RepoFile, bool, error) {
	g.reads = append(g.reads, ref)
	if repo != "misarescu/Goodreads-CD" || path != stagePath {
		return domain.RepoFile{}, false, nil
	}
	sha := ref
	if ref == "main" {
		sha = g.head().SHA
	}
	c, ok := g.content[sha]
	if !ok {
		return domain.RepoFile{}, false, nil
	}
	return domain.RepoFile{Content: []byte(c), BlobSHA: "blob-" + sha}, true, nil
}

func (g *gitopsRepo) WriteFile(_ context.Context, repo, path, branch string, content []byte, blobSHA, message string) (string, error) {
	if repo != "misarescu/Goodreads-CD" || path != stagePath || branch != "main" {
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

func (g *gitopsRepo) FileCommits(_ context.Context, _, _, _ string, limit int) ([]domain.Commit, error) {
	var out []domain.Commit
	for i := len(g.history) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, g.history[i])
	}
	return out, nil
}

// pin makes someone else's commit pinning tag.
func (g *gitopsRepo) pin(t *testing.T, tag string) {
	t.Helper()
	f, _, _ := g.ReadFile(t.Context(), "misarescu/Goodreads-CD", stagePath, "main")
	content, err := setImageTag(f.Content, "ghcr.io/misarescu/goodreads-clone", tag)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.WriteFile(t.Context(), "misarescu/Goodreads-CD", stagePath, "main", content, f.BlobSHA, "by hand"); err != nil {
		t.Fatal(err)
	}
}

func (g *gitopsRepo) tag(t *testing.T) string {
	t.Helper()
	tag, err := imageTag([]byte(g.content[g.head().SHA]), "ghcr.io/misarescu/goodreads-clone")
	if err != nil {
		t.Fatal(err)
	}
	return tag
}

func request(versionURL string) drivers.Request {
	cfg := strings.Replace(goodConfig, "http://goodreads-stg.192.168.3.7.nip.io/api/version", versionURL, 1)
	return drivers.Request{
		Stage:      drivers.Stage{Name: "staging", Config: json.RawMessage(cfg)},
		Subject:    drivers.Subject{Repo: "misarescu/Goodreads-Clone", SHA: releaseSHA, ReleaseID: uuid.New()},
		Deployment: drivers.Deployment{ID: uuid.New(), State: drivers.EmptyState},
	}
}

func decode(t *testing.T, s drivers.State) state {
	t.Helper()
	var st state
	if err := json.Unmarshal(s, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// M7, D117: publishing commits the release's tag; rolling back commits the
// previous one back.
func TestPublishAndRollback(t *testing.T) {
	g := newGitopsRepo()
	d := drivers.Checked(New(g, nil))
	r := request("http://x/api/version")

	s, err := d.Publish(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	st := decode(t, s)
	if st.Commit != "c1" || st.Tag != "sha-1234567" || st.PreviousTag != "sha-a44235f" || g.tag(t) != "sha-1234567" {
		t.Errorf("state = %+v, pinned %s", st, g.tag(t))
	}
	msg := g.head().Message
	for _, want := range []string{"Release 1234567 to staging", "was sha-a44235f", r.Subject.ReleaseID.String(), "Frankie-Deployment: " + r.Deployment.ID.String()} {
		if !strings.Contains(msg, want) {
			t.Errorf("commit message lacks %q:\n%s", want, msg)
		}
	}
	if !strings.Contains(g.content["c1"], "newTag: sha-1234567 # set by Frankie") {
		t.Errorf("comments lost:\n%s", g.content["c1"])
	}

	// Publishing again with the state changes nothing.
	r.Deployment.State = s
	if again, err := d.Publish(t.Context(), r); err != nil || decode(t, again) != st || g.writes != 1 {
		t.Errorf("republish = %s, %v, %d writes", again, err, g.writes)
	}

	s, err = d.Rollback(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if rb := decode(t, s); rb.RevertCommit != "c2" || g.tag(t) != "sha-a44235f" || !strings.Contains(g.head().Message, "Reverts c1") ||
		!strings.Contains(g.head().Message, "Frankie-Rollback: "+r.Deployment.ID.String()) {
		t.Errorf("rollback state = %+v, pinned %s, message %q", rb, g.tag(t), g.head().Message)
	}
	// Rolling back again changes nothing.
	r.Deployment.State = s
	if _, err := d.Rollback(t.Context(), r); err != nil || g.writes != 2 {
		t.Errorf("second rollback = %v, %d writes", err, g.writes)
	}
}

// A retry whose earlier attempt committed but lost its state finds the commit.
func TestPublishRetryFindsItsCommit(t *testing.T) {
	g := newGitopsRepo()
	d := drivers.Checked(New(g, nil))
	r := request("http://x/api/version")
	if _, err := d.Publish(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	g.pin(t, "sha-1234567") // an unrelated commit on the file in between keeps the tag
	s, err := d.Publish(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	// Two writes: the driver's, and the unrelated one.
	if st := decode(t, s); st.Commit != "c1" || st.PreviousTag != "sha-a44235f" || g.writes != 2 {
		t.Errorf("state = %+v after %d writes, want the first commit found", st, g.writes)
	}
}

// A stage that already pins the release's tag is not changed, and not rolled back.
func TestPublishWhenAlreadyPinned(t *testing.T) {
	g := newGitopsRepo()
	g.pin(t, "sha-1234567")
	d := drivers.Checked(New(g, nil))
	r := request("http://x/api/version")
	s, err := d.Publish(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if st := decode(t, s); st.Commit != "" || st.Tag != "sha-1234567" || g.writes != 1 {
		t.Errorf("state = %+v, %d writes", st, g.writes)
	}
	r.Deployment.State = s
	if _, err := d.Rollback(t.Context(), r); err != nil || g.writes != 1 || g.tag(t) != "sha-1234567" {
		t.Errorf("rollback of nothing = %v, %d writes, pinned %s", err, g.writes, g.tag(t))
	}
}

// A rollback never undoes a later change.
func TestRollbackWhenMovedOn(t *testing.T) {
	g := newGitopsRepo()
	d := drivers.Checked(New(g, nil))
	r := request("http://x/api/version")
	s, err := d.Publish(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	g.pin(t, "sha-7777777")
	r.Deployment.State = s
	if _, err := d.Rollback(t.Context(), r); !errors.Is(err, ErrMovedOn) || g.tag(t) != "sha-7777777" {
		t.Errorf("rollback = %v, pinned %s; want ErrMovedOn and the later tag kept", err, g.tag(t))
	}
}

func TestPublishErrors(t *testing.T) {
	g := newGitopsRepo()
	d := drivers.Checked(New(g, nil))
	r := request("http://x/api/version")
	r.Stage.Config = json.RawMessage(strings.Replace(string(r.Stage.Config), stagePath, "apps/other/kustomization.yaml", 1))
	if _, err := d.Publish(t.Context(), r); !errors.Is(err, drivers.ErrInvalidConfig) {
		t.Errorf("missing file = %v, want ErrInvalidConfig", err)
	}
	r = request("http://x/api/version")
	r.Stage.Config = json.RawMessage(strings.Replace(string(r.Stage.Config), "ghcr.io/misarescu/goodreads-clone", "ghcr.io/x/y", 1))
	if _, err := d.Publish(t.Context(), r); !errors.Is(err, drivers.ErrInvalidConfig) {
		t.Errorf("unknown image = %v, want ErrInvalidConfig", err)
	}
	if err := d.ValidateConfig(t.Context(), json.RawMessage(`{"repo":"x"}`)); !errors.Is(err, drivers.ErrInvalidConfig) {
		t.Errorf("ValidateConfig = %v", err)
	}
	if err := d.ValidateConfig(t.Context(), json.RawMessage(goodConfig)); err != nil {
		t.Errorf("ValidateConfig(good) = %v", err)
	}
	if c := d.Capabilities(); !c.Rollback || !c.Health || c.Preview || c.Progressive || d.Name() != "flux" {
		t.Errorf("capabilities = %+v", c)
	}
	if _, err := New(g, nil).Promote(t.Context(), r, drivers.RolloutStep{Name: "x"}); !errors.Is(err, drivers.ErrUnsupported) {
		t.Errorf("Promote = %v, want ErrUnsupported", err)
	}
}

// The stage runs a release once its version URL reports the commit.
func TestStatus(t *testing.T) {
	answer := `{"version":"1234567"}`
	code := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	d := drivers.Checked(New(newGitopsRepo(), srv.Client()))
	r := request(srv.URL + "/api/version")

	tests := []struct {
		answer string
		code   int
		phase  drivers.Phase
		msg    string
	}{
		{`{"version":"1234567"}`, 200, drivers.PhaseReady, "version is 1234567"},
		{`{"version":"` + releaseSHA + `"}`, 200, drivers.PhaseReady, "version is " + releaseSHA},
		{`{"version":"a44235f"}`, 200, drivers.PhaseProgressing, "version is a44235f"},
		{`{"version":"123"}`, 200, drivers.PhaseProgressing, "version is 123"},
		{`{"version":"` + strings.Repeat("x", 100) + `"}`, 200, drivers.PhaseProgressing, "version is " + strings.Repeat("x", 64) + "..."},
		{`{"other":"1234567"}`, 200, drivers.PhaseProgressing, "reports no version"},
		{`<html>secret</html>`, 200, drivers.PhaseProgressing, "did not answer JSON"},
		{`{"version":"1234567"}`, 503, drivers.PhaseProgressing, "answered 503"},
	}
	for _, tt := range tests {
		answer, code = tt.answer, tt.code
		st, err := d.Status(t.Context(), r)
		if err != nil || st.Phase != tt.phase || !strings.HasSuffix(st.Message, tt.msg) {
			t.Errorf("%s (%d): status = %+v, %v; want %s %q", tt.answer, tt.code, st, err, tt.phase, tt.msg)
		}
	}
	srv.Close()
	if _, err := d.Status(t.Context(), r); err == nil {
		t.Error("an unreachable version URL is no error")
	}
}
