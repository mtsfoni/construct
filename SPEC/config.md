# construct — Home Config Spec

Covers R-HOME-1, R-HOME-2, R-HOME-3, R-PLAT-4.

---

## Goal

The agent running inside a construct session must have the same global opencode
configuration experience as if it were running directly on the host (R-HOME-1).
This means skills, slash commands, AGENTS.md files, model selection, keybindings,
and theme all work identically.

At the same time, construct must inject additional context about the container
environment so the agent knows where it is and what it can do (R-HOME-3).

---

## Host opencode config — read-only mount

The host's opencode configuration directory is bind-mounted into the session
container at the same path it has on the host. The CLI resolves the actual
config directory by checking `$XDG_CONFIG_HOME` (if set) and falling back to
`~/.config`:

```
$XDG_CONFIG_HOME/opencode/  →  $XDG_CONFIG_HOME/opencode/  (read-only inside container)
```

Or if `$XDG_CONFIG_HOME` is not set:

```
~/.config/opencode/  →  ~/.config/opencode/  (read-only inside container)
```

The CLI passes the resolved host opencode config path to the daemon at session
creation time. The daemon uses this path for the read-only bind mount.

Because the container runs with `HOME=/agent/home` (the agent layer), opencode
inside the container needs to be pointed at this config directory. The
`OPENCODE_CONFIG_DIR` environment variable is set to the resolved host config
path inside the container.

The host is the source of truth. The agent can read the config but not modify it
(R-HOME-1). If the agent tries to write to a config path inside the container, it
writes to `/agent/home/...` (the agent layer) via the $HOME overlay, not to the
read-only host mount.

---

## Per-folder agent instructions (R-HOME-2)

Any agent instruction files in the root of the mounted folder (e.g. `AGENTS.md`,
`CLAUDE.md`, `opencode.md`) are available to the agent automatically because the
entire folder is already bind-mounted (R-ISO-2). No special configuration is
needed.

---

## Construct-injected AGENTS.md (R-HOME-3)

construct injects additional context into the agent's global instructions by
generating a `construct-agents.md` file and mounting it where opencode will find
it.

### What is injected

The generated file tells the agent:

- That it is running inside a construct container.
- The current docker mode (`none`, `dind`, `dood`) and what that means for
  Docker usage.
- Which ports are published (so the agent binds dev servers to `0.0.0.0` on the
  correct ports, not localhost only).
- That auth tokens acquired interactively are scoped to the agent layer (will not
  persist after `reset`); for durable auth, authenticate on the host.
- That the repo is at the exact same path as on the host.
- Tool-installation advice: install tools to `/agent/bin` or using standard
  package managers (they install into `/agent/lib`) to ensure persistence.

### How it is injected

The daemon generates the file content at session start (or restart), writes it to:

```
/state/sessions/<short-id>/construct-agents.md
```

(maps to `~/.config/construct/sessions/<short-id>/construct-agents.md` on host)

and bind-mounts it into the container at:

```
/agent/home/.config/opencode/construct-agents.md   (read-only)
```

This path is inside the agent layer volume, **not** inside the read-only host
config mount. This avoids the Docker limitation where a file bind mount cannot
overlay a path inside another bind mount (see `SPEC/containers.md` for the full
mount layering explanation).

opencode reads global instruction files from both `OPENCODE_CONFIG_DIR` (the
host config mount) and `$XDG_CONFIG_HOME/opencode/` (which resolves to
`/agent/home/.config/opencode/`). The construct file is therefore picked up
from the latter path without any special opencode configuration.

### Combination with user's global AGENTS.md

The user's own `AGENTS.md` (if it exists) in the host opencode config directory
is picked up from the read-only host config mount. construct's
`construct-agents.md` is a separate file at a different path. opencode
concatenates all global instruction files, so both are in effect. The user's own
instructions take precedence in any conflict because they are read last (opencode's
file-ordering behaviour is documented in the opencode spec).

---

## Per-session working directory lifecycle

The daemon creates a per-session working directory at session start:

```
/state/sessions/<short-id>/
```

(maps to `~/.config/construct/sessions/<short-id>/` on host)

This directory is used to store the generated `construct-agents.md` file and
any other per-session daemon-side state.

- **Created at:** session start (`session.start` with a new session).
- **Preserved across:** `session.stop` / `session.start` cycles, daemon restarts.
- **Regenerated at:** `session.reset` (the `construct-agents.md` file is
  regenerated, but the directory is not removed).
- **Removed at:** `session.destroy` (the entire directory is removed).

---

## Config paths summary

| What | Host path | Container path | Mode |
|---|---|---|---|
| opencode global config dir | `$XDG_CONFIG_HOME/opencode/` (or `~/.config/opencode/`) | same as host path | read-only |
| construct injected instructions | `~/.config/construct/sessions/<id>/construct-agents.md` | `/agent/home/.config/opencode/construct-agents.md` | read-only |
| Agent's writable home | (volume) | `/agent/home/` | read-write |
| Repo | `<repo>` | `<repo>` (same path) | read-write |

---

## XDG and HOME environment inside the container

| Variable | Value | Reason |
|---|---|---|
| `HOME` | `/agent/home` | Agent writes go to the persistent agent layer |
| `XDG_CONFIG_HOME` | `/agent/home/.config` | Overrides XDG default so opencode writes land in agent layer |
| `OPENCODE_CONFIG_DIR` | resolved host opencode config path (e.g. `~/.config/opencode`) | Points opencode at the host config for reading |

The CLI resolves the host opencode config path by checking `$XDG_CONFIG_HOME/opencode`
(falling back to `~/.config/opencode`) and passes it to the daemon. The daemon
sets `OPENCODE_CONFIG_DIR` in the container to this resolved path.

The distinction matters: opencode reads global config from `OPENCODE_CONFIG_DIR`
(the host mount), but any runtime state it writes (session state, OAuth tokens
from interactive auth) goes to the `XDG_CONFIG_HOME` path (the agent layer).

This is the mechanism by which R-HOME-1 (read the host config) and R-AUTH-2
(auth persists globally) coexist: read-only host config is always present;
interactive write-back goes to the agent layer.
