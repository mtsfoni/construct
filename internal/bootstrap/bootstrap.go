// Package bootstrap implements the CLI-side daemon bootstrap sequence.
// It checks whether the daemon container is running, builds the daemon image if
// needed, and waits for the daemon socket to become connectable.
package bootstrap

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"

	"github.com/construct-run/construct/internal/stacks"
	"github.com/construct-run/construct/internal/version"
)

const (
	daemonContainerName = "construct-daemon"
	daemonImageName     = "construct-daemon:latest"
	socketWaitTimeout   = 30 * time.Second
	socketPollInterval  = 200 * time.Millisecond
	maxRetries          = 3
)

// Options holds configuration for bootstrap.
type Options struct {
	// ConstructConfigDir is the host-side construct config directory
	// (e.g. ~/.config/construct/). It is mounted into the daemon as /state.
	ConstructConfigDir string
	// Progress is an optional writer for progress messages.
	Progress io.Writer
}

// EnsureDaemon ensures the daemon container is running and the socket is
// connectable. It returns the path to the Unix socket.
func EnsureDaemon(ctx context.Context, opts Options) (socketPath string, err error) {
	socketPath = filepath.Join(opts.ConstructConfigDir, "daemon.sock")

	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return "", fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close()

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(500 * time.Millisecond)
		}
		err = ensureDaemonOnce(ctx, cli, opts)
		if err == nil {
			break
		}
		// If this is a name conflict (another CLI started simultaneously), retry.
		if isNameConflict(err) {
			logProgress(opts.Progress, "Daemon start conflict, retrying...")
			continue
		}
		return "", err
	}
	if err != nil {
		return "", err
	}

	if err := waitForSocket(socketPath, socketWaitTimeout); err != nil {
		return "", fmt.Errorf("daemon socket not ready: %w", err)
	}
	return socketPath, nil
}

func ensureDaemonOnce(ctx context.Context, cli *dockerclient.Client, opts Options) error {
	ctrStatus, ctrLabels, err := inspectContainer(ctx, cli, daemonContainerName)
	if err != nil && !dockerclient.IsErrNotFound(err) {
		return fmt.Errorf("inspect daemon container: %w", err)
	}
	notFound := dockerclient.IsErrNotFound(err)

	if notFound {
		// Container doesn't exist — build image and create container.
		logProgress(opts.Progress, "Building daemon image...")
		if err := buildDaemonImage(ctx, opts); err != nil {
			return fmt.Errorf("build daemon image: %w", err)
		}
		logProgress(opts.Progress, "Starting daemon container...")
		return runDaemonContainer(ctx, opts)
	}

	switch ctrStatus {
	case "running":
		// Check version label.
		if shouldRebuild(ctrLabels) {
			logProgress(opts.Progress, "Daemon image version mismatch. Rebuilding...")
			if err := stopAndRemoveContainer(ctx, cli); err != nil {
				return err
			}
			if err := buildDaemonImage(ctx, opts); err != nil {
				return fmt.Errorf("build daemon image: %w", err)
			}
			return runDaemonContainer(ctx, opts)
		}
		return nil // already running and up-to-date

	case "exited", "created":
		logProgress(opts.Progress, "Restarting daemon container...")
		return cli.ContainerStart(ctx, daemonContainerName, container.StartOptions{})

	default:
		return cli.ContainerStart(ctx, daemonContainerName, container.StartOptions{})
	}
}

func inspectContainer(ctx context.Context, cli *dockerclient.Client, name string) (status string, labels map[string]string, err error) {
	data, _, err := cli.ContainerInspectWithRaw(ctx, name, false)
	if err != nil {
		return "", nil, err
	}
	if data.State != nil {
		status = data.State.Status
	}
	if data.Config != nil {
		labels = data.Config.Labels
	}
	return status, labels, nil
}

func shouldRebuild(labels map[string]string) bool {
	if version.IsDev() {
		return false // dev builds skip version checks
	}
	v, ok := labels[stacks.VersionLabel]
	if !ok {
		return true
	}
	return v != version.Version
}

func buildDaemonImage(ctx context.Context, opts Options) error {
	buildCtxDir, err := stacks.ExtractBuildContext("daemon")
	if err != nil {
		return fmt.Errorf("extract daemon build context: %w", err)
	}
	defer os.RemoveAll(buildCtxDir)

	// Cross-compile constructd for linux/amd64 and place it in the build context.
	// The Dockerfile does a simple COPY rather than building from source, so no
	// Go toolchain is needed inside the daemon image.
	logProgress(opts.Progress, "Compiling constructd for linux/amd64...")
	constructdPath := filepath.Join(buildCtxDir, "constructd")
	buildCmd := exec.CommandContext(ctx, "go", "build",
		"-ldflags", "-X github.com/construct-run/construct/internal/version.Version="+version.Version+" -s -w",
		"-o", constructdPath,
		"./cmd/constructd/",
	)
	buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compile constructd: %w\n%s", err, out)
	}

	args := []string{
		"build",
		"-t", daemonImageName,
		"--label", stacks.VersionLabel + "=" + version.Version,
		buildCtxDir,
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	if opts.Progress != nil {
		cmd.Stdout = opts.Progress
		cmd.Stderr = opts.Progress
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	return nil
}

func runDaemonContainer(ctx context.Context, opts Options) error {
	stateDir := opts.ConstructConfigDir
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	// The daemon container needs to write daemon.sock into /state (owned by the
	// host user) and access /var/run/docker.sock (owned by root:docker).
	// We run as root inside the container (UID 0) which can write anywhere,
	// but the docker socket GID must be added so docker CLI calls work.
	dockerGID, err := dockerSocketGID()
	if err != nil {
		return fmt.Errorf("get docker socket GID: %w", err)
	}

	args := []string{
		"run", "-d",
		"--name", daemonContainerName,
		"--restart", "unless-stopped",
		"--network", "host",
		"--user", "root",
		"--group-add", dockerGID,
		"--security-opt", "label=disable",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", stateDir + ":/state:z",
		"-e", "CONSTRUCT_HOST_STATE_DIR=" + stateDir,
		daemonImageName,
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker run daemon: %w\n%s", err, out)
	}
	return nil
}

// dockerSocketGID returns the GID of /var/run/docker.sock as a string.
func dockerSocketGID() (string, error) {
	fi, err := os.Stat("/var/run/docker.sock")
	if err != nil {
		return "", err
	}
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "0", nil // fallback: use root
	}
	return fmt.Sprintf("%d", stat.Gid), nil
}

func stopAndRemoveContainer(ctx context.Context, cli *dockerclient.Client) error {
	timeout := 10
	_ = cli.ContainerStop(ctx, daemonContainerName, container.StopOptions{Timeout: &timeout})
	return cli.ContainerRemove(ctx, daemonContainerName, container.RemoveOptions{Force: true})
}

func waitForSocket(socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, socketPollInterval)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(socketPollInterval)
	}
	return fmt.Errorf("timed out waiting for socket %s after %v", socketPath, timeout)
}

func isNameConflict(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return containsStr(s, "already in use") || containsStr(s, "Conflict")
}

func containsStr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func logProgress(w io.Writer, msg string) {
	if w != nil {
		fmt.Fprintf(w, "  %s\n", msg)
	}
}

// DockerServerVersion returns the Docker Engine version string (e.g. "28.5.2")
// by querying the local Docker daemon. This is used by the CLI to check platform
// requirements before bootstrapping.
func DockerServerVersion(ctx context.Context) (string, error) {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return "", fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close()

	v, err := cli.ServerVersion(ctx)
	if err != nil {
		return "", err
	}
	return v.Version, nil
}
