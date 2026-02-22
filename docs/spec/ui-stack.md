# Spec: `ui` stack

## Problem

UI development benefits from a visual feedback loop: write a component,
see it rendered, iterate, then codify the accepted state as tests. The
existing stacks provide no browser, so agents working on front-end code
have no way to screenshot output or drive user interactions programmatically.

## Solution

Add a `ui` stack built on top of `construct-node` that pre-installs Chromium
and the `@playwright/mcp` package. OpenCode registers the MCP server via a
seeded `opencode.json` config; the server is started on demand and exposes
Playwright tools directly as MCP tool calls.

## Behaviour

```
construct --tool opencode --stack ui .
```

Produces a `construct-ui` Docker image that extends `construct-node` with:

- `@playwright/mcp` npm package installed globally.
- Chromium and all its system dependencies installed at build time to `/ms-playwright`
  using the playwright binary bundled inside `@playwright/mcp`.
- `ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright` baked in so every process finds
  the browser without any per-user configuration.

On first launch the agent's home volume is seeded with
`.config/opencode/opencode.json` which registers `@playwright/mcp` as an MCP
server. OpenCode starts it on demand when the agent needs browser automation.

## Dependency chain

`construct-ui` → `construct-node` → `construct-base`

`EnsureBuilt` resolves this via the `stackDeps` map in `internal/stacks/stacks.go`.
When building `ui`, both `construct-base` and `construct-node` are built first
(if not already cached).

## Agent instructions

`.construct/instructions.md` (in this repository) guides the agent on the
visual iteration workflow:

1. Start the dev server as a background process.
2. Use Playwright MCP tools to navigate and screenshot.
3. Screenshot immediately after writing or modifying a component.
4. Iterate visually until the component looks correct.
5. Generate Playwright tests from the verified working state.
6. Run the tests headlessly inside the container.

## Implementation

| File | Change |
|---|---|
| `internal/stacks/dockerfiles/ui/Dockerfile` | New — extends `construct-node`; installs `@playwright/mcp` globally and Chromium to `/ms-playwright` |
| `internal/stacks/stacks.go` | Added `"ui"` to `validStacks`; added `stackDeps` map; updated `EnsureBuilt` to resolve multi-level deps |
| `internal/stacks/stacks_test.go` | New — unit tests for `IsValid`, `All`, `ImageName`, `EnsureBuilt` error message, `stackDeps`, and embedded Dockerfile content |
| `internal/tools/opencode.go` | Seeds `.config/opencode/opencode.json` with MCP server config via `HomeFiles` |

## Non-goals

- No automatic version pinning; `npm install -g @playwright/mcp` always
  pulls the latest at image build time. Pin the version in the Dockerfile when
  stability is required.
- No VNC or display server; Playwright runs in headless mode only.
- No changes to the `copilot` tool; browser automation is opencode-specific.
