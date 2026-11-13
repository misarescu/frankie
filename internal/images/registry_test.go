package images

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPRegistryHas(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		want    bool
		wantErr bool
	}{
		{"present", http.StatusOK, true, false},
		{"absent", http.StatusNotFound, false, false},
		{"server error is not absence", http.StatusInternalServerError, false, true},
		{"auth failure is not absence", http.StatusUnauthorized, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead || r.URL.Path != "/v2/toolchains/misarescu/repo/manifests/tc-abc" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if !strings.Contains(r.Header.Get("Accept"), "application/vnd.oci.image.manifest.v1+json") {
					t.Errorf("Accept does not include OCI manifests: %q", r.Header.Get("Accept"))
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()
			got, err := HTTPRegistry{BaseURL: srv.URL}.Has(context.Background(), "toolchains/misarescu/repo", "tc-abc")
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("Has = %v, %v; want %v, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestHTTPRegistryHasUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := (HTTPRegistry{BaseURL: url}).Has(context.Background(), "r", "t"); err == nil {
		t.Fatal("expected an error for an unreachable registry")
	}
}
