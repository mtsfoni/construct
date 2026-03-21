# construct — CLI Spec

Covers R-SES-2, R-SES-3, R-SES-3a, R-SES-4, R-SES-5, R-SES-6, R-SES-7, R-UX-1, R-UX-2, R-UX-3,
R-UX-4, R-UX-6, R-UX-7, R-AUTH-4, R-OBS-5, R-LIFE-5.

---

## Binary name and invocation

The CLI binary is named `construct`. It is a single static binary distributed for
Linux x86-64 (and arm64).

```
construct [global-flags] <command> [command-flags] [args]
```

Global flags apply to all commands:

| Flag | Description |
|---|---|
| `--debug` | On `run`: drop into an interactive shell instead of starting the agent (R-UX-4). On all other commands: enable verbose CLI logging to stderr. |
| `--daemon-socket <path>` | Override the default daemon socket path. Useful for testing. Default: `<construct-config-dir>/daemon.sock` (see `SPEC/overview.md` for path resolution). |
| `--version` | Print the construct version and exit. |

---

## Help output (R-UX-6)

When `construct` is invoked with no arguments and no flags, it prints a help
summary to stdout and exits with code 0. No daemon bootstrap is performed.

The help output lists all commands with a one-line description:

```
Usage: construct [global-flags] <command> [flags] [args]

Global flags:
  --debug               Enable verbose logging (or drop into shell for 'run')
  --daemon-socket <p>   Override daemon socket path
  --version             Print version and exit

Commands:
  run       Start or attach to a session for a folder (default command)
  qs        Quickstart: replay last invocation for a folder
  ls        List all sessions
  attach    Attach to a running session
  stop      Stop a running session
  destroy   Permanently destroy a session and all its state
  purge     Remove all construct containers, volumes, and images
  logs      View or stream session log output
  config    Manage credentials (config cred set/unset/list)

Run 'construct <command> --help' for command-specific flags.
```

The same output is printed when `--help` or `-help` is passed as the only
argument. No error message is shown; the exit code is 0.

---

## Command reference

### `construct [run]` — Start or attach to a session

This is the primary command. When invoked from inside a folder (or with
`--folder`), it starts a session if none exists or attaches to the existing one
(R-SES-2, R-SES-3).

```
construct [run] [flags]
construct [run] --folder <path> [flags]
construct [run] <path> [flags]
```

A bare positional argument (without `--folder`) is also accepted as the folder
path. For example, `construct /home/alice/src/myapp` and
`construct --folder /home/alice/src/myapp` are equivalent.

**Flags:**

| Flag | Description |
|---|---|
| `--folder <path>` | Folder path. Default: current working directory. |
| `--tool <name>` | Agent tool to use. Currently only `opencode` is supported. Default: `opencode`. |
| `--stack <name>` | Stack image to use. Default: `base`. See `SPEC/stacks.md` for names. |
| `--docker <mode>` | Docker mode. One of: `none`, `dind`, `dood`. Default: `none`. |
| `--port <spec>` | Publish a container port. Repeatable. Format: `<host-port>:<container-port>` or `<container-port>` (host port auto-assigned). |
| `--web` | Open the agent web UI in the browser after attaching (default: true if a web client is available). |
| `--no-web` | Disable auto-open of web UI. |
| `--debug` | Drop into an interactive shell instead of starting the agent (R-UX-4). |

**Behaviour:**

1. Resolve repo path to canonical absolute path (see "Canonical path resolution"
   below).
2. Check platform requirements: kernel >= 5.12, Docker >= 25.0 (see
   `SPEC/containers.md`). Exit with error if not met.
3. Bootstrap daemon if not running (see `SPEC/daemon.md`).
4. Send `session.start` to daemon with all flags as params, including `host_uid`
   (from `os.Getuid()`), `host_gid` (from `os.Getgid()`),
   `opencode_config_dir` (resolved host opencode config path), and
   `opencode_data_dir` (resolved host opencode data path).
5. If the daemon response includes `settings_conflict: true` (R-SES-3a):
   - Print a summary of the conflict, e.g.:
     ```
     Session for /home/alice/src/myapp is already running with different settings:
       tool:   opencode  (requested: opencode)  [same]
       stack:  base      (requested: node)       [differ]
       docker: none      (requested: none)       [same]
       ports:  []        (requested: [3000:3000])[differ]
     ```
   - Prompt the user:
     ```
     [r] restart with new settings  [a] attach to existing  [c] cancel
     Choice:
     ```
   - **restart**: call `session.destroy` then re-call `session.start` with the
     original requested flags. Continue with step 6 using the new response.
   - **attach**: continue with step 6 using the original response (ignoring
     the new flags).
   - **cancel**: print `Cancelled.` and exit 0.
   - If stdin is not a TTY (non-interactive), treat as **cancel** and print a
     message explaining that `--force-restart` is required to restart
     non-interactively. (Future: add `--force-restart` flag.)
6. Receive back the session connection info (web URL and/or TUI attach command).
7. If `--web` (default): print the URL and optionally open it in the browser.
   Print the TUI attach hint if present.
8. Wait until the agent's web server is reachable (readiness probe): HTTP GET
   `http://localhost:<port>/` in a loop, retrying every 250 ms, for up to 60 s.
   Print a `Waiting for agent...` progress indicator while probing. If the probe
   succeeds, the CLI exits 0 and the shell is returned (R-UX-7). If the 60 s
   timeout expires without a successful probe, print a warning and exit 0 anyway
   (the session is still running; the user can open the URL manually).

Note: log streaming is **not** performed by `run` or `attach`. Use
`construct logs -f` to follow session output.

**When `--debug` is set:**
- The daemon starts the container but does not run the agent. The CLI then runs
  `docker exec -it <container> /bin/bash` directly, attaching the user's terminal.
- Requires stdout to be a TTY. If not, prints error and exits with code 1.
- See `SPEC/sessions.md` for debug mode details.

---

### `construct qs` — Quickstart

Replay the last invocation for the current folder (R-UX-1).

```
construct qs [--folder <path>]
```

Reads the saved quickstart record for the folder, then behaves identically to
`construct run` with those saved flags. If no quickstart record exists, prints an
error suggesting the user run `construct` first.

The following settings are saved and replayed:
- `--tool`
- `--stack`
- `--docker`
- `--port` (all port specs)

`--debug` is intentionally excluded from quickstart. Debug mode is a one-off
troubleshooting action, not a setting to replay.

`--web` / `--no-web` is intentionally excluded from quickstart. It is a CLI-side
display preference, not a session setting. The CLI uses its default behaviour
(auto-open if a web client is available) unless the user explicitly passes
`--web` or `--no-web` at invocation time.

---

### `construct ls` — List sessions

Show all sessions managed by the daemon (R-SES-4).

```
construct ls [--json]
```

**Default output (table):**

```
ID          REPO                      TOOL       STACK  DOCKER  STATUS   PORTS              URL                       AGE
a1b2c3d4    /home/alice/src/myapp     opencode   node   none    running  3000:3000          http://localhost:4096      2h 14m
                                                                         4096:4096
e5f6g7h8    /home/alice/src/other     opencode   base   dind    stopped                                               5d 3h
```

Columns:
- `ID` — short (8-char) session ID.
- `REPO` — canonical folder path.
- `TOOL` — agent tool name.
- `STACK` — stack image name.
- `DOCKER` — docker mode.
- `STATUS` — `running` or `stopped`.
- `PORTS` — published port mappings in `host:container` format, newline-separated
  if multiple. Empty if no ports are published.
- `URL` — web UI URL (e.g. `http://localhost:4096`). Empty if the tool has no
  web UI or the session is stopped.
- `AGE` — human-readable time since `created_at`.

**`--json` flag:** Emit the full session record list as a JSON array (one object
per session, untruncated fields). Useful for scripting.

---

### `construct attach` — Attach to a running session

Connect to a session that is already running (R-SES-5).

```
construct attach [<session-id-or-folder>]
```

If no argument is given, defaults to the folder at the current working directory.
If the argument is a path (starts with `/` or `.`), it is treated as a filesystem path.
Otherwise it is treated as a session ID (or unambiguous prefix thereof).

Behaviour: first sends `session.list` to the daemon and checks whether a session
exists for the resolved folder or ID. If no session is found, prints:
`No session found for <folder-or-id>. Use 'construct run' to start one.` and
exits with code 1. If a session is found, sends `session.start` to the daemon
(same as `construct run`). If the session is running, this is a pure attach —
prints the web URL and waits for the readiness probe without modification. If
the session is stopped, it is restarted automatically (same as `construct run`
would do).

---

### `construct stop` — Stop a session

Gracefully stop a running session (R-SES-6).

```
construct stop [<session-id-or-folder>]
```

1. Sends `session.stop` to the daemon.
2. Daemon sends SIGTERM to the agent process, waits up to 30 seconds, then
   SIGKILL if not exited.
3. Daemon stops the container but does not remove it.
4. All state (agent layer, build caches, tool installations) is preserved.
5. CLI prints confirmation and exits.

If the session is already stopped, prints a notice and exits cleanly.

---

### `construct destroy` — Destroy a session

Permanently destroy a session (R-SES-7).

```
construct destroy [<session-id-or-folder>]
```

1. Prompts for confirmation: `Destroy session for /path/to/folder? This cannot be undone. [y/N]`
2. On confirmation, sends `session.destroy` to the daemon.
3. Daemon stops the container (if running), removes it, removes the agent layer
   volume, removes the per-session working directory, removes the session
   from the registry, and deletes the quickstart record for the folder.
4. CLI prints confirmation and exits.

`--force` flag skips the confirmation prompt (for scripting).

---

### `construct purge` — Remove all construct state

Wipe all construct Docker resources (R-LIFE-5).

```
construct purge [--force]
```

1. Prompts for confirmation:
   `Purge all construct containers, volumes, and images? This cannot be undone. [y/N]`
2. On confirmation, the CLI:
   a. Stops and removes all containers whose names start with `construct-`
      (session containers, daemon container, dind sidecars).
   b. Removes all Docker volumes whose names start with `construct-`
      (agent layer volumes, home volumes).
   c. Removes all Docker images whose repository starts with `construct-`
      (all stack images, daemon image).
   d. Removes the daemon state directory contents: `~/.config/construct/sessions/`,
      `~/.config/construct/quickstart/`, and `~/.config/construct/daemon-state.json`.
3. Auth credentials (`~/.config/construct/credentials/`) are **not** removed
   (R-AUTH-2) — the user keeps their API keys and tokens.
4. CLI prints a summary of what was removed and exits.

`--force` flag skips the confirmation prompt.

**Implementation note:** `purge` is a pure CLI-side operation — it talks directly
to Docker (not through the daemon), because the daemon itself is one of the things
being removed. The CLI uses the Docker SDK directly for this command only.

---

### `construct logs` — View session output

Stream or display session log output (R-OBS-4).

```
construct logs [<session-id-or-folder>] [--follow] [--tail <n>]
```

| Flag | Description |
|---|---|
| `--follow`, `-f` | Keep streaming new output as it arrives. Default: false. |
| `--tail <n>` | Show only the last N lines from the buffer. Default: all buffered lines. |

Sends `session.logs` to the daemon, which streams buffered lines and (if `--follow`)
continues streaming live.

---

### `construct config` — Manage credentials

Manage credentials for the agent (R-AUTH-4).

```
construct config cred set <key> [--folder <path>]
construct config cred unset <key> [--folder <path>]
construct config cred list [--folder <path>]
```

**`cred set <key>`**
- Reads the value from stdin (not as a flag, to avoid shell history exposure).
- Prompts: `Enter value for <key>: ` (input hidden).
- Stores under global scope by default; `--folder` stores under per-folder scope.
- Sends `config.cred.set` to the daemon.

**`cred unset <key>`**
- Removes the credential from the specified scope.
- Sends `config.cred.unset` to the daemon.

**`cred list`**
- Lists all credential keys in the specified scope with masked values (e.g. `****`).
- `--folder` shows both global and folder-specific keys, with folder overrides noted.
- Sends `config.cred.list` to the daemon.

---

## Output conventions

- All output is to stdout. Errors and warnings go to stderr.
- When stdout is a TTY: human-readable table/text output with ANSI colour for
  status (`running` = green, `stopped` = yellow).
- When stdout is not a TTY (piped/redirected): plain text, no ANSI codes.
- `--json` is available on commands where it makes sense (`ls`, `logs`).
- The CLI exits with code 0 on success, non-zero on error.

---

## Canonical path resolution

The CLI resolves folder paths to canonical absolute paths using two steps:

1. `filepath.Abs(path)` — resolve relative to cwd.
2. `filepath.EvalSymlinks(path)` — resolve all symlinks to their real targets.

This ensures that two invocations pointing to the same physical directory (via
different symlinks or relative paths) produce the same session key. The resolved
path is what gets sent to the daemon as `repo`.

---

## Folder slug algorithm

The `<folder-slug>` is a filesystem-safe identifier derived from the canonical
folder path. It is used in two places:
- Quickstart records: `~/.config/construct/quickstart/<folder-slug>.json`
- Per-folder credentials: `~/.config/construct/credentials/folders/<folder-slug>/`

**Algorithm:**

1. Start with the canonical absolute path (after `EvalSymlinks` + `Abs`).
2. Replace all `/` characters with `_`.
3. Strip the leading `_` (from the leading `/`).
4. Truncate to 200 characters (to avoid filesystem path length limits).

**Examples:**

| Canonical path | Slug |
|---|---|
| `/home/alice/src/myapp` | `home_alice_src_myapp` |
| `/tmp/test` | `tmp_test` |
| `/home/alice/src/very/deep/nested/path` | `home_alice_src_very_deep_nested_path` |

This algorithm is implemented in the shared `slug` package and used by both the
quickstart and auth modules.

---

## Quickstart record format

Stored at `~/.config/construct/quickstart/<folder-slug>.json`.

```json
{
  "folder": "/home/alice/src/myapp",
  "tool": "opencode",
  "stack": "node",
  "docker_mode": "none",
  "ports": ["3000:3000", "8080"],
  "saved_at": "2026-01-15T10:30:00Z"
}
```

This file is written by the **daemon** after every successful `session.start`
invocation (new or restart). It is not written on pure attach (session already
running and no changes made). The daemon writes it because it knows the actual
settings used, which may differ from what the CLI requested (e.g. if flags were
ignored for an existing session).

---

## Disambiguation: session-id-or-folder argument

Commands that accept an optional `<session-id-or-folder>` argument use the following
resolution order:

1. If argument starts with `/` or `./` or `../`: treat as a filesystem path.
   Resolve to canonical absolute path. Look up session by folder path.
2. If argument looks like a UUID or 8+ hex chars: look up session by ID prefix.
3. Otherwise: try as a folder path first (relative to cwd), then as an ID prefix.
4. If no argument: use the canonical path of the current working directory.
5. If ambiguous (multiple sessions match a prefix): print a list and exit with error.
