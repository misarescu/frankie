package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/misarescu/frankie/internal/auth"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/store/storetest"
)

// testToken is the personal API token of the default test admin user.
const (
	testToken     = "frk_test0123456789abcdef0123456789abcdef"
	testActorName = "Test Admin"
)

// testServer runs the API against a fresh database with a live hub.
func testServer(t *testing.T) (*store.Store, *httptest.Server, domain.Run, *Hub) {
	t.Helper()
	return testServerWith(t, nil)
}

// testServerWith is testServer with a Signaler for human gate decisions.
func testServerWith(t *testing.T, sig Signaler) (*store.Store, *httptest.Server, domain.Run, *Hub) {
	t.Helper()
	return testServerAuth(t, sig, AuthConfig{})
}

// testServerAuth creates a test API server with custom AuthConfig and a seeded
// admin user holding testToken.
func testServerAuth(t *testing.T, sig Signaler, authCfg AuthConfig) (*store.Store, *httptest.Server, domain.Run, *Hub) {
	t.Helper()
	dbURL := storetest.DatabaseURL(t)
	st, err := store.Open(t.Context(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(t.Context(), st.Pool()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewHub(dbURL, log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go hub.Run(ctx)

	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(t.Context(), store.NewUser{
		Username:     "test-admin",
		DisplayName:  testActorName,
		Email:        "admin@example.com",
		Role:         domain.RoleAdmin,
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAPIToken(t.Context(), store.NewAPIToken{
		UserID:      u.ID,
		Name:        "test-suite",
		TokenHash:   auth.HashToken(testToken),
		TokenPrefix: testToken[:12],
	}); err != nil {
		t.Fatal(err)
	}

	p, _ := st.CreateProject(t.Context(), "misarescu/Goodreads-Clone", "gc")
	is, _ := st.CreateIssue(t.Context(), store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "Add <b>search</b>", Status: domain.IssueRunning, CreatedBy: "t"})
	run, err := st.CreateRun(t.Context(), store.NewRun{IssueID: is.ID, WorkflowSnapshot: []byte(`{}`), WorkflowVersion: "1", TemporalWorkflowID: "issue-gc-1"})
	if err != nil {
		t.Fatal(err)
	}
	if run, err = st.TransitionRun(t.Context(), run.ID, domain.ExecPending, domain.ExecRunning); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&Server{Store: st, Hub: hub, Log: log, Signaler: sig, Auth: authCfg}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return st, srv, run, hub
}
