//go:build !windows

package dind

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr places the command in its own process group so that a
// SIGINT from Ctrl+C on the terminal is not forwarded to the subprocess.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
