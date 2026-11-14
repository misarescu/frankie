package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// D58: every write endpoint needs the bearer token; reads stay open.
func TestRequireToken(t *testing.T) {
	writes := []string{
		"/api/gates/00000000-0000-0000-0000-000000000000/decision",
		"/api/steps/00000000-0000-0000-0000-000000000000/steer",
		"/api/steps/00000000-0000-0000-0000-000000000000/abort",
		"/api/questions/00000000-0000-0000-0000-000000000000/answer",
		"/api/issues/start",
		"/api/issues/gc-1/retry",
	}
	newServer := func(token string) *httptest.Server {
		mux := http.NewServeMux()
		(&Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Token: token}).Register(mux)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return srv
	}
	// do returns the status and the WWW-Authenticate header.
	do := func(t *testing.T, url, auth string) (int, string) {
		t.Helper()
		// An invalid body: a request that gets past the token check fails validation
		// (400) before it needs a store or a workflow.
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader("{"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("WWW-Authenticate")
	}

	srv := newServer(testToken)
	tests := []struct {
		name, auth string
		want       int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"token prefix only", "Bearer " + testToken[:10], http.StatusUnauthorized},
		{"wrong scheme", "Basic " + testToken, http.StatusUnauthorized},
		{"right token", "Bearer " + testToken, http.StatusBadRequest},
	}
	for _, path := range writes {
		for _, tt := range tests {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				code, challenge := do(t, srv.URL+path, tt.auth)
				if code != tt.want {
					t.Fatalf("status = %d, want %d", code, tt.want)
				}
				if tt.want == http.StatusUnauthorized && !strings.HasPrefix(challenge, "Bearer") {
					t.Errorf("WWW-Authenticate = %q, want a Bearer challenge", challenge)
				}
			})
		}
	}

	t.Run("no token configured refuses writes", func(t *testing.T) {
		srv := newServer("")
		for _, auth := range []string{"", "Bearer ", "Bearer " + testToken} {
			if code, _ := do(t, srv.URL+writes[0], auth); code != http.StatusServiceUnavailable {
				t.Errorf("auth %q: status = %d, want 503", auth, code)
			}
		}
	})
}

// Reads need no token: the runs list answers without one.
func TestReadsNeedNoToken(t *testing.T) {
	_, srv, _, _ := testServer(t)
	var runs []map[string]any
	if code := getJSON(t, srv.URL+"/api/runs", &runs); code != http.StatusOK {
		t.Fatalf("GET /api/runs = %d, want 200", code)
	}
}

func TestTokenEqual(t *testing.T) {
	if !tokenEqual("abc", "abc") || tokenEqual("abc", "abd") || tokenEqual("", "abc") || tokenEqual("abcd", "abc") {
		t.Fatal("tokenEqual is not plain equality")
	}
}
