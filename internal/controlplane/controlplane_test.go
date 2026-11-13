package controlplane

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/misarescu/frankie/internal/store/storetest"
)

func listen(t *testing.T) net.Listener {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func get(t *testing.T, url string) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

func TestRunMigratesAndServes(t *testing.T) {
	dbURL := storetest.DatabaseURL(t)
	api, git := listen(t), listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			DatabaseURL: dbURL,
			GitProxyKey: []byte("0123456789abcdef0123456789abcdef"),
			Repos:       []RepoConfig{{Name: "misarescu/toy", Upstream: "https://github.com/misarescu/toy.git", KeyPrefix: "toy", BaseBranch: "main"}},
		}, Listeners{API: api, GitProxy: git}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	apiURL, gitURL := "http://"+api.Addr().String(), "http://"+git.Addr().String()
	deadline := time.Now().Add(30 * time.Second)
	for get(t, apiURL+"/readyz") != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("control plane never became ready")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code := get(t, apiURL+"/healthz"); code != http.StatusOK {
		t.Errorf("healthz = %d", code)
	}

	// Migrations ran: the schema exists.
	conn, err := pgx.Connect(t.Context(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	var tables int
	if err := conn.QueryRow(t.Context(), `SELECT count(*) FROM information_schema.tables WHERE table_name IN ('issues','runs','events')`).Scan(&tables); err != nil || tables != 3 {
		t.Errorf("tables = %d, %v; want 3", tables, err)
	}
	// The configured repo became a project.
	var prefix string
	if err := conn.QueryRow(t.Context(), `SELECT key_prefix FROM projects WHERE name = 'misarescu/toy'`).Scan(&prefix); err != nil || prefix != "toy" {
		t.Errorf("project prefix = %q, %v", prefix, err)
	}
	_ = conn.Close(t.Context())

	// The git proxy is mounted and demands a token.
	if code := get(t, gitURL+"/git/misarescu/toy.git/info/refs?service=git-upload-pack"); code != http.StatusUnauthorized {
		t.Errorf("git proxy without token = %d, want 401", code)
	}
	if code := get(t, apiURL+"/git/misarescu/toy.git/info/refs?service=git-upload-pack"); code != http.StatusNotFound {
		t.Errorf("git proxy on the API port = %d, want 404 (only the git proxy port serves it)", code)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run after cancel: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunGivesUpOnUnreachableDatabase(t *testing.T) {
	err := Run(t.Context(), Config{
		DatabaseURL: "postgres://x:y@127.0.0.1:1/none?sslmode=disable&connect_timeout=1",
		GitProxyKey: []byte("0123456789abcdef0123456789abcdef"),
		DBWait:      time.Second,
	}, Listeners{API: listen(t), GitProxy: listen(t)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "database not reachable") {
		t.Errorf("err = %v", err)
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := Run(t.Context(), Config{GitProxyKey: []byte("short")}, Listeners{}, quiet); err == nil {
		t.Error("short signing key accepted")
	}
	if err := Run(t.Context(), Config{GitProxyKey: []byte("0123456789abcdef0123456789abcdef"),
		Repos: []RepoConfig{{Name: "a/b", Upstream: "git@github.com:a/b.git", KeyPrefix: "ab"}}}, Listeners{}, quiet); err == nil {
		t.Error("ssh upstream accepted")
	}
}
