package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapErr(t *testing.T) {
	if mapErr("op", nil) != nil {
		t.Error("mapErr(nil) != nil")
	}
	if err := mapErr("get", pgx.ErrNoRows); !errors.Is(err, ErrNotFound) {
		t.Errorf("no rows -> %v, want ErrNotFound", err)
	}
	unique := &pgconn.PgError{Code: "23505", ConstraintName: "issues_key_key"}
	if err := mapErr("create", unique); !errors.Is(err, ErrConflict) {
		t.Errorf("unique violation -> %v, want ErrConflict", err)
	}
	fk := &pgconn.PgError{Code: "23503"} // foreign key violation stays a plain error
	if err := mapErr("create", fk); errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		t.Errorf("fk violation -> %v, want neither sentinel", err)
	}
	cause := fmt.Errorf("boom")
	if err := mapErr("op", cause); !errors.Is(err, cause) {
		t.Errorf("unrelated error not wrapped: %v", err)
	}
}
