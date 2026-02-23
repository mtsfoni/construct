package tools

func init() {
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
		// AuthVolumePath points to the directory where opencode stores its OAuth tokens
		// and provider auth state (auth.json). Mounting a global named volume here means
		// GitHub OAuth tokens survive --reset and are shared across all repos, so the
		// user only needs to authenticate once per machine.
		AuthVolumePath: "/home/agent/.local/share/opencode",
		// MCP server config (@playwright/mcp) is NOT seeded here. It is written at
		// container startup by the entrypoint script when --mcp is passed, via the
		// CONSTRUCT_MCP=1 environment variable. See docs/spec/mcp-flag.md.
	})
}
