package storetest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Each call must yield its own database, not the admin database.
func TestDatabaseURLIsIsolated(t *testing.T) {
	a, b := DatabaseURL(t), DatabaseURL(t)
	if a == b {
		t.Fatal("two calls returned the same database")
	}
	conn, err := pgx.Connect(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var name string
	if err := conn.QueryRow(context.Background(), "select current_database()").Scan(&name); err != nil || name == "postgres" {
		t.Fatalf("connected to %q (%v); want the test's own database", name, err)
	}
}
