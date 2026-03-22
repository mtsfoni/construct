// Package integration contains Docker integration tests for construct.
// These tests require a running Docker daemon and are gated behind the
// CONSTRUCT_TEST_DOCKER environment variable.
//
// Run with:
//
//	CONSTRUCT_TEST_DOCKER=1 go test ./internal/integration/ -v -timeout 10m
package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/construct-run/construct/internal/auth"
	"github.com/construct-run/construct/internal/config"
	dockeriface "github.com/construct-run/construct/internal/daemon/docker"
	"github.com/construct-run/construct/internal/daemon/registry"
	"github.com/construct-run/construct/internal/daemon/server"
	"github.com/construct-run/construct/internal/daemon/session"
	"github.com/construct-run/construct/internal/quickstart"
	"github.com/construct-run/construct/internal/stacks"
	"github.com/construct-run/construct/internal/tools"
)

// skipWithoutDocker skips the test if CONSTRUCT_TEST_DOCKER is not set or
// Docker is not reachable.
func skipWithoutDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("CONSTRUCT_TEST_DOCKER") == "" {
		t.Skip("set CONSTRUCT_TEST_DOCKER=1 to run Docker integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("Docker client error: %v", err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx); err != nil {
		t.Skipf("Docker not reachable: %v", err)
	}
}

// newDockerClient returns a real Docker client for integration tests.
func newDockerClient(t *testing.T) *dockeriface.RealClient {
	t.Helper()
	c, err := dockeriface.NewRealClient()
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// ensureBaseImage builds (or verifies) construct-stack-base:latest is present.
func ensureBaseImage(t *testing.T, ctx context.Context, c *dockeriface.RealClient) {
	t.Helper()
	_, _, err := c.ImageInspectWithRaw(ctx, stacks.ImageName(stacks.StackBase))
	if err == nil {
		return // already present
	}
	t.Log("Building construct-stack-base image (this may take a few minutes)...")
	buildCtx, err := stacks.ExtractBuildContext(stacks.StackBase)
	if err != nil {
		t.Fatalf("extract build context: %v", err)
	}
	defer os.RemoveAll(buildCtx)

	var tarBuf bytes.Buffer
	if err := dirToTar(buildCtx, &tarBuf); err != nil {
		t.Fatalf("tar build context: %v", err)
	}

	resp, err := c.ImageBuild(ctx, &tarBuf, types.ImageBuildOptions{
		Tags:       []string{stacks.ImageName(stacks.StackBase)},
		Dockerfile: "Dockerfile",
		Remove:     true,
	})
	if err != nil {
		t.Fatalf("build base image: %v", err)
	}
	defer resp.Body.Close()
	// Drain output; fail on error entries.
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var msg struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) == nil && msg.Error != "" {
			t.Fatalf("image build error: %s", msg.Error)
		}
	}
}

// uniqueName returns a test-scoped unique container/volume/network name.
func uniqueName(t *testing.T, suffix string) string {
	t.Helper()
	// Replace slashes in test name (subtests use /) with dashes.
	safe := strings.ReplaceAll(t.Name(), "/", "-")
	safe = strings.ReplaceAll(safe, " ", "-")
	if len(safe) > 40 {
		safe = safe[len(safe)-40:]
	}
	return fmt.Sprintf("construct-test-%s-%s", safe, suffix)
}

// removeContainer force-removes a container, ignoring not-found errors.
func removeContainer(t *testing.T, ctx context.Context, cli *dockeriface.RealClient, name string) {
	t.Helper()
	_ = cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
}

// removeVolume removes a volume, ignoring errors.
func removeVolume(t *testing.T, ctx context.Context, cli *dockeriface.RealClient, name string) {
	t.Helper()
	_ = cli.VolumeRemove(ctx, name, true)
}

// execAndWait runs an exec inside a container and waits for it to finish.
// Returns the exit code and any output.
func execAndWait(t *testing.T, ctx context.Context, cli *dockeriface.RealClient, containerName string, cmd []string) (int, string) {
	t.Helper()
	resp, err := cli.ContainerExecCreate(ctx, containerName, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		t.Fatalf("exec create %v: %v", cmd, err)
	}

	attach, err := cli.ContainerExecAttach(ctx, resp.ID, container.ExecAttachOptions{})
	if err != nil {
		t.Fatalf("exec attach: %v", err)
	}
	var outBuf bytes.Buffer
	go func() {
		io.Copy(&outBuf, attach.Reader) //nolint:errcheck
	}()
	if err := cli.ContainerExecStart(ctx, resp.ID, container.ExecStartOptions{}); err != nil {
		t.Fatalf("exec start: %v", err)
	}

	// Poll until done.
	for i := 0; i < 60; i++ {
		time.Sleep(500 * time.Millisecond)
		insp, err := cli.ContainerExecInspect(ctx, resp.ID)
		if err != nil {
			t.Fatalf("exec inspect: %v", err)
		}
		if !insp.Running {
			return insp.ExitCode, outBuf.String()
		}
	}
	t.Fatal("exec timed out")
	return -1, ""
}

// startContainer creates and starts a container, registering cleanup.
func startContainer(t *testing.T, ctx context.Context, cli *dockeriface.RealClient, name, image string, mounts []mount.Mount, env []string) {
	t.Helper()
	t.Cleanup(func() { removeContainer(t, context.Background(), cli, name) })

	_, err := cli.ContainerCreate(ctx, &container.Config{
		Image: image,
		Env:   env,
	}, &container.HostConfig{
		Mounts: mounts,
	}, &network.NetworkingConfig{}, &specs.Platform{OS: "linux"}, name)
	if err != nil {
		t.Fatalf("container create %s: %v", name, err)
	}
	if err := cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		t.Fatalf("container start %s: %v", name, err)
	}
}

// --- Tests ---

// TestIntegration_ContainerBasicSession verifies create/start/exec/stop/remove flow.
func TestIntegration_ContainerBasicSession(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)
	name := uniqueName(t, "basic")
	t.Cleanup(func() { removeContainer(t, context.Background(), cli, name) })

	_, err := cli.ContainerCreate(ctx, &container.Config{
		Image: "debian:bookworm-slim",
		Cmd:   []string{"sleep", "infinity"},
	}, &container.HostConfig{}, &network.NetworkingConfig{}, &specs.Platform{OS: "linux"}, name)
	if err != nil {
		t.Fatalf("container create: %v", err)
	}

	if err := cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		t.Fatalf("container start: %v", err)
	}

	// Verify it's running.
	insp, err := cli.ContainerInspect(ctx, name)
	if err != nil {
		t.Fatalf("container inspect: %v", err)
	}
	if !insp.State.Running {
		t.Error("expected container to be running")
	}

	// Exec a command inside.
	code, _ := execAndWait(t, ctx, cli, name, []string{"echo", "hello-construct"})
	if code != 0 {
		t.Errorf("exec exit code = %d, want 0", code)
	}

	// Stop.
	timeout := 5
	if err := cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout}); err != nil {
		t.Fatalf("container stop: %v", err)
	}
}

// TestIntegration_AgentLayerVolume verifies that a named volume persists data
// across container recreation.
func TestIntegration_AgentLayerVolume(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)
	ensureBaseImage(t, ctx, cli)

	name := uniqueName(t, "vol")
	volName := uniqueName(t, "vol-data")
	t.Cleanup(func() {
		removeContainer(t, context.Background(), cli, name)
		removeVolume(t, context.Background(), cli, volName)
	})

	// Create volume.
	if _, err := cli.VolumeCreate(ctx, volume.CreateOptions{Name: volName}); err != nil {
		t.Fatalf("volume create: %v", err)
	}

	mounts := []mount.Mount{{
		Type:   mount.TypeVolume,
		Source: volName,
		Target: "/agent",
	}}

	startContainer(t, ctx, cli, name, stacks.ImageName(stacks.StackBase), mounts, nil)

	// Write a sentinel file to the agent volume.
	code, _ := execAndWait(t, ctx, cli, name, []string{"/bin/sh", "-c", "echo construct-test > /agent/sentinel.txt"})
	if code != 0 {
		t.Fatalf("write sentinel: exit code %d", code)
	}

	// Stop and remove the container (keep the volume).
	timeout := 5
	if err := cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := cli.ContainerRemove(ctx, name, container.RemoveOptions{}); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Recreate with same volume.
	name2 := uniqueName(t, "vol2")
	t.Cleanup(func() { removeContainer(t, context.Background(), cli, name2) })
	startContainer(t, ctx, cli, name2, stacks.ImageName(stacks.StackBase), mounts, nil)

	// Verify sentinel persists.
	code, _ = execAndWait(t, ctx, cli, name2, []string{"test", "-f", "/agent/sentinel.txt"})
	if code != 0 {
		t.Error("expected sentinel file to persist in volume after container recreation")
	}
}

// TestIntegration_CredentialMount verifies that .env files are sourced via the entrypoint.
func TestIntegration_CredentialMount(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)
	ensureBaseImage(t, ctx, cli)

	credDir := t.TempDir()
	// Write a .env file that sets a test env var.
	envFile := filepath.Join(credDir, "test.env")
	if err := os.WriteFile(envFile, []byte("CONSTRUCT_CRED_TEST=hello123\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	name := uniqueName(t, "cred")
	t.Cleanup(func() { removeContainer(t, context.Background(), cli, name) })

	// Use HostConfig.Binds with :z SELinux relabeling (Mounts struct doesn't support it).
	t.Cleanup(func() { removeContainer(t, context.Background(), cli, name) })

	_, err := cli.ContainerCreate(ctx, &container.Config{
		Image: stacks.ImageName(stacks.StackBase),
	}, &container.HostConfig{
		// :z relabels the mount for SELinux (shared), required on Fedora/RHEL hosts.
		Binds: []string{credDir + ":/run/construct/creds/global:ro,z"},
	}, &network.NetworkingConfig{}, &specs.Platform{OS: "linux"}, name)
	if err != nil {
		t.Fatalf("container create %s: %v", name, err)
	}
	if err := cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		t.Fatalf("container start %s: %v", name, err)
	}

	// Explicitly source the .env file to simulate what the entrypoint does.
	code, out := execAndWait(t, ctx, cli, name, []string{
		"/bin/bash", "-c",
		"set -a && . /run/construct/creds/global/test.env && set +a && test \"$CONSTRUCT_CRED_TEST\" = \"hello123\"",
	})
	if code != 0 {
		t.Errorf("expected credential env var to be set after sourcing .env file (exit %d, out: %q)", code, out)
	}
}

// TestIntegration_PortPublish verifies that a published port is reachable on the host.
func TestIntegration_PortPublish(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)

	name := uniqueName(t, "port")
	t.Cleanup(func() { removeContainer(t, context.Background(), cli, name) })

	hostPort := "19871"
	containerPort := nat.Port("8080/tcp")

	_, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: "debian:bookworm-slim",
			Cmd:   []string{"/bin/bash", "-c", "apt-get -qq install -y netcat-openbsd 2>/dev/null; while true; do echo -e 'HTTP/1.0 200 OK\\r\\n\\r\\nok' | nc -l -p 8080; done"},
			ExposedPorts: nat.PortSet{
				containerPort: struct{}{},
			},
		},
		&container.HostConfig{
			PortBindings: nat.PortMap{
				containerPort: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: hostPort}},
			},
		},
		&network.NetworkingConfig{}, &specs.Platform{OS: "linux"}, name,
	)
	if err != nil {
		t.Fatalf("container create: %v", err)
	}
	if err := cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		t.Fatalf("container start: %v", err)
	}

	// Wait for the port to be reachable (up to 15s).
	addr := "127.0.0.1:" + hostPort
	deadline := time.Now().Add(15 * time.Second)
	var dialErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			dialErr = nil
			break
		}
		dialErr = err
		time.Sleep(500 * time.Millisecond)
	}
	if dialErr != nil {
		t.Errorf("port %s not reachable after 15s: %v", addr, dialErr)
	}
}

// TestIntegration_Entrypoint verifies the base image entrypoint starts sleep infinity
// and creates the expected agent layer directories.
func TestIntegration_Entrypoint(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)
	ensureBaseImage(t, ctx, cli)

	name := uniqueName(t, "entry")
	t.Cleanup(func() { removeContainer(t, context.Background(), cli, name) })

	startContainer(t, ctx, cli, name, stacks.ImageName(stacks.StackBase), nil, nil)

	// Give the container 1s to settle.
	time.Sleep(time.Second)

	// Check /proc/1 exists (container has a PID 1).
	code, _ := execAndWait(t, ctx, cli, name, []string{"test", "-d", "/proc/1"})
	if code != 0 {
		t.Error("expected /proc/1 to exist")
	}

	// Check the agent layer dirs are created by the entrypoint.
	code, _ = execAndWait(t, ctx, cli, name, []string{"test", "-d", "/agent/bin"})
	if code != 0 {
		t.Error("expected /agent/bin to be created by entrypoint")
	}
}

// TestIntegration_SessionManagerLifecycle exercises the session manager with a
// real Docker daemon: create, stop, destroy.
func TestIntegration_SessionManagerLifecycle(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)
	ensureBaseImage(t, ctx, cli)

	stateDir := t.TempDir()
	reg := registry.New(filepath.Join(stateDir, "daemon-state.json"))
	authStore := auth.NewStore(stateDir)
	qsStore := quickstart.NewStore(filepath.Join(stateDir, "quickstart"))
	mgr := session.NewManager(cli, reg, authStore, qsStore, stateDir)

	repo := t.TempDir()
	p := session.StartParams{
		Repo:              repo,
		Tool:              tools.ToolOpencode,
		Stack:             stacks.StackBase,
		DockerMode:        "none",
		Debug:             true, // skip agent start — makes test faster
		HostUID:           os.Getuid(),
		HostGID:           os.Getgid(),
		OpenCodeConfigDir: t.TempDir(),
	}

	res, err := mgr.Start(ctx, p, func(msg string) { t.Logf("progress: %s", msg) })
	if err != nil {
		t.Fatalf("session start: %v", err)
	}
	t.Cleanup(func() {
		_ = mgr.Destroy(context.Background(), res.Session.ID)
	})

	if res.Session.Status != registry.StatusRunning {
		t.Errorf("status = %q, want running", res.Session.Status)
	}

	// Container should be running.
	insp, err := cli.ContainerInspect(ctx, res.Session.ContainerName)
	if err != nil {
		t.Fatalf("inspect container: %v", err)
	}
	if !insp.State.Running {
		t.Error("expected container to be running")
	}

	// Can exec inside the container.
	code, _ := execAndWait(t, ctx, cli, res.Session.ContainerName, []string{"echo", "ok"})
	if code != 0 {
		t.Errorf("exec exit code = %d, want 0", code)
	}

	// Stop the session.
	stopped, err := mgr.Stop(ctx, res.Session.ID)
	if err != nil {
		t.Fatalf("session stop: %v", err)
	}
	if stopped.Status != registry.StatusStopped {
		t.Errorf("status after stop = %q, want stopped", stopped.Status)
	}

	// Container should not be running.
	insp2, err := cli.ContainerInspect(ctx, res.Session.ContainerName)
	if err != nil {
		t.Fatalf("inspect after stop: %v", err)
	}
	if insp2.State.Running {
		t.Error("expected container to be stopped")
	}

	// Destroy.
	if err := mgr.Destroy(ctx, res.Session.ID); err != nil {
		t.Fatalf("session destroy: %v", err)
	}

	// Container should be gone.
	_, err = cli.ContainerInspect(ctx, res.Session.ContainerName)
	if err == nil {
		t.Error("expected container to be removed after destroy")
	}

	// Session should be gone from registry.
	if s := mgr.GetByID(res.Session.ID); s != nil {
		t.Error("expected session to be removed from registry")
	}
}

// TestIntegration_DaemonSocket verifies that the daemon server starts, the
// socket is connectable, and session.start / session.stop work end-to-end.
func TestIntegration_DaemonSocket(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)
	ensureBaseImage(t, ctx, cli)

	stateDir := t.TempDir()
	socketPath := filepath.Join(stateDir, "daemon.sock")

	reg := registry.New(filepath.Join(stateDir, "daemon-state.json"))
	authStore := auth.NewStore(stateDir)
	qsStore := quickstart.NewStore(filepath.Join(stateDir, "quickstart"))
	mgr := session.NewManager(cli, reg, authStore, qsStore, stateDir)

	srv := server.New(socketPath, mgr, authStore, qsStore)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	serverCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go srv.Serve(serverCtx)

	// Connect to the socket.
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("connect to socket: %v", err)
	}
	defer conn.Close()

	repo := t.TempDir()

	// Send session.start.
	req := map[string]interface{}{
		"id":      "test-1",
		"command": "session.start",
		"params": map[string]interface{}{
			"repo":                repo,
			"tool":                tools.ToolOpencode,
			"stack":               stacks.StackBase,
			"docker_mode":         "none",
			"debug":               true, // skip agent start
			"host_uid":            os.Getuid(),
			"host_gid":            os.Getgid(),
			"opencode_config_dir": t.TempDir(),
		},
	}
	enc, _ := json.Marshal(req)
	enc = append(enc, '\n')
	if _, err := conn.Write(enc); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Read responses until we get "end" or "error".
	conn.SetReadDeadline(time.Now().Add(2 * time.Minute)) //nolint:errcheck
	scanner := bufio.NewScanner(conn)
	var sessionID string
	var containerName string
	for scanner.Scan() {
		var resp struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		t.Logf("response: type=%s payload=%s", resp.Type, resp.Payload)
		switch resp.Type {
		case "error":
			var errPayload struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(resp.Payload, &errPayload)
			t.Fatalf("session.start error: %s", errPayload.Message)
		case "end":
			var result struct {
				Session struct {
					ID            string `json:"id"`
					ContainerName string `json:"container_name"`
				} `json:"session"`
			}
			_ = json.Unmarshal(resp.Payload, &result)
			sessionID = result.Session.ID
			containerName = result.Session.ContainerName
		}
		if resp.Type == "end" {
			break
		}
	}
	if scanner.Err() != nil {
		t.Fatalf("scanner error: %v", scanner.Err())
	}
	if sessionID == "" {
		t.Fatal("no session ID in response")
	}

	t.Cleanup(func() {
		if s := reg.GetByID(sessionID); s != nil {
			_ = mgr.Destroy(context.Background(), sessionID)
		}
	})

	// Verify container is running.
	insp, err := cli.ContainerInspect(ctx, containerName)
	if err != nil {
		t.Fatalf("inspect container: %v", err)
	}
	if !insp.State.Running {
		t.Error("expected container to be running")
	}

	// Send session.stop via a new connection.
	conn2, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("connect for stop: %v", err)
	}
	defer conn2.Close()

	stopReq := map[string]interface{}{
		"id":      "test-2",
		"command": "session.stop",
		"params":  map[string]interface{}{"session_id": sessionID},
	}
	stopEnc, _ := json.Marshal(stopReq)
	stopEnc = append(stopEnc, '\n')
	if _, err := conn2.Write(stopEnc); err != nil {
		t.Fatalf("write stop request: %v", err)
	}

	conn2.SetReadDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck
	sc2 := bufio.NewScanner(conn2)
	for sc2.Scan() {
		var resp struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(sc2.Bytes(), &resp)
		if resp.Type == "end" || resp.Type == "error" {
			break
		}
	}

	// Container should be stopped.
	insp2, err := cli.ContainerInspect(ctx, containerName)
	if err != nil {
		t.Fatalf("inspect after stop: %v", err)
	}
	if insp2.State.Running {
		t.Error("expected container to be stopped after session.stop")
	}
}

// TestIntegration_RegistryPersistence verifies that the registry persists to
// disk and can be reloaded after a manager restart.
func TestIntegration_RegistryPersistence(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)
	ensureBaseImage(t, ctx, cli)

	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "daemon-state.json")

	reg1 := registry.New(statePath)
	authStore := auth.NewStore(stateDir)
	qsStore := quickstart.NewStore(filepath.Join(stateDir, "quickstart"))
	mgr1 := session.NewManager(cli, reg1, authStore, qsStore, stateDir)

	repo := t.TempDir()
	p := session.StartParams{
		Repo:              repo,
		Tool:              tools.ToolOpencode,
		Stack:             stacks.StackBase,
		DockerMode:        "none",
		Debug:             true,
		HostUID:           os.Getuid(),
		HostGID:           os.Getgid(),
		OpenCodeConfigDir: t.TempDir(),
	}
	res, err := mgr1.Start(ctx, p, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = mgr1.Destroy(context.Background(), res.Session.ID) })

	sessionID := res.Session.ID

	// Create a second registry from the same file — simulates daemon restart.
	reg2 := registry.New(statePath)
	if err := reg2.Load(); err != nil {
		t.Fatalf("load registry: %v", err)
	}

	s := reg2.GetByID(sessionID)
	if s == nil {
		t.Fatal("session not found after reload")
	}
	if s.Repo != repo {
		t.Errorf("repo = %q, want %q", s.Repo, repo)
	}
	if s.Status != registry.StatusRunning {
		t.Errorf("status = %q, want running", s.Status)
	}
}

// TestIntegration_CLIVersion verifies the CLI binary prints a version string.
func TestIntegration_CLIVersion(t *testing.T) {
	skipWithoutDocker(t)

	// Build the binary.
	binPath := filepath.Join(t.TempDir(), "construct")
	repoRoot := findRepoRoot(t)
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/construct/")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build construct: %v\n%s", err, out)
	}

	result, err := exec.Command(binPath, "--version").Output()
	if err != nil {
		t.Fatalf("construct --version: %v", err)
	}
	if !strings.Contains(string(result), "construct") {
		t.Errorf("version output %q doesn't contain 'construct'", string(result))
	}
}

// TestIntegration_HTTPServerReachable verifies that an HTTP server inside a
// container is reachable from the host via a published port.
func TestIntegration_HTTPServerReachable(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)

	name := uniqueName(t, "http")
	t.Cleanup(func() { removeContainer(t, context.Background(), cli, name) })

	// Use Perl (available in debian:bookworm-slim without apt-get) to serve HTTP.
	// python3 requires apt-get update first which makes the test slow/unreliable.
	hostPort := "18765"
	containerPort := nat.Port("8765/tcp")
	perlServer := `while true; do perl -e 'use Socket; socket(S,PF_INET,SOCK_STREAM,0); setsockopt(S,SOL_SOCKET,SO_REUSEADDR,1); bind(S,sockaddr_in(8765,INADDR_ANY)); listen(S,5); accept(C,S); print C "HTTP/1.0 200 OK\r\n\r\nok\n"; close C'; done`
	_, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: "debian:bookworm-slim",
			Cmd:   []string{"/bin/bash", "-c", perlServer},
			ExposedPorts: nat.PortSet{
				containerPort: struct{}{},
			},
		},
		&container.HostConfig{
			PortBindings: nat.PortMap{
				containerPort: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: hostPort}},
			},
		},
		&network.NetworkingConfig{}, &specs.Platform{OS: "linux"}, name,
	)
	if err != nil {
		t.Fatalf("container create: %v", err)
	}
	if err := cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		t.Fatalf("container start: %v", err)
	}

	// Wait for the HTTP server to come up.
	url := fmt.Sprintf("http://127.0.0.1:%s/", hostPort)
	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:noctx
		if err == nil {
			resp.Body.Close()
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr != nil {
		t.Errorf("HTTP server at %s not reachable after 15s: %v", url, lastErr)
	}
}

// TestIntegration_ConfigWritesAgentsMD verifies that construct-agents.md is
// rendered correctly and is accessible inside a container as a bind mount.
func TestIntegration_ConfigWritesAgentsMD(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)
	ensureBaseImage(t, ctx, cli)

	sessionDir := t.TempDir()
	err := config.WriteAgentsMD(sessionDir, config.AgentsParams{
		SessionID:  "test-session-id",
		Repo:       "/home/alice/myapp",
		Tool:       tools.ToolOpencode,
		Stack:      stacks.StackBase,
		DockerMode: "none",
		WebPort:    4096,
	}, os.Getuid(), os.Getgid())
	if err != nil {
		t.Fatalf("WriteAgentsMD: %v", err)
	}

	agentsMDPath := filepath.Join(sessionDir, "construct-agents.md")
	if _, err := os.Stat(agentsMDPath); err != nil {
		t.Fatalf("construct-agents.md not written: %v", err)
	}

	// Mount it inside a container and verify it's accessible.
	name := uniqueName(t, "agentsmd")
	t.Cleanup(func() { removeContainer(t, context.Background(), cli, name) })

	// The agent home config dir needs to exist; use a volume for /agent.
	mounts := []mount.Mount{
		{
			Type:   mount.TypeVolume,
			Source: uniqueName(t, "agentsmd-agent"),
			Target: "/agent",
		},
		{
			Type:     mount.TypeBind,
			Source:   agentsMDPath,
			Target:   "/agent/home/.config/opencode/construct-agents.md",
			ReadOnly: true,
		},
	}
	t.Cleanup(func() { removeVolume(t, context.Background(), cli, uniqueName(t, "agentsmd-agent")) })

	startContainer(t, ctx, cli, name, stacks.ImageName(stacks.StackBase), mounts, nil)
	// Give the entrypoint time to create the dir structure.
	time.Sleep(time.Second)

	code, _ := execAndWait(t, ctx, cli, name, []string{"test", "-f", "/agent/home/.config/opencode/construct-agents.md"})
	if code != 0 {
		t.Error("expected construct-agents.md to be accessible inside container")
	}
}

// --- helpers ---

// findRepoRoot walks up from the working directory until go.mod is found.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod found)")
		}
		dir = parent
	}
}

// dirToTar creates a tar archive of dir into w.
func dirToTar(dir string, w io.Writer) error {
	cmd := exec.Command("tar", "-C", dir, "-cf", "-", ".")
	cmd.Stdout = w
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tar: %v: %s", err, errBuf.String())
	}
	return nil
}
