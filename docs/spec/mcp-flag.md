# Spec: `--mcp` flag

## Problem

`@playwright/mcp` is installed in the `ui` stack image, but the MCP server
config (`opencode.json`) was unconditionally seeded into the agent home volume
for every `opencode` invocation — regardless of stack. This meant that agents
running on `go`, `node`, `python`, or other non-`ui` stacks would always have
an MCP entry pointing at a package that isn't installed, causing opencode to
log errors on startup and attempt to start a server that cannot exist.

## Solution

Separate installation from activation:

- **Installation** is determined by the stack image. The `ui`, `dotnet-ui`, and
  `dotnet-big-ui` stacks install `@playwright/mcp` and Chromium.
- **Activation** is determined by the `--mcp` flag. Passing `--mcp` causes the
  entrypoint script to write `~/.config/opencode/opencode.json` at container
  startup, registering the MCP server with opencode.

This means MCP is opt-in and explicit rather than always-on.

## Behaviour

| Command | `@playwright/mcp` installed | MCP config written | Result |
|---|---|---|---|
| `--stack ui` | Yes | No (deleted if stale) | Clean; no MCP |
| `--stack ui --mcp` | Yes | Yes | MCP fully functional |
| `--stack dotnet-ui --mcp` | Yes | Yes | MCP fully functional |
| `--stack dotnet-big-ui --mcp` | Yes | Yes | MCP fully functional |
| `--stack go --mcp` | No | Yes | Config written; opencode logs MCP error but continues |
| `--stack go` | No | No (deleted if stale) | Clean; no MCP |

`--mcp` is a plain on/off switch. The stack determines what is installed;
the flag determines what is active. No warning is emitted for combining
`--mcp` with a non-`ui` stack — that is the user's choice.

Because the agent home directory is a persistent Docker volume (keyed by repo
path and tool name), a `opencode.json` written by a previous `--mcp` run would
survive into the next run even when `--mcp` is not passed. The entrypoint
therefore **always** manages the config file at startup:

- `CONSTRUCT_MCP=1` → write the file.
- `CONSTRUCT_MCP` unset → `rm -f` the file (no-op if absent).

## Implementation

### `--mcp` CLI flag

A new boolean flag `--mcp` is added to `runAgent`. When set it:

1. Prints a warning if `--stack` is not `ui`, `dotnet-ui`, or `dotnet-big-ui`.
2. Passes `MCP: true` to `runner.Config`.

### `runner.Config`

A new `MCP bool` field is added. When true, the runner injects
`CONSTRUCT_MCP=1` as an environment variable into the agent container via
`docker run -e`.

### Entrypoint script

The generated `construct-entrypoint.sh` (built into every tool image) gains a
block that runs before `exec "$@"`:

```sh
# Write opencode MCP config if --mcp was requested; delete it otherwise so
# that a persistent home volume does not carry a stale config from a previous
# run that used --mcp.
if [ "${CONSTRUCT_MCP}" = "1" ]; then
  mkdir -p "${HOME}/.config/opencode"
  cat > "${HOME}/.config/opencode/opencode.json" << 'MCPEOF'
{
  "mcp": {
    "playwright": {
      "type": "local",
      "command": ["npx", "-y", "@playwright/mcp", "--browser", "chromium"]
    }
  }
}
MCPEOF
else
  rm -f "${HOME}/.config/opencode/opencode.json"
fi
```

This runs on every container start, keeping the config in sync with the
`--mcp` flag regardless of what the persistent home volume contains. It
replaces the previous `HomeFiles`-based approach which wrote the config once on
home volume creation and could not be updated without `--reset`.

### `HomeFiles` removal

The `HomeFiles` map in `internal/tools/opencode.go` is removed. The MCP config
is no longer seeded via the home volume initialisation path.

## Files changed

| File | Change |
|---|---|
| `cmd/construct/main.go` | Add `--mcp` bool flag; warn if used without `--stack ui`, `--stack dotnet-ui`, or `--stack dotnet-big-ui`; pass to `runner.Config` |
| `internal/runner/runner.go` | Add `MCP bool` to `Config`; inject `CONSTRUCT_MCP=1` env var; extend generated entrypoint |
| `internal/tools/opencode.go` | Remove `HomeFiles` map |
| `docs/spec/playwright-mcp-config.md` | Updated to reference `--mcp` flag and entrypoint-based activation |
| `docs/spec/ui-stack.md` | Updated to reflect that MCP activation requires `--mcp` |
