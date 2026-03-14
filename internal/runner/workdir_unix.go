//go:build !windows

package runner

// containerWorkdir returns the path inside the container where the host repo
// path should be mounted and used as the working directory. On Linux and macOS
// the host path is mirrored verbatim so that absolute paths (e.g. git worktree
// references) resolve identically inside and outside the container.
func containerWorkdir(repoPath string) string {
	return repoPath
}
