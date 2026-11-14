package pi

import (
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/runner"
	"github.com/misarescu/frankie/internal/runner/runnertest"
)

func TestConformance(t *testing.T) {
	runnertest.RunConformance(t, runnertest.Adapter{
		Binary: "pi",
		New: func(t *testing.T, bin string) runner.Harness {
			return &Harness{Binary: bin, TempDir: t.TempDir(), KillGrace: 2 * time.Second}
		},
		ShellTool:    shellTool,
		AskTool:      "mcp__frankie__ask_human",
		APIs:         []runner.ModelAPI{runner.ModelAPIOpenAI, runner.ModelAPIAnthropic},
		ReadOnlyRole: []string{"read", "grep", "glob", "bash:readonly"},
		WriteTools:   []string{"edit", "write"},
	})
}
