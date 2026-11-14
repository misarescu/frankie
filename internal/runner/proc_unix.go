//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

// SetProcessGroup puts a harness in its own process group so signals also
// reach the commands it spawns (shell tools, test runs).
func SetProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// TerminateGroup asks the harness's process group to stop (SIGTERM).
func TerminateGroup(cmd *exec.Cmd) { signalGroup(cmd, syscall.SIGTERM) }

// KillGroup kills the harness's process group (SIGKILL).
func KillGroup(cmd *exec.Cmd) { signalGroup(cmd, syscall.SIGKILL) }

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, sig)
	}
}
