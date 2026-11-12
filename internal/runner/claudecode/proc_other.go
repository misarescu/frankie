//go:build !unix

package claudecode

import (
	"os"
	"os/exec"
)

// The runner targets Linux pods; this fallback only keeps other builds compiling.
const (
	sigTerm = 0
	sigKill = 1
)

func setProcessGroup(*exec.Cmd) {}

func signalGroup(cmd *exec.Cmd, _ int) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Kill)
	}
}
