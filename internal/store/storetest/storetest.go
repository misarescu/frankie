// Package storetest gives tests outside the store package a fresh, migrated
// database. Without a test Postgres it skips locally and fails in CI.
package storetest

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/misarescu/frankie/internal/store"
)

// DatabaseURL creates an empty database for the test, dropped afterwards.
func DatabaseURL(t testing.TB) string {
	t.Helper()
	admin := os.Getenv("FRANKIE_TEST_DATABASE_URL")
	unavailable := func(msg string) {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s (required in CI)", msg)
		}
		t.Skipf("%s; run `mise run db:up`", msg)
	}
	if admin == "" {
		unavailable("FRANKIE_TEST_DATABASE_URL is not set")
	}
	conn, err := pgx.Connect(context.Background(), admin)
	if err != nil {
		unavailable("test database unreachable: " + err.Error())
	}
	defer func() { _ = conn.Close(context.Background()) }()
	name := "frankie_t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := conn.Exec(context.Background(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if c, err := pgx.Connect(context.Background(), admin); err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
			_ = c.Close(context.Background())
		}
	})
	// Replace the database in the URL itself: pgx's Config.ConnString returns the
	// original string, so changing ConnConfig.Database would not show up there.
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// New returns a Store on a fresh, migrated database.
func New(t testing.TB) *store.Store {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return store.New(pool)
}
