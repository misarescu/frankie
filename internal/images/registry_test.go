package images

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testDigest = "sha256:" + strings.Repeat("a", 64)

func TestHTTPRegistryHasManifest(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		header  string
		want    bool
		wantErr bool
	}{
		{"present", http.StatusOK, testDigest, true, false},
		{"present without a digest header", http.StatusOK, "", true, false},
		{"another manifest answered", http.StatusOK, "sha256:" + strings.Repeat("b", 64), false, true},
		{"absent", http.StatusNotFound, "", false, false},
		{"server error is not absence", http.StatusInternalServerError, "", false, true},
		{"auth failure is not absence", http.StatusUnauthorized, "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead || r.URL.Path != "/v2/toolchains/misarescu/repo/manifests/"+testDigest {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if !strings.Contains(r.Header.Get("Accept"), "application/vnd.oci.image.manifest.v1+json") {
					t.Errorf("Accept does not include OCI manifests: %q", r.Header.Get("Accept"))
				}
				if tt.header != "" {
					w.Header().Set("Docker-Content-Digest", tt.header)
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()
			got, err := HTTPRegistry{BaseURL: srv.URL}.HasManifest(context.Background(), "toolchains/misarescu/repo", testDigest)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("HasManifest = %v, %v; want %v, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestHTTPRegistryHasManifestRefusesTags(t *testing.T) {
	if _, err := (HTTPRegistry{BaseURL: "http://unused"}).HasManifest(context.Background(), "r", "tc-abc"); err == nil {
		t.Fatal("a tag was accepted where only a digest proves anything")
	}
}

func TestHTTPRegistryHasManifestUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := (HTTPRegistry{BaseURL: url}).HasManifest(context.Background(), "r", testDigest); err == nil {
		t.Fatal("expected an error for an unreachable registry")
	}
}

func TestHTTPRegistryTag(t *testing.T) {
	const manifest = `{"schemaVersion":2}`
	var put, contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/toolchains/o/r/manifests/"+testDigest:
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_, _ = w.Write([]byte(manifest))
		case r.Method == http.MethodPut && r.URL.Path == "/v2/toolchains/o/r/manifests/tc-abc":
			b, _ := io.ReadAll(r.Body)
			put, contentType = string(b), r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	if err := (HTTPRegistry{BaseURL: srv.URL}).Tag(context.Background(), "toolchains/o/r", testDigest, "tc-abc"); err != nil {
		t.Fatal(err)
	}
	if put != manifest || contentType != "application/vnd.oci.image.manifest.v1+json" {
		t.Errorf("put %q as %q", put, contentType)
	}
}

// M6 (D84): listings follow the registry's Link header from page to page.
func TestHTTPRegistryRepositoriesPaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/_catalog" {
			t.Errorf("unexpected request %s", r.URL)
		}
		switch r.URL.Query().Get("last") {
		case "":
			w.Header().Set("Link", `</v2/_catalog?last=b&n=1000>; rel="next"`)
			_, _ = io.WriteString(w, `{"repositories":["a","b"]}`)
		case "b":
			_, _ = io.WriteString(w, `{"repositories":["c"]}`)
		default:
			t.Errorf("unexpected page %s", r.URL)
		}
	}))
	defer srv.Close()
	got, err := HTTPRegistry{BaseURL: srv.URL}.Repositories(t.Context())
	if err != nil || strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("Repositories = %v, %v", got, err)
	}
}

func TestHTTPRegistryTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/toolchains/o/r/tags/list":
			_, _ = io.WriteString(w, `{"name":"toolchains/o/r","tags":["tc-1","tc-2"]}`)
		case "/v2/toolchains/o/empty/tags/list":
			_, _ = io.WriteString(w, `{"name":"toolchains/o/empty","tags":null}`)
		case "/v2/toolchains/o/broken/tags/list":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	reg := HTTPRegistry{BaseURL: srv.URL}
	tests := []struct {
		repo    string
		want    string
		wantErr bool
	}{
		{"toolchains/o/r", "tc-1,tc-2", false},
		{"toolchains/o/empty", "", false},
		{"toolchains/o/unknown", "", false},
		{"toolchains/o/broken", "", true},
		{"Not A Repo", "", true},
	}
	for _, tt := range tests {
		got, err := reg.Tags(t.Context(), tt.repo)
		if (err != nil) != tt.wantErr || strings.Join(got, ",") != tt.want {
			t.Errorf("Tags(%q) = %v, %v; want %q, error %v", tt.repo, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestHTTPRegistryResolve(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		header    string
		wantFound bool
		wantErr   bool
	}{
		{"tagged", http.StatusOK, testDigest, true, false},
		{"no digest header", http.StatusOK, "", false, true},
		{"absent", http.StatusNotFound, "", false, false},
		{"server error", http.StatusInternalServerError, "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead || r.URL.Path != "/v2/base/runner/manifests/sha-1" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if tt.header != "" {
					w.Header().Set("Docker-Content-Digest", tt.header)
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()
			got, found, err := HTTPRegistry{BaseURL: srv.URL}.Resolve(t.Context(), "base/runner", "sha-1")
			if (err != nil) != tt.wantErr || found != tt.wantFound || (found && got != testDigest) {
				t.Fatalf("Resolve = %q, %v, %v", got, found, err)
			}
		})
	}
}

func TestHTTPRegistryDelete(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"accepted", http.StatusAccepted, false},
		{"already gone", http.StatusNotFound, false},
		{"deletes disabled", http.StatusMethodNotAllowed, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/v2/toolchains/o/r/manifests/"+testDigest {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()
			err := HTTPRegistry{BaseURL: srv.URL}.Delete(t.Context(), "toolchains/o/r", testDigest)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Delete = %v, want error %v", err, tt.wantErr)
			}
		})
	}
	if err := (HTTPRegistry{BaseURL: "http://unused"}).Delete(t.Context(), "r", "tc-1"); err == nil {
		t.Error("a tag was deleted by name, which would delete its manifest's other tags too")
	}
}
