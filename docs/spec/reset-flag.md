# --reset Flag — Spec

## Problem

`Tool.HomeFiles` are seeded into the agent's home volume exactly once — when `ensureHomeVolume` creates the volume for the first time. If `HomeFiles` are updated (e.g. when skill files change), existing volumes are never touched. Users with old volumes must manually identify and remove them with Docker commands before the new defaults take effect.

`--reset` is the user-facing escape hatch: a single flag that wipes the home volume for the current (repo, tool) pair and forces a clean re-seed on the next start.

## Solution

### New `--reset` flag

```
construct --stack ui --reset /path/to/repo
```

When `--reset` is passed, `runner.Run` removes the named home volume before calling `ensureHomeVolume`. `ensureHomeVolume` then creates a fresh volume and re-seeds all `HomeFiles` as if it were the first launch.

The rest of the run proceeds normally — images are not rebuilt unless `--rebuild` is also passed.

### Warning

`--reset` permanently deletes all agent state for the (repo, tool) pair, including:

- Shell history and tool caches
- Auth tokens stored by the tool (e.g. `gh auth login` tokens, API keys written by the agent)
- Any runtime modifications the agent made to its config

Users who need to preserve this data should back up the volume first (`docker run --rm -v <vol>:/src ubuntu tar cz /src > backup.tar.gz`).

## Persistence details

Home volumes are named `construct-home-<toolname>-<sha256(repoPath)[:8]>`, one per (repo, tool) pair. `--reset` targets exactly this volume; no other volumes are affected.

`removeHomeVolume` calls `docker volume rm <name>`. If the volume does not exist (e.g. very first run with `--reset`), the call is a no-op.

## Out of scope

General automatic migration of existing home volumes when `HomeFiles` change is deferred for a separate future effort. `--reset` is the intentional manual escape hatch.

## Files changed

| File | Change |
|---|---|
| `internal/runner/runner.go` | Add `Reset bool` to `Config`; add `removeHomeVolume` helper; call it in `Run` when `Reset` is set |
| `cmd/construct/main.go` | Add `--reset` flag to `runAgent`; pass `Reset` to `runner.Config`; update usage string |
| `internal/runner/runner_test.go` | Add `TestRemoveHomeVolume_RemovesVolume` and `TestRemoveHomeVolume_NoopWhenAbsent` |
| `docs/spec/reset-flag.md` | This document |
