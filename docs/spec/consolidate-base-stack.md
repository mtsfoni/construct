# Spec: Consolidate `node` and `python` stacks into `base`

## Problem

The `node` stack was a no-op layer (`FROM construct-base` with nothing added)
because Node.js 20 was already installed in `construct-base`. The `python`
stack added only three apt packages on top of `construct-base`. Having three
separate images for what is effectively one common runtime environment added
unnecessary build steps, extra image cache entries, and conceptual overhead —
every tool needed Node.js regardless of stack, and Python is a near-universal
scripting dependency.

## Solution

Merge `node` and `python` into `construct-base` and remove them as distinct
stacks. `construct-base` is now the single, fully-featured common image that
all other stacks (and tools directly) build on.

## New `construct-base` contents

| Component | How it gets there |
|---|---|
| Ubuntu 22.04 | `FROM ubuntu:22.04` |
| curl, git, ca-certificates | apt |
| python3, python3-pip, python3-venv | apt (merged from former python stack) |
| Node.js 20 | nodesource setup script (was already in base) |
| Docker CLI + buildx plugin | Docker apt repo (was already in base) |
| `agent` user (non-root, docker group) | `useradd` (was already in base) |

## Stack list before and after

| Stack | Before | After |
|---|---|---|
| `base` | Ubuntu + Node + Docker CLI | Ubuntu + Node + Python + Docker CLI |
| `node` | no-op over base | **removed** |
| `python` | base + python3/pip/venv | **removed** |
| `dotnet` | base + .NET 10 | unchanged |
| `go` | base + Go 1.24 | unchanged |
| `ui` | node + @playwright/mcp + Chromium | base + @playwright/mcp + Chromium |

## Dependency chain after

```
construct-base
  └─ construct-ui
  └─ construct-go
  └─ construct-dotnet
```

`ui` previously depended on `construct-node` (which itself depended on
`construct-base`). With `node` removed, `ui` now depends directly on
`construct-base`.

## Files changed

| File | Change |
|---|---|
| `internal/stacks/dockerfiles/base/Dockerfile` | Added python3/pip/venv apt packages |
| `internal/stacks/dockerfiles/node/` | **Deleted** |
| `internal/stacks/dockerfiles/python/` | **Deleted** |
| `internal/stacks/dockerfiles/ui/Dockerfile` | `FROM construct-node` → `FROM construct-base` |
| `internal/stacks/stacks.go` | Removed `"node"` and `"python"` from `validStacks`; updated `stackDeps["ui"]` to `["base"]` |
| `internal/stacks/stacks_test.go` | Updated known-stacks list; replaced `TestStackDeps_UIHasNodeAndBase` with `TestStackDeps_UIHasBase`; added `TestIsValid_RemovedStacks`; added `TestEmbeddedDockerfiles_BaseContent` |
| `docs/spec/image-build-layers.md` | Stack table updated to reflect merged base |
