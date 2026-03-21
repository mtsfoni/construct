// Package config handles host opencode config path resolution and generation
// of the construct-injected AGENTS.md file (construct-agents.md).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenCodeConfigDir returns the host opencode configuration directory path,
// respecting the XDG_CONFIG_HOME convention.
func OpenCodeConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".config", "opencode")
	}
	return filepath.Join(home, ".config", "opencode")
}

// OpenCodeDataDir returns the host opencode data directory path,
// respecting the XDG_DATA_HOME convention. opencode writes auth.json here.
func OpenCodeDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".local", "share", "opencode")
	}
	return filepath.Join(home, ".local", "share", "opencode")
}

// ConstructConfigDir returns the construct configuration/state directory path,
// respecting the XDG_CONFIG_HOME convention.
func ConstructConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "construct")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".config", "construct")
	}
	return filepath.Join(home, ".config", "construct")
}

// AgentsParams holds the parameters used to generate the construct-agents.md file.
type AgentsParams struct {
	SessionID  string
	Repo       string
	Tool       string
	Stack      string
	DockerMode string // "none", "dind", "dood"
	Ports      []PortMapping
	WebPort    int
}

// PortMapping represents a published port.
type PortMapping struct {
	HostPort      int
	ContainerPort int
}

// GenerateAgentsMD generates the content of the construct-agents.md file
// for a session.
func GenerateAgentsMD(p AgentsParams) string {
	var sb strings.Builder

	sb.WriteString("# construct session context\n\n")
	sb.WriteString("You are running inside a **construct** container. This document describes your environment.\n\n")

	sb.WriteString("## Container environment\n\n")
	fmt.Fprintf(&sb, "- **Repo path:** `%s` (same path as on the host — no normalization)\n", p.Repo)
	fmt.Fprintf(&sb, "- **Tool:** %s\n", p.Tool)
	fmt.Fprintf(&sb, "- **Stack:** %s\n", p.Stack)
	fmt.Fprintf(&sb, "- **Docker mode:** %s\n", p.DockerMode)
	sb.WriteString("\n")

	sb.WriteString("## Filesystem\n\n")
	sb.WriteString("Your working directory is the repo path above. All file operations should use this exact path.\n")
	sb.WriteString("You have read-write access to the repo. You do **not** have access to any other host paths.\n\n")

	sb.WriteString("## Docker access\n\n")
	switch p.DockerMode {
	case "dind":
		sb.WriteString("You have access to a private Docker daemon (Docker-in-Docker). The `DOCKER_HOST` environment variable\n")
		sb.WriteString("points to it. You can build images, run containers, etc. You **cannot** see host containers or images.\n")
	case "dood":
		sb.WriteString("**Warning:** You have access to the **host Docker daemon** (Docker-outside-of-Docker). Be careful —\n")
		sb.WriteString("you can see and affect host containers and images.\n")
	default:
		sb.WriteString("You do **not** have access to Docker. If you need Docker, restart the session with `--docker dind`.\n")
	}
	sb.WriteString("\n")

	if len(p.Ports) > 0 {
		sb.WriteString("## Port forwarding\n\n")
		sb.WriteString("The following container ports are published to the host:\n\n")
		for _, port := range p.Ports {
			fmt.Fprintf(&sb, "- Container port `%d` → Host port `%d`\n", port.ContainerPort, port.HostPort)
		}
		sb.WriteString("\nWhen starting a dev server or any service that should be accessible from the host:\n")
		sb.WriteString("- Bind to `0.0.0.0` (not `127.0.0.1` or `localhost`)\n")
		sb.WriteString("- Use the **container port** number from the list above\n\n")
	}

	sb.WriteString("## Tool installation\n\n")
	sb.WriteString("To install tools that persist between sessions, install them to `/agent/bin` or use standard\n")
	sb.WriteString("package managers (they auto-install to the persistent `/agent` layer):\n\n")
	sb.WriteString("- `npm install -g <pkg>` — installs to `/agent/lib/node_modules/.bin` (on PATH)\n")
	sb.WriteString("- `pip install --user <pkg>` — installs to `/agent/home/.local/bin`\n")
	sb.WriteString("- `go install <pkg>` — installs to `/agent/lib/go/bin`\n")
	sb.WriteString("- For other tools: install to `/agent/bin/` directly\n\n")
	sb.WriteString("Installed tools **survive container restarts and stack image rebuilds**.\n\n")

	sb.WriteString("## Auth and credentials\n\n")
	sb.WriteString("Credentials (API keys, tokens) are injected as environment variables from bind-mounted files.\n")
	sb.WriteString("The opencode auth file (`~/.local/share/opencode/auth.json` on the host) is bind-mounted\n")
	sb.WriteString("read-write, so tokens written by an interactive auth flow (e.g. `/connect`) persist to\n")
	sb.WriteString("the host and are shared across all sessions automatically.\n")

	return sb.String()
}

// WriteAgentsMD writes the construct-agents.md file to the given directory
// and chowns both the directory and the file to uid:gid so the host user
// (not the daemon's root) owns them and can remove them on purge/destroy.
// Chown errors are ignored when the caller lacks permission (e.g. in unit
// tests running as a non-root user with a foreign uid/gid).
func WriteAgentsMD(dir string, p AgentsParams, uid, gid int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	_ = os.Chown(dir, uid, gid) //nolint:errcheck
	content := GenerateAgentsMD(p)
	path := filepath.Join(dir, "construct-agents.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	_ = os.Chown(path, uid, gid) //nolint:errcheck
	return nil
}
