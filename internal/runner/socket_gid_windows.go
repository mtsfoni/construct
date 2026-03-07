//go:build windows

package runner

// dockerSocketGID always returns "" on Windows. Docker Desktop on Windows does
// not expose a Unix socket at /var/run/docker.sock, so group-add is never needed.
var dockerSocketGID = func() string {
	return ""
}
