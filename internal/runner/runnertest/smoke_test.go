package runnertest

import "testing"

// Without a key the smoke suite skips: it must never fail a plain test run
// or spend usage by accident.
func TestRunSmokeSkipsWithoutKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	var skipped bool
	t.Run("smoke", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		RunSmoke(t, Adapter{Binary: "does-not-exist"})
	})
	if !skipped {
		t.Error("RunSmoke ran without OPENROUTER_API_KEY")
	}
}
