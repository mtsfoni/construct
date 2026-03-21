# construct — Auth Spec

Covers R-AUTH-1, R-AUTH-2, R-AUTH-3, R-AUTH-4.

---

## Design principles

1. **Credentials are never passed as Docker env vars.** `docker inspect` must not
   expose secrets (R-AUTH-1). All secrets reach the container as bind-mounted files
   sourced by the entrypoint script.
2. **Global auth persists across folders and survives `reset`.** Auth state is
   scoped to the credential store, not to the session container or the agent layer
   (R-AUTH-2).
3. **Per-folder credentials override global ones.** If both exist for the same key,
   the folder-scoped value wins (R-AUTH-3).
4. **The user manages credentials via the CLI**, not by editing files (R-AUTH-4).

---

## Credential storage layout

All credential data lives under the daemon's state directory:

```
/state/credentials/           (= ~/.config/construct/credentials/ on host)
├── global/
│   ├── opencode.env          # Credentials for opencode (global)
│   ├── anthropic.env         # Example: raw API key files
│   └── ...
└── folders/
    └── <folder-slug>/
        ├── opencode.env      # Per-folder override
        └── ...
```

Each `.env` file is a simple `KEY=VALUE` text file with one credential per line.
These are the exact files that get bind-mounted into the session container.

The files are owned by the daemon process user and have mode `0600`.

### Empty directory guarantee

When creating a session, the daemon ensures a per-folder credentials directory
exists for the session's folder slug, even if it contains no `.env` files. This
is done by calling `os.MkdirAll` on `/state/credentials/folders/<folder-slug>/`
at session start. This ensures the bind mount source for per-folder credentials
is always a valid directory, preventing Docker mount errors.

---

## How credentials reach the container

At container creation, the daemon bind-mounts the credential directories into the
session container at well-known paths (read-only):

| Host path | Container path | Description |
|---|---|---|
| `/state/credentials/global/` | `/run/construct/creds/global/` | Global credentials |
| `/state/credentials/folders/<slug>/` | `/run/construct/creds/folder/` | Per-folder credentials (always mounted) |

The container's entrypoint script (see `SPEC/containers.md`) sources these files
at startup:

1. Sources all `*.env` files under `/run/construct/creds/global/`.
2. Sources all `*.env` files under `/run/construct/creds/folder/` (these override
   global values for matching keys, R-AUTH-3).
3. Exports the resulting environment variables into the container's environment.

Because the credential directories are bind-mounted (not copied), updates made
via `config cred set` are reflected immediately — the files change on the host
and the bind mount shows the new content. However, running processes only pick up
new env vars on next restart or if they re-read the files.

---

## Credential scope and naming

A **credential key** is an env-var-style name such as `ANTHROPIC_API_KEY` or
`OPENCODE_GITHUB_TOKEN`. Keys are uppercase, letters/digits/underscores only.

A **provider** is a logical grouping (e.g. `opencode`, `anthropic`, `github`).
Providers map to `.env` filenames for organisation. The provider name is
derived from the key using a simple heuristic: take the first word of the key
(everything before the first `_`) and lowercase it. For example:

| Key | Derived provider | File |
|---|---|---|
| `ANTHROPIC_API_KEY` | `anthropic` | `anthropic.env` |
| `OPENCODE_GITHUB_TOKEN` | `opencode` | `opencode.env` |
| `GITHUB_TOKEN` | `github` | `github.env` |
| `MY_CUSTOM_KEY` | `my` | `my.env` |

If the key contains no underscores (e.g. `APIKEY`), the entire key lowercased
is used as the provider name (`apikey.env`).

From the user's perspective, this grouping is an implementation detail. They
set `KEY=VALUE` pairs and the scope (global or folder). The provider-based file
grouping keeps the credential directory organised but does not affect
credential resolution or override behaviour.

---

## `config cred set`

```
construct config cred set <KEY> [--folder <path>]
```

1. Prompts for the value on stdin with echo off.
2. Daemon receives `config.cred.set` with `{ key, value, folder? }`.
3. Daemon determines the target `.env` file:
   - Global: `/state/credentials/global/<provider>.env` where `<provider>` is
     derived from the key by taking the first word before the first `_` and
     lowercasing it (e.g. `ANTHROPIC_API_KEY` → `anthropic.env`). If the key
     contains no underscores, the entire key lowercased is used as the provider
     name (e.g. `APIKEY` → `apikey.env`).
   - Folder: `/state/credentials/folders/<folder-slug>/<provider>.env`.
4. Daemon appends or replaces the `KEY=VALUE` line in the file atomically.
5. **Live session update:** If a session for this repo (or any session, for global)
   is currently running, the bind-mount will reflect the updated file automatically
   (bind mounts are live). The running agent will pick up new values on next read.
   No container restart is required.

---

## `config cred unset`

```
construct config cred unset <KEY> [--folder <path>]
```

1. Daemon receives `config.cred.unset` with `{ key, folder? }`.
2. Daemon finds and removes the `KEY=VALUE` line from the appropriate `.env` file.
3. If the file becomes empty, it is removed.

Note: the daemon protocol uses `folder` (not `repo`) as the parameter name for
credential commands, consistent with the CLI flag `--folder`. The `<folder-slug>`
is derived from the folder path using the shared slug algorithm (see
`SPEC/cli.md`).

---

## `config cred list`

```
construct config cred list [--folder <path>]
```

1. Daemon reads all keys from the credential files in the requested scope.
2. Returns a list of `{ key, scope, masked_value }` objects, where `masked_value`
   is `****` for all values (never returned in plaintext, R-AUTH-4).
3. If `--folder` is specified, the response includes both global and folder-specific
   keys. Folder-specific keys are annotated as overrides.

Example output:

```
KEY                    SCOPE    VALUE
ANTHROPIC_API_KEY      global   ****
OPENCODE_GITHUB_TOKEN  global   ****
ANTHROPIC_API_KEY      folder   ****  (overrides global)
```

---

## Security of the Unix socket

The daemon communicates with the CLI over a Unix domain socket at
`~/.config/construct/daemon.sock`. This socket carries credential values
(during `config cred set`).

Unix domain sockets have filesystem permissions. The socket is created with
mode `0600` (owner read-write only), and the containing directory
(`~/.config/construct/`) has mode `0700`. Only the user who owns the daemon
can connect. This provides the same level of protection as the credential
files themselves and is acceptable for a single-user tool.

No additional authentication or encryption (e.g. TLS) is applied to the
socket. The threat model assumes a trusted local user (R-SEC-4).

---

## Interactive auth (e.g. opencode /connect)

Some tools use interactive OAuth flows that produce tokens stored in the tool's
own config directory (e.g. `~/.config/opencode/`). Because the host's
`~/.config/opencode/` is mounted read-only into the container, any tokens the
agent acquires via interactive auth inside the container will need to be persisted
somewhere.

Since the opencode config dir is read-only inside the container (R-HOME-1 requires
the host to be the source of truth), the agent's interactive auth writes to
`/agent/home/.config/opencode/` (the agent home overlay) rather than the
read-only host mount.

**Consequence:** interactive auth tokens acquired inside the container are scoped
to the agent layer of that session. They survive `stop`/`start` but are lost on
`reset` or `destroy`. Users who want auth to persist globally should run the
auth flow once on the host (outside construct) so it lands in `~/.config/opencode/`
where it is automatically picked up by all sessions (R-AUTH-2).

This behaviour is documented in the CLI and in the injected `construct-agents.md`
(see `SPEC/config.md`).
