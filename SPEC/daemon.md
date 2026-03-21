# construct — Daemon Spec

Covers R-OBS-1, R-OBS-2, R-OBS-3, R-OBS-4, R-OBS-5, R-SES-8.

---

## What the daemon is

The daemon is a long-running Docker container named `construct-daemon`. It is the
single process that owns the Docker socket and manages all session containers.
The CLI is a thin client that sends requests to the daemon and streams responses
back to the terminal.

The user never manages the daemon manually. It starts automatically when the CLI
is first invoked and continues running indefinitely (R-OBS-5).

---

## Daemon container properties

| Property | Value |
|---|---|
| Container name | `construct-daemon` |
| Image | `construct-daemon:latest` (built from embedded Dockerfile) |
| Restart policy | `unless-stopped` (survives host reboots without systemd) |
| Mounts | `/var/run/docker.sock` (host Docker socket, read-write) |
| Mounts | `<construct-config-dir>` → `/state` (persistent state volume; see below) |
| Network | host network (simplest path for Unix socket access by CLI) |
| Ports | none exposed; CLI communicates via Unix socket on the shared state dir |

The daemon mounts the host Docker socket so it can manage session containers.
This is the **only** container that ever holds the Docker socket directly.

`<construct-config-dir>` is the resolved construct config directory:
`$XDG_CONFIG_HOME/construct/` if `$XDG_CONFIG_HOME` is set, otherwise
`~/.config/construct/`. The CLI resolves this at bootstrap time and uses it
for both the daemon mount and the socket path.

---

## Daemon image

The daemon image is built from a Dockerfile that is **embedded in the CLI binary**
via `go:embed`. The CLI extracts the Dockerfile and build context to a temporary
directory and runs `docker build` on first use.

The image is minimal, containing:

- The `constructd` Go binary (the daemon server)
- The Docker CLI (for managing session containers)

The daemon Dockerfile (`stacks/daemon/Dockerfile`) is a multi-stage build:
1. **Build stage:** Uses a Go builder image to compile `cmd/constructd/` with
   the embedded stack Dockerfiles (via `go:embed`).
2. **Runtime stage:** Copies the compiled `constructd` binary into a minimal
   Debian-based image with the Docker CLI installed.

The CLI embeds this Dockerfile and sends it (along with the full Go source
tree as build context) to `docker build` during bootstrap. This means the
daemon binary is compiled inside Docker, which requires no Go toolchain on
the host.

### Image version stamping

The CLI stamps all images it builds with a label:

```
io.construct.version=<build-version>
```

The version is baked into the binary at build time via Go `ldflags`. Dev builds
(no ldflags) use the sentinel value `dev` and skip version-mismatch checks.

---

## Bootstrap sequence (CLI side)

When the CLI is invoked, before doing anything else:

1. Check whether a container named `construct-daemon` exists and inspect its
   state. Use Docker's container inspect API; check `.State.Status`:
   - `running`: proceed to step 4.
   - `exited` / `created`: go to step 3.
   - Not found (404): go to step 2.
2. Build the daemon image if not present (or if the `io.construct.version` label
   does not match the CLI version). Extract the embedded Dockerfile to a temp
   dir, run `docker build -t construct-daemon:latest .`, then run
   `docker run -d --name construct-daemon ...` with the properties above.
3. If stopped: `docker start construct-daemon`.
4. Wait (with timeout) for the daemon Unix socket to become connectable. Timeout:
   10 seconds, polling every 200ms.
5. Proceed with the requested command.

### Bootstrap race condition

If two CLI instances start simultaneously, both may try to create the daemon.
The second `docker run --name construct-daemon` will fail with a name conflict.
The CLI catches this error and retries from step 1 (up to 3 retries). This is
sufficient because the daemon container will exist by the time the retry runs.

### Locking

No file-level lock is used for bootstrap. Docker's container name uniqueness
provides the serialisation point.

The CLI does this bootstrap using the Docker SDK (Go `docker/client` package).
This is the only direct Docker API call the CLI makes for session management.
The CLI also makes a direct `docker exec -it` call for debug mode (see
`SPEC/sessions.md`). All other Docker operations go through the daemon.

---

## Communication protocol

The CLI and daemon communicate over a **Unix domain socket** at:

```
<construct-config-dir>/daemon.sock
```

(defaults to `~/.config/construct/daemon.sock` unless `$XDG_CONFIG_HOME` is set)

The protocol is newline-delimited JSON (one JSON object per line in each
direction). Each CLI request uses a **separate connection** to the daemon socket.
There is no multiplexing of multiple requests over a single connection. This
simplifies the implementation: the daemon reads one request, sends zero or more
responses, and the connection is closed when done.

### Request envelope

```json
{
  "id": "<uuid>",
  "command": "<command-name>",
  "params": { ... }
}
```

### Response envelope

```json
{
  "id": "<uuid>",
  "type": "data" | "error" | "end",
  "payload": { ... }
}
```

- `data` responses may be sent multiple times for streaming commands (e.g. logs).
- `error` terminates the stream with an error payload `{ "message": "..." }`.
- `end` terminates the stream successfully with an optional final payload.

### Cancellation

The client cancels a streaming request by closing the connection. The daemon
detects the closed connection (write error or EOF on read) and stops any
in-progress work for that request (e.g. stops streaming logs). No explicit
cancel message is needed.

---

## Command schemas

### Naming convention: `repo` vs `folder`

Session commands (`session.*`) use the parameter name `repo` for the canonical
folder path. Credential commands (`config.cred.*`) use `folder`. This is a
historical naming inconsistency — "repo" is legacy terminology from when
construct only targeted git repositories. Both refer to the canonical absolute
path of the target directory. The CLI flag is `--folder` in all cases; the
CLI maps it to `repo` when sending session commands.

### `session.start`

**Params:**

```json
{
  "repo": "/home/alice/src/myapp",
  "tool": "opencode",
  "stack": "base",
  "docker_mode": "none",
  "ports": ["3000:3000", "8080"],
  "debug": false,
  "host_uid": 1000,
  "host_gid": 1000,
  "opencode_config_dir": "/home/alice/.config/opencode"
}
```

**Response (end):**

```json
{
  "session": { ... session record ... },
  "web_url": "http://localhost:4096",
  "tui_hint": "opencode --socket /path/to/socket",
  "warning": "tool/stack/docker/debug flags ignored; session already exists"
}
```

- `web_url` is present if the tool provides a web UI (opencode does).
- `tui_hint` is a hint for TUI attachment (informational).
- `warning` is present only when flags were ignored for an existing session.

During image builds or tool installation, `data` responses are sent with
progress output:

```json
{
  "type": "progress",
  "message": "Building stack image construct-stack-base:latest..."
}
```

### `session.stop`

**Params:**

```json
{
  "session_id": "a1b2c3d4-...",
  "repo": "/home/alice/src/myapp"
}
```

Either `session_id` or `repo` must be provided. If both are provided,
`session_id` takes precedence.

**Response (end):**

```json
{
  "session": { ... session record with status: "stopped" ... }
}
```

### `session.destroy`

**Params:** Same as `session.stop`.

**Response (end):**

```json
{
  "destroyed": true
}
```

### `session.reset`

**Params:** Same as `session.stop`.

`session.reset` uses the stored `host_uid`, `host_gid`, and
`opencode_config_dir` from the session record (not from the CLI request).
The container's mount parameters cannot be changed without recreating it,
and reset does not recreate the container.

**Response (end):**

```json
{
  "session": { ... session record with status: "running" ... }
}
```

### `session.list`

**Params:** `{}` (empty object)

**Response (end):**

```json
{
  "sessions": [ ... array of session records ... ]
}
```

### `session.logs`

**Params:**

```json
{
  "session_id": "...",
  "repo": "...",
  "follow": false,
  "tail": 100
}
```

Either `session_id` or `repo` must be provided. `tail` is optional (default:
all buffered lines). `follow` is optional (default: false).

**Response (data, streamed):**

```json
{
  "timestamp": "2026-01-15T10:30:00.123Z",
  "line": "opencode v1.2.3 starting...",
  "stream": "stdout"
}
```

`stream` is `"stdout"` or `"stderr"`. `timestamp` is RFC 3339 with
milliseconds, recorded by the daemon at receipt time.

When `follow` is true, the daemon continues streaming until the client
disconnects (closes the connection) or the session stops (daemon sends `end`).

When `follow` is false, the daemon streams all buffered lines and then sends
`end`.

For a session that has never run or whose buffer is empty, the daemon sends
`end` immediately with no preceding `data` responses.

### `config.cred.set`

**Params:**

```json
{
  "key": "ANTHROPIC_API_KEY",
  "value": "sk-ant-...",
  "folder": "/home/alice/src/myapp"
}
```

`folder` is optional. If omitted, the credential is stored globally.

**Response (end):**

```json
{
  "stored": true,
  "scope": "global"
}
```

### `config.cred.unset`

**Params:**

```json
{
  "key": "ANTHROPIC_API_KEY",
  "folder": "/home/alice/src/myapp"
}
```

`folder` is optional. If omitted, the global credential is removed.

**Response (end):**

```json
{
  "removed": true
}
```

### `config.cred.list`

**Params:**

```json
{
  "folder": "/home/alice/src/myapp"
}
```

`folder` is optional.

**Response (end):**

```json
{
  "credentials": [
    { "key": "ANTHROPIC_API_KEY", "scope": "global", "masked_value": "****" },
    { "key": "ANTHROPIC_API_KEY", "scope": "folder", "masked_value": "****" }
  ]
}
```

### `session.get` — removed

This command is removed. `session.list` returns all sessions; the CLI filters
client-side. There is no need for a separate get-by-ID command.

### `session.attach` — alias for `session.start`

`session.attach` is not a separate daemon command. The CLI implements
`construct attach` by first calling `session.list` to verify a session exists,
then sending `session.start`. When attaching to an existing running session,
`session.start` returns the connection info without modification. The
distinction between "start" and "attach" is a CLI-side UX concept, not a
protocol-level one.

---

## Session registry

The daemon maintains an in-memory session registry and persists it to:

```
/state/daemon-state.json
```

(which maps to `<construct-config-dir>/daemon-state.json` on the host, defaulting
to `~/.config/construct/daemon-state.json`).

### `daemon-state.json` schema

```json
{
  "version": 1,
  "sessions": {
    "<session-uuid>": {
      "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      "repo": "/home/alice/src/myapp",
      "tool": "opencode",
      "stack": "base",
      "docker_mode": "none",
      "debug": false,
      "ports": [
        { "host_port": 3000, "container_port": 3000 },
        { "host_port": 4096, "container_port": 4096 }
      ],
      "web_port": 4096,
      "container_name": "construct-a1b2c3d4",
      "host_uid": 1000,
      "host_gid": 1000,
      "opencode_config_dir": "/home/alice/.config/opencode",
      "status": "running",
      "created_at": "2026-01-15T10:30:00Z",
      "started_at": "2026-01-15T10:30:05Z",
      "stopped_at": null
    }
  }
}
```

The `version` field is for forward compatibility. The current version is `1`.

The `sessions` object is keyed by session UUID for O(1) lookup by ID. The
daemon also maintains an in-memory index by `repo` path for O(1) lookup by
folder.

### Port mapping object

```json
{ "host_port": 3000, "container_port": 3000 }
```

Both fields are integers. This is the canonical representation used in the
registry, in `CONSTRUCT_PORTS`, and in API responses. The `CONSTRUCT_PORTS`
environment variable serialises these as `<host_port>:<container_port>` pairs,
comma-separated (e.g. `3000:3000,4096:4096`).

### Port spec parsing

The `session.start` params accept ports as an array of strings (e.g.
`["3000:3000", "8080"]`). The daemon parses these into structured port objects:

- `"3000:3000"` → `{ "host_port": 3000, "container_port": 3000 }`
- `"8080:9000"` → `{ "host_port": 8080, "container_port": 9000 }`
- `"8080"` (container port only) → host port is auto-assigned by Docker at
  container creation time. The daemon reads back the actual assigned host port
  from Docker's container inspect response and stores it in the session record.

The auto-published web UI port (see `SPEC/tools.md`) is added to the ports
list automatically if the user did not explicitly map the tool's web port.

### Persisted fields

The following fields are now persisted in the session record (in addition to the
fields from the original spec):

| Field | Type | Description |
|---|---|---|
| `host_uid` | int | Host user's UID at session creation time |
| `host_gid` | int | Host user's GID at session creation time |
| `opencode_config_dir` | string | Resolved host opencode config path |
| `web_port` | int | Assigned host port for the web UI |
| `debug` | bool | Whether this is a debug session (no agent, shell instead) |

These are needed to correctly restart stopped sessions and recreate containers
after reset without requiring the CLI to re-supply them.

### Atomic writes

The registry is written atomically (write to temp file in the same directory,
then `os.Rename`) after every state change. `os.Rename` on the same filesystem
is atomic on Linux.

### Reconciliation on startup

On daemon startup, the registry is loaded and reconciled against actual Docker
container states:

1. For each session in the registry, inspect the Docker container by name.
2. If the container is running but the registry says `stopped`: update to
   `running`.
3. If the container is stopped/exited but the registry says `running`: update
   to `stopped`.
4. If the container does not exist at all: remove the session from the registry
   and log a warning. (The user likely removed it manually via `docker rm`.)
5. Look for orphaned `construct-*` containers and `construct-net-*` networks
   that have no registry entry. Remove them and log a warning.

---

## Log buffer

The daemon attaches to the stdout/stderr stream of each running session container
and feeds it into a per-session ring buffer (R-OBS-4).

- Buffer size: 10,000 lines per session (configurable via `CONSTRUCT_LOG_BUFFER`
  daemon env var).
- Lines are timestamped at receipt (RFC 3339 with milliseconds).
- When a client connects and requests logs, the daemon streams the buffer contents
  first, then switches to live streaming (if `follow` is true) until the client
  disconnects or the session stops.
- The buffer is in-memory only and is lost on daemon restart. After restart, only
  new lines are buffered. This is a documented limitation.
- When the buffer is full, the oldest line is silently dropped. No marker is sent
  to clients indicating dropped lines.

---

## Daemon lifecycle

### First start

1. CLI checks: no `construct-daemon` container exists.
2. CLI builds the image from the embedded Dockerfile (if needed).
3. CLI runs `docker run -d --name construct-daemon --restart unless-stopped
   --network host -v /var/run/docker.sock:/var/run/docker.sock
   -v <construct-config-dir>:/state construct-daemon:latest`.
4. Daemon initialises state dir, loads empty registry, starts listening on socket.

### Restart after CLI detach

- Session containers keep running; the daemon keeps running (R-SES-8).
- The CLI exiting has no effect on the daemon or sessions.

### Daemon container restart (e.g. host reboot)

1. Docker restarts `construct-daemon` due to `unless-stopped` restart policy.
2. Daemon loads registry from `daemon-state.json`.
3. Daemon reconciles registry against actual container states (see above).
4. Daemon re-attaches log streaming to any containers it finds running. Lines
   produced between the daemon going down and coming back up are lost from the
   buffer — the daemon starts capturing from the current point forward.
5. CLI can immediately connect and see sessions as they were.

### Daemon image upgrade

- The CLI checks the daemon container's `io.construct.version` label against the
  CLI binary's version.
- If the versions differ, the CLI stops the daemon container, removes it, and
  starts a fresh one with the new image.
- Session containers are **not** affected — they are not managed by the daemon
  image, only by the daemon process.
- The session registry is preserved across daemon upgrades (it lives on the shared
  state volume, not inside the daemon container).

---

## Error handling

- If the daemon socket is not connectable within 10 seconds of the container being
  detected as running, the CLI prints a diagnostic and exits with a non-zero code.
- The daemon logs all errors to its own stdout (visible via `docker logs construct-daemon`).
- Protocol errors (malformed JSON, unknown command) return an `error` response;
  the daemon does not crash.
- If a Docker API call fails during a session operation, the daemon returns an
  `error` response with the Docker error message. The daemon does not leave
  sessions in a partially-created state: if container creation fails, the daemon
  cleans up all resources created so far (container, dind sidecar, network,
  volume) in reverse order before returning the error. See `SPEC/sessions.md`
  for the full cleanup sequence.
