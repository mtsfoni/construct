//go:build !windows

package runner

import (
	"fmt"
	"os"
	"syscall"
)

// dockerSocketGID returns the GID of /var/run/docker.sock as a string, or ""
// if the socket cannot be stat'd. It is a package-level variable so tests can
// inject a stub without shelling out to Docker.
var dockerSocketGID = func() string {
	info, err := os.Stat("/var/run/docker.sock")
	if err != nil {
		return ""
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d", st.Gid)
}
