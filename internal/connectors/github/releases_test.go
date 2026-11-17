package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
)

const relSHA = "0123456789abcdef0123456789abcdef01234567"

// fakeReleases serves a repo's tags and releases: tags maps a tag to the
// object it points at ("tag:<sha>" for an annotated tag); releases maps a
// tag to its release page.
type fakeReleases struct {
	tags, releases map[string]string
	created        []map[string]any
	raceOnCreate   bool // the release appears while POST answers 422
}

func (f *fakeReleases) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/repos/o/r")
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/git/ref/tags/"):
		obj, ok := f.tags[strings.TrimPrefix(p, "/git/ref/tags/")]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		typ, sha := "commit", obj
		if after, ok := strings.CutPrefix(obj, "tag:"); ok {
			typ, sha = "tag", "annotated-"+after
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": sha, "type": typ}})
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/git/tags/annotated-"):
		_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": strings.TrimPrefix(p, "/git/tags/annotated-"), "type": "commit"}})
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/releases/tags/"):
		u, ok := f.releases[strings.TrimPrefix(p, "/releases/tags/")]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"html_url": u})
	case r.Method == http.MethodPost && p == "/releases":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created = append(f.created, body)
		tag := body["tag_name"].(string)
		if f.raceOnCreate {
			f.releases[tag] = "https://github.com/o/r/releases/tag/" + tag
			http.Error(w, `{"message":"Validation Failed","errors":[{"resource":"Release","code":"already_exists","field":"tag_name"}]}`, http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"html_url": "https://github.com/o/r/releases/tag/" + tag})
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
	}
}

// M7 (D129): a succeeded release's version is published as a GitHub Release
// at its commit, once, and never on a tag that names another commit.
func TestCreateRelease(t *testing.T) {
	rel := domain.NewProviderRelease{Tag: "v0.2.0", SHA: relSHA, Name: "v0.2.0", Body: "- gc-77: similar books"}
	run := func(t *testing.T, f *fakeReleases) (string, error) {
		t.Helper()
		srv := httptest.NewServer(f)
		t.Cleanup(srv.Close)
		return NewClient("pat", srv.URL).CreateRelease(t.Context(), "o/r", rel)
	}
	t.Run("a new tag", func(t *testing.T) {
		f := &fakeReleases{tags: map[string]string{}, releases: map[string]string{}}
		u, err := run(t, f)
		if err != nil || u != "https://github.com/o/r/releases/tag/v0.2.0" {
			t.Fatalf("CreateRelease = %q, %v", u, err)
		}
		if len(f.created) != 1 || f.created[0]["target_commitish"] != relSHA || f.created[0]["body"] != rel.Body || f.created[0]["name"] != "v0.2.0" {
			t.Errorf("created = %+v", f.created)
		}
	})
	t.Run("already published at the commit", func(t *testing.T) {
		f := &fakeReleases{tags: map[string]string{"v0.2.0": "tag:" + relSHA}, releases: map[string]string{"v0.2.0": "https://github.com/o/r/releases/tag/v0.2.0"}}
		if u, err := run(t, f); err != nil || u == "" || len(f.created) != 0 {
			t.Errorf("CreateRelease = %q, %v, created %v", u, err, f.created)
		}
	})
	t.Run("a tag at the commit without a release", func(t *testing.T) {
		f := &fakeReleases{tags: map[string]string{"v0.2.0": relSHA}, releases: map[string]string{}}
		if u, err := run(t, f); err != nil || u == "" || len(f.created) != 1 {
			t.Errorf("CreateRelease = %q, %v, created %v", u, err, f.created)
		}
	})
	t.Run("the tag names another commit", func(t *testing.T) {
		f := &fakeReleases{tags: map[string]string{"v0.2.0": "tag:ffffffffffffffffffffffffffffffffffffffff"}, releases: map[string]string{}}
		if _, err := run(t, f); !errors.Is(err, domain.ErrTagTaken) || len(f.created) != 0 {
			t.Errorf("err = %v, created %v; want ErrTagTaken and nothing created", err, f.created)
		}
	})
	t.Run("created meanwhile", func(t *testing.T) {
		f := &fakeReleases{tags: map[string]string{}, releases: map[string]string{}, raceOnCreate: true}
		if u, err := run(t, f); err != nil || u != "https://github.com/o/r/releases/tag/v0.2.0" {
			t.Errorf("CreateRelease = %q, %v", u, err)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		f := &fakeReleases{tags: map[string]string{}, releases: map[string]string{}}
		srv := httptest.NewServer(f)
		defer srv.Close()
		for _, bad := range []domain.NewProviderRelease{{Tag: "0.2.0", SHA: relSHA}, {Tag: "v0.2.0", SHA: "abc123"}} {
			if _, err := NewClient("pat", srv.URL).CreateRelease(t.Context(), "o/r", bad); err == nil {
				t.Errorf("%+v was published", bad)
			}
		}
	})
}
