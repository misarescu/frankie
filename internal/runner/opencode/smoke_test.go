//go:build smoke

// Smoke test against a real model through OpenRouter. It spends real usage,
// so it only runs on demand: mise run test:smoke-opencode (needs OPENROUTER_API_KEY).
package opencode

import (
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/runner"
	"github.com/misarescu/frankie/internal/runner/runnertest"
)

func TestSmokeRealModel(t *testing.T) {
	runnertest.RunSmoke(t, runnertest.Adapter{
		Binary: "opencode",
		New: func(t *testing.T, bin string) runner.Harness {
			return &Harness{Binary: bin, TempDir: t.TempDir(), KillGrace: 2 * time.Second}
		},
	})
}
