package opencode

import (
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/runner"
	"github.com/misarescu/frankie/internal/runner/runnertest"
)

func TestConformance(t *testing.T) {
	runnertest.RunConformance(t, runnertest.Adapter{
		Binary: "opencode",
		New: func(t *testing.T, bin string) runner.Harness {
			return &Harness{Binary: bin, TempDir: t.TempDir(), KillGrace: 2 * time.Second}
		},
		ShellTool:    shellTool,
		APIs:         []runner.ModelAPI{runner.ModelAPIOpenAI, runner.ModelAPIAnthropic},
		ReadOnlyRole: []string{"read", "grep", "bash:readonly"},
		WriteTools:   []string{"edit", "write", "webfetch", "question"},
	})
}
