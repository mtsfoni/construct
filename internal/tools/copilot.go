package tools

func init() {
	register(&Tool{
		Name: "copilot",
		InstallCmds: []string{
			"npm install -g @github/copilot",
		},
		AuthEnvVars: []string{"GH_TOKEN"},
		RunCmd:      []string{"copilot", "--yolo"},
		ExtraEnv:    map[string]string{},
		HomeFiles: map[string]string{
			".copilot/config.json": `{
  "banner": "never",
  "render_markdown": true,
  "theme": "auto",
  "trusted_folders": [
    "/workspace"
  ]
}
`,
		},
	})
}
