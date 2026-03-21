package session

import (
	"context"
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
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/construct-run/construct/internal/auth"
	dockeriface "github.com/construct-run/construct/internal/daemon/docker"
	"github.com/construct-run/construct/internal/daemon/registry"
	"github.com/construct-run/construct/internal/quickstart"
	"github.com/construct-run/construct/internal/stacks"
	"github.com/construct-run/construct/internal/tools"
)

// --- fake Docker client ---

type fakeDocker struct {
	// containers maps name→created
	containers map[string]bool
	// volumes maps name→created
	volumes map[string]bool
	// networks maps name→created
	networks map[string]bool
	// execResults maps execID→exit code
	execResults map[string]int
	// imageExists controls whether ImageInspectWithRaw returns an image
	imageExists bool
	// errors maps method name→error to return
	errors map[string]error
	// calls records the sequence of method names called
	calls []string
	// execCounter for generating unique exec IDs
	execCounter int
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

func (f *fakeDocker) ContainerCreate(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *specs.Platform, name string) (container.CreateResponse, error) {
	f.record("ContainerCreate")
	if err := f.errors["ContainerCreate"]; err != nil {
		return container.CreateResponse{}, err
	}
	f.containers[name] = false // created but not started
	return container.CreateResponse{ID: name}, nil
}

func (f *fakeDocker) ContainerCreateWithIDMap(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *specs.Platform, name string, _, _ string, _ dockeriface.IDMapping) (container.CreateResponse, error) {
	f.record("ContainerCreateWithIDMap")
	if err := f.errors["ContainerCreateWithIDMap"]; err != nil {
		return container.CreateResponse{}, err
	}
	f.containers[name] = false
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

func (f *fakeDocker) ContainerInspect(_ context.Context, _ string) (types.ContainerJSON, error) {
	f.record("ContainerInspect")
	return types.ContainerJSON{}, nil
}

func (f *fakeDocker) ContainerExecCreate(_ context.Context, _ string, _ container.ExecOptions) (types.IDResponse, error) {
	f.record("ContainerExecCreate")
	if err := f.errors["ContainerExecCreate"]; err != nil {
		return types.IDResponse{}, err
	}
	f.execCounter++
	id := "exec-" + string(rune('0'+f.execCounter))
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

func (f *fakeDocker) ImageInspectWithRaw(_ context.Context, _ string) (types.ImageInspect, []byte, error) {
	f.record("ImageInspectWithRaw")
	if !f.imageExists {
		return types.ImageInspect{}, nil, &fakeNotFoundError{}
	}
	return types.ImageInspect{}, nil, nil
}

func (f *fakeDocker) ImageList(_ context.Context, _ image.ListOptions) ([]image.Summary, error) {
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
		if call == "ContainerCreateWithIDMap" || call == "ContainerCreate" {
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

	// Re-start with different stack — should warn
	p2 := defaultParams(repo)
	p2.Stack = stacks.StackNode
	res, err := m.Start(context.Background(), p2, nil)
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if res.Warning == "" {
		t.Error("expected warning for conflicting stack, got empty string")
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

func TestSession_Reset(t *testing.T) {
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

	// Reset
	resetRes, err := m.Reset(context.Background(), id)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if resetRes.Status != registry.StatusRunning {
		t.Errorf("status after reset = %q, want running", resetRes.Status)
	}
	// Volume should still exist (recreated fresh)
	if !fd.volumes[volName] {
		t.Error("expected volume to exist after reset")
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

	_, err = m.Reset(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for unknown session ID")
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
