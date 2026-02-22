# Spec: Global Auth Volume

## Problem

opencode stores its OAuth tokens and provider auth state in
`~/.local/share/opencode/auth.json` (XDG data dir). Inside the container that
resolves to `/home/agent/.local/share/opencode/auth.json`.

The home volume (`construct-home-opencode-<hash>`) is keyed by repo path, so:

1. Running `--reset` wipes the home volume and deletes `auth.json` → the user
   must re-authenticate on the next run.
2. Opening a different repo creates a fresh home volume with no `auth.json` →
   the user must authenticate again.

Both cases are frustrating when the auth flow requires a browser-based OAuth
round-trip (e.g. connecting to GitHub).

## Solution

Add an `AuthVolumePath` field to `Tool`. When non-empty, the runner creates a
**global named Docker volume** (`construct-auth-<tool>`) and mounts it at
`AuthVolumePath` inside the container. This volume is:

- **Not keyed by repo** — the same volume is used regardless of which repo is
  being worked on.
- **Not wiped by `--reset`** — `--reset` only removes the per-repo home volume.
- **Labelled** `io.construct.managed=true` — consistent with home volumes so
  `docker volume prune` does not remove it.

For opencode, `AuthVolumePath` is set to `/home/agent/.local/share/opencode`.
Docker mounts the global auth volume at that path, which shadows the
corresponding subdirectory inside the home volume. opencode writes `auth.json`
into the auth volume; subsequent runs (on any repo, after any `--reset`) find
the token already present.

## Persistence details

| Volume | Name pattern | Keyed by | Wiped by `--reset` |
|--------|-------------|----------|--------------------|
| Home | `construct-home-<tool>-<8-hex>` | repo path + tool | yes |
| Auth | `construct-auth-<tool>` | tool only | no |

## Files changed

| File | Change |
|------|--------|
| `internal/tools/tools.go` | Add `AuthVolumePath string` field to `Tool` struct |
| `internal/tools/opencode.go` | Set `AuthVolumePath: "/home/agent/.local/share/opencode"` |
| `internal/runner/runner.go` | Add `authVolumeName`, `ensureAuthVolume`; thread auth volume through `Run` and `buildRunArgs` |
| `internal/runner/runner_test.go` | Tests for `authVolumeName`, `buildRunArgs` auth volume mounting, `ensureAuthVolume` label and idempotency |
| `docs/spec/auth-volume.md` | This document |
