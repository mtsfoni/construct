# Path mirroring: mount the host path at its exact container location

## Problem

The previous approach always mounted the user's repository at the fixed path
`/workspace` inside the container:

```
-v /home/user/src/myrepo:/workspace:z  -w /workspace
```

This creates two related problems:

**1. Git worktrees break.** Git worktrees store their metadata using absolute
host paths (`/home/user/src/myrepo/.git/worktrees/feature/gitdir`,
`/home/user/src/myrepo-feat/.git` → `/home/user/src/myrepo/.git`). When the
container only sees the path as `/workspace`, git cannot resolve these
cross-references and operations like `git status`, `git log`, and branch
switching fail.

**2. Multi-repo workflows require re-mounting.** With the serve/client
architecture, users want to open multiple workspaces (separate repos or
worktrees) without stopping the host client. The fixed `/workspace` path makes
it impossible to work in two containers simultaneously where both paths need
to coexist on the filesystem.

## Solution

On Linux and macOS, mount the host path at its exact absolute location inside
the container:

```
-v /home/user/src/myrepo:/home/user/src/myrepo:z  -w /home/user/src/myrepo
```

The container's working directory is set to the same path. Git's internal path
references now resolve correctly because the container filesystem mirrors the
host layout for the mounted subtree.

A `CONSTRUCT_WORKSPACE_PATH` environment variable is injected into the
container, carrying the container-side path. The entrypoint uses it when
writing `~/.config/opencode/AGENTS.md` to tell the agent which directory is
shared with the user.

No `/workspace` symlink or alias is created. The fixed path `/workspace` is
gone from the container filesystem entirely (on Linux/macOS).

## Windows

On Windows, `os.Getuid()` returns `-1` (Docker Desktop runs containers through
a Linux VM where host UID/GID have no meaning). Windows paths (`C:\Users\...`)
have no valid equivalent Linux path, so path mirroring is not applied.
Windows falls back to the previous behaviour: the repo is mounted at
`/workspace` and `CONSTRUCT_WORKSPACE_PATH=/workspace`.

## Behaviour

| Platform     | Host path                         | Container mount point             | `-w` workdir                      |
|---|---|---|---|
| Linux/macOS  | `/home/user/src/myrepo`           | `/home/user/src/myrepo`           | `/home/user/src/myrepo`           |
| Linux/macOS  | `/home/user/src/myrepo-feat`      | `/home/user/src/myrepo-feat`      | `/home/user/src/myrepo-feat`      |
| Windows      | `C:\Users\user\src\myrepo`        | `/workspace`                      | `/workspace`                      |

### Git worktree example

```
# Host layout
/home/user/src/
  myrepo/          ← main worktree  (construct /home/user/src/myrepo)
  myrepo-feat/     ← linked worktree (construct /home/user/src/myrepo-feat)
```

Both containers now see the paths that git's worktree metadata references, so
all git operations work correctly. Each container gets its own home volume
(home volume name is keyed by SHA256 of the repo path, so they are always
distinct).

### Opening multiple workspaces

Because each container's working directory is the real host path, users can
run `construct` against different repos or worktrees simultaneously without
path conflicts:

```
construct /home/user/src/projectA    # workdir inside container: /home/user/src/projectA
construct /home/user/src/projectB    # workdir inside container: /home/user/src/projectB
```

## Persistence

No changes to persistence. Home volume naming (`construct-home-<tool>-<hex8>`,
where `hex8` is SHA256 of the absolute repo path) is unchanged.

## Files changed

| File | Change |
|---|---|
| `docs/spec/path-mirror.md` | This spec |
| `internal/runner/runner.go` | Add `containerWorkdir` helper; replace hardcoded `:/workspace:z` + `-w /workspace` in `buildRunArgs`, `buildServeArgs`, `buildDebugArgs`; inject `CONSTRUCT_WORKSPACE_PATH` env var |
| `internal/runner/runner_test.go` | Tests for `containerWorkdir`; verify `-v` and `-w` flags in build*Args; verify env var injection |
| `CHANGELOG.md` | Entry under `[Unreleased]` |
