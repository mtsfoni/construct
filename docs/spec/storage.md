# Storage Reference

This document is the single source of truth for every piece of persistent state
that `construct` creates or manages. Individual feature specs explain *why* a
design decision was made; this document answers the practical question: **where
does my data live, who owns it, and when does it go away?**

---

## Overview

construct manages three categories of persistent state:

| Category | Mechanism | Scope | Wiped by `--reset` |
|----------|-----------|-------|--------------------|
| **Launch settings** | JSON file on host | per-repo | no |
| **Credentials** | `.env` files on host | global or per-repo | no |
| **Agent home** | Docker volume | per-repo + tool | yes |
| **Auth tokens** | File bind-mount from host | global | no |

---

## 1. Launch settings — `~/.construct/last-used.json`

**What:** The `--stack`, `--docker`, `--mcp`, `--port`, `--serve-port`, and
`--client` values used in the last `construct` invocation for each repository.
Used by `construct qs` to replay the previous invocation without retyping flags.

**Where:** `~/.construct/last-used.json` — a single JSON file on the host,
keyed by absolute repository path.

```json
{
  "/home/alice/projects/api":   { "stack": "go",   "docker": "dind" },
  "/home/alice/projects/web":   { "stack": "ui",   "mcp": true, "ports": ["3000"] },
  "/home/alice/projects/srv":   { "stack": "go",   "serve_port": 4096, "client": "web" }
}
```

**Scope:** per-repo (keyed by absolute path).

**Wiped by `--reset`:** no. `--reset` only affects the agent home volume.

**See also:** `docs/spec/quickstart-qs.md`

---

## 2. Credentials — `.env` files

**What:** API keys and other environment variables needed by the tool
(e.g. `ANTHROPIC_API_KEY`). Never passed via `-e` to `docker run`; written to a
temporary secrets directory and exported by the entrypoint, keeping values out
of `docker inspect` output.

**Where:** Up to two files, both on the host:

| File | Scope | How to write |
|------|-------|-------------|
| `~/.construct/.env` | global — applies to every repo | `construct config set KEY VALUE` |
| `<repo>/.construct/.env` | per-invocation override — applies only when launching from that repo | `construct config set --local KEY VALUE` |

The per-repo file lives **inside the repository directory** (not in
`~/.construct/`). It is user-created and opt-in; construct never creates it
automatically. Its purpose is to let a developer use a different API key for one
specific project without touching the global file — for example, a different
billing account or a project-specific service token.

At launch time the global file is loaded first; the per-repo file is merged on
top, so per-repo values override global ones for the same key.

**Wiped by `--reset`:** no. Neither file is touched by `--reset`.

**See also:** `docs/spec/config-command.md`

---

## 3. Agent home volume — `construct-home-<tool>-<hash>`

**What:** Everything the agent writes inside its home directory during a session:
shell history, tool caches, language server downloads, opencode's session
database (`opencode.db`), configuration written by the entrypoint on startup,
and any other files the agent creates under `/home/agent/`.

**Where:** A named Docker volume.

**Name pattern:** `construct-home-<toolname>-<hex8>` where `<hex8>` is the first
8 bytes of `SHA256(absoluteRepoPath)` hex-encoded.

Examples:
```
construct-home-opencode-a3f1c28d   # repo /home/alice/projects/api
construct-home-opencode-f09e44b1   # repo /home/alice/projects/web
```

**Scope:** per (repo path, tool). Two different repos always get distinct volumes.
Renaming a repo directory on the host produces a new volume (the old one is
orphaned until manually removed).

**Mounted at:** `/home/agent` inside the container.

**Wiped by `--reset`:** yes. `--reset` calls `docker volume rm` on the home
volume and lets `ensureHomeVolume` recreate it from scratch, re-seeding any
`Tool.HomeFiles` as if it were a first launch.

**Label:** `io.construct.managed=true` — prevents `docker volume prune` from
removing it.

### What lives here (opencode)

| Path inside `/home/agent` | Contents | Notes |
|--------------------------|----------|-------|
| `.config/opencode/` | `AGENTS.md`, `opencode.json`, MCP config | Rewritten at every container start by the entrypoint |
| `.config/opencode/commands/` | User slash commands | Bind-mounted read-only from `~/.config/opencode/commands/` on the host; not stored in the volume |
| `.local/share/opencode/` | `opencode.db` (sessions), `bin/` (LSP binaries), `log/` | Per-repo; `auth.json` is bind-mounted on top (see §4) |
| `.local/state/opencode/` | (Currently unused by opencode) | — |
| `.bash_history`, etc. | Shell history | Per-repo |

---

## 4. Auth tokens — `~/.construct/opencode/auth.json`

**What:** opencode's OAuth token file. Written by opencode when the user runs
`opencode auth` or authenticates via the web UI (e.g. GitHub OAuth). Must
survive `--reset` and be shared across repos so the user only authenticates once
per machine.

**Where:** `~/.construct/opencode/auth.json` on the **host filesystem**. This
file is bind-mounted into the container at
`/home/agent/.local/share/opencode/auth.json` with the `:z` SELinux relabel
suffix.

**Why a file bind-mount, not a volume:** opencode stores `auth.json` and its
session database (`opencode.db`) in the same directory
(`$XDG_DATA_HOME/opencode/`). Mounting a Docker volume at the whole directory
would shadow `opencode.db` too, making sessions global across repos. Mounting
only the single file keeps `opencode.db` in the per-repo home volume while
globalising only `auth.json`.

**Scope:** global (one file on the host, shared across all repos and all tool
versions).

**Wiped by `--reset`:** no. `--reset` removes the Docker home volume; the host
file is unaffected.

**Created when absent:** `ensureAuthFile` creates the file (and its parent
directory `~/.construct/opencode/`) with mode `0600` before starting the
container, ensuring Docker bind-mounts a regular file rather than a directory.

**Mechanism in code:** `Tool.AuthFiles []AuthFile` — each entry specifies a
`HostPath` and a `ContainerPath`. The runner calls `ensureAuthFile(HostPath)`
then appends `-v HostPath:ContainerPath:z` to the `docker run` command.

---

## 5. Auth volume — `construct-auth-<tool>` (infrastructure, not opencode)

**What:** A global named Docker volume for tools that need to persist an entire
auth *directory* (not just a single file). Not currently used by opencode.

**Mechanism:** `Tool.AuthVolumePath string` — when non-empty, the runner creates
the volume and mounts it at `AuthVolumePath` inside the container.

**Name pattern:** `construct-auth-<toolname>` — keyed by tool only, not by repo.

**Wiped by `--reset`:** no.

---

## Lifecycle summary

### First `construct` run on a new repo

1. `last-used.json` entry created/updated for the repo path.
2. Home volume created (`docker volume create`), seeded with `Tool.HomeFiles`,
   parent directories for any nested mounts pre-created and chowned.
3. `~/.construct/opencode/auth.json` created if absent (empty file, mode 0600).
4. Container starts; entrypoint writes `~/.config/opencode/AGENTS.md` and any
   other runtime config.

### `construct qs` (subsequent runs, same repo)

Steps 2 and 3 are no-ops (volume and file already exist). The home volume
accumulates state across sessions.

### `construct --reset`

1. Home volume is deleted (`docker volume rm`).
2. All agent state for this (repo, tool) pair is gone: sessions, shell history,
   LSP binaries, tool caches, runtime config changes.
3. `auth.json` on the host is **not** affected — OAuth tokens survive.
4. `last-used.json` is **not** affected — the next `construct qs` still works.
5. A fresh home volume is created as if it were the first run.

---

## `--reset` data loss warning

`--reset` permanently deletes all agent state stored in the home volume. For
opencode this includes:

- All session history (`opencode.db`)
- Shell history
- Downloaded LSP binaries and tool caches (re-downloaded automatically on next run)
- Any runtime changes the agent made to its config

**It does NOT delete:**

- OAuth tokens (`auth.json` on the host — see §4)
- API keys and credentials (`.env` files — see §2)
- Last-used launch settings (`last-used.json` — see §1)

To back up the home volume before resetting:
```sh
docker run --rm -v construct-home-opencode-<hash>:/src ubuntu \
  tar czf - /src > home-backup.tar.gz
```

---

## Related specs

| Spec | What it describes |
|------|-------------------|
| `docs/spec/reset-flag.md` | `--reset` flag implementation details |
| `docs/spec/quickstart-qs.md` | `qs` subcommand and `last-used.json` persistence |
| `docs/spec/config-command.md` | `config` subcommand and `.env` credential files |
