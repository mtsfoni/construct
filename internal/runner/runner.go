package runner

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mtsfoni/construct/internal/buildinfo"
	"github.com/mtsfoni/construct/internal/dind"
	"github.com/mtsfoni/construct/internal/stacks"
	"github.com/mtsfoni/construct/internal/tools"
)

// Config holds all options needed to start a construct session.
type Config struct {
	Tool     *tools.Tool
	Stack    string
	RepoPath string
	Rebuild  bool
	// Debug drops into an interactive shell instead of starting the tool.
	Debug bool
	// Reset removes the persistent home volume before starting so it is
	// re-created and re-seeded from scratch. Useful when HomeFiles have
	// changed and the user wants the new defaults without manual Docker commands.
	Reset bool
	// MCP enables MCP server activation. When true, the entrypoint writes the
	// opencode MCP config (~/.config/opencode/opencode.json) at container startup
	// via the CONSTRUCT_MCP=1 environment variable. @playwright/mcp must be
	// installed in the stack image (i.e. --stack ui) for the MCP server to work.
	MCP bool
	// Ports is the list of host-to-container port mappings to publish, in the
	// same format as docker run -p: "3000", "3000:3000", or "127.0.0.1:3000:3000".
	// Each entry is passed as a separate -p flag. When non-empty, CONSTRUCT=1 and
	// CONSTRUCT_PORTS (comma-separated list) are also injected as env vars.
	Ports []string
	// DockerMode controls whether and how the agent container gets Docker access.
	// Valid values: "none" (no Docker; default), "dood" (Docker-outside-of-Docker
	// via host socket bind-mount), "dind" (Docker-in-Docker sidecar).
	DockerMode string
	// ExtraArgs are additional arguments passed verbatim to the tool after
	// Tool.RunCmd. They are collected from everything the user supplies after
	// a bare "--" separator on the command line. Ignored in Debug mode.
	//
	// In serve mode (non-Debug), when ExtraArgs is non-empty it is treated as a
	// headless prompt: "opencode run --attach <url> <extra-args...>" is run
	// locally. When empty, "opencode attach <url>" is run locally (interactive).
	ExtraArgs []string
	// ServePort is the port on which the opencode HTTP server listens inside the
	// container (opencode serve --port <ServePort>). The same port is published to
	// the host unchanged (127.0.0.1:<ServePort>:<ServePort>). Defaults to 4096
	// when zero.
	ServePort int
	// Client selects the local client that connects to the opencode server after
	// it starts. Valid values:
	//   ""     auto-detect: try "opencode attach", fall back to browser (default)
	//   "tui"  always use "opencode attach"; error if opencode not on PATH
	//   "web"  always open the browser directly; incompatible with ExtraArgs
	Client string
}

// defaultServePort is the port used by opencode serve when Config.ServePort is zero.
const defaultServePort = 4096

// isPortFree reports whether the given TCP port is free on the loopback interface.
func isPortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// findFreePort returns the first free TCP port in [start, start+maxPortSearch)
// by probing the loopback interface. It returns 0 if no free port is found
// within the search range.
func findFreePort(start int) int {
	const maxPortSearch = 100
	for p := start; p < start+maxPortSearch && p <= 65535; p++ {
		if isPortFree(p) {
			return p
		}
	}
	return 0
}

// servePort returns the effective serve port for the given config.
func servePort(cfg *Config) int {
	if cfg.ServePort > 0 {
		return cfg.ServePort
	}
	return defaultServePort
}

// Run builds images, starts any requested Docker sidecar, and runs the agent
// container. It blocks until the container exits and then cleans up.
//
// In normal (non-debug) mode the container runs "opencode serve" headlessly and
// the local client (opencode attach or browser) is launched from the host.
// In debug mode the old interactive shell behaviour is preserved.
func Run(cfg *Config) error {
	// Validate --client value up front.
	switch cfg.Client {
	case "", "tui", "web":
		// valid
	default:
		return fmt.Errorf("unknown client %q; supported values: tui, web", cfg.Client)
	}

	// 1. Ensure the stack image exists (build if needed).
	if err := stacks.EnsureBuilt(cfg.Stack, cfg.Rebuild); err != nil {
		return err
	}

	// 2. Ensure the tool image (derived from the stack) exists.
	toolImage := stacks.ImageName(cfg.Stack) + "-" + cfg.Tool.Name
	if cfg.Rebuild || !toolImageExists(toolImage) || !toolImageVersionCurrent(toolImage) {
		if err := buildToolImage(toolImage, stacks.ImageName(cfg.Stack), cfg.Tool); err != nil {
			return fmt.Errorf("build tool image: %w", err)
		}
	}

	// 3. Generate a unique session ID for deterministic container/network names.
	sessionID, err := generateSessionID()
	if err != nil {
		return fmt.Errorf("generate session id: %w", err)
	}

	// 4. Ensure the persistent home volume exists and is owned by the host user.
	homVol := homeVolumeName(cfg.RepoPath, cfg.Tool.Name)
	if cfg.Reset {
		fmt.Println("construct: resetting home volume…")
		if err := removeHomeVolume(homVol); err != nil {
			return fmt.Errorf("reset home volume: %w", err)
		}
	}
	if err := ensureHomeVolume(homVol, stacks.ImageName(cfg.Stack)+"-"+cfg.Tool.Name, cfg.Tool.HomeFiles, cfg.Tool.AuthVolumePath); err != nil {
		return fmt.Errorf("ensure home volume: %w", err)
	}

	// 4b. Ensure the global auth volume exists when the tool needs one.
	// This volume is NOT keyed by repo and is NOT wiped by --reset, so OAuth
	// tokens and other auth state persist across repos and resets.
	var authVol string
	if cfg.Tool.AuthVolumePath != "" {
		authVol = authVolumeName(cfg.Tool.Name)
		if err := ensureAuthVolume(authVol); err != nil {
			return fmt.Errorf("ensure auth volume: %w", err)
		}
	}

	// 4c. Ensure host-side auth files exist for bind-mounting. Each file is
	// created (empty, mode 0600) if absent so Docker bind-mounts a regular file
	// rather than creating a directory at that path.
	for _, af := range cfg.Tool.AuthFiles {
		if err := ensureAuthFile(af.HostPath); err != nil {
			return fmt.Errorf("ensure auth file %s: %w", af.HostPath, err)
		}
	}

	// 5. Load environment variables (global then per-repo override).
	env, err := loadEnv(cfg.RepoPath)
	if err != nil {
		return fmt.Errorf("load env: %w", err)
	}

	// 6. Write per-credential secret files.
	// Done before starting dind so the path is available to the signal handler,
	// which must clean it up explicitly — os.Exit does not run deferred calls.
	secretsDir, err := writeSecretFiles(cfg.Tool.AuthEnvVars, env)
	if err != nil {
		return fmt.Errorf("write secret files: %w", err)
	}
	defer os.RemoveAll(secretsDir)

	// 7. Start the dind sidecar only when explicitly requested.
	var dindInst *dind.Instance
	if cfg.DockerMode == "dind" {
		fmt.Printf("construct: starting dind sidecar (session %s)…\n", sessionID)
		dindInst, err = dind.Start(sessionID)
		if err != nil {
			os.RemoveAll(secretsDir)
			return fmt.Errorf("start dind: %w", err)
		}
	}

	// containerName is used for cleanup in the signal handler.
	containerName := "construct-agent-" + sessionID

	// Always clean up on exit — even on SIGINT/SIGTERM.
	// os.Exit does not run deferred functions, so the signal handler must
	// explicitly remove the secrets directory before calling os.Exit.
	stopped := make(chan struct{})
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		select {
		case <-sigs:
			fmt.Println("\nconstruct: interrupted — cleaning up…")
			os.RemoveAll(secretsDir)
			stopContainer(containerName)
			if dindInst != nil {
				dindInst.Stop()
			}
			os.Exit(1)
		case <-stopped:
		}
	}()
	defer func() {
		close(stopped)
		if dindInst != nil {
			dindInst.Stop()
		}
	}()

	// 8. Debug mode: run an interactive shell the old way (docker run -it /bin/bash).
	if cfg.Debug {
		fmt.Printf("construct: debug mode — starting shell in %s container (no agent)…\n", cfg.Stack)
		args := buildDebugArgs(cfg, dindInst, toolImage, sessionID, homVol, authVol, secretsDir)
		cmd := exec.Command("docker", args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// 9. Normal mode: start the opencode server detached inside the container,
	//    wait for it to be ready, then connect a local client.
	port := servePort(cfg)
	// When the user has not specified a port explicitly, auto-select the next
	// free port if the default is already in use on the host.
	if cfg.ServePort == 0 {
		free := findFreePort(defaultServePort)
		if free == 0 {
			return fmt.Errorf("no free port found in range %d-%d; use --serve-port to specify a port explicitly", defaultServePort, defaultServePort+99)
		}
		if free != defaultServePort {
			// ANSI yellow on stderr — visible but not alarming.
			fmt.Fprintf(os.Stderr, "\033[33mconstruct: port %d is already in use; using port %d instead\033[0m\n", defaultServePort, free)
			port = free
		}
	}
	fmt.Printf("construct: launching %s serve in %s container (port %d)…\n", cfg.Tool.Name, cfg.Stack, port)

	serverArgs := buildServeArgs(cfg, dindInst, toolImage, sessionID, homVol, authVol, secretsDir, port)
	startCmd := exec.Command("docker", serverArgs...)
	startCmd.Stdout = os.Stdout
	startCmd.Stderr = os.Stderr
	if err := startCmd.Run(); err != nil {
		return fmt.Errorf("start server container: %w", err)
	}

	// 10. Wait for the opencode HTTP server to accept connections.
	serverURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitForServer(serverURL, 15*time.Second); err != nil {
		printServeTimeoutDiagnostics(os.Stderr, containerName)
		stopContainer(containerName)
		return fmt.Errorf("server did not become ready: %w", err)
	}

	// 11. Connect the local client. Stop the container when the client exits.
	defer stopContainer(containerName)

	if len(cfg.ExtraArgs) > 0 {
		if cfg.Client == "web" {
			stopContainer(containerName)
			return fmt.Errorf("--client web is incompatible with passthrough args (headless requires opencode)")
		}
		// Headless mode: fire a task and stream output.
		return runLocalHeadless(serverURL, cfg.ExtraArgs)
	}
	// Interactive mode: attach TUI or fall back to browser.
	return runLocalAttach(serverURL, cfg.Client)
}

// buildRunArgs assembles the arguments for "docker run" that starts the agent.
// dindInst is non-nil only when cfg.DockerMode == "dind".
func buildRunArgs(cfg *Config, dindInst *dind.Instance, image, sessionID, homeVolume, authVolume, secretsDir string) []string {
	// Run as the host user so the bind-mounted workspace directory is accessible.
	uid := os.Getuid()
	gid := os.Getgid()

	args := []string{"run", "--rm", "-it"}

	// Attach to the dind session network when using Docker-in-Docker.
	if cfg.DockerMode == "dind" && dindInst != nil {
		args = append(args, "--network", dindInst.NetworkName)
	}

	args = append(args, "--name", "construct-agent-"+sessionID)

	// On Windows, os.Getuid/os.Getgid return -1. Skip --user; Docker Desktop
	// runs containers through a Linux VM so host UID/GID mapping is not applicable.
	if uid >= 0 && gid >= 0 {
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, gid))
	}

	authorName, authorEmail, committerName, committerEmail := hostGitIdentity()
	args = append(args,
		// Persistent named volume for the agent home dir — isolated from the host
		// filesystem but preserved across sessions for history/config/caches.
		"-v", homeVolume+":/home/agent",
		"-e", "HOME=/home/agent",
		// Git identity — use the host user's real identity so commits are attributed
		// to the person who ran construct. Author and committer are read separately
		// so that a host with distinct GIT_AUTHOR_* / GIT_COMMITTER_* env vars is
		// faithfully mirrored.
		"-e", "GIT_AUTHOR_NAME="+authorName,
		"-e", "GIT_AUTHOR_EMAIL="+authorEmail,
		"-e", "GIT_COMMITTER_NAME="+committerName,
		"-e", "GIT_COMMITTER_EMAIL="+committerEmail,
	)

	// Set DOCKER_HOST and mount the Docker socket according to the requested mode.
	switch cfg.DockerMode {
	case "dind":
		// Docker-in-Docker: point at the dind sidecar via its network alias.
		args = append(args, "-e", "DOCKER_HOST="+dindInst.DockerHost())
	case "dood":
		// Docker-outside-of-Docker: bind-mount the host socket.
		// --security-opt label=disable disables SELinux confinement for this
		// container so the process can access the socket regardless of its
		// SELinux label (avoids permission denied on Fedora/RHEL hosts where
		// :z relabeling alone is insufficient for sockets).
		args = append(args,
			"--security-opt", "label=disable",
			"-v", "/var/run/docker.sock:/var/run/docker.sock",
			"-e", "DOCKER_HOST=unix:///var/run/docker.sock",
		)
		// Add the agent to the socket's group so it can reach the daemon
		// without root. The host docker group GID often differs from the GID
		// baked into the image, so we pass it explicitly via --group-add.
		if gid := dockerSocketGID(); gid != "" {
			args = append(args, "--group-add", gid)
		}
		// "none" (default): no DOCKER_HOST, no socket — agent has no Docker access.
	}

	// Always inject the docker mode so the entrypoint can write mode-appropriate
	// context into ~/.config/opencode/AGENTS.md.
	args = append(args, "-e", "CONSTRUCT_DOCKER_MODE="+cfg.DockerMode)

	args = append(args,
		"-v", cfg.RepoPath+":/workspace:z",
		"-w", "/workspace",
	)

	// Mount the host's global opencode commands directory read-only so the
	// agent can use slash commands the user has defined globally
	// (~/.config/opencode/commands/). Only opencode understands this path.
	// Skip silently when the directory does not exist on the host.
	if cfg.Tool.Name == "opencode" {
		if hostHome, err := os.UserHomeDir(); err == nil {
			hostCommandsDir := filepath.Join(hostHome, ".config", "opencode", "commands")
			if info, err := os.Stat(hostCommandsDir); err == nil && info.IsDir() {
				args = append(args, "-v", hostCommandsDir+":/home/agent/.config/opencode/commands:ro,z")
			}
		}
	}

	// Mount the global auth volume when the tool defines one. This volume is
	// NOT wiped by --reset so OAuth tokens survive across resets and repos.
	if authVolume != "" && cfg.Tool.AuthVolumePath != "" {
		args = append(args, "-v", authVolume+":"+cfg.Tool.AuthVolumePath)
	}

	// Bind-mount individual auth files (e.g. auth.json) from the host into the
	// container. Each file is created on the host by ensureAuthFile before the
	// container starts. Using :z applies the SELinux relabel suffix required on
	// Fedora/RHEL hosts.
	for _, af := range cfg.Tool.AuthFiles {
		args = append(args, "-v", af.HostPath+":"+af.ContainerPath+":z")
	}

	// Mount the secrets directory read-only at /run/secrets; the entrypoint
	// wrapper will export each file as an environment variable. This keeps
	// credential values out of docker inspect output (unlike -e KEY=val).
	if secretsDir != "" {
		args = append(args, "-v", secretsDir+":/run/secrets:ro,z")
	}

	// Inject extra env vars required by the tool (e.g. OPENCODE_PERMISSION for yolo mode).
	for k, v := range cfg.Tool.ExtraEnv {
		args = append(args, "-e", k+"="+v)
	}

	// Signal the entrypoint to write the MCP config when --mcp is set.
	if cfg.MCP {
		args = append(args, "-e", "CONSTRUCT_MCP=1")
	}

	// CONSTRUCT=1 is always set so the agent knows it is running inside construct.
	args = append(args, "-e", "CONSTRUCT=1")

	// Publish ports and advertise the container-side port list via CONSTRUCT_PORTS.
	// A bare port number (e.g. "3000") is expanded to "3000:3000" so the same
	// port number is used on both the host and the container, matching user
	// expectations when they pass --port 3000.
	if len(cfg.Ports) > 0 {
		containerPorts := make([]string, len(cfg.Ports))
		for i, p := range cfg.Ports {
			parts := strings.Split(p, ":")
			containerPorts[i] = parts[len(parts)-1]
			// Expand bare "N" to "N:N" so Docker maps host port N → container port N.
			if len(parts) == 1 {
				p = p + ":" + p
			}
			args = append(args, "-p", p)
		}
		args = append(args,
			"-e", "CONSTRUCT_PORTS="+strings.Join(containerPorts, ","),
		)
	}

	args = append(args, image)
	if cfg.Debug {
		args = append(args, "/bin/bash")
	} else {
		args = append(args, cfg.Tool.RunCmd...)
		args = append(args, cfg.ExtraArgs...)
	}
	return args
}

// buildServeArgs assembles the arguments for "docker run -d" that starts the
// opencode HTTP server inside the container. The container is run detached so
// the host can poll for readiness and then connect a local client.
func buildServeArgs(cfg *Config, dindInst *dind.Instance, image, sessionID, homeVolume, authVolume, secretsDir string, port int) []string {
	uid := os.Getuid()
	gid := os.Getgid()

	args := []string{"run", "--rm", "-d"}

	// Attach to the dind session network when using Docker-in-Docker.
	if cfg.DockerMode == "dind" && dindInst != nil {
		args = append(args, "--network", dindInst.NetworkName)
	}

	args = append(args, "--name", "construct-agent-"+sessionID)

	// Publish the serve port to loopback only so it is not exposed to the LAN.
	args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, port))

	if uid >= 0 && gid >= 0 {
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, gid))
	}

	authorName, authorEmail, committerName, committerEmail := hostGitIdentity()
	args = append(args,
		"-v", homeVolume+":/home/agent",
		"-e", "HOME=/home/agent",
		"-e", "GIT_AUTHOR_NAME="+authorName,
		"-e", "GIT_AUTHOR_EMAIL="+authorEmail,
		"-e", "GIT_COMMITTER_NAME="+committerName,
		"-e", "GIT_COMMITTER_EMAIL="+committerEmail,
	)

	switch cfg.DockerMode {
	case "dind":
		args = append(args, "-e", "DOCKER_HOST="+dindInst.DockerHost())
	case "dood":
		args = append(args,
			"--security-opt", "label=disable",
			"-v", "/var/run/docker.sock:/var/run/docker.sock",
			"-e", "DOCKER_HOST=unix:///var/run/docker.sock",
		)
		if gid := dockerSocketGID(); gid != "" {
			args = append(args, "--group-add", gid)
		}
	}

	args = append(args, "-e", "CONSTRUCT_DOCKER_MODE="+cfg.DockerMode)

	args = append(args,
		"-v", cfg.RepoPath+":/workspace:z",
		"-w", "/workspace",
	)

	if cfg.Tool.Name == "opencode" {
		if hostHome, err := os.UserHomeDir(); err == nil {
			hostCommandsDir := filepath.Join(hostHome, ".config", "opencode", "commands")
			if info, err := os.Stat(hostCommandsDir); err == nil && info.IsDir() {
				args = append(args, "-v", hostCommandsDir+":/home/agent/.config/opencode/commands:ro,z")
			}
		}
	}

	if authVolume != "" && cfg.Tool.AuthVolumePath != "" {
		args = append(args, "-v", authVolume+":"+cfg.Tool.AuthVolumePath)
	}

	// Bind-mount individual auth files (e.g. auth.json) from the host.
	for _, af := range cfg.Tool.AuthFiles {
		args = append(args, "-v", af.HostPath+":"+af.ContainerPath+":z")
	}

	if secretsDir != "" {
		args = append(args, "-v", secretsDir+":/run/secrets:ro,z")
	}

	for k, v := range cfg.Tool.ExtraEnv {
		args = append(args, "-e", k+"="+v)
	}

	if cfg.MCP {
		args = append(args, "-e", "CONSTRUCT_MCP=1")
	}

	args = append(args, "-e", "CONSTRUCT=1")
	// Expose the serve port to the entrypoint so it can document the server URL in AGENTS.md.
	args = append(args, "-e", fmt.Sprintf("CONSTRUCT_SERVE_PORT=%d", port))

	if len(cfg.Ports) > 0 {
		containerPorts := make([]string, len(cfg.Ports))
		for i, p := range cfg.Ports {
			parts := strings.Split(p, ":")
			containerPorts[i] = parts[len(parts)-1]
			if len(parts) == 1 {
				p = p + ":" + p
			}
			args = append(args, "-p", p)
		}
		args = append(args,
			"-e", "CONSTRUCT_PORTS="+strings.Join(containerPorts, ","),
		)
	}

	args = append(args, image)
	// Serve mode: run opencode serve with the configured hostname and port.
	args = append(args, "opencode", "serve", "--hostname", "0.0.0.0", "--port", fmt.Sprintf("%d", port))
	return args
}

// buildDebugArgs assembles the arguments for "docker run -it /bin/bash" used
// in debug mode. It is identical to the old buildRunArgs but always uses
// /bin/bash as the command.
func buildDebugArgs(cfg *Config, dindInst *dind.Instance, image, sessionID, homeVolume, authVolume, secretsDir string) []string {
	uid := os.Getuid()
	gid := os.Getgid()

	args := []string{"run", "--rm", "-it"}

	if cfg.DockerMode == "dind" && dindInst != nil {
		args = append(args, "--network", dindInst.NetworkName)
	}

	args = append(args, "--name", "construct-agent-"+sessionID)

	if uid >= 0 && gid >= 0 {
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, gid))
	}

	authorName, authorEmail, committerName, committerEmail := hostGitIdentity()
	args = append(args,
		"-v", homeVolume+":/home/agent",
		"-e", "HOME=/home/agent",
		"-e", "GIT_AUTHOR_NAME="+authorName,
		"-e", "GIT_AUTHOR_EMAIL="+authorEmail,
		"-e", "GIT_COMMITTER_NAME="+committerName,
		"-e", "GIT_COMMITTER_EMAIL="+committerEmail,
	)

	switch cfg.DockerMode {
	case "dind":
		args = append(args, "-e", "DOCKER_HOST="+dindInst.DockerHost())
	case "dood":
		args = append(args,
			"--security-opt", "label=disable",
			"-v", "/var/run/docker.sock:/var/run/docker.sock",
			"-e", "DOCKER_HOST=unix:///var/run/docker.sock",
		)
		if gid := dockerSocketGID(); gid != "" {
			args = append(args, "--group-add", gid)
		}
	}

	args = append(args, "-e", "CONSTRUCT_DOCKER_MODE="+cfg.DockerMode)

	args = append(args,
		"-v", cfg.RepoPath+":/workspace:z",
		"-w", "/workspace",
	)

	if cfg.Tool.Name == "opencode" {
		if hostHome, err := os.UserHomeDir(); err == nil {
			hostCommandsDir := filepath.Join(hostHome, ".config", "opencode", "commands")
			if info, err := os.Stat(hostCommandsDir); err == nil && info.IsDir() {
				args = append(args, "-v", hostCommandsDir+":/home/agent/.config/opencode/commands:ro,z")
			}
		}
	}

	if authVolume != "" && cfg.Tool.AuthVolumePath != "" {
		args = append(args, "-v", authVolume+":"+cfg.Tool.AuthVolumePath)
	}

	// Bind-mount individual auth files (e.g. auth.json) from the host.
	for _, af := range cfg.Tool.AuthFiles {
		args = append(args, "-v", af.HostPath+":"+af.ContainerPath+":z")
	}

	if secretsDir != "" {
		args = append(args, "-v", secretsDir+":/run/secrets:ro,z")
	}

	for k, v := range cfg.Tool.ExtraEnv {
		args = append(args, "-e", k+"="+v)
	}

	if cfg.MCP {
		args = append(args, "-e", "CONSTRUCT_MCP=1")
	}

	args = append(args, "-e", "CONSTRUCT=1")

	if len(cfg.Ports) > 0 {
		containerPorts := make([]string, len(cfg.Ports))
		for i, p := range cfg.Ports {
			parts := strings.Split(p, ":")
			containerPorts[i] = parts[len(parts)-1]
			if len(parts) == 1 {
				p = p + ":" + p
			}
			args = append(args, "-p", p)
		}
		args = append(args,
			"-e", "CONSTRUCT_PORTS="+strings.Join(containerPorts, ","),
		)
	}

	args = append(args, image, "/bin/bash")
	return args
}

// stopContainer stops and removes the named container. Errors are ignored
// because the container may already be stopped or removed.
func stopContainer(name string) {
	exec.Command("docker", "stop", name).Run()     //nolint:errcheck
	exec.Command("docker", "rm", "-f", name).Run() //nolint:errcheck
}

// waitForServer polls GET <serverURL>/global/health every 200ms until the
// response body contains {"healthy":true} or the timeout expires.
func waitForServer(serverURL string, timeout time.Duration) error {
	healthURL := serverURL + "/global/health"
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(healthURL)
		if err == nil {
			var body struct {
				Healthy bool `json:"healthy"`
			}
			if jerr := json.NewDecoder(resp.Body).Decode(&body); jerr == nil && body.Healthy {
				resp.Body.Close()
				return nil
			}
			resp.Body.Close()
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s waiting for %s", timeout, healthURL)
}

// containerLogs is an injectable function that returns the combined stdout+stderr
// of the named container via "docker logs". Returns an empty string if the
// command fails (e.g. container already removed).
var containerLogs = func(name string) string {
	out, err := exec.Command("docker", "logs", name).CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}

// printServeTimeoutDiagnostics writes container log output (if any) and
// recovery hints to w. It is called when the health-check times out so the
// user has actionable context instead of a bare timeout message.
func printServeTimeoutDiagnostics(w io.Writer, containerName string) {
	if logs := containerLogs(containerName); logs != "" {
		fmt.Fprintf(w, "\nconstruct: server did not become ready — container logs:\n")
		fmt.Fprintf(w, "--- begin container logs ---\n")
		fmt.Fprint(w, logs)
		if !strings.HasSuffix(logs, "\n") {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "--- end container logs ---\n")
	}
	fmt.Fprintf(w, "\nconstruct: hint: if opencode just updated, try:                construct --rebuild\n")
	fmt.Fprintf(w, "construct: hint: if the home volume is corrupt or stale, try: construct --reset\n")
	fmt.Fprintf(w, "construct: hint: to inspect the container interactively, try: construct --debug\n")
}

// runLocalAttach connects a local client to the opencode server according to
// the client selection:
//
//	""     (auto) — try "opencode attach <url>"; fall back to browser if not found
//	"tui"  — always use "opencode attach <url>"; error if opencode not on PATH
//	"web"  — always open browser directly, skip opencode check
func runLocalAttach(serverURL, client string) error {
	switch client {
	case "web":
		return openBrowser(serverURL)
	case "tui":
		path, err := exec.LookPath("opencode")
		if err != nil {
			return fmt.Errorf("opencode not found on PATH; install opencode or use --client web")
		}
		cmd := exec.Command(path, "attach", serverURL)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	default: // "" — auto-detect
		if path, err := exec.LookPath("opencode"); err == nil {
			cmd := exec.Command(path, "attach", serverURL)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
		// Fallback: open browser.
		return openBrowser(serverURL)
	}
}

// runLocalHeadless runs "opencode run --attach <url> <args...>" locally,
// streaming stdout/stderr so the user sees the task output.
func runLocalHeadless(serverURL string, args []string) error {
	path, err := exec.LookPath("opencode")
	if err != nil {
		return fmt.Errorf("opencode not found on PATH (required for headless mode): %w", err)
	}
	cmdArgs := append([]string{"run", "--attach", serverURL}, args...)
	cmd := exec.Command(path, cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// openBrowser opens the given URL in the system default browser and then blocks
// until the user presses Ctrl+C (the signal handler will clean up the container).
func openBrowser(url string) error {
	var browserCmd string
	switch {
	case commandExists("xdg-open"):
		browserCmd = "xdg-open"
	case commandExists("open"):
		browserCmd = "open"
	}

	if browserCmd != "" {
		fmt.Printf("construct: opening %s in your browser\n", url)
		exec.Command(browserCmd, url).Start() //nolint:errcheck
	} else {
		fmt.Printf("construct: opencode not found on PATH; open %s in your browser\n", url)
	}
	fmt.Println("construct: press Ctrl+C to stop the server")
	// Block until the signal handler fires and calls os.Exit.
	select {}
}

// commandExists returns true if the named command is available on $PATH.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// toolDockerfile generates the Dockerfile content for a tool image derived from
// stackImage. It is a separate function so tests can assert on the generated
// content without building a real Docker image.
func toolDockerfile(stackImage string, tool *tools.Tool) string {
	var sb strings.Builder
	sb.WriteString("FROM " + stackImage + "\n")
	sb.WriteString("USER root\n")
	for _, cmd := range tool.InstallCmds {
		sb.WriteString("RUN " + cmd + "\n")
	}
	sb.WriteString("COPY construct-entrypoint.sh /usr/local/bin/construct-entrypoint\n")
	sb.WriteString("RUN chmod +x /usr/local/bin/construct-entrypoint\n")
	sb.WriteString("USER agent\n")
	// Pre-create parent directories for any auth file bind-mounts so that Docker
	// does not create them as root-owned when mounting the file at runtime.
	// These dirs are created as the agent user so they have correct ownership
	// regardless of the home volume state.
	for _, af := range tool.AuthFiles {
		parentDir := filepath.Dir(af.ContainerPath)
		if parentDir != "" && parentDir != "." && parentDir != "/" {
			sb.WriteString("RUN mkdir -p " + parentDir + "\n")
		}
	}
	sb.WriteString("ENTRYPOINT [\"/usr/local/bin/construct-entrypoint\"]\n")
	return sb.String()
}

// buildToolImage creates a derived Docker image that installs the tool on top of the stack image.
func buildToolImage(toolImage, stackImage string, tool *tools.Tool) error {
	dir, err := os.MkdirTemp("", "construct-tool-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(toolDockerfile(stackImage, tool)), 0o644); err != nil {
		return err
	}

	// Write the entrypoint wrapper that exports /run/secrets/* as env vars
	// and optionally writes the opencode MCP config when CONSTRUCT_MCP=1.
	if err := os.WriteFile(filepath.Join(dir, "construct-entrypoint.sh"), []byte(generatedEntrypoint()), 0o755); err != nil {
		return err
	}

	cmd := exec.Command("docker", "build", "-t", toolImage, dir)
	if buildinfo.Version != "" {
		cmd = exec.Command("docker", "build",
			"--label", "io.construct.version="+buildinfo.Version,
			"-t", toolImage, dir)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// toolImageLabel is the function used to retrieve a Docker image label value
// for tool images. It is a variable so tests can substitute a fake without
// shelling out to Docker. Returns ("", error) when the image is not found.
var toolImageLabel = func(imageName, label string) (string, error) {
	out, err := exec.Command(
		"docker", "image", "inspect",
		"--format", `{{index .Config.Labels "`+label+`"}}`,
		imageName,
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// toolImageVersionCurrent returns true when the named tool image carries an
// io.construct.version label matching the running binary's version, or when
// buildinfo.Version is empty (dev build — skip the check).
// Returns false when the image was built by a different version or predates
// this feature (no label), triggering an automatic rebuild.
func toolImageVersionCurrent(name string) bool {
	if buildinfo.Version == "" {
		return true // dev build: never force a rebuild based on version
	}
	got, err := toolImageLabel(name, "io.construct.version")
	if err != nil {
		return false // image not found or inspect failed — treat as stale
	}
	return got == buildinfo.Version
}

// generatedEntrypoint returns the shell script baked into every tool image as
// /usr/local/bin/construct-entrypoint. It is a separate function so that tests
// can assert on its content without building a Docker image.
func generatedEntrypoint() string {
	return "#!/bin/sh\n" +
		"# Export any secrets mounted at /run/secrets/ as environment variables.\n" +
		"if [ -d /run/secrets ]; then\n" +
		"  for f in /run/secrets/*; do\n" +
		"    [ -f \"$f\" ] || continue\n" +
		"    export \"$(basename \"$f\")=$(cat \"$f\")\"\n" +
		"  done\n" +
		"fi\n" +
		"# Write opencode MCP config if --mcp was requested; delete it otherwise so\n" +
		"# that a persistent home volume does not carry a stale config from a previous\n" +
		"# run that used --mcp.\n" +
		"if [ \"${CONSTRUCT_MCP}\" = \"1\" ]; then\n" +
		"  mkdir -p \"${HOME}/.config/opencode\"\n" +
		"  cat > \"${HOME}/.config/opencode/opencode.json\" << 'MCPEOF'\n" +
		"{\n" +
		"  \"mcp\": {\n" +
		"    \"playwright\": {\n" +
		"      \"type\": \"local\",\n" +
		"      \"command\": [\"npx\", \"-y\", \"@playwright/mcp\", \"--browser\", \"chromium\"]\n" +
		"    }\n" +
		"  }\n" +
		"}\n" +
		"MCPEOF\n" +
		"else\n" +
		"  rm -f \"${HOME}/.config/opencode/opencode.json\"\n" +
		"fi\n" +
		"# CONSTRUCT=1 is always set, so always write ~/.config/opencode/AGENTS.md\n" +
		"# to inform the agent that it is running inside a construct container.\n" +
		"# The networking section depends on CONSTRUCT_DOCKER_MODE.\n" +
		"mkdir -p \"${HOME}/.config/opencode\"\n" +
		"cat > \"${HOME}/.config/opencode/AGENTS.md\" << 'AGENTSEOF'\n" +
		"# Construct container context\n" +
		"\n" +
		"You are running inside a construct container.\n" +
		"\n" +
		"## Workspace\n" +
		"\n" +
		"`/workspace` is the user's repository, bind-mounted from their machine.\n" +
		"Changes you make there are immediately visible to the user.\n" +
		"This is the only directory shared with the user.\n" +
		"\n" +
		"## Isolation\n" +
		"\n" +
		"Everything outside `/workspace` is isolated inside the container.\n" +
		"Your home directory (`/home/agent`) persists across sessions via a named Docker\n" +
		"volume, so shell history, tool caches, and config files survive container\n" +
		"restarts. The user's machine is separate — you cannot reach their localhost and\n" +
		"they cannot reach yours.\n" +
		"AGENTSEOF\n" +
		"if [ \"${CONSTRUCT_DOCKER_MODE}\" = \"dind\" ]; then\n" +
		"  cat >> \"${HOME}/.config/opencode/AGENTS.md\" << AGENTSEOF\n" +
		"\n" +
		"## Networking\n" +
		"\n" +
		"Docker runs on a separate sidecar host, not localhost. DOCKER_HOST is already set.\n" +
		"Containers you start are reachable at hostname **dind**, not 127.0.0.1.\n" +
		"Example: a Postgres container is at dind:5432, not localhost:5432.\n" +
		"\n" +
		"The user can access ports on this machine directly, but cannot reach ports inside Docker containers.\n" +
		"Run services on this machine (not in Docker containers) so the user can access them.\n" +
		"AGENTSEOF\n" +
		"elif [ \"${CONSTRUCT_DOCKER_MODE}\" = \"dood\" ]; then\n" +
		"  cat >> \"${HOME}/.config/opencode/AGENTS.md\" << AGENTSEOF\n" +
		"\n" +
		"## Networking\n" +
		"\n" +
		"Docker is available via the host socket (Docker-outside-of-Docker). DOCKER_HOST is already set.\n" +
		"Containers you start share the host Docker network and are reachable at 127.0.0.1 or their container name.\n" +
		"\n" +
		"The user can access ports on this machine directly, and containers that publish ports are also accessible.\n" +
		"AGENTSEOF\n" +
		"else\n" +
		"  cat >> \"${HOME}/.config/opencode/AGENTS.md\" << AGENTSEOF\n" +
		"\n" +
		"## Networking\n" +
		"\n" +
		"No Docker access is available in this container (--docker none).\n" +
		"Do not attempt to run Docker commands.\n" +
		"AGENTSEOF\n" +
		"fi\n" +
		"if [ -n \"${CONSTRUCT_PORTS}\" ]; then\n" +
		"  cat >> \"${HOME}/.config/opencode/AGENTS.md\" << AGENTSEOF\n" +
		"\n" +
		"## Server binding rules\n" +
		"\n" +
		"- Always bind servers to **0.0.0.0** (not 127.0.0.1 or localhost).\n" +
		"  The container network requires 0.0.0.0 for the host to reach the server.\n" +
		"- Use the port(s) listed in \\$CONSTRUCT_PORTS: **${CONSTRUCT_PORTS}**\n" +
		"- When the server is ready, print a clear message so the user knows to open\n" +
		"  their browser, e.g.: \"Server ready at http://localhost:${CONSTRUCT_PORTS}\"\n" +
		"AGENTSEOF\n" +
		"fi\n" +
		"if [ -n \"${CONSTRUCT_SERVE_PORT}\" ]; then\n" +
		"  cat >> \"${HOME}/.config/opencode/AGENTS.md\" << AGENTSEOF\n" +
		"\n" +
		"## opencode server\n" +
		"\n" +
		"This opencode instance is running in server mode on port ${CONSTRUCT_SERVE_PORT}.\n" +
		"The host connects to it via http://localhost:${CONSTRUCT_SERVE_PORT}.\n" +
		"AGENTSEOF\n" +
		"fi\n" +
		"exec \"$@\"\n"
}

// toolImageExists checks whether a Docker image is available locally.
func toolImageExists(name string) bool {
	out, err := exec.Command("docker", "images", "-q", name).Output()
	return err == nil && len(out) > 0
}

// hostGitIdentity reads the host user's git identity and returns separate
// author and committer name/email values. The resolution order mirrors git's
// own precedence:
//
//  1. GIT_AUTHOR_NAME / GIT_COMMITTER_NAME host environment variables
//  2. git config user.name (common fallback for both)
//  3. Resolved author value (committer falls back to author, as git does)
//  4. Synthetic fallback ("construct user" / "user@construct.local") with a
//     warning only when no identity at all is available on the host.
func hostGitIdentity() (authorName, authorEmail, committerName, committerEmail string) {
	// Base identity from git config (used when no per-role env var is set).
	nameOut, _ := exec.Command("git", "config", "user.name").Output()
	emailOut, _ := exec.Command("git", "config", "user.email").Output()
	baseName := strings.TrimSpace(string(nameOut))
	baseEmail := strings.TrimSpace(string(emailOut))

	// Per-role overrides from the host environment (same keys git itself reads).
	envAuthorName := os.Getenv("GIT_AUTHOR_NAME")
	envAuthorEmail := os.Getenv("GIT_AUTHOR_EMAIL")
	envCommitterName := os.Getenv("GIT_COMMITTER_NAME")
	envCommitterEmail := os.Getenv("GIT_COMMITTER_EMAIL")

	// Resolve author: env var > git config.
	authorName = first(envAuthorName, baseName)
	authorEmail = first(envAuthorEmail, baseEmail)

	// Resolve committer: env var > git config > author (git's own fallback).
	committerName = first(envCommitterName, baseName, authorName)
	committerEmail = first(envCommitterEmail, baseEmail, authorEmail)

	// Warn and apply synthetic fallback only when no author identity exists at all.
	if authorName == "" || authorEmail == "" {
		fmt.Fprintln(os.Stderr, "construct: warning: no git identity found on host")
		fmt.Fprintln(os.Stderr, `  run: git config --global user.name "Your Name"`)
		fmt.Fprintln(os.Stderr, `       git config --global user.email "you@example.com"`)
		fmt.Fprintln(os.Stderr, `  falling back to "construct user <user@construct.local>"`)
		if authorName == "" {
			authorName = "construct user"
		}
		if authorEmail == "" {
			authorEmail = "user@construct.local"
		}
		if committerName == "" {
			committerName = authorName
		}
		if committerEmail == "" {
			committerEmail = authorEmail
		}
	}
	return authorName, authorEmail, committerName, committerEmail
}

// first returns the first non-empty string from the provided values.
func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// loadEnv reads ~/.construct/.env then .construct/.env in the repo root,
// with the per-repo file taking precedence over the global one.
func loadEnv(repoPath string) (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	env := make(map[string]string)

	// Global env file.
	globalEnvFile := filepath.Join(home, ".construct", ".env")
	if err := mergeEnvFile(env, globalEnvFile); err != nil {
		return nil, err
	}

	// Per-repo env file overrides global.
	repoEnvFile := filepath.Join(repoPath, ".construct", ".env")
	if err := mergeEnvFile(env, repoEnvFile); err != nil {
		return nil, err
	}

	return env, nil
}

// mergeEnvFile parses a KEY=VALUE .env file into dst. Missing files are silently ignored.
func mergeEnvFile(dst map[string]string, path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Strip surrounding quotes (single or double).
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		dst[key] = value
	}
	return scanner.Err()
}

// homeVolumeName returns a deterministic Docker volume name for the given repo
// path and tool. Using a hash keeps the name short and Docker-safe while
// remaining unique per (repo, tool) pair.
func homeVolumeName(repoPath, toolName string) string {
	h := sha256.Sum256([]byte(repoPath))
	return "construct-home-" + toolName + "-" + hex.EncodeToString(h[:8])
}

// authVolumeName returns a deterministic Docker volume name for the tool's
// global auth state. Unlike homeVolumeName it is NOT keyed by repo path, so
// the same volume is shared across all repos and survives --reset.
func authVolumeName(toolName string) string {
	return "construct-auth-" + toolName
}

// ensureAuthVolume creates the named auth volume if it does not already exist.
// Unlike ensureHomeVolume it does not seed any files — the tool writes its own
// auth state at runtime — and ownership is set to the current user.
func ensureAuthVolume(volumeName string) error {
	out, err := exec.Command("docker", "volume", "inspect", "--format", "{{.Name}}", volumeName).Output()
	if err == nil && strings.TrimSpace(string(out)) == volumeName {
		return nil // already exists
	}

	if out, err := exec.Command("docker", "volume", "create",
		"--label", "io.construct.managed=true",
		volumeName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("create auth volume %s: %w\n%s", volumeName, err, string(out))
	}

	// Set ownership so the agent user can write to it.
	uid := os.Getuid()
	gid := os.Getgid()
	shellCmd := fmt.Sprintf("chown %d:%d /auth", uid, gid)
	if out, err := exec.Command("docker", "run", "--rm",
		"-v", volumeName+":/auth",
		"ubuntu:22.04",
		"sh", "-c", shellCmd,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("init auth volume: %w\n%s", err, string(out))
	}
	return nil
}

// ensureAuthFile creates the host-side file at hostPath if it does not already
// exist, including any parent directories. This ensures the file exists as a
// regular file before Docker bind-mounts it — Docker would create a directory
// at that path if the file were absent, breaking the mount.
// The file is created empty (0600) so the tool can write into it on first use.
func ensureAuthFile(hostPath string) error {
	dir := filepath.Dir(hostPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create auth file dir %s: %w", dir, err)
	}
	f, err := os.OpenFile(hostPath, os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil // already exists, nothing to do
		}
		return fmt.Errorf("create auth file %s: %w", hostPath, err)
	}
	f.Close()
	return nil
}

// removeHomeVolume removes the named Docker volume. It is a no-op when the
// volume does not exist, so callers need not check first.
func removeHomeVolume(volumeName string) error {
	out, err := exec.Command("docker", "volume", "rm", volumeName).CombinedOutput()
	if err != nil {
		// docker volume rm exits non-zero when the volume does not exist;
		// treat that as success.
		if strings.Contains(strings.ToLower(string(out)), "no such volume") {
			return nil
		}
		return fmt.Errorf("remove volume %s: %w\n%s", volumeName, err, string(out))
	}
	return nil
}

// ensureHomeVolume creates the named volume if it does not already exist,
// sets its ownership to the current user, and seeds any tool config files.
// authMountPath is the container path where the auth volume will be nested
// inside the home dir (e.g. /home/agent/.local/share/opencode). When set, the
// parent directories of that path are pre-created inside the home volume with
// correct ownership so that Docker's mount-point creation for the nested auth
// volume does not produce root-owned intermediate directories that would block
// the agent user from writing siblings (e.g. /home/agent/.local/state).
func ensureHomeVolume(volumeName, image string, seedFiles map[string]string, authMountPath string) error {
	// Check whether the volume already exists.
	out, err := exec.Command("docker", "volume", "inspect", "--format", "{{.Name}}", volumeName).Output()
	if err == nil && strings.TrimSpace(string(out)) == volumeName {
		return nil // already initialised
	}

	// Create the volume with a label so `docker volume prune` does not remove it.
	if out, err := exec.Command("docker", "volume", "create",
		"--label", "io.construct.managed=true",
		volumeName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("create volume %s: %w\n%s", volumeName, err, string(out))
	}

	// Write seed files to a temp dir so we can copy them into the volume.
	tmpDir, err := os.MkdirTemp("", "construct-home-seed-*")
	if err != nil {
		return fmt.Errorf("create seed temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	for relPath, content := range seedFiles {
		dst := filepath.Join(tmpDir, relPath)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return fmt.Errorf("create seed dir for %s: %w", relPath, err)
		}
		if err := os.WriteFile(dst, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write seed file %s: %w", relPath, err)
		}
	}

	// When the tool mounts an auth volume nested inside the home dir, pre-create
	// the parent directories so Docker's automatic mount-point creation for the
	// auth volume does not leave intermediate dirs as root-owned. For example,
	// with authMountPath=/home/agent/.local/share/opencode we pre-create
	// /home/agent/.local/share, ensuring /home/agent/.local is user-owned and
	// the agent can create siblings like /home/agent/.local/state.
	extraMkdir := ""
	const homePrefix = "/home/agent/"
	if authMountPath != "" && strings.HasPrefix(authMountPath, homePrefix) {
		parentDir := filepath.Dir(authMountPath)
		if parentDir != "/home/agent" {
			extraMkdir = " && mkdir -p " + parentDir
		}
	}

	// Initialise ownership and copy seed files using a single container.
	uid := os.Getuid()
	gid := os.Getgid()
	var shellCmd string
	if uid >= 0 && gid >= 0 {
		shellCmd = fmt.Sprintf(
			"chown %d:%d /home/agent%s && cp -r /seed/. /home/agent/ && chown -R %d:%d /home/agent",
			uid, gid, extraMkdir, uid, gid,
		)
	} else {
		// Windows: os.Getuid/os.Getgid return -1; Docker Desktop containers run
		// as root through a Linux VM, so skip chown and only copy seed files.
		shellCmd = "cp -r /seed/. /home/agent/" + extraMkdir
	}
	if out, err := exec.Command("docker", "run", "--rm",
		"-v", volumeName+":/home/agent",
		"-v", tmpDir+":/seed:ro,z",
		"ubuntu:22.04",
		"sh", "-c", shellCmd,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("init home volume: %w\n%s", err, string(out))
	}
	return nil
}

// generateSessionID returns a random 8-byte hex string suitable for naming containers.
func generateSessionID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeSecretFiles writes each auth credential to its own 0600 temp file inside
// a newly created temp directory. The caller is responsible for removing the
// directory (os.RemoveAll) when the container exits.
func writeSecretFiles(keys []string, env map[string]string) (string, error) {
	dir, err := os.MkdirTemp("", "construct-secrets-*")
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		val, ok := env[key]
		if !ok {
			continue
		}
		path := filepath.Join(dir, key)
		if err := os.WriteFile(path, []byte(val), 0o600); err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("write secret %s: %w", key, err)
		}
	}
	return dir, nil
}
