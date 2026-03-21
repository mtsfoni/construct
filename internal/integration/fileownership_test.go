package integration

// TestIntegration_FileOwnership and TestIntegration_AgentVolumeWritableByHostUser
// are the make-or-break tests for R-LIFE-3 / R-SEC-3: files created by the
// agent process inside the container must be owned by the invoking host user
// (not root) when viewed from the host filesystem.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/construct-run/construct/internal/auth"
	dockeriface "github.com/construct-run/construct/internal/daemon/docker"
	"github.com/construct-run/construct/internal/daemon/registry"
	"github.com/construct-run/construct/internal/daemon/session"
	"github.com/construct-run/construct/internal/quickstart"
	"github.com/construct-run/construct/internal/stacks"
	"github.com/construct-run/construct/internal/tools"
)

// TestIntegration_FileOwnership is the make-or-break test for R-LIFE-3 / R-SEC-3.
//
// It verifies that files created by the agent process inside the container are
// owned by the invoking host user (not root) when viewed from the host
// filesystem. This is the core promise of the --user uid:gid fix.
//
// Three levels of assertion, each catching a different failure mode:
//
//  1. ContainerInspect Config.User == "uid:gid"
//     (catches: daemon silently dropped the User field)
//
//  2. exec "id -u" inside the container returns the host uid
//     (catches: Docker ignored the User field at runtime)
//
//  3. host-side stat of a file the container wrote into the agent volume
//     has the host uid as owner
//     (catches: volume permissions prevented the write, or uid mapping was
//     applied after the fact by some other mechanism)
func TestIntegration_FileOwnership(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)
	ensureBaseImage(t, ctx, cli)

	hostUID := os.Getuid()
	hostGID := os.Getgid()

	stateDir := t.TempDir()
	reg := registry.New(stateDir + "/daemon-state.json")
	authStore := auth.NewStore(stateDir)
	qsStore := quickstart.NewStore(stateDir + "/quickstart")
	mgr := session.NewManager(cli, reg, authStore, qsStore, stateDir)

	repo := t.TempDir()
	p := session.StartParams{
		Repo:              repo,
		Tool:              tools.ToolOpencode,
		Stack:             stacks.StackBase,
		DockerMode:        "none",
		Debug:             true, // skip agent start — we only need the container running
		HostUID:           hostUID,
		HostGID:           hostGID,
		OpenCodeConfigDir: t.TempDir(),
	}

	res, err := mgr.Start(ctx, p, func(msg string) { t.Logf("progress: %s", msg) })
	if err != nil {
		t.Fatalf("session start: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Destroy(context.Background(), res.Session.ID) })

	containerName := res.Session.ContainerName
	wantUser := fmt.Sprintf("%d:%d", hostUID, hostGID)

	// --- assertion 1: Config.User is "uid:gid" ---
	insp, err := cli.ContainerInspect(ctx, containerName)
	if err != nil {
		t.Fatalf("container inspect: %v", err)
	}
	if insp.Config.User != wantUser {
		t.Errorf("container Config.User = %q, want %q — daemon did not set --user uid:gid", insp.Config.User, wantUser)
	}

	// --- assertion 2: process inside container runs as host uid ---
	idCode, idOutput := execAndWait(t, ctx, cli, containerName, []string{"id", "-u"})
	if idCode != 0 {
		t.Fatalf("id -u failed with exit code %d", idCode)
	}
	gotUID := strings.TrimSpace(idOutput)
	wantUID := fmt.Sprintf("%d", hostUID)
	if gotUID != wantUID {
		t.Errorf("id -u inside container = %q, want %q — container process not running as host user", gotUID, wantUID)
	}

	// --- assertion 3: files written into the agent volume are owned by the host uid ---
	//
	// Write a file to /agent (the named volume) from inside the container,
	// then stat it on the host via the volume's Mountpoint.
	code, _ := execAndWait(t, ctx, cli, containerName, []string{
		"/bin/sh", "-c", "touch /agent/ownership-test.txt",
	})
	if code != 0 {
		t.Fatalf("touch /agent/ownership-test.txt: exit code %d — container user cannot write to /agent (missing chmod 0777 in Dockerfile?)", code)
	}

	// Resolve the volume Mountpoint using the raw Docker SDK client.
	// The dockeriface.Client interface does not expose VolumeList, so we
	// open a second raw client here — it is read-only metadata and does not
	// affect the test container.
	rawCli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("raw docker client: %v", err)
	}
	defer rawCli.Close()

	shortID := res.Session.ID[:8]
	volName := fmt.Sprintf("construct-layer-%s", shortID)

	volInsp, err := rawCli.VolumeInspect(ctx, volName)
	if err != nil {
		t.Fatalf("volume inspect %q: %v", volName, err)
	}
	hostFilePath := volInsp.Mountpoint + "/ownership-test.txt"

	fi, err := os.Stat(hostFilePath)
	if err != nil {
		t.Fatalf("stat %s on host: %v — cannot verify file ownership from host", hostFilePath, err)
	}
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("os.Stat did not return *syscall.Stat_t — cannot verify file ownership")
	}
	if int(stat.Uid) != hostUID {
		t.Errorf("host-side file uid = %d, want %d — files created by agent are NOT owned by the invoking user (root-owned?)", stat.Uid, hostUID)
	}
	if int(stat.Gid) != hostGID {
		t.Errorf("host-side file gid = %d, want %d — files created by agent have wrong group on host", stat.Gid, hostGID)
	}
}

// TestIntegration_AgentVolumeWritableByHostUser verifies that a container
// running as a non-root host user can write to the /agent volume. If the base
// Dockerfile did not set chmod 0777 on /agent before the volume is first
// mounted, the named volume's root directory is owned by root and the
// non-root --user process gets EACCES when trying to write.
func TestIntegration_AgentVolumeWritableByHostUser(t *testing.T) {
	skipWithoutDocker(t)
	ctx := context.Background()
	cli := newDockerClient(t)
	ensureBaseImage(t, ctx, cli)

	hostUID := os.Getuid()
	hostGID := os.Getgid()

	// Skip if running as root — root can always write, so this test only
	// has meaning for non-root users.
	if hostUID == 0 {
		t.Skip("running as root; non-root write check skipped")
	}

	volName := uniqueName(t, "writable-vol")
	t.Cleanup(func() { removeVolume(t, context.Background(), cli, volName) })
	if _, err := cli.VolumeCreate(ctx, volume.CreateOptions{Name: volName}); err != nil {
		t.Fatalf("volume create: %v", err)
	}

	name := uniqueName(t, "writable")

	mounts := []mount.Mount{{Type: mount.TypeVolume, Source: volName, Target: "/agent"}}
	userStr := fmt.Sprintf("%d:%d", hostUID, hostGID)
	startContainerWithUser(t, ctx, cli, name, stacks.ImageName(stacks.StackBase), mounts, userStr)

	// Non-root user must be able to write to /agent.
	code, output := execAndWait(t, ctx, cli, name, []string{
		"/bin/sh", "-c", "touch /agent/write-test.txt && echo ok",
	})
	if code != 0 {
		t.Errorf(
			"non-root user %d cannot write to /agent: exit code %d, output: %q — "+
				"base Dockerfile is missing 'chmod 0777 /agent'",
			hostUID, code, output,
		)
	}
}

// startContainerWithUser creates and starts a container using a specific
// User (uid:gid string), registering cleanup. It relies on the image's CMD.
func startContainerWithUser(t *testing.T, ctx context.Context, cli *dockeriface.RealClient, name, image string, mounts []mount.Mount, user string) {
	t.Helper()
	t.Cleanup(func() { removeContainer(t, context.Background(), cli, name) })

	_, err := cli.ContainerCreate(ctx,
		&container.Config{Image: image, User: user},
		&container.HostConfig{Mounts: mounts},
		&network.NetworkingConfig{},
		&specs.Platform{OS: "linux"},
		name,
	)
	if err != nil {
		t.Fatalf("container create %s: %v", name, err)
	}
	if err := cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		t.Fatalf("container start %s: %v", name, err)
	}
}
