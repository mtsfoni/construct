# Playwright MCP Config

## Problem

The agent needs browser automation capabilities when running on the `ui` stack.

## Solution

Seed an `opencode.json` config into the agent's home volume that registers
`@playwright/mcp` as an MCP server. OpenCode starts the MCP server on demand
and exposes Playwright tools directly as MCP tool calls.

## Behaviour

### ui stack image

`construct --tool opencode --stack ui .`

Produces a `construct-ui` Docker image that extends `construct-node` with:

- `@playwright/mcp` npm package installed globally.
- Chromium installed to `/ms-playwright` (fixed path, world-readable).
- `ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright` baked in.

### Config seeding

On first launch with a new home volume, `ensureHomeVolume` writes
`.config/opencode/opencode.json` into `/home/agent/`:

```json
{
  "mcp": {
    "playwright": {
      "type": "local",
      "command": ["npx", "-y", "@playwright/mcp"]
    }
  }
}
```

### Migrating home volumes

`HomeFiles` are written only when a volume is created for the first time. Run
once with `--reset` to wipe and re-seed the home volume with the new config:

```bash
construct --tool opencode --stack ui --reset .
```

## Files changed

| File | Change |
|---|---|
| `internal/stacks/dockerfiles/ui/Dockerfile` | Install `@playwright/mcp` globally; install Chromium to fixed path |
| `internal/tools/opencode.go` | Seed `.config/opencode/opencode.json` with MCP server config |
