package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// frk-4: GET /api/version answers 200 without a token and always reports an
// empty commit, stamped or not.
func TestGetVersion(t *testing.T) {
	tests := []struct {
		name, version, want string
	}{
		{"stamped", "6bb0a0bd6fb5c906f81536cad97ad65c3e72a773", ""},
		{"unstamped", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			(&Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: tt.version}).Register(mux)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/version", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type %q, want application/json", ct)
			}
			var got versionJSON
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.Version != tt.want {
				t.Errorf("version %q, want %q", got.Version, tt.want)
			}
		})
	}
}
