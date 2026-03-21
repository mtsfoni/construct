// Package session implements session lifecycle logic: create, attach, stop,
// destroy, and reset. It drives the Docker client and registry to manage
// session containers.
package session

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
	specs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/construct-run/construct/internal/auth"
	"github.com/construct-run/construct/internal/config"
	dockeriface "github.com/construct-run/construct/internal/daemon/docker"
	"github.com/construct-run/construct/internal/daemon/logbuffer"
	"github.com/construct-run/construct/internal/daemon/registry"
	netpkg "github.com/construct-run/construct/internal/network"
	"github.com/construct-run/construct/internal/quickstart"
	"github.com/construct-run/construct/internal/slug"
	"github.com/construct-run/construct/internal/stacks"
	"github.com/construct-run/construct/internal/tools"
	"github.com/construct-run/construct/internal/version"
)

// StartParams holds all parameters for session.start.
type StartParams struct {
	Repo              string
	Tool              string
	Stack             string
	DockerMode        string
	Ports             []string
	Debug             bool
	HostUID           int
	HostGID           int
	OpenCodeConfigDir string
}

// StartResult is the response from session.start.
type StartResult struct {
	Session *registry.Session
	WebURL  string
	TUIHint string
	Warning string
}

// ProgressFn is a callback for reporting progress to the caller.
type ProgressFn func(msg string)

// Manager owns all session lifecycle logic and coordinates docker, registry,
// auth, logbuffer, and quickstart.
type Manager struct {
	docker        dockeriface.Client
	reg           *registry.Registry
	authStore     *auth.Store
	qsStore       *quickstart.Store
	stateDir      string
	hostStateDir  string // host-side path corresponding to stateDir; used as bind mount sources
	logBuffers    map[string]*logbuffer.Buffer
	execIDs       map[string]string // session ID -> agent exec ID
	tailExecIDs   map[string]string // session ID -> log-tail exec ID
	logBufferSize int               // 0 means use logbuffer.DefaultSize
}

// NewManager creates a new session manager.
// stateDir and hostStateDir are the same when running outside a container.
func NewManager(
	docker dockeriface.Client,
	reg *registry.Registry,
	authStore *auth.Store,
	qsStore *quickstart.Store,
	stateDir string,
) *Manager {
	return NewManagerWithBufferSize(docker, reg, authStore, qsStore, stateDir, stateDir, 0)
}

// NewManagerWithBufferSize creates a new session manager with an explicit log
// buffer size. Pass 0 to use logbuffer.DefaultSize.
// hostStateDir is the host-side path for the state directory (used as bind mount
// sources when the daemon itself runs inside a container). Pass the same value as
// stateDir when running directly on the host.
func NewManagerWithBufferSize(
	docker dockeriface.Client,
	reg *registry.Registry,
	authStore *auth.Store,
	qsStore *quickstart.Store,
	stateDir string,
	hostStateDir string,
	logBufferSize int,
) *Manager {
	if hostStateDir == "" {
		hostStateDir = stateDir
	}
	return &Manager{
		docker:        docker,
		reg:           reg,
		authStore:     authStore,
		qsStore:       qsStore,
		stateDir:      stateDir,
		hostStateDir:  hostStateDir,
		logBuffers:    make(map[string]*logbuffer.Buffer),
		execIDs:       make(map[string]string),
		tailExecIDs:   make(map[string]string),
		logBufferSize: logBufferSize,
	}
}

// newLogBuffer creates a new log buffer using the manager's configured size.
func (m *Manager) newLogBuffer() *logbuffer.Buffer {
	return logbuffer.New(m.logBufferSize)
}

// LogBuffer returns the log buffer for a session, or nil if not found.
func (m *Manager) LogBuffer(sessionID string) *logbuffer.Buffer {
	return m.logBuffers[sessionID]
}

// InjectLogBuffer sets a log buffer for a session. Intended for testing only.
func (m *Manager) InjectLogBuffer(sessionID string, buf *logbuffer.Buffer) {
	m.logBuffers[sessionID] = buf
}

// AttachLogStream re-attaches log streaming for a running session.
// This is called during daemon reconciliation to restore log capture after
// a daemon restart. It creates a new exec in the container that tails the
// agent process output and pipes it into the session's log buffer.
//
// If no log buffer exists for the session, one is created. If attaching fails
// (e.g. the container is no longer running), the error is returned and the
// caller should log a warning rather than treating it as fatal.
func (m *Manager) AttachLogStream(ctx context.Context, sessionID string) error {
	s := m.reg.GetByID(sessionID)
	if s == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}

	buf := m.logBuffers[s.ID]
	if buf == nil {
		buf = m.newLogBuffer()
		m.logBuffers[s.ID] = buf
	}

	// Re-exec a tail of the agent's stdout by attaching a new exec that reads
	// from the agent process's log file (if any) or, more practically, creates
	// a lightweight exec that streams the container's stdout going forward.
	// Since we cannot re-attach an existing exec after daemon restart, we start
	// a fresh exec that tails the agent log path if available, or silently
	// initialises an empty buffer so `construct logs` works without crashing.
	logPath := tools.LogPath(s.Tool)
	if logPath == "" {
		// Tool doesn't support a log file — buffer stays empty but valid.
		return nil
	}

	execResp, err := m.docker.ContainerExecCreate(ctx, s.ContainerName, container.ExecOptions{
		Cmd:          []string{"tail", "-n", "+0", "-f", logPath},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("create log-tail exec: %w", err)
	}

	attachResp, err := m.docker.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("attach log-tail exec: %w", err)
	}

	m.tailExecIDs[s.ID] = execResp.ID

	go streamOutput(attachResp.Reader, buf)

	// Start the exec in a goroutine — Detach:false blocks until the process exits,
	// which for tail -f is indefinite. We don't need to wait for it.
	go m.docker.ContainerExecStart(ctx, execResp.ID, container.ExecStartOptions{Detach: false}) //nolint:errcheck

	return nil
}

// Start creates or attaches to a session.
func (m *Manager) Start(ctx context.Context, p StartParams, progress ProgressFn) (*StartResult, error) {
	if p.Tool == "" {
		p.Tool = tools.DefaultTool
	}
	if p.Stack == "" {
		p.Stack = stacks.StackBase
	}
	if p.DockerMode == "" {
		p.DockerMode = "none"
	}

	existing := m.reg.GetByRepo(p.Repo)
	if existing != nil {
		return m.handleExisting(ctx, existing, p, progress)
	}
	return m.createNew(ctx, p, progress)
}

func (m *Manager) handleExisting(ctx context.Context, s *registry.Session, p StartParams, progress ProgressFn) (*StartResult, error) {
	var warning string

	if (p.Tool != "" && p.Tool != s.Tool) ||
		(p.Stack != "" && p.Stack != s.Stack) ||
		(p.DockerMode != "" && p.DockerMode != s.DockerMode) ||
		p.Debug != s.Debug {
		warning = "tool/stack/docker/debug flags ignored; session already exists"
	}

	if s.Status == registry.StatusRunning {
		return &StartResult{
			Session: s,
			WebURL:  webURL(s),
			TUIHint: tuiHint(s),
			Warning: warning,
		}, nil
	}

	// Stopped — restart
	if progress != nil {
		progress("Restarting session container...")
	}
	if err := m.docker.ContainerStart(ctx, s.ContainerName, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("start container: %w", err)
	}
	if s.DockerMode == "dind" {
		dindName := netpkg.DindContainerName(s.ShortID())
		if err := m.docker.ContainerStart(ctx, dindName, container.StartOptions{}); err != nil {
			return nil, fmt.Errorf("start dind container: %w", err)
		}
	}

	sessionDir := m.sessionDir(s.ShortID())
	if err := config.WriteAgentsMD(sessionDir, agentsParams(s)); err != nil {
		return nil, fmt.Errorf("write agents.md: %w", err)
	}

	if progress != nil {
		progress("Checking tool installation...")
	}
	if err := m.ensureToolInstalled(ctx, s, progress); err != nil {
		return nil, fmt.Errorf("install tool: %w", err)
	}

	if !s.Debug {
		if progress != nil {
			progress("Starting agent...")
		}
		if err := m.startAgent(ctx, s); err != nil {
			return nil, fmt.Errorf("start agent: %w", err)
		}
	}

	now := time.Now().UTC()
	if err := m.reg.UpdateStatus(s.ID, registry.StatusRunning, &now, nil); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}
	s.Status = registry.StatusRunning
	s.StartedAt = &now

	m.saveQuickstart(s)

	return &StartResult{
		Session: s,
		WebURL:  webURL(s),
		TUIHint: tuiHint(s),
		Warning: warning,
	}, nil
}

func (m *Manager) createNew(ctx context.Context, p StartParams, progress ProgressFn) (*StartResult, error) {
	id := uuid.New().String()
	shortID := id[:8]
	containerName := fmt.Sprintf("construct-%s", shortID)
	volumeName := fmt.Sprintf("construct-layer-%s", shortID)

	imageName := stacks.ImageName(p.Stack)
	if progress != nil {
		progress(fmt.Sprintf("Checking stack image %s...", imageName))
	}
	if err := m.ensureStackImage(ctx, p.Stack, imageName, progress); err != nil {
		return nil, fmt.Errorf("ensure stack image: %w", err)
	}

	webPort, err := netpkg.FindFreePort(tools.WebPort)
	if err != nil {
		return nil, fmt.Errorf("find free port: %w", err)
	}

	portMappings, err := parsePortSpecs(p.Ports, webPort)
	if err != nil {
		return nil, fmt.Errorf("parse ports: %w", err)
	}

	if err := m.authStore.EnsureFolderDir(p.Repo); err != nil {
		return nil, fmt.Errorf("ensure credential dir: %w", err)
	}

	now := time.Now().UTC()
	s := &registry.Session{
		ID:                id,
		Repo:              p.Repo,
		Tool:              p.Tool,
		Stack:             p.Stack,
		DockerMode:        p.DockerMode,
		Debug:             p.Debug,
		Ports:             portMappings,
		WebPort:           webPort,
		ContainerName:     containerName,
		HostUID:           p.HostUID,
		HostGID:           p.HostGID,
		OpenCodeConfigDir: p.OpenCodeConfigDir,
		Status:            registry.StatusRunning,
		CreatedAt:         now,
		StartedAt:         &now,
	}

	sessionDir := m.sessionDir(shortID)
	if err := config.WriteAgentsMD(sessionDir, agentsParams(s)); err != nil {
		return nil, fmt.Errorf("write agents.md: %w", err)
	}

	if progress != nil {
		progress("Creating agent layer volume...")
	}
	if _, err := m.docker.VolumeCreate(ctx, volume.CreateOptions{Name: volumeName}); err != nil {
		os.RemoveAll(sessionDir)
		return nil, fmt.Errorf("create volume: %w", err)
	}

	if p.DockerMode == "dind" {
		if progress != nil {
			progress("Creating dind network and sidecar...")
		}
		if err := m.createDind(ctx, s); err != nil {
			m.docker.VolumeRemove(ctx, volumeName, true)
			os.RemoveAll(sessionDir)
			return nil, fmt.Errorf("create dind: %w", err)
		}
	}

	if progress != nil {
		progress("Creating session container...")
	}
	if err := m.createContainer(ctx, s, volumeName); err != nil {
		m.cleanupOnFailure(ctx, s, volumeName, sessionDir)
		return nil, fmt.Errorf("create container: %w", err)
	}

	if progress != nil {
		progress("Starting session container...")
	}
	if err := m.docker.ContainerStart(ctx, containerName, container.StartOptions{}); err != nil {
		m.cleanupOnFailure(ctx, s, volumeName, sessionDir)
		return nil, fmt.Errorf("start container: %w", err)
	}

	if progress != nil {
		progress(fmt.Sprintf("Installing %s...", p.Tool))
	}
	if err := m.ensureToolInstalled(ctx, s, progress); err != nil {
		m.cleanupOnFailure(ctx, s, volumeName, sessionDir)
		return nil, fmt.Errorf("install tool: %w", err)
	}

	if !p.Debug {
		if progress != nil {
			progress("Starting agent process...")
		}
		if err := m.startAgent(ctx, s); err != nil {
			m.cleanupOnFailure(ctx, s, volumeName, sessionDir)
			return nil, fmt.Errorf("start agent: %w", err)
		}
	}

	if err := m.reg.Add(s); err != nil {
		m.cleanupOnFailure(ctx, s, volumeName, sessionDir)
		return nil, fmt.Errorf("register session: %w", err)
	}

	m.logBuffers[s.ID] = m.newLogBuffer()
	m.saveQuickstart(s)

	return &StartResult{
		Session: s,
		WebURL:  webURL(s),
		TUIHint: tuiHint(s),
	}, nil
}

// Stop gracefully stops a running session.
func (m *Manager) Stop(ctx context.Context, sessionID string) (*registry.Session, error) {
	s := m.reg.GetByID(sessionID)
	if s == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	if s.Status == registry.StatusStopped {
		return s, nil
	}

	if execID, ok := m.execIDs[s.ID]; ok {
		_ = m.killAgentExec(ctx, s.ContainerName, execID)
		delete(m.execIDs, s.ID)
	}
	// The tail exec will terminate naturally when the container stops.
	delete(m.tailExecIDs, s.ID)

	timeout := 30
	if err := m.docker.ContainerStop(ctx, s.ContainerName, container.StopOptions{Timeout: &timeout}); err != nil {
		return nil, fmt.Errorf("stop container: %w", err)
	}

	if s.DockerMode == "dind" {
		dindName := netpkg.DindContainerName(s.ShortID())
		_ = m.docker.ContainerStop(ctx, dindName, container.StopOptions{Timeout: &timeout})
	}

	now := time.Now().UTC()
	if err := m.reg.UpdateStatus(s.ID, registry.StatusStopped, nil, &now); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}
	s.Status = registry.StatusStopped
	s.StoppedAt = &now
	return s, nil
}

// Destroy permanently removes a session and all its resources.
func (m *Manager) Destroy(ctx context.Context, sessionID string) error {
	s := m.reg.GetByID(sessionID)
	if s == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}

	if s.Status == registry.StatusRunning {
		if _, err := m.Stop(ctx, sessionID); err != nil {
			return fmt.Errorf("stop session: %w", err)
		}
	}

	shortID := s.ShortID()
	volumeName := fmt.Sprintf("construct-layer-%s", shortID)

	_ = m.docker.ContainerRemove(ctx, s.ContainerName, container.RemoveOptions{Force: true})
	_ = m.docker.VolumeRemove(ctx, volumeName, true)

	if s.DockerMode == "dind" {
		dindName := netpkg.DindContainerName(shortID)
		netName := netpkg.SessionNetworkName(shortID)
		_ = m.docker.ContainerRemove(ctx, dindName, container.RemoveOptions{Force: true})
		_ = m.docker.NetworkRemove(ctx, netName)
	}

	sessionDir := m.sessionDir(shortID)
	_ = os.RemoveAll(sessionDir)

	delete(m.logBuffers, s.ID)
	delete(m.execIDs, s.ID)
	delete(m.tailExecIDs, s.ID)

	if err := m.reg.Remove(s.ID); err != nil {
		return fmt.Errorf("remove from registry: %w", err)
	}
	_ = m.qsStore.Delete(s.Repo)
	return nil
}

// Reset wipes the agent layer and restarts the session fresh.
func (m *Manager) Reset(ctx context.Context, sessionID string) (*registry.Session, error) {
	s := m.reg.GetByID(sessionID)
	if s == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	shortID := s.ShortID()
	volumeName := fmt.Sprintf("construct-layer-%s", shortID)

	if s.Status == registry.StatusRunning {
		if _, err := m.Stop(ctx, sessionID); err != nil {
			return nil, fmt.Errorf("stop session: %w", err)
		}
	}

	// Remove the container so we can recreate it with the new empty volume.
	// Docker bakes volume mount specs into the container at creation time, so
	// simply removing and recreating the volume is not enough — the old volume
	// ID is still referenced in the container config.
	if err := m.docker.ContainerRemove(ctx, s.ContainerName, container.RemoveOptions{Force: true}); err != nil {
		return nil, fmt.Errorf("remove container for reset: %w", err)
	}

	_ = m.docker.VolumeRemove(ctx, volumeName, true)
	if _, err := m.docker.VolumeCreate(ctx, volume.CreateOptions{Name: volumeName}); err != nil {
		return nil, fmt.Errorf("create fresh volume: %w", err)
	}

	sessionDir := m.sessionDir(shortID)
	if err := config.WriteAgentsMD(sessionDir, agentsParams(s)); err != nil {
		return nil, fmt.Errorf("write agents.md: %w", err)
	}

	// Recreate the container with the fresh volume.
	if err := m.createContainer(ctx, s, volumeName); err != nil {
		return nil, fmt.Errorf("recreate container: %w", err)
	}

	if err := m.docker.ContainerStart(ctx, s.ContainerName, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("start container: %w", err)
	}
	if s.DockerMode == "dind" {
		dindName := netpkg.DindContainerName(shortID)
		_ = m.docker.ContainerStart(ctx, dindName, container.StartOptions{})
	}

	if err := m.ensureToolInstalled(ctx, s, nil); err != nil {
		return nil, fmt.Errorf("install tool: %w", err)
	}

	if !s.Debug {
		if err := m.startAgent(ctx, s); err != nil {
			return nil, fmt.Errorf("start agent: %w", err)
		}
	}

	now := time.Now().UTC()
	if err := m.reg.UpdateStatus(s.ID, registry.StatusRunning, &now, nil); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}
	s.Status = registry.StatusRunning
	s.StartedAt = &now
	return s, nil
}

// List returns all sessions.
func (m *Manager) List() []*registry.Session { return m.reg.List() }

// GetByID looks up a session by ID.
func (m *Manager) GetByID(id string) *registry.Session { return m.reg.GetByID(id) }

// GetByRepo looks up a session by repo path.
func (m *Manager) GetByRepo(repo string) *registry.Session { return m.reg.GetByRepo(repo) }

// GetByPrefix looks up a session by ID prefix.
func (m *Manager) GetByPrefix(prefix string) (*registry.Session, error) {
	return m.reg.GetByPrefix(prefix)
}

// --- helpers ---

func (m *Manager) sessionDir(shortID string) string {
	return filepath.Join(m.stateDir, "sessions", shortID)
}

// hostSessionDir returns the host-side path for a session directory.
// When running outside a container this equals sessionDir.
func (m *Manager) hostSessionDir(shortID string) string {
	return filepath.Join(m.hostStateDir, "sessions", shortID)
}

// hostGlobalCredDir returns the host-side path for the global credentials directory.
func (m *Manager) hostGlobalCredDir() string {
	return filepath.Join(m.hostStateDir, "credentials", "global")
}

// hostFolderCredDir returns the host-side path for the per-folder credentials directory.
func (m *Manager) hostFolderCredDir(folderPath string) string {
	sl := slug.FromPath(folderPath)
	return filepath.Join(m.hostStateDir, "credentials", "folders", sl)
}

func (m *Manager) ensureStackImage(ctx context.Context, stackName, imageName string, progress ProgressFn) error {
	_, _, err := m.docker.ImageInspectWithRaw(ctx, imageName)
	if err == nil {
		return nil
	}

	if progress != nil {
		progress(fmt.Sprintf("Building stack image %s...", imageName))
	}

	buildCtxDir, err := stacks.ExtractBuildContext(stackName)
	if err != nil {
		return fmt.Errorf("extract build context: %w", err)
	}
	defer os.RemoveAll(buildCtxDir)

	tarBuf, err := dirToTar(buildCtxDir)
	if err != nil {
		return fmt.Errorf("create build tar: %w", err)
	}

	ver := version.Version
	labels := map[string]string{stacks.VersionLabel: ver}
	buildArgs := stacks.BuildArgs()

	resp, err := m.docker.ImageBuild(ctx, tarBuf, types.ImageBuildOptions{
		Tags:       []string{imageName},
		Dockerfile: "Dockerfile",
		BuildArgs:  buildArgs,
		Labels:     labels,
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("build image: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (m *Manager) createContainer(ctx context.Context, s *registry.Session, volumeName string) error {
	shortID := s.ShortID()

	// Volume and idmap-bind mounts go in Mounts.
	// Credential and config bind mounts use HostConfig.Binds with :z SELinux
	// relabeling so they work on Fedora/RHEL hosts with SELinux enforcing.
	mounts := []mount.Mount{
		{Type: mount.TypeVolume, Source: volumeName, Target: "/agent"},
		{
			// The idmap (UID/GID mapping) for this bind mount is injected by
			// ContainerCreateWithIDMap, which extends the BindOptions JSON with
			// an IDMapping field not present in the Go SDK's mount.BindOptions.
			Type:   mount.TypeBind,
			Source: s.Repo,
			Target: s.Repo,
			BindOptions: &mount.BindOptions{
				CreateMountpoint: true,
			},
		},
	}

	// :z relabels the bind mount for SELinux (shared label), required on
	// Fedora/RHEL with SELinux enforcing. The Mounts struct has no such field.
	binds := []string{
		s.OpenCodeConfigDir + ":" + s.OpenCodeConfigDir + ":ro,z",
		m.hostGlobalCredDir() + ":/run/construct/creds/global:ro,z",
		m.hostFolderCredDir(s.Repo) + ":/run/construct/creds/folder:ro,z",
		filepath.Join(m.hostSessionDir(shortID), "construct-agents.md") +
			":/agent/home/.config/opencode/construct-agents.md:ro,z",
	}

	if s.DockerMode == "dood" {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: "/var/run/docker.sock",
			Target: "/var/run/docker.sock",
		})
	}

	portBindings, exposedPorts := buildPorts(s.Ports)

	env := buildEnv(s, shortID)

	restartPolicy := container.RestartPolicy{Name: "unless-stopped"}
	if s.Debug {
		restartPolicy = container.RestartPolicy{Name: "no"}
	}

	networkMode := container.NetworkMode("bridge")
	networkConfig := &network.NetworkingConfig{}
	if s.DockerMode == "dind" {
		networkConfig = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				netpkg.SessionNetworkName(shortID): {},
			},
		}
	}

	cfg := &container.Config{
		Image:        stacks.ImageName(s.Stack),
		Env:          env,
		ExposedPorts: nat.PortSet(exposedPorts),
	}
	hostCfg := &container.HostConfig{
		Mounts:        mounts,
		Binds:         binds,
		PortBindings:  nat.PortMap(portBindings),
		RestartPolicy: restartPolicy,
		NetworkMode:   networkMode,
	}
	if s.DockerMode == "dood" {
		hostCfg.SecurityOpt = []string{"label=disable"}
	}

	idmap := dockeriface.IDMapping{
		UIDMappings: []dockeriface.IDMap{{ContainerID: 0, HostID: uint32(s.HostUID), Size: 1}},
		GIDMappings: []dockeriface.IDMap{{ContainerID: 0, HostID: uint32(s.HostGID), Size: 1}},
	}
	_, err := m.docker.ContainerCreateWithIDMap(
		ctx, cfg, hostCfg, networkConfig,
		&specs.Platform{OS: "linux"},
		s.ContainerName,
		s.Repo, s.Repo,
		idmap,
	)
	return err
}

func (m *Manager) createDind(ctx context.Context, s *registry.Session) error {
	shortID := s.ShortID()
	netName := netpkg.SessionNetworkName(shortID)
	dindName := netpkg.DindContainerName(shortID)

	if _, err := m.docker.NetworkCreate(ctx, netName, network.CreateOptions{Driver: "bridge"}); err != nil {
		return fmt.Errorf("create network: %w", err)
	}

	_, err := m.docker.ContainerCreate(
		ctx,
		&container.Config{Image: "docker:27-dind", Env: []string{"DOCKER_TLS_CERTDIR="}},
		&container.HostConfig{
			Privileged:    true,
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
			NetworkMode:   container.NetworkMode(netName),
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{netName: {}},
		},
		&specs.Platform{OS: "linux"},
		dindName,
	)
	if err != nil {
		m.docker.NetworkRemove(ctx, netName)
		return fmt.Errorf("create dind container: %w", err)
	}
	if err := m.docker.ContainerStart(ctx, dindName, container.StartOptions{}); err != nil {
		m.docker.ContainerRemove(ctx, dindName, container.RemoveOptions{Force: true})
		m.docker.NetworkRemove(ctx, netName)
		return fmt.Errorf("start dind container: %w", err)
	}
	return nil
}

func (m *Manager) ensureToolInstalled(ctx context.Context, s *registry.Session, progress ProgressFn) error {
	checkResp, err := m.docker.ContainerExecCreate(ctx, s.ContainerName, container.ExecOptions{
		Cmd: []string{"test", "-f", tools.BinaryPath(s.Tool)},
	})
	if err == nil {
		m.docker.ContainerExecStart(ctx, checkResp.ID, container.ExecStartOptions{})
		if inspect, err := m.docker.ContainerExecInspect(ctx, checkResp.ID); err == nil && inspect.ExitCode == 0 {
			return nil
		}
	}

	if progress != nil {
		progress(fmt.Sprintf("Installing %s (this may take a moment)...", s.Tool))
	}

	installCmd := tools.InstallCommand(s.Tool)
	execResp, err := m.docker.ContainerExecCreate(ctx, s.ContainerName, container.ExecOptions{
		Cmd: []string{"/bin/sh", "-c", installCmd},
	})
	if err != nil {
		return fmt.Errorf("create install exec: %w", err)
	}
	if err := m.docker.ContainerExecStart(ctx, execResp.ID, container.ExecStartOptions{}); err != nil {
		return fmt.Errorf("start install exec: %w", err)
	}
	for i := 0; i < 120; i++ {
		time.Sleep(500 * time.Millisecond)
		inspect, err := m.docker.ContainerExecInspect(ctx, execResp.ID)
		if err != nil {
			return fmt.Errorf("inspect install exec: %w", err)
		}
		if !inspect.Running {
			if inspect.ExitCode != 0 {
				return fmt.Errorf("tool install failed with exit code %d", inspect.ExitCode)
			}
			return nil
		}
	}
	return fmt.Errorf("tool install timed out")
}

func (m *Manager) startAgent(ctx context.Context, s *registry.Session) error {
	cmd := tools.InvokeCommand(s.Tool, tools.WebPort)
	execResp, err := m.docker.ContainerExecCreate(ctx, s.ContainerName, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: false,
		AttachStderr: false,
	})
	if err != nil {
		return fmt.Errorf("create agent exec: %w", err)
	}

	if err := m.docker.ContainerExecStart(ctx, execResp.ID, container.ExecStartOptions{Detach: true}); err != nil {
		return fmt.Errorf("start agent exec: %w", err)
	}

	m.execIDs[s.ID] = execResp.ID

	if m.logBuffers[s.ID] == nil {
		m.logBuffers[s.ID] = m.newLogBuffer()
	}

	// Start a log-tail exec to stream agent output into the buffer.
	// We run this in a goroutine because AttachLogStream may briefly block
	// waiting for the log file to appear; failures are non-fatal.
	go func() {
		if err := m.AttachLogStream(ctx, s.ID); err != nil {
			// Non-fatal: logs simply won't stream, but the session is running.
			_ = err
		}
	}()

	return nil
}

func (m *Manager) killAgentExec(ctx context.Context, containerName, execID string) error {
	inspect, err := m.docker.ContainerExecInspect(ctx, execID)
	if err != nil {
		return err
	}
	if inspect.Pid == 0 {
		return nil
	}
	resp, err := m.docker.ContainerExecCreate(ctx, containerName, container.ExecOptions{
		Cmd: []string{"/bin/sh", "-c", fmt.Sprintf("kill -TERM %d", inspect.Pid)},
	})
	if err != nil {
		return err
	}
	return m.docker.ContainerExecStart(ctx, resp.ID, container.ExecStartOptions{})
}

func (m *Manager) cleanupOnFailure(ctx context.Context, s *registry.Session, volumeName, sessionDir string) {
	shortID := s.ShortID()
	_ = m.docker.ContainerRemove(ctx, s.ContainerName, container.RemoveOptions{Force: true})
	if s.DockerMode == "dind" {
		_ = m.docker.ContainerRemove(ctx, netpkg.DindContainerName(shortID), container.RemoveOptions{Force: true})
		_ = m.docker.NetworkRemove(ctx, netpkg.SessionNetworkName(shortID))
	}
	_ = m.docker.VolumeRemove(ctx, volumeName, true)
	_ = os.RemoveAll(sessionDir)
}

func (m *Manager) saveQuickstart(s *registry.Session) {
	ports := make([]string, 0, len(s.Ports))
	for _, p := range s.Ports {
		if p.HostPort > 0 {
			ports = append(ports, fmt.Sprintf("%d:%d", p.HostPort, p.ContainerPort))
		} else {
			ports = append(ports, fmt.Sprintf("%d", p.ContainerPort))
		}
	}
	_ = m.qsStore.Save(quickstart.Record{
		Folder:     s.Repo,
		Tool:       s.Tool,
		Stack:      s.Stack,
		DockerMode: s.DockerMode,
		Ports:      ports,
	})
}

// --- pure functions ---

func agentsParams(s *registry.Session) config.AgentsParams {
	ports := make([]config.PortMapping, 0, len(s.Ports))
	for _, p := range s.Ports {
		ports = append(ports, config.PortMapping{HostPort: p.HostPort, ContainerPort: p.ContainerPort})
	}
	return config.AgentsParams{
		SessionID:  s.ID,
		Repo:       s.Repo,
		Tool:       s.Tool,
		Stack:      s.Stack,
		DockerMode: s.DockerMode,
		Ports:      ports,
		WebPort:    s.WebPort,
	}
}

func webURL(s *registry.Session) string {
	if s.Status != registry.StatusRunning || !tools.HasWebUI(s.Tool) {
		return ""
	}
	return fmt.Sprintf("http://localhost:%d", s.WebPort)
}

func tuiHint(s *registry.Session) string {
	if s.Tool == tools.ToolOpencode {
		return "opencode"
	}
	return ""
}

func formatPorts(ports []registry.PortMapping) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		parts = append(parts, fmt.Sprintf("%d:%d", p.HostPort, p.ContainerPort))
	}
	return strings.Join(parts, ",")
}

func buildEnv(s *registry.Session, shortID string) []string {
	env := []string{
		"HOME=/agent/home",
		"XDG_CONFIG_HOME=/agent/home/.config",
		"PATH=/agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"NPM_CONFIG_PREFIX=/agent",
		fmt.Sprintf("OPENCODE_CONFIG_DIR=%s", s.OpenCodeConfigDir),
		fmt.Sprintf("CONSTRUCT_SESSION_ID=%s", s.ID),
		fmt.Sprintf("CONSTRUCT_REPO=%s", s.Repo),
		fmt.Sprintf("CONSTRUCT_TOOL=%s", s.Tool),
		fmt.Sprintf("CONSTRUCT_STACK=%s", s.Stack),
		fmt.Sprintf("CONSTRUCT_DOCKER_MODE=%s", s.DockerMode),
		fmt.Sprintf("CONSTRUCT_PORTS=%s", formatPorts(s.Ports)),
	}
	if s.DockerMode == "dind" {
		env = append(env, fmt.Sprintf("DOCKER_HOST=tcp://construct-dind-%s:2375", shortID))
	}
	return env
}

func parsePortSpecs(portSpecs []string, webPort int) ([]registry.PortMapping, error) {
	var result []registry.PortMapping
	hasWebPort := false

	for _, spec := range portSpecs {
		h, c, err := netpkg.ParsePortSpec(spec)
		if err != nil {
			return nil, err
		}
		if c == tools.WebPort {
			hasWebPort = true
			if h == 0 {
				h = webPort
			}
		}
		result = append(result, registry.PortMapping{HostPort: h, ContainerPort: c})
	}

	if !hasWebPort {
		result = append(result, registry.PortMapping{HostPort: webPort, ContainerPort: tools.WebPort})
	}
	return result, nil
}

func buildPorts(ports []registry.PortMapping) (map[nat.Port][]nat.PortBinding, map[nat.Port]struct{}) {
	bindings := make(map[nat.Port][]nat.PortBinding)
	exposed := make(map[nat.Port]struct{})

	for _, p := range ports {
		port := nat.Port(fmt.Sprintf("%d/tcp", p.ContainerPort))
		exposed[port] = struct{}{}
		bindings[port] = append(bindings[port], nat.PortBinding{
			HostIP:   "0.0.0.0",
			HostPort: fmt.Sprintf("%d", p.HostPort),
		})
	}
	return bindings, exposed
}

func streamOutput(r io.Reader, buf *logbuffer.Buffer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		buf.Append(logbuffer.Line{
			Timestamp: time.Now().UTC(),
			Text:      scanner.Text(),
			Stream:    "stdout",
		})
	}
}

func dirToTar(dir string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !d.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	tw.Close()
	return &buf, nil
}
