package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// D131, D146: GET /version reports the build commit without auth (cluster probe)
// and GET /api/version reports it with auth (or in PreviewMode).
func TestGetVersion(t *testing.T) {
	tests := []struct {
		name, version, want string
	}{
		{"stamped", "6bb0a0bd6fb5c906f81536cad97ad65c3e72a773", "6bb0a0bd6fb5c906f81536cad97ad65c3e72a773"},
		{"unstamped", "", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			srvObj := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: tt.version}
			mux.HandleFunc("GET /version", srvObj.GetVersion)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/version", nil)
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
