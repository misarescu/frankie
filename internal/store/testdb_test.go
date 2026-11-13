package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/misarescu/frankie/internal/domain"
)

// unavailable skips locally but fails in CI, so a missing database can never
// silently skip the store tests where they must run.
func unavailable(t *testing.T, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	if os.Getenv("CI") != "" {
		t.Fatalf("%s (required in CI)", msg)
	}
	t.Skipf("%s; run `mise run db:up`", msg)
}

// newTestPool creates a fresh, empty database for one test and drops it afterwards.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminURL := os.Getenv("FRANKIE_TEST_DATABASE_URL")
	if adminURL == "" {
		unavailable(t, "FRANKIE_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		unavailable(t, "test database unreachable: %v", err)
	}
	defer func() { _ = admin.Close(context.Background()) }()

	name := "frankie_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		admin, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			t.Errorf("drop test database: %v", err)
			return
		}
		defer func() { _ = admin.Close(ctx) }()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database: %v", err)
		}
	})

	cfg, err := pgxpool.ParseConfig(adminURL)
	if err != nil {
		t.Fatalf("parse test database url: %v", err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newTestStore returns a Store on a fresh, fully migrated database.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	pool := newTestPool(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(pool)
}

func mustProject(t *testing.T, s *Store) domain.Project {
	t.Helper()
	id := uuid.NewString()
	p, err := s.CreateProject(t.Context(), "proj-"+id[:8], "p"+strings.ReplaceAll(id[:8], "-", ""))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return p
}

func mustIssue(t *testing.T, s *Store, projectID uuid.UUID, status domain.IssueStatus) domain.Issue {
	t.Helper()
	is, err := s.CreateIssue(t.Context(), NewIssue{
		ProjectID: projectID, Type: domain.IssueTask, Title: "do the thing",
		Status: status, CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	return is
}

func mustRun(t *testing.T, s *Store, issueID uuid.UUID) domain.Run {
	t.Helper()
	r, err := s.CreateRun(t.Context(), NewRun{
		IssueID:            issueID,
		WorkflowSnapshot:   json.RawMessage(`{"name":"default-build","steps":[]}`),
		WorkflowVersion:    "1",
		TemporalWorkflowID: "wf-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	return r
}
