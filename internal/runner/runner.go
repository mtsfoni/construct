package runner

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

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
}

// Run builds images, starts any requested Docker sidecar, and runs the agent
// container. It blocks until the container exits and then cleans up.
func Run(cfg *Config) error {
	// 1. Ensure the stack image exists (build if needed).
	if err := stacks.EnsureBuilt(cfg.Stack, cfg.Rebuild); err != nil {
		return err
	}

	// 2. Ensure the tool image (derived from the stack) exists.
	toolImage := stacks.ImageName(cfg.Stack) + "-" + cfg.Tool.Name
	if cfg.Rebuild || !toolImageExists(toolImage) {
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

	// 8. Build the docker run argument list.
	args := buildRunArgs(cfg, dindInst, toolImage, sessionID, homVol, authVol, secretsDir)

	// 9. Run the agent container interactively.

	if cfg.Debug {
		fmt.Printf("construct: debug mode — starting shell in %s container (no agent)…\n", cfg.Stack)
	} else {
		fmt.Printf("construct: launching %s in %s container…\n", cfg.Tool.Name, cfg.Stack)
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
		// faithfully mirrored. The commit-msg hook (written by the entrypoint)
		// appends a "Generated by construct" trailer to every message.
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
		args = append(args,
			"-v", "/var/run/docker.sock:/var/run/docker.sock",
			"-e", "DOCKER_HOST=unix:///var/run/docker.sock",
		)
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
	}
	return args
}

// buildToolImage creates a derived Docker image that installs the tool on top of the stack image.
func buildToolImage(toolImage, stackImage string, tool *tools.Tool) error {
	dir, err := os.MkdirTemp("", "construct-tool-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// Build a minimal Dockerfile that installs the tool and adds the entrypoint wrapper.
	var sb strings.Builder
	sb.WriteString("FROM " + stackImage + "\n")
	sb.WriteString("USER root\n")
	for _, cmd := range tool.InstallCmds {
		sb.WriteString("RUN " + cmd + "\n")
	}
	sb.WriteString("COPY construct-entrypoint.sh /usr/local/bin/construct-entrypoint\n")
	sb.WriteString("RUN chmod +x /usr/local/bin/construct-entrypoint\n")
	sb.WriteString("USER agent\n")
	sb.WriteString("ENTRYPOINT [\"/usr/local/bin/construct-entrypoint\"]\n")

	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(sb.String()), 0o644); err != nil {
		return err
	}

	// Write the entrypoint wrapper that exports /run/secrets/* as env vars
	// and optionally writes the opencode MCP config when CONSTRUCT_MCP=1.
	if err := os.WriteFile(filepath.Join(dir, "construct-entrypoint.sh"), []byte(generatedEntrypoint()), 0o755); err != nil {
		return err
	}

	cmd := exec.Command("docker", "build", "-t", toolImage, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
		"cat > \"${HOME}/.config/opencode/AGENTS.md\" << AGENTSEOF\n" +
		"# Construct container context\n" +
		"\n" +
		"You are running inside a construct container.\n" +
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
		"# Set up a global commit-msg hook that appends a 'Generated by construct'\n" +
		"# git trailer to every commit message. The hook is idempotent: it checks for\n" +
		"# the trailer before appending so amend/rebase does not produce duplicates.\n" +
		"mkdir -p \"${HOME}/.githooks\"\n" +
		"cat > \"${HOME}/.githooks/commit-msg\" << 'HOOKEOF'\n" +
		"#!/bin/sh\n" +
		"trailer='Generated by construct'\n" +
		"if ! grep -qF \"$trailer\" \"$1\"; then\n" +
		"  printf '\\n%s\\n' \"$trailer\" >> \"$1\"\n" +
		"fi\n" +
		"HOOKEOF\n" +
		"chmod +x \"${HOME}/.githooks/commit-msg\"\n" +
		"git config --global core.hooksPath \"${HOME}/.githooks\"\n" +
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
