//go:build windows

package dind

import "os/exec"

// setSysProcAttr is a no-op on Windows; process group isolation is not
// needed because Windows does not use POSIX process groups for signal delivery.
func setSysProcAttr(cmd *exec.Cmd) {}
