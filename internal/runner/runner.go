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
}

// Run builds images, starts dind, and runs the agent container.
// It blocks until the container exits and then cleans up.
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
	if err := ensureHomeVolume(homVol, stacks.ImageName(cfg.Stack)+"-"+cfg.Tool.Name, cfg.Tool.HomeFiles); err != nil {
		return fmt.Errorf("ensure home volume: %w", err)
	}

	// 5. Start the dind sidecar.
	fmt.Printf("construct: starting dind sidecar (session %s)…\n", sessionID)
	dindInst, err := dind.Start(sessionID)
	if err != nil {
		return fmt.Errorf("start dind: %w", err)
	}

	// Always clean up on exit — even on SIGINT/SIGTERM.
	stopped := make(chan struct{})
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		select {
		case <-sigs:
			fmt.Println("\nconstruct: interrupted — cleaning up…")
			dindInst.Stop()
			os.Exit(1)
		case <-stopped:
		}
	}()
	defer func() {
		close(stopped)
		dindInst.Stop()
	}()

	// 6. Load environment variables (global then per-repo override).
	env, err := loadEnv(cfg.RepoPath)
	if err != nil {
		return fmt.Errorf("load env: %w", err)
	}

	// 7. Write per-credential secret files (cleaned up after the container exits).
	secretsDir, err := writeSecretFiles(cfg.Tool.AuthEnvVars, env)
	if err != nil {
		return fmt.Errorf("write secret files: %w", err)
	}
	defer os.RemoveAll(secretsDir)

	// 8. Build the docker run argument list.
	args := buildRunArgs(cfg, dindInst, toolImage, sessionID, homVol, secretsDir)

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
func buildRunArgs(cfg *Config, d *dind.Instance, image, sessionID, homeVolume, secretsDir string) []string {
	// Run as the host user so the bind-mounted workspace directory is accessible.
	uid := os.Getuid()
	gid := os.Getgid()

	args := []string{
		"run", "--rm", "-it",
		"--name", "construct-agent-" + sessionID,
		"--network", d.NetworkName,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		// Persistent named volume for the agent home dir — isolated from the host
		// filesystem but preserved across sessions for history/config/caches.
		"-v", homeVolume + ":/home/agent",
		"-e", "HOME=/home/agent",
		// Git identity — avoids "please tell me who you are" errors inside the container.
		"-e", "GIT_AUTHOR_NAME=construct agent",
		"-e", "GIT_AUTHOR_EMAIL=agent@construct.local",
		"-e", "GIT_COMMITTER_NAME=construct agent",
		"-e", "GIT_COMMITTER_EMAIL=agent@construct.local",
		"-e", "DOCKER_HOST=" + d.DockerHost(),
		"-v", cfg.RepoPath + ":/workspace:z",
		"-w", "/workspace",
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

	// Mount instruction files when present.
	args = append(args, instructionMounts(cfg.RepoPath, cfg.Tool.Name)...)

	args = append(args, image)
	if cfg.Debug {
		args = append(args, "/bin/bash")
	} else {
		args = append(args, cfg.Tool.RunCmd...)
	}
	return args
}

// instructionMounts returns -v flags for any relevant instruction files found in the repo.
func instructionMounts(repoPath, toolName string) []string {
	var mounts []string

	copilotInstructions := filepath.Join(repoPath, ".github", "copilot-instructions.md")
	if _, err := os.Stat(copilotInstructions); err == nil {
		mounts = append(mounts, "-v", copilotInstructions+":/workspace/.github/copilot-instructions.md:ro,z")
	}

	constructInstructions := filepath.Join(repoPath, ".construct", "instructions.md")
	if _, err := os.Stat(constructInstructions); err == nil {
		switch toolName {
		case "copilot":
			mounts = append(mounts, "-v", constructInstructions+":/workspace/.github/copilot-instructions.md:ro,z")
		case "opencode":
			mounts = append(mounts, "-v", constructInstructions+":/workspace/.opencode/instructions.md:ro,z")
		}
	}

	return mounts
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

	// Write the entrypoint wrapper that exports /run/secrets/* as env vars.
	entrypoint := "#!/bin/sh\n" +
		"# Export any secrets mounted at /run/secrets/ as environment variables.\n" +
		"if [ -d /run/secrets ]; then\n" +
		"  for f in /run/secrets/*; do\n" +
		"    [ -f \"$f\" ] || continue\n" +
		"    export \"$(basename \"$f\")=$(cat \"$f\")\"\n" +
		"  done\n" +
		"fi\n" +
		"exec \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "construct-entrypoint.sh"), []byte(entrypoint), 0o755); err != nil {
		return err
	}

	cmd := exec.Command("docker", "build", "-t", toolImage, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// toolImageExists checks whether a Docker image is available locally.
func toolImageExists(name string) bool {
	out, err := exec.Command("docker", "images", "-q", name).Output()
	return err == nil && len(out) > 0
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

// ensureHomeVolume creates the named volume if it does not already exist,
// sets its ownership to the current user, and seeds any tool config files.
func ensureHomeVolume(volumeName, image string, seedFiles map[string]string) error {
	// Check whether the volume already exists.
	out, err := exec.Command("docker", "volume", "inspect", "--format", "{{.Name}}", volumeName).Output()
	if err == nil && strings.TrimSpace(string(out)) == volumeName {
		return nil // already initialised
	}

	// Create the volume.
	if out, err := exec.Command("docker", "volume", "create", volumeName).CombinedOutput(); err != nil {
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

	// Initialise ownership and copy seed files using a single container.
	uid := os.Getuid()
	gid := os.Getgid()
	shellCmd := fmt.Sprintf(
		"chown %d:%d /home/agent && cp -r /seed/. /home/agent/ && chown -R %d:%d /home/agent",
		uid, gid, uid, gid,
	)
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
