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
	})
}
