package session

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/construct-run/construct/internal/auth"
	"github.com/construct-run/construct/internal/daemon/registry"
	"github.com/construct-run/construct/internal/quickstart"
	"github.com/construct-run/construct/internal/stacks"
	"github.com/construct-run/construct/internal/tools"
)

// --- fake Docker client ---

type fakeDocker struct {
	// containers maps name→started (true=running, false=created/stopped)
	containers map[string]bool
	// volumes maps name→created
	volumes map[string]bool
	// networks maps name→created
	networks map[string]bool
	// execResults maps execID→exit code
	execResults map[string]int
	// imageExists controls whether ImageInspectWithRaw returns an image
	imageExists bool
	// imageSpecLabel overrides the io.construct.image-spec label returned by
	// ImageInspectWithRaw when imageExists is true. When empty (the default),
	// the fake auto-derives the correct label for the image's stack so that
	// tests not concerned with staleness see an up-to-date image. Set this
	// to a fixed string (e.g. "embedded:stalevalue") to simulate a stale image.
	imageSpecLabel string
	// errors maps method name→error to return
	errors map[string]error
	// calls records the sequence of method names called
	calls []string
	// execCounter for generating unique exec IDs
	execCounter int
	// lastContainerConfig is the most recent container.Config passed to ContainerCreate
	lastContainerConfig *container.Config
	// lastExecOptions is the most recent ExecOptions passed to ContainerExecCreate
	lastExecOptions *container.ExecOptions
	// allExecOptions is all ExecOptions passed to ContainerExecCreate
	allExecOptions []container.ExecOptions
	// containerInspectRunning controls what ContainerInspect reports for State.Running.
	// When nil (default), it reports the container as running iff containers[name]==true.
	// Set to a non-nil *bool to override for all containers.
	containerInspectRunning *bool
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{
		containers:  make(map[string]bool),
		volumes:     make(map[string]bool),
		networks:    make(map[string]bool),
		execResults: make(map[string]int),
		imageExists: true, // default: image already present
		errors:      make(map[string]error),
	}
}

func (f *fakeDocker) record(method string) { f.calls = append(f.calls, method) }

func (f *fakeDocker) ContainerCreate(_ context.Context, cfg *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *specs.Platform, name string) (container.CreateResponse, error) {
	f.record("ContainerCreate")
	if err := f.errors["ContainerCreate"]; err != nil {
		return container.CreateResponse{}, err
	}
	f.lastContainerConfig = cfg
	f.containers[name] = false // created but not started
	return container.CreateResponse{ID: name}, nil
}

func (f *fakeDocker) ContainerStart(_ context.Context, containerID string, _ container.StartOptions) error {
	f.record("ContainerStart")
	if err := f.errors["ContainerStart"]; err != nil {
		return err
	}
	f.containers[containerID] = true
	return nil
}

func (f *fakeDocker) ContainerStop(_ context.Context, containerID string, _ container.StopOptions) error {
	f.record("ContainerStop")
	if err := f.errors["ContainerStop"]; err != nil {
		return err
	}
	f.containers[containerID] = false
	return nil
}

func (f *fakeDocker) ContainerRemove(_ context.Context, containerID string, _ container.RemoveOptions) error {
	f.record("ContainerRemove")
	delete(f.containers, containerID)
	return nil
}

func (f *fakeDocker) ContainerInspect(_ context.Context, name string) (types.ContainerJSON, error) {
	f.record("ContainerInspect")
	if err := f.errors["ContainerInspect"]; err != nil {
		return types.ContainerJSON{}, err
	}
	running := f.containers[name] // default: mirrors ContainerStart/Stop state
	if f.containerInspectRunning != nil {
		running = *f.containerInspectRunning
	}
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			State: &types.ContainerState{Running: running},
		},
	}, nil
}

func (f *fakeDocker) ContainerExecCreate(_ context.Context, _ string, opts container.ExecOptions) (types.IDResponse, error) {
	f.record("ContainerExecCreate")
	if err := f.errors["ContainerExecCreate"]; err != nil {
		return types.IDResponse{}, err
	}
	f.execCounter++
	id := "exec-" + string(rune('0'+f.execCounter))
	f.lastExecOptions = &opts
	f.allExecOptions = append(f.allExecOptions, opts)
	return types.IDResponse{ID: id}, nil
}

func (f *fakeDocker) ContainerExecStart(_ context.Context, _ string, _ container.ExecStartOptions) error {
	f.record("ContainerExecStart")
	return nil
}

func (f *fakeDocker) ContainerExecAttach(_ context.Context, _ string, _ container.ExecAttachOptions) (types.HijackedResponse, error) {
	f.record("ContainerExecAttach")
	// Use net.Pipe to get a real net.Conn pair; close the server side so
	// the reader immediately sees EOF and streamOutput terminates quickly.
	serverConn, clientConn := net.Pipe()
	serverConn.Close()
	return types.NewHijackedResponse(clientConn, ""), nil
}

func (f *fakeDocker) ContainerExecInspect(_ context.Context, execID string) (container.ExecInspect, error) {
	f.record("ContainerExecInspect")
	exit, ok := f.execResults[execID]
	if !ok {
		// Default: success (exit 0). This makes the tool check pass ("already installed")
		// and any install exec also pass.
		exit = 0
	}
	return container.ExecInspect{
		Running:  false,
		ExitCode: exit,
		Pid:      42,
	}, nil
}

func (f *fakeDocker) ContainerList(_ context.Context, _ container.ListOptions) ([]types.Container, error) {
	return nil, nil
}

func (f *fakeDocker) ContainerLogs(_ context.Context, _ string, _ container.LogsOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeDocker) ContainerKill(_ context.Context, _ string, _ string) error {
	return nil
}

func (f *fakeDocker) ImageBuild(_ context.Context, _ io.Reader, _ types.ImageBuildOptions) (types.ImageBuildResponse, error) {
	f.record("ImageBuild")
	return types.ImageBuildResponse{Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (f *fakeDocker) ImageInspectWithRaw(_ context.Context, imageName string) (types.ImageInspect, []byte, error) {
	f.record("ImageInspectWithRaw")
	if !f.imageExists {
		return types.ImageInspect{}, nil, &fakeNotFoundError{}
	}
	var specLabel string
	if f.imageSpecLabel != "" {
		// Explicit override — use as-is (including empty-string sentinel via
		// imageSpecLabel = " " trick; for tests that want no label use a
		// different mechanism). Empty imageSpecLabel means "derive from image".
		specLabel = f.imageSpecLabel
	} else {
		// Auto-derive the correct spec label so tests that don't care about
		// staleness behave as though the image is up to date.
		stackName := strings.TrimPrefix(imageName, "construct-stack-")
		stackName = strings.TrimSuffix(stackName, ":latest")
		if spec, err := stacks.SpecFor(stackName); err == nil {
			specLabel = spec.Label()
		}
	}
	labels := map[string]string{}
	if specLabel != "" {
		labels[stacks.ImageSpecLabel] = specLabel
	}
	cfg := &dockerspec.DockerOCIImageConfig{
		ImageConfig: ocispec.ImageConfig{Labels: labels},
	}
	return types.ImageInspect{Config: cfg}, nil, nil
}

func (f *fakeDocker) ImageList(_ context.Context, _ image.ListOptions) ([]image.Summary, error) {
	return nil, nil
}

func (f *fakeDocker) ImageRemove(_ context.Context, imageID string, _ image.RemoveOptions) ([]image.DeleteResponse, error) {
	f.record("ImageRemove")
	return nil, nil
}

func (f *fakeDocker) NetworkCreate(_ context.Context, name string, _ network.CreateOptions) (network.CreateResponse, error) {
	f.record("NetworkCreate")
	f.networks[name] = true
	return network.CreateResponse{ID: name}, nil
}

func (f *fakeDocker) NetworkRemove(_ context.Context, networkID string) error {
	f.record("NetworkRemove")
	delete(f.networks, networkID)
	return nil
}

func (f *fakeDocker) NetworkList(_ context.Context, _ network.ListOptions) ([]network.Summary, error) {
	return nil, nil
}

func (f *fakeDocker) VolumeCreate(_ context.Context, opts volume.CreateOptions) (volume.Volume, error) {
	f.record("VolumeCreate")
	if err := f.errors["VolumeCreate"]; err != nil {
		return volume.Volume{}, err
	}
	f.volumes[opts.Name] = true
	return volume.Volume{Name: opts.Name}, nil
}

func (f *fakeDocker) VolumeRemove(_ context.Context, volumeID string, _ bool) error {
	f.record("VolumeRemove")
	delete(f.volumes, volumeID)
	return nil
}

func (f *fakeDocker) ServerVersion(_ context.Context) (types.Version, error) {
	return types.Version{Version: "28.0.0"}, nil
}

func (f *fakeDocker) Ping(_ context.Context) (types.Ping, error) {
	return types.Ping{}, nil
}

func (f *fakeDocker) Close() error { return nil }

// fakeNotFoundError implements the error interface and satisfies the Docker SDK's
// IsNotFound check via the message.
type fakeNotFoundError struct{}

func (e *fakeNotFoundError) Error() string    { return "No such image" }
func (e *fakeNotFoundError) NotFound() bool   { return true }
func (e *fakeNotFoundError) IsNotFound() bool { return true }

// --- helpers ---

func newTestManager(t *testing.T, fd *fakeDocker) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()

	reg := registry.New(filepath.Join(dir, "daemon-state.json"))
	authStore := auth.NewStore(dir)
	qsStore := quickstart.NewStore(filepath.Join(dir, "quickstart"))
	m := NewManager(fd, reg, authStore, qsStore, dir)
	return m, dir
}

func defaultParams(repo string) StartParams {
	return StartParams{
		Repo:              repo,
		Tool:              tools.ToolOpencode,
		Stack:             stacks.StackBase,
		DockerMode:        "none",
		HostUID:           1000,
		HostGID:           1000,
		OpenCodeConfigDir: "/home/alice/.config/opencode",
	}
}

// --- tests ---

func TestSession_Start_CreatesNewSession(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir // use the temp dir as repo path

	p := defaultParams(repo)
	res, err := m.Start(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.Session == nil {
		t.Fatal("expected session, got nil")
	}
	if res.Session.Status != registry.StatusRunning {
		t.Errorf("status = %q, want %q", res.Session.Status, registry.StatusRunning)
	}
	if res.Session.Repo != repo {
		t.Errorf("repo = %q, want %q", res.Session.Repo, repo)
	}
	if res.Session.Tool != tools.ToolOpencode {
		t.Errorf("tool = %q, want %q", res.Session.Tool, tools.ToolOpencode)
	}
	// Container should be created and started
	if !fd.containers[res.Session.ContainerName] {
		t.Error("expected container to be started")
	}
	// Volume should be created
	shortID := res.Session.ShortID()
	volName := "construct-layer-" + shortID
	if !fd.volumes[volName] {
		t.Error("expected volume to be created")
	}
	// Web URL should be present
	if res.WebURL == "" {
		t.Error("expected web URL, got empty string")
	}
}

// TestSession_Start_AttachesToRunning verifies that a second Start for the same
// folder while the container is live returns the same session without creating
// anything new.
func TestSession_Start_AttachesToRunning(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	p := defaultParams(repo)

	// First start
	res1, err := m.Start(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	// Second start (same folder)
	fd.calls = nil // reset call trace
	res2, err := m.Start(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}

	if res2.Session.ID != res1.Session.ID {
		t.Errorf("expected same session ID %q, got %q", res1.Session.ID, res2.Session.ID)
	}
	// No new container should have been created
	for _, call := range fd.calls {
		if call == "ContainerCreate" {
			t.Errorf("unexpected Docker call %q on attach", call)
		}
	}
}

func TestSession_Start_FlagConflictWarning(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	// Start with base stack
	p1 := defaultParams(repo)
	p1.Stack = stacks.StackBase
	if _, err := m.Start(context.Background(), p1, nil); err != nil {
		t.Fatalf("first Start: %v", err)
	}

	// Re-start with different stack — should signal settings conflict
	p2 := defaultParams(repo)
	p2.Stack = stacks.StackNode
	res, err := m.Start(context.Background(), p2, nil)
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if !res.SettingsConflict {
		t.Error("expected SettingsConflict=true for conflicting stack, got false")
	}
	// Stack should not have changed
	if res.Session.Stack != stacks.StackBase {
		t.Errorf("stack = %q, want %q (should be unchanged)", res.Session.Stack, stacks.StackBase)
	}
}

func TestSession_Start_RestartsStopped(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	p := defaultParams(repo)

	// Create session
	res1, err := m.Start(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Stop it
	if _, err := m.Stop(context.Background(), res1.Session.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	s := m.GetByID(res1.Session.ID)
	if s.Status != registry.StatusStopped {
		t.Errorf("status after stop = %q, want stopped", s.Status)
	}

	// Restart via Start
	res2, err := m.Start(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	if res2.Session.Status != registry.StatusRunning {
		t.Errorf("status after restart = %q, want running", res2.Session.Status)
	}
}

func TestSession_Stop(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	res, err := m.Start(context.Background(), defaultParams(repo), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	stopped, err := m.Stop(context.Background(), res.Session.ID)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stopped.Status != registry.StatusStopped {
		t.Errorf("status = %q, want stopped", stopped.Status)
	}
	if stopped.StoppedAt == nil {
		t.Error("expected StoppedAt to be set")
	}
}

func TestSession_Stop_AlreadyStopped(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	res, err := m.Start(context.Background(), defaultParams(repo), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := m.Stop(context.Background(), res.Session.ID); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	// Second stop should succeed idempotently
	if _, err := m.Stop(context.Background(), res.Session.ID); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestSession_Destroy(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	res, err := m.Start(context.Background(), defaultParams(repo), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	id := res.Session.ID
	shortID := res.Session.ShortID()
	volName := "construct-layer-" + shortID

	if err := m.Destroy(context.Background(), id); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Session should be gone from registry
	if s := m.GetByID(id); s != nil {
		t.Error("expected session to be removed from registry")
	}
	// Volume should be removed
	if fd.volumes[volName] {
		t.Error("expected volume to be removed")
	}
	// Container should be removed
	if _, ok := fd.containers[res.Session.ContainerName]; ok {
		t.Error("expected container to be removed")
	}
}

func TestSession_NotFound(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	_ = dir

	_, err := m.Stop(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for unknown session ID")
	}

	err = m.Destroy(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for unknown session ID")
	}
}

func TestSession_RestartUpdatesUIDGID(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	// Start with UID 1000.
	p := defaultParams(repo)
	p.HostUID = 1000
	p.HostGID = 1000
	res, err := m.Start(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	id := res.Session.ID
	containerName := res.Session.ContainerName

	// Stop the session.
	if _, err := m.Stop(context.Background(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Restart with a different UID.
	p2 := defaultParams(repo)
	p2.HostUID = 2000
	p2.HostGID = 2000
	res2, err := m.Start(context.Background(), p2, nil)
	if err != nil {
		t.Fatalf("Start (restart with new UID): %v", err)
	}

	// Registry record should reflect the new UID/GID.
	if res2.Session.HostUID != 2000 {
		t.Errorf("HostUID = %d, want 2000", res2.Session.HostUID)
	}
	if res2.Session.HostGID != 2000 {
		t.Errorf("HostGID = %d, want 2000", res2.Session.HostGID)
	}

	// The container was removed and recreated; it must run as root (no User
	// field) and pass the new UID/GID via env vars.
	if fd.lastContainerConfig == nil {
		t.Fatal("expected ContainerCreate to have been called")
	}
	if fd.lastContainerConfig.User != "" {
		t.Errorf("container User = %q, want empty (root)", fd.lastContainerConfig.User)
	}
	var hasUID, hasGID bool
	for _, e := range fd.lastContainerConfig.Env {
		if e == "CONSTRUCT_UID=2000" {
			hasUID = true
		}
		if e == "CONSTRUCT_GID=2000" {
			hasGID = true
		}
	}
	if !hasUID {
		t.Error("env missing CONSTRUCT_UID=2000")
	}
	if !hasGID {
		t.Error("env missing CONSTRUCT_GID=2000")
	}

	// Container should be running.
	if !fd.containers[containerName] {
		t.Error("expected container to be running after restart")
	}
}

func TestSession_ParsePortSpecs(t *testing.T) {
	tests := []struct {
		name        string
		specs       []string
		webPort     int
		wantLen     int
		wantHostWeb int
	}{
		{
			name:        "empty_specs",
			specs:       nil,
			webPort:     4096,
			wantLen:     1, // just the auto-added web port
			wantHostWeb: 4096,
		},
		{
			name:        "explicit_web_port",
			specs:       []string{"4096:4096"},
			webPort:     4096,
			wantLen:     1,
			wantHostWeb: 4096,
		},
		{
			name:        "extra_port_plus_web",
			specs:       []string{"3000:3000"},
			webPort:     4097,
			wantLen:     2,
			wantHostWeb: 4097,
		},
		{
			name:        "host_port_auto",
			specs:       []string{"9000"},
			webPort:     4096,
			wantLen:     2, // 9000 + auto web
			wantHostWeb: 4096,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mappings, err := parsePortSpecs(tt.specs, tt.webPort)
			if err != nil {
				t.Fatalf("parsePortSpecs: %v", err)
			}
			if len(mappings) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(mappings), tt.wantLen)
			}
			// Find the web port mapping
			found := false
			for _, m := range mappings {
				if m.ContainerPort == tools.WebPort {
					if m.HostPort != tt.wantHostWeb {
						t.Errorf("web host port = %d, want %d", m.HostPort, tt.wantHostWeb)
					}
					found = true
				}
			}
			if !found {
				t.Errorf("web port %d not found in mappings", tools.WebPort)
			}
		})
	}
}

func TestSession_ParsePortSpecs_Invalid(t *testing.T) {
	_, err := parsePortSpecs([]string{"notaport"}, 4096)
	if err == nil {
		t.Error("expected error for invalid port spec")
	}
}

func TestSession_WebURL(t *testing.T) {
	s := &registry.Session{
		Tool:    tools.ToolOpencode,
		WebPort: 4096,
		Status:  registry.StatusRunning,
	}
	got := webURL(s)
	if got != "http://localhost:4096" {
		t.Errorf("webURL = %q, want %q", got, "http://localhost:4096")
	}

	s.Status = registry.StatusStopped
	got = webURL(s)
	if got != "" {
		t.Errorf("webURL stopped = %q, want empty", got)
	}
}

func TestSession_List(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)

	if len(m.List()) != 0 {
		t.Error("expected empty list initially")
	}

	repo := dir
	if _, err := m.Start(context.Background(), defaultParams(repo), nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if len(m.List()) != 1 {
		t.Errorf("expected 1 session, got %d", len(m.List()))
	}
}

func TestSession_Progress(t *testing.T) {
	fd := newFakeDocker()
	fd.imageExists = false // force image build
	m, dir := newTestManager(t, fd)
	repo := dir

	var messages []string
	progress := func(msg string) {
		messages = append(messages, msg)
	}

	if _, err := m.Start(context.Background(), defaultParams(repo), progress); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if len(messages) == 0 {
		t.Error("expected progress messages, got none")
	}
}

func TestSession_BuildEnv(t *testing.T) {
	s := &registry.Session{
		ID:                "abc-123",
		Repo:              "/home/alice/src/myapp",
		Tool:              "opencode",
		Stack:             "base",
		DockerMode:        "none",
		WebPort:           4096,
		OpenCodeConfigDir: "/home/alice/.config/opencode",
		Ports: []registry.PortMapping{
			{HostPort: 3000, ContainerPort: 3000},
			{HostPort: 4096, ContainerPort: 4096},
		},
	}
	env := buildEnv(s, "abc-123")
	has := func(key, val string) bool {
		want := key + "=" + val
		for _, e := range env {
			if e == want {
				return true
			}
		}
		return false
	}
	checks := [][2]string{
		{"HOME", "/agent/home"},
		{"XDG_CONFIG_HOME", "/agent/home/.config"},
		{"CONSTRUCT_SESSION_ID", s.ID},
		{"CONSTRUCT_REPO", s.Repo},
		{"CONSTRUCT_TOOL", s.Tool},
		{"CONSTRUCT_STACK", s.Stack},
		{"CONSTRUCT_DOCKER_MODE", s.DockerMode},
		{"CONSTRUCT_UID", "0"},
		{"CONSTRUCT_GID", "0"},
	}
	for _, c := range checks {
		if !has(c[0], c[1]) {
			t.Errorf("env missing %s=%s", c[0], c[1])
		}
	}
}

func TestSession_BuildEnv_DindMode(t *testing.T) {
	s := &registry.Session{
		ID:         "abcd1234",
		DockerMode: "dind",
		Ports:      nil,
	}
	env := buildEnv(s, "abcd1234")
	found := false
	for _, e := range env {
		if e == "DOCKER_HOST=tcp://construct-dind-abcd1234:2375" {
			found = true
		}
	}
	if !found {
		t.Error("expected DOCKER_HOST env var for dind mode")
	}
}

func TestSession_BuildEnv_NoDindMode(t *testing.T) {
	s := &registry.Session{
		ID:         "abcd1234",
		DockerMode: "none",
		Ports:      nil,
	}
	env := buildEnv(s, "abcd1234")
	for _, e := range env {
		if strings.HasPrefix(e, "DOCKER_HOST=") {
			t.Errorf("unexpected DOCKER_HOST in none mode: %q", e)
		}
	}
}

func TestSession_DefaultsApplied(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	// Pass empty tool/stack/docker — should get defaults
	p := StartParams{
		Repo:              repo,
		HostUID:           1000,
		HostGID:           1000,
		OpenCodeConfigDir: "/home/alice/.config/opencode",
	}
	res, err := m.Start(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.Session.Tool != tools.DefaultTool {
		t.Errorf("tool = %q, want %q", res.Session.Tool, tools.DefaultTool)
	}
	if res.Session.Stack != stacks.StackBase {
		t.Errorf("stack = %q, want %q", res.Session.Stack, stacks.StackBase)
	}
	if res.Session.DockerMode != "none" {
		t.Errorf("docker_mode = %q, want none", res.Session.DockerMode)
	}
}

func TestSession_ImageBuildSkippedWhenPresent(t *testing.T) {
	fd := newFakeDocker()
	fd.imageExists = true
	m, dir := newTestManager(t, fd)

	if _, err := m.Start(context.Background(), defaultParams(dir), nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	for _, call := range fd.calls {
		if call == "ImageBuild" {
			t.Error("expected no ImageBuild when image already present")
		}
	}
}

func TestSession_ImageBuildCalledWhenMissing(t *testing.T) {
	fd := newFakeDocker()
	fd.imageExists = false
	m, dir := newTestManager(t, fd)

	if _, err := m.Start(context.Background(), defaultParams(dir), nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	built := false
	for _, call := range fd.calls {
		if call == "ImageBuild" {
			built = true
		}
	}
	if !built {
		t.Error("expected ImageBuild when image missing")
	}
}

// TestSession_ImageBuildWhenStale verifies that when the image exists but its
// io.construct.image-spec label does not match the current spec, ensureStackImage
// removes the stale image and rebuilds it.
func TestSession_ImageBuildWhenStale(t *testing.T) {
	fd := newFakeDocker()
	fd.imageExists = true
	fd.imageSpecLabel = "embedded:000000000000stale" // outdated label
	m, dir := newTestManager(t, fd)

	if _, err := m.Start(context.Background(), defaultParams(dir), nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var removed, built bool
	for _, call := range fd.calls {
		if call == "ImageRemove" {
			removed = true
		}
		if call == "ImageBuild" {
			built = true
		}
	}
	if !removed {
		t.Error("expected ImageRemove for stale image")
	}
	if !built {
		t.Error("expected ImageBuild after removing stale image")
	}
}

// TestSession_StaleImageOnRestart verifies that when a stopped session is
// restarted and the stack image is stale, the image is rebuilt and the
// container is recreated — but the agent layer volume is preserved.
func TestSession_StaleImageOnRestart(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	// Create and stop a session.
	res, err := m.Start(context.Background(), defaultParams(repo), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	shortID := res.Session.ShortID()
	volName := "construct-layer-" + shortID

	if _, err := m.Stop(context.Background(), res.Session.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Simulate a stale image by setting an outdated label.
	fd.imageSpecLabel = "embedded:000000000000stale"
	fd.calls = nil

	// Restart — should detect stale image, rebuild, and recreate container.
	res2, err := m.Start(context.Background(), defaultParams(repo), nil)
	if err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	if res2.Session.Status != registry.StatusRunning {
		t.Errorf("status = %q, want running", res2.Session.Status)
	}

	var removed, built, created bool
	var volumeRemoved bool
	for _, call := range fd.calls {
		switch call {
		case "ImageRemove":
			removed = true
		case "ImageBuild":
			built = true
		case "ContainerCreate":
			created = true
		case "VolumeRemove":
			volumeRemoved = true
		}
	}
	if !removed {
		t.Error("expected ImageRemove for stale image")
	}
	if !built {
		t.Error("expected ImageBuild after stale image detected")
	}
	if !created {
		t.Error("expected ContainerCreate to recreate container with new image")
	}
	if volumeRemoved {
		t.Error("expected volume to be preserved (not removed) during image update")
	}
	// Volume must still exist.
	if !fd.volumes[volName] {
		t.Error("expected agent layer volume to survive image update")
	}
}

func TestSession_DindMode(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	p := defaultParams(repo)
	p.DockerMode = "dind"

	res, err := m.Start(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	shortID := res.Session.ShortID()
	dindName := "construct-dind-" + shortID
	netName := "construct-net-" + shortID

	if !fd.containers[dindName] {
		t.Error("expected dind container to be started")
	}
	if !fd.networks[netName] {
		t.Error("expected session network to be created")
	}
}

func TestSession_DindDestroy_CleansUpNetwork(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	p := defaultParams(repo)
	p.DockerMode = "dind"

	res, err := m.Start(context.Background(), p, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	shortID := res.Session.ShortID()
	netName := "construct-net-" + shortID

	if err := m.Destroy(context.Background(), res.Session.ID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if fd.networks[netName] {
		t.Error("expected network to be removed after destroy")
	}
}

func TestSession_GetByPrefix(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)

	res, err := m.Start(context.Background(), defaultParams(dir), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	prefix := res.Session.ID[:6]
	found, err := m.GetByPrefix(prefix)
	if err != nil {
		t.Fatalf("GetByPrefix: %v", err)
	}
	if found.ID != res.Session.ID {
		t.Errorf("found ID %q, want %q", found.ID, res.Session.ID)
	}
}

func TestSession_LogBuffer(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)

	res, err := m.Start(context.Background(), defaultParams(dir), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	buf := m.LogBuffer(res.Session.ID)
	if buf == nil {
		t.Error("expected log buffer to be created for session")
	}
}

func TestSession_FormatPorts(t *testing.T) {
	ports := []registry.PortMapping{
		{HostPort: 3000, ContainerPort: 3000},
		{HostPort: 4096, ContainerPort: 4096},
	}
	got := formatPorts(ports)
	if !strings.Contains(got, "3000:3000") {
		t.Errorf("formatPorts missing 3000:3000, got %q", got)
	}
	if !strings.Contains(got, "4096:4096") {
		t.Errorf("formatPorts missing 4096:4096, got %q", got)
	}
}

// TestSession_StateTransitions verifies the state machine.
func TestSession_StateTransitions(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)

	// created → running
	res, err := m.Start(context.Background(), defaultParams(dir), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.Session.Status != registry.StatusRunning {
		t.Fatalf("expected running after start, got %q", res.Session.Status)
	}

	// running → stopped
	stopped, err := m.Stop(context.Background(), res.Session.ID)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stopped.Status != registry.StatusStopped {
		t.Fatalf("expected stopped after stop, got %q", stopped.Status)
	}

	// stopped → running (via start)
	restarted, err := m.Start(context.Background(), defaultParams(dir), nil)
	if err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	if restarted.Session.Status != registry.StatusRunning {
		t.Fatalf("expected running after restart, got %q", restarted.Session.Status)
	}

	// running → destroyed
	if err := m.Destroy(context.Background(), restarted.Session.ID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if m.GetByID(restarted.Session.ID) != nil {
		t.Error("expected session to be removed from registry after destroy")
	}
}

// TestSession_CreatedAt verifies that CreatedAt is set.
func TestSession_CreatedAt(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)

	before := time.Now().UTC().Add(-time.Second)
	res, err := m.Start(context.Background(), defaultParams(dir), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	if res.Session.CreatedAt.Before(before) || res.Session.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v not in expected range", res.Session.CreatedAt)
	}
}

// TestSession_SavesQuickstart verifies quickstart is saved after start.
func TestSession_SavesQuickstart(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	if _, err := m.Start(context.Background(), defaultParams(repo), nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Quickstart file should exist
	qsDir := filepath.Join(dir, "quickstart")
	entries, err := os.ReadDir(qsDir)
	if err != nil {
		t.Fatalf("ReadDir quickstart: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected quickstart record to be saved")
	}
}

// TestSession_ContainerUserIsHostUID verifies that the container runs as root
// (so the entrypoint can register the UID in /etc/passwd for sudo), that
// CONSTRUCT_UID/CONSTRUCT_GID env vars carry the host UID/GID into the
// container, and that the agent exec is launched as the host user.
func TestSession_ContainerUserIsHostUID(t *testing.T) {
	tests := []struct {
		name string
		uid  int
		gid  int
	}{
		{"typical user", 1000, 1000},
		{"root", 0, 0},
		{"uid gid differ", 1001, 1002},
		{"high uid", 65534, 65534},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := newFakeDocker()
			m, dir := newTestManager(t, fd)

			p := defaultParams(dir)
			p.HostUID = tt.uid
			p.HostGID = tt.gid

			if _, err := m.Start(context.Background(), p, nil); err != nil {
				t.Fatalf("Start: %v", err)
			}

			if fd.lastContainerConfig == nil {
				t.Fatal("ContainerCreate was never called")
			}
			// Container must run as root so the entrypoint can register the
			// host UID in /etc/passwd before dropping privileges via gosu.
			if fd.lastContainerConfig.User != "" {
				t.Errorf("container User = %q, want empty (root)", fd.lastContainerConfig.User)
			}

			// CONSTRUCT_UID / CONSTRUCT_GID must be present in the env so the
			// entrypoint knows which user to register and drop to.
			wantUID := fmt.Sprintf("CONSTRUCT_UID=%d", tt.uid)
			wantGID := fmt.Sprintf("CONSTRUCT_GID=%d", tt.gid)
			var hasUID, hasGID bool
			for _, e := range fd.lastContainerConfig.Env {
				if e == wantUID {
					hasUID = true
				}
				if e == wantGID {
					hasGID = true
				}
			}
			if !hasUID {
				t.Errorf("env missing %s", wantUID)
			}
			if !hasGID {
				t.Errorf("env missing %s", wantGID)
			}

			// The agent exec must be launched as the host user. The log-tail
			// exec (also created by startAgent) does not set User, so we look
			// for the exec whose User field matches the expected value.
			wantExecUser := fmt.Sprintf("%d:%d", tt.uid, tt.gid)
			var foundExecUser bool
			for _, opts := range fd.allExecOptions {
				if opts.User == wantExecUser {
					foundExecUser = true
					break
				}
			}
			if !foundExecUser {
				t.Errorf("no exec with User=%q found; all exec users: %v",
					wantExecUser, func() []string {
						var users []string
						for _, o := range fd.allExecOptions {
							users = append(users, o.User)
						}
						return users
					}())
			}
		})
	}
}

// TestSession_Start_RestartsWhenContainerGone verifies that if a session is
// marked running in the registry but its container no longer exists (e.g. it
// was manually deleted), Start recreates and relaunches it rather than
// returning the stale running session.
func TestSession_Start_RestartsWhenContainerGone(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	// First start — creates the session.
	res1, err := m.Start(context.Background(), defaultParams(repo), nil)
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if res1.Session.Status != registry.StatusRunning {
		t.Fatalf("expected running after first start, got %q", res1.Session.Status)
	}

	// Simulate the container being manually removed: delete it from the fake
	// map and make ContainerInspect report not-found.
	delete(fd.containers, res1.Session.ContainerName)
	notRunning := false
	fd.containerInspectRunning = &notRunning
	fd.errors["ContainerInspect"] = &fakeNotFoundError{}

	// Second Start for the same folder — should detect the missing container,
	// recreate it, and return a running session.
	fd.calls = nil
	res2, err := m.Start(context.Background(), defaultParams(repo), nil)
	if err != nil {
		t.Fatalf("second Start (after container gone): %v", err)
	}
	if res2.Session.Status != registry.StatusRunning {
		t.Errorf("status = %q, want running", res2.Session.Status)
	}
	// Same session ID (we restart, not create a new session).
	if res2.Session.ID != res1.Session.ID {
		t.Errorf("session ID changed: got %q, want %q", res2.Session.ID, res1.Session.ID)
	}
	// A new container should have been started.
	var started bool
	for _, call := range fd.calls {
		if call == "ContainerStart" {
			started = true
		}
	}
	if !started {
		t.Error("expected ContainerStart to be called after missing container detected")
	}
}

// TestSession_Start_RestartsWhenContainerStopped verifies that if a session is
// marked running but its container has exited (exists but State.Running==false),
// Start restarts it.
func TestSession_Start_RestartsWhenContainerStopped(t *testing.T) {
	fd := newFakeDocker()
	m, dir := newTestManager(t, fd)
	repo := dir

	// First start.
	res1, err := m.Start(context.Background(), defaultParams(repo), nil)
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	// Simulate container having exited without construct knowing: mark it
	// stopped in the fake but leave the registry as "running".
	fd.containers[res1.Session.ContainerName] = false // stopped, not removed
	notRunning := false
	fd.containerInspectRunning = &notRunning

	fd.calls = nil
	res2, err := m.Start(context.Background(), defaultParams(repo), nil)
	if err != nil {
		t.Fatalf("second Start (after container stopped): %v", err)
	}
	if res2.Session.Status != registry.StatusRunning {
		t.Errorf("status = %q, want running", res2.Session.Status)
	}
	if res2.Session.ID != res1.Session.ID {
		t.Errorf("session ID changed: got %q, want %q", res2.Session.ID, res1.Session.ID)
	}
	var started bool
	for _, call := range fd.calls {
		if call == "ContainerStart" {
			started = true
		}
	}
	if !started {
		t.Error("expected ContainerStart after stopped container detected")
	}
}
