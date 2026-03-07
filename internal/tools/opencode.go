package tools

import (
	"os"
	"path/filepath"
)

func init() {
	home, _ := os.UserHomeDir()
	register(&Tool{
		Name: "opencode",
		InstallCmds: []string{
			"npm install -g opencode-ai",
		},
		// opencode supports multiple providers; pass whichever key the user has.
		AuthEnvVars: []string{
			"ANTHROPIC_API_KEY",
			"OPENAI_API_KEY",
		},
		RunCmd: []string{"opencode"},
		// OPENCODE_PERMISSION={"*":"allow"} grants auto-approval for all tool calls (yolo mode).
		ExtraEnv: map[string]string{
			"OPENCODE_PERMISSION":                          `{"*":"allow"}`,
			"OPENCODE_EXPERIMENTAL_DISABLE_COPY_ON_SELECT": "true",
		},
		// AuthFiles bind-mounts only the auth.json token file from the host, leaving
		// the rest of ~/.local/share/opencode/ (including opencode.db) inside the
		// per-repo home volume. This scopes session history to the current project
		// while keeping OAuth tokens global across repos and --reset.
		AuthFiles: authFilesForOpencode(home),
		// MCP server config (@playwright/mcp) is NOT seeded here. It is written at
		// container startup by the entrypoint script when --mcp is passed, via the
		// CONSTRUCT_MCP=1 environment variable. See docs/spec/mcp-flag.md.
	})
}

// authFilesForOpencode returns the AuthFiles list for opencode, using the
// provided home directory. An empty home is allowed (returns nil) so that
// tests can construct Tool values without a real home directory.
func authFilesForOpencode(home string) []AuthFile {
	if home == "" {
		return nil
	}
	return []AuthFile{
		{
			HostPath:      filepath.Join(home, ".construct", "opencode", "auth.json"),
			ContainerPath: "/home/agent/.local/share/opencode/auth.json",
		},
	}
}

// AuthFilesForOpencode is the exported form of authFilesForOpencode, used by
// runner tests to verify the expected host and container paths without
// importing internal implementation details.
func AuthFilesForOpencode(home string) []AuthFile {
	return authFilesForOpencode(home)
}
