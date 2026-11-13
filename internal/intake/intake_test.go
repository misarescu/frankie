package intake

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow"
)

// testStore returns a store on a fresh, migrated database (skips locally without
// Postgres, fails in CI), like the store tests.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	admin := os.Getenv("FRANKIE_TEST_DATABASE_URL")
	conn, err := pgx.Connect(t.Context(), admin)
	if admin == "" || err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("test database required in CI: %v", err)
		}
		t.Skip("test database unavailable; run `mise run db:up`")
	}
	defer func() { _ = conn.Close(context.Background()) }()
	name := "frankie_intake_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := conn.Exec(t.Context(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if c, err := pgx.Connect(context.Background(), admin); err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
			_ = c.Close(context.Background())
		}
	})
	cfg, _ := pgxpool.ParseConfig(admin)
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return store.New(pool)
}

type fakeSource struct {
	issues []github.Issue
	err    error
}

func (f *fakeSource) ListOpenIssues(context.Context, string, string) ([]github.Issue, error) {
	return f.issues, f.err
}

type fakeStarter struct{ started []workflow.IssueRunInput }

func (f *fakeStarter) StartIssueRun(_ context.Context, in workflow.IssueRunInput) error {
	f.started = append(f.started, in)
	return nil
}

func TestPollTakesInLabeledIssuesOnce(t *testing.T) {
	s := testStore(t)
	p, err := s.CreateProject(t.Context(), "misarescu/Goodreads-Clone", "gc")
	if err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{issues: []github.Issue{{Number: 3, Title: "Add search", Body: "Users can search.",
		URL: "https://github.com/misarescu/Goodreads-Clone/issues/3", Author: "mi", Labels: []string{"factory"}}}}
	starter := &fakeStarter{}
	in := &Intake{Store: s, Source: src, Starter: starter, Label: "factory", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Projects: []Project{{ID: p.ID, Repo: "misarescu/Goodreads-Clone", BaseBranch: "main"}}}

	if err := in.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	is, err := s.GetIssueBySourceRef(t.Context(), p.ID, src.issues[0].URL)
	if err != nil || is.Key != "gc-1" || is.Status != domain.IssueReady || is.CreatedBy != "github:mi" {
		t.Fatalf("issue = %+v, %v", is, err)
	}
	if len(starter.started) != 1 {
		t.Fatalf("started %d runs", len(starter.started))
	}
	got := starter.started[0]
	if got.IssueKey != "gc-1" || got.SourceNumber != 3 || got.Repo != "misarescu/Goodreads-Clone" || got.BaseBranch != "main" || got.Spec != "Users can search." {
		t.Errorf("run input = %+v", got)
	}

	// A second poll while still ready asks again (the starter dedupes by workflow ID)
	// but never creates a second issue.
	if err := in.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if next, _ := s.CreateIssue(t.Context(), store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "x", CreatedBy: "t"}); next.Key != "gc-2" {
		t.Errorf("a duplicate issue was created: next key %s", next.Key)
	}

	// Once the run took the issue, polling stops starting it.
	if _, err := s.TransitionIssue(t.Context(), is.ID, domain.IssueReady, domain.IssueRunning); err != nil {
		t.Fatal(err)
	}
	before := len(starter.started)
	if err := in.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(starter.started) != before {
		t.Error("a running issue was started again")
	}
}

func TestPollReportsSourceErrors(t *testing.T) {
	s := testStore(t)
	p, _ := s.CreateProject(t.Context(), "o/r", "or")
	in := &Intake{Store: s, Source: &fakeSource{err: errors.New("401 bad credentials")}, Starter: &fakeStarter{},
		Label: "factory", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Projects: []Project{{ID: p.ID, Repo: "o/r"}}}
	if err := in.Poll(t.Context()); err == nil || !strings.Contains(err.Error(), "bad credentials") {
		t.Errorf("err = %v", err)
	}
}
