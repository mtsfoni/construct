//go:build windows

package runner

// containerWorkdir returns the path inside the container where the host repo
// path should be mounted and used as the working directory. On Windows, Docker
// Desktop runs containers through a Linux VM; Windows paths (C:\...) have no
// meaningful equivalent Linux path, so we fall back to the fixed /workspace
// mount point.
func containerWorkdir(repoPath string) string {
	return "/workspace"
}
