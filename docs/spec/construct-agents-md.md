# Spec: Construct AGENTS.md injection

## Problem

When `--port` is used, construct injects `CONSTRUCT=1` and `CONSTRUCT_PORTS` into
the container so the agent knows port forwarding is active. But without additional
context the agent has no instructions telling it to:

- Bind servers to `0.0.0.0` (not `127.0.0.1` / `localhost`)
- Use the specific port numbers in `CONSTRUCT_PORTS`
- Print a ready message so the user knows to open their browser

The result is that agents typically start dev servers on `127.0.0.1:PORT`, which
is unreachable from the host even though the port is published.

## Solution

The construct entrypoint script writes port-binding instructions to
`~/.config/opencode/AGENTS.md` at container start when `CONSTRUCT=1` is set.
Opencode reads this file as **global rules** that apply to every session, giving
the agent the context it needs without modifying the workspace.

When `CONSTRUCT` is not set, the entrypoint deletes the file so a stale copy
from a previous `--port` session does not linger in the persistent home volume.

## Behaviour

### When `--port` is used (`CONSTRUCT=1`)

The entrypoint writes `~/.config/opencode/AGENTS.md` with content like:

```markdown
# Construct container context

You are running inside a construct container with port forwarding enabled.

## Server binding rules

- Always bind servers to **0.0.0.0** (not 127.0.0.1 or localhost).
  The container network requires 0.0.0.0 for the host to reach the server.
- Use the port(s) listed in $CONSTRUCT_PORTS: **3000**
- When the server is ready, print a clear message so the user knows to open
  their browser, e.g.: "Server ready at http://localhost:3000"
```

The `${CONSTRUCT_PORTS}` value is expanded at container start, so the agent
sees the actual port numbers (e.g. `3000` or `3000,8080`).

### When `--port` is not used (`CONSTRUCT` unset)

The entrypoint runs: `rm -f "${HOME}/.config/opencode/AGENTS.md"`

This ensures the file is always consistent with the current invocation, even
across sessions that alternate between using and not using `--port`.

## Why `~/.config/opencode/AGENTS.md`

Opencode reads `~/.config/opencode/AGENTS.md` as its **global rules file**,
which is included in every session's context regardless of the project.
Writing here:

- Does not modify the user's workspace (no git diff, no side effects)
- Is picked up automatically — no config flag needed
- Is ephemeral per session (rewritten on every container start)
- Works with the persistent home volume: the file is always in sync because
  the entrypoint runs before the agent starts

## Precedence with project AGENTS.md

Opencode merges global and project-level rule files. The global
`~/.config/opencode/AGENTS.md` written by construct is **additive** — it does
not override a project-level `AGENTS.md` or `CLAUDE.md` in the workspace.

## Copilot and other tools

The global AGENTS.md at `~/.config/opencode/AGENTS.md` is opencode-specific.
Copilot does not read this file. If agent awareness is needed for copilot in a
future iteration, the same instructions could be injected via copilot's
`~/.copilot/config.json` or a separate mechanism.

## Files changed

| File | Change |
|---|---|
| `internal/runner/runner.go` | `generatedEntrypoint()` now writes/removes `~/.config/opencode/AGENTS.md` based on `CONSTRUCT` env var |
| `internal/runner/runner_test.go` | 4 new tests: unit tests for entrypoint content, container integration tests for file presence/content/absence |
| `docs/spec/construct-agents-md.md` | This document |
