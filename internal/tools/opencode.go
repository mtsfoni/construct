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
			"OPENCODE_PERMISSION": `{"*":"allow"}`,
		},
		// AuthVolumePath points to the directory where opencode stores its OAuth tokens
		// and provider auth state (auth.json). Mounting a global named volume here means
		// GitHub OAuth tokens survive --reset and are shared across all repos, so the
		// user only needs to authenticate once per machine.
		AuthVolumePath: "/home/agent/.local/share/opencode",
		// Seed an opencode config that registers @playwright/mcp as an MCP server.
		// The MCP server is started on demand by opencode when the agent needs browser
		// automation and exposes Playwright tools directly as MCP tool calls.
		HomeFiles: map[string]string{
			".config/opencode/opencode.json": `{
  "mcp": {
    "playwright": {
      "type": "local",
      "command": ["npx", "-y", "@playwright/mcp", "--browser", "chromium"]
    }
  }
}
`,
		},
	})
}
