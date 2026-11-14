package claudecode

import (
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/runner"
	"github.com/misarescu/frankie/internal/runner/runnertest"
)

// TestConformance runs the real claude binary against the fake model, which
// speaks the Anthropic API: no model usage, unlike test:smoke-claude.
func TestConformance(t *testing.T) {
	runnertest.RunConformance(t, runnertest.Adapter{
		Binary: "claude",
		New: func(t *testing.T, bin string) runner.Harness {
			return &Harness{Binary: bin, KillGrace: 2 * time.Second}
		},
		ShellTool:    shellTool,
		APIs:         []runner.ModelAPI{runner.ModelAPIAnthropic},
		ReadOnlyRole: []string{"read", "grep", "glob", "bash:readonly"},
		WriteTools:   []string{"Edit", "Write", "WebFetch"},
		// As in the runner image (deploy/images/runner/Dockerfile).
		Env: map[string]string{"DISABLE_AUTOUPDATER": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"},
	})
}
