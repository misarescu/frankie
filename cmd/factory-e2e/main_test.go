package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/e2e"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// D132: the command reports through the e2e suite's contract (D116): JUnit at
// E2E_RESULTS and the exit code.
func TestRun(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/issues", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{{"key": "pr7dum-1", "status": "failed"}})
	})
	mux.HandleFunc("GET /api/issues/pr7dum-1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"key": "pr7dum-1", "status": "failed", "runs": []map[string]string{{"id": "r1"}}})
	})
	mux.HandleFunc("GET /api/runs/r1/steps", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"step_id": "checks", "attempt": 1, "kind": "gate", "gate": "command", "status": "failed"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	results := filepath.Join(t.TempDir(), "results.xml")
	var out strings.Builder
	code := run(t.Context(), envOf(map[string]string{"E2E_BASE_URL": srv.URL, "E2E_RESULTS": results, "FACTORY_E2E_TIMEOUT": "1s"}), &out, time.Millisecond)
	if code != 1 || !strings.Contains(out.String(), "FAIL  factory e2e > pr7dum-1 > checks #1: command gate checks failed on attempt 1") {
		t.Fatalf("exit %d, output:\n%s", code, out.String())
	}
	b, err := os.ReadFile(results)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := e2e.ParseJUnit(b)
	if err != nil || len(cases) != 2 || cases[0].Outcome != e2e.Failed || cases[1].Name != "the issue merged" {
		t.Errorf("results = %+v, %v", cases, err)
	}
}

func TestRunBadEnvironment(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no base url": {},
		"bad timeout": {"E2E_BASE_URL": "http://x", "FACTORY_E2E_TIMEOUT": "soon"},
	} {
		t.Run(name, func(t *testing.T) {
			var out strings.Builder
			if code := run(t.Context(), envOf(env), &out, time.Millisecond); code != 2 {
				t.Errorf("exit %d, want 2; output %q", code, out.String())
			}
		})
	}
}
