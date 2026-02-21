package runner

import (
	"bufio"
	"crypto/rand"
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

// Config holds all options needed to start an agentbox session.
type Config struct {
	Tool     *tools.Tool
	Stack    string
	RepoPath string
	Rebuild  bool
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

	// 4. Start the dind sidecar.
	fmt.Printf("agentbox: starting dind sidecar (session %s)…\n", sessionID)
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
			fmt.Println("\nagentbox: interrupted — cleaning up…")
			dindInst.Stop()
			os.Exit(1)
		case <-stopped:
		}
	}()
	defer func() {
		close(stopped)
		dindInst.Stop()
	}()

	// 5. Load environment variables (global then per-repo override).
	env, err := loadEnv(cfg.RepoPath)
	if err != nil {
		return fmt.Errorf("load env: %w", err)
	}

	// 6. Build the docker run argument list.
	args := buildRunArgs(cfg, dindInst, toolImage, sessionID, env)

	// 7. Run the agent container interactively.
	fmt.Printf("agentbox: launching %s in %s container…\n", cfg.Tool.Name, cfg.Stack)
	cmd := exec.Command("docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// buildRunArgs assembles the arguments for "docker run" that starts the agent.
func buildRunArgs(cfg *Config, d *dind.Instance, image, sessionID string, env map[string]string) []string {
	args := []string{
		"run", "--rm", "-it",
		"--name", "agentbox-agent-" + sessionID,
		"--network", d.NetworkName,
		"-e", "DOCKER_HOST=" + d.DockerHost(),
		"-v", cfg.RepoPath + ":/workspace",
		"-w", "/workspace",
	}

	// Inject tool-specific auth env vars from the loaded environment.
	for _, key := range cfg.Tool.AuthEnvVars {
		if val, ok := env[key]; ok {
			args = append(args, "-e", key+"="+val)
		}
	}

	// Inject extra env vars required by the tool (e.g. OPENCODE_PERMISSION for yolo mode).
	for k, v := range cfg.Tool.ExtraEnv {
		args = append(args, "-e", k+"="+v)
	}

	// Mount instruction files when present.
	args = append(args, instructionMounts(cfg.RepoPath, cfg.Tool.Name)...)

	args = append(args, image)
	args = append(args, cfg.Tool.RunCmd...)
	return args
}

// instructionMounts returns -v flags for any relevant instruction files found in the repo.
func instructionMounts(repoPath, toolName string) []string {
	var mounts []string

	copilotInstructions := filepath.Join(repoPath, ".github", "copilot-instructions.md")
	if _, err := os.Stat(copilotInstructions); err == nil {
		mounts = append(mounts, "-v", copilotInstructions+":/workspace/.github/copilot-instructions.md:ro")
	}

	agentboxInstructions := filepath.Join(repoPath, ".agentbox", "instructions.md")
	if _, err := os.Stat(agentboxInstructions); err == nil {
		switch toolName {
		case "copilot":
			mounts = append(mounts, "-v", agentboxInstructions+":/workspace/.github/copilot-instructions.md:ro")
		case "opencode":
			mounts = append(mounts, "-v", agentboxInstructions+":/workspace/.opencode/instructions.md:ro")
		}
	}

	return mounts
}

// buildToolImage creates a derived Docker image that installs the tool on top of the stack image.
func buildToolImage(toolImage, stackImage string, tool *tools.Tool) error {
	dir, err := os.MkdirTemp("", "agentbox-tool-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// Build a minimal Dockerfile that installs the tool.
	var sb strings.Builder
	sb.WriteString("FROM " + stackImage + "\n")
	sb.WriteString("USER root\n")
	for _, cmd := range tool.InstallCmds {
		sb.WriteString("RUN " + cmd + "\n")
	}
	sb.WriteString("USER agent\n")

	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(sb.String()), 0o644); err != nil {
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

// loadEnv reads ~/.agentbox/.env then .agentbox/.env in the repo root,
// with the per-repo file taking precedence over the global one.
func loadEnv(repoPath string) (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	env := make(map[string]string)

	// Global env file.
	globalEnvFile := filepath.Join(home, ".agentbox", ".env")
	if err := mergeEnvFile(env, globalEnvFile); err != nil {
		return nil, err
	}

	// Per-repo env file overrides global.
	repoEnvFile := filepath.Join(repoPath, ".agentbox", ".env")
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

// generateSessionID returns a random 8-byte hex string suitable for naming containers.
func generateSessionID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
