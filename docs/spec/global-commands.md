# Global opencode slash commands in construct

## Problem

OpenCode loads custom slash commands from two locations:

- **Per-project:** `.opencode/commands/` in the repo root (already works — the repo is bind-mounted at `/workspace`)
- **Global:** `~/.config/opencode/commands/` in the user's home directory

The construct agent container has an isolated named Docker volume for `/home/agent`, so the host's `~/.config/opencode/commands/` directory is never visible to the agent. Global slash commands defined on the host are silently absent.

## Solution

When starting the agent container, bind-mount the host's `~/.config/opencode/commands/` directory into the container at `/home/agent/.config/opencode/commands/` — read-only — if and only if:

1. The current tool is `opencode` (this is an opencode-specific path convention), and
2. The directory exists on the host (no-op when the user has not created it).

## Behaviour

- **Directory exists on host:** the commands are mounted read-only at `/home/agent/.config/opencode/commands/` and OpenCode picks them up automatically at startup. The agent can invoke them as `/command-name` exactly as it would on the host.
- **Directory absent on host:** no mount is added; behaviour is unchanged from before.
- **Tool is not opencode:** no mount is added regardless of directory existence.
- **Read-only:** the agent cannot write back to the host config directory. Per-project commands (`.opencode/commands/`) remain writable as part of the workspace mount.
- **SELinux:** the mount uses `:ro,z` so hosts running SELinux (Fedora, RHEL, etc.) grant the container access.

## Mount conflict

The home directory is a named Docker volume mounted at `/home/agent`. Bind-mounting a subpath (`/home/agent/.config/opencode/commands/`) shadows that specific path inside the volume — this is standard Docker behaviour. The entrypoint script writes `~/.config/opencode/AGENTS.md` and optionally `~/.config/opencode/opencode.json` but never touches `commands/`, so there is no conflict.

## No new flags

This is always-on for the opencode tool when the host directory exists. No opt-in flag is needed.

## Files changed

| File | Change |
|---|---|
| `docs/spec/global-commands.md` | This spec |
| `internal/runner/runner.go` | Add conditional bind mount in `buildRunArgs` |
| `internal/runner/runner_test.go` | Unit tests: mount present/absent/tool-gated |
| `CHANGELOG.md` | Entry under `[Unreleased]` |
