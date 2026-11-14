//go:build !unix

package runner

import (
	"os"
	"os/exec"
)

// The runner targets Linux pods; these fallbacks only keep other builds compiling.

// SetProcessGroup does nothing outside unix.
func SetProcessGroup(*exec.Cmd) {}

// TerminateGroup kills the harness process.
func TerminateGroup(cmd *exec.Cmd) { KillGroup(cmd) }

// KillGroup kills the harness process.
func KillGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Kill)
	}
}
