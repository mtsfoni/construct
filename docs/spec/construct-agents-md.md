# Spec: Construct AGENTS.md injection

## Problem

Agents running inside construct have no way of knowing they are in a construct
container. When `--port` is used they also need to know to bind servers to
`0.0.0.0` and which ports to use. Without this context agents typically start
dev servers on `127.0.0.1:PORT`, which is unreachable from the host even
though the port is published.

## Solution

The construct entrypoint script **always** writes
`~/.config/opencode/AGENTS.md` at container start. Because `CONSTRUCT=1` is
always injected by the runner, the file is always present and always tells the
agent it is inside construct.

When `--port` is also used, `CONSTRUCT_PORTS` is set and the entrypoint
appends port-binding rules to the file.

Opencode reads `~/.config/opencode/AGENTS.md` as **global rules** that apply
to every session, giving the agent the context it needs without modifying the
workspace.

## Behaviour

### Always (every construct session)

The entrypoint writes `~/.config/opencode/AGENTS.md` with at minimum:

```markdown
# Construct container context

You are running inside a construct container.

## Workspace

`/workspace` is the user's repository, bind-mounted from their machine.
Changes you make there are immediately visible to the user.
This is the only directory shared with the user.

## Isolation

Everything outside `/workspace` is isolated inside the container.
Your home directory (`/home/agent`) persists across sessions via a named Docker
volume, so shell history, tool caches, and config files survive container
restarts. The user's machine is separate — you cannot reach their localhost and
they cannot reach yours.
```

The networking section (see below) is appended after this block.

### When `--port` is used (`CONSTRUCT_PORTS` is set)

The entrypoint appends port-binding rules:

```markdown

## Server binding rules

- Always bind servers to **0.0.0.0** (not 127.0.0.1 or localhost).
  The container network requires 0.0.0.0 for the host to reach the server.
- Use the port(s) listed in $CONSTRUCT_PORTS: **3000**
- When the server is ready, print a clear message so the user knows to open
  their browser, e.g.: "Server ready at http://localhost:3000"
```

The `${CONSTRUCT_PORTS}` value is expanded at container start, so the agent
sees the actual port numbers (e.g. `3000` or `3000,8080`).

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

## Files changed

| File | Change |
|---|---|
| `internal/runner/runner.go` | `CONSTRUCT=1` always injected; `generatedEntrypoint()` always writes `~/.config/opencode/AGENTS.md`, appending port rules only when `CONSTRUCT_PORTS` is set |
| `internal/runner/runner_test.go` | Tests updated to reflect always-present `CONSTRUCT=1` and always-present `AGENTS.md` |
| `docs/spec/construct-agents-md.md` | This document |

