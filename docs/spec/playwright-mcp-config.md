# Playwright MCP Config

## Problem

The agent needs browser automation capabilities when running on the `ui` stack.
`@playwright/mcp` must be installed in the image **and** the MCP server must be
registered with opencode before it can be used.

## Solution

Separate installation from activation:

- The `ui`, `dotnet-ui`, and `dotnet-big-ui` stack images install `@playwright/mcp` and Chromium at build time.
- Passing `--mcp` on the CLI causes the entrypoint script to write
  `~/.config/opencode/opencode.json` at container startup, registering the MCP
  server with opencode.

See [`docs/spec/mcp-flag.md`](mcp-flag.md) for the full specification of the
`--mcp` flag.

## Behaviour

### ui stack image

`construct --stack ui --mcp .`

Produces a `construct-ui` Docker image that extends `construct-base` with:

- `@playwright/mcp` npm package installed globally.
- Chromium installed to `/ms-playwright` (fixed path, world-readable).
- `ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright` baked in.

### Config activation

When `--mcp` is passed, the entrypoint script writes
`~/.config/opencode/opencode.json` before the agent starts:

```json
{
  "mcp": {
    "playwright": {
      "type": "local",
      "command": ["npx", "-y", "@playwright/mcp", "--browser", "chromium"]
    }
  }
}
```

The file is written on every container start, so it is always fresh. No
`--reset` is needed when the config changes.

### Migrating from home-volume-seeded config

Earlier versions seeded `opencode.json` via `HomeFiles` on first home volume
creation. To remove the stale file from an existing volume, run once with
`--reset`:

```bash
construct --stack ui --reset .
```

## Files changed

| File | Change |
|---|---|
| `internal/stacks/dockerfiles/ui/Dockerfile` | Install `@playwright/mcp` globally; install Chromium to fixed path |
| `internal/tools/opencode.go` | Remove `HomeFiles` — MCP config written by entrypoint, not home volume seeding |
| `internal/runner/runner.go` | Inject `CONSTRUCT_MCP=1`; extend entrypoint to write config when set |
| `cmd/construct/main.go` | `--mcp` flag controls activation |
