package main

import "testing"

// The binary only wires configuration; behavior is tested in internal/controlplane.
func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Setenv("FRANKIE_DATABASE_URL", "")
	if err := run(); err == nil {
		t.Error("run without FRANKIE_DATABASE_URL succeeded")
	}
}
