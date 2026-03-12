# Spec: Git identity passthrough and commit attribution

## Problem

All commits made inside a construct session previously carried a synthetic git
identity (`construct agent <agent@construct.local>`). This made it impossible to
tell from git history which human triggered a session, and lost the natural
attribution that comes from the developer's own git identity.

## Solution

construct reads the host user's git identity at launch and injects it into the
container. Every commit the agent makes is attributed to the real developer.

## Behaviour

### 1. Read host identity at launch

`hostGitIdentity()` resolves four values — author name, author email, committer
name, committer email — using the same precedence git itself applies:

| Field | Resolution order |
|---|---|
| `authorName` | `GIT_AUTHOR_NAME` host env → `git config user.name` → fallback |
| `authorEmail` | `GIT_AUTHOR_EMAIL` host env → `git config user.email` → fallback |
| `committerName` | `GIT_COMMITTER_NAME` host env → `git config user.name` → `authorName` |
| `committerEmail` | `GIT_COMMITTER_EMAIL` host env → `git config user.email` → `authorEmail` |

The committer falls back to the author — not to a separate synthetic value —
matching git's own behaviour when no committer is explicitly configured. This
means a host with only `user.name` / `user.email` set (the common case) gets
identical author and committer, while a host that sets `GIT_COMMITTER_*`
explicitly gets those values honoured independently.

The synthetic fallback (`construct user <user@construct.local>`) is only used
when the author identity is entirely absent (no host env vars, no git config).
In that case construct prints a warning to stderr:

```
construct: warning: no git identity found on host
  run: git config --global user.name "Your Name"
       git config --global user.email "you@example.com"
  falling back to "construct user <user@construct.local>"
```

### 2. Inject identity into the container

The four git identity environment variables in `buildRunArgs` are populated from
the resolved host values:

```
GIT_AUTHOR_NAME=<resolved>
GIT_AUTHOR_EMAIL=<resolved>
GIT_COMMITTER_NAME=<resolved>
GIT_COMMITTER_EMAIL=<resolved>
```

The previous hard-coded `construct agent / agent@construct.local` identity is
removed entirely.

### 3. Scope

This is a runner-level change. It applies to all tools.

## Persistence details

The hook is written inside the container at startup. It is not seeded into the
home volume; it is re-written on every container start by the entrypoint script.
No files are written to the host or to the workspace repo.

## Files changed

| File | Change |
|---|---|
| `internal/runner/runner.go` | `hostGitIdentity()`: resolves author/committer separately, honouring host env vars with committer falling back to author; `buildRunArgs`: injects resolved values |
| `docs/spec/git-identity.md` | This document |
| `CHANGELOG.md` | Entry under `## [Unreleased]` |

