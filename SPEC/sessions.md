# construct — Sessions Spec

Covers R-SES-1 through R-SES-8, R-LIFE-1 through R-LIFE-5, R-OBS-3.

---

## Session definition

A session is the fundamental unit of construct (R-SES-1). It binds together:

- A **folder** (canonical absolute path)
- A **tool** (opencode)
- A **stack** (Docker image)
- A **docker mode** (none, dind, dood)
- A **container** (the running environment)
- **Published ports**
- **Lifecycle state** (created, running, stopped, destroyed)

A session persists between invocations of the CLI. Its identity is its session ID
(UUID), and its natural key is the canonical repo path (at most one active session
per folder, R-SES-3).

---

## Session states

```
         ┌─────────┐
  start  │         │  attach
─────────▶ running ◀──────────
         │         │
         └────┬────┘
              │ stop
              ▼
         ┌─────────┐
         │ stopped │
         └────┬────┘
              │ restart (via attach/start)
              ▼
         ┌─────────┐
         │ running │  ◀──── (same running state)
         └─────────┘
              │
              │ destroy (from either state)
              ▼
         ┌─────────┐
         │destroyed│  (removed from registry)
         └─────────┘
```

`reset` is a special transition: stopped → (image-reset) → running.
It is only available from a running or stopped state (not destroyed).

---

## Session creation (`session.start`)

### Input parameters

| Parameter | Type | Required | Default | Description |
|---|---|---|---|---|
| `repo` | string | yes | — | Canonical absolute path of the folder |
| `tool` | string | no | `opencode` | Tool name (only `opencode` supported) |
| `stack` | string | no | `base` | Stack name |
| `docker_mode` | string | no | `none` | `none`, `dind`, `dood` |
| `ports` | array of strings | no | `[]` | Port mapping specs |
| `debug` | boolean | no | `false` | Start shell instead of agent |
| `host_uid` | int | yes | — | Host user's UID (from CLI) |
| `host_gid` | int | yes | — | Host user's GID (from CLI) |
| `opencode_config_dir` | string | yes | — | Resolved host opencode config path (from CLI) |

### Behaviour

1. Daemon looks up the session registry by `repo` (the folder path).
2. **No existing session:**
   a. Generate a new UUID session ID.
   b. Determine the container name: `construct-<short-id>` where `<short-id>` is
      the first 8 chars of the session ID.
   c. Create the per-session working directory: `/state/sessions/<short-id>/`.
   d. Build the stack image if not present locally (with progress reported to client).
   e. Create the agent layer volume: `construct-layer-<short-id>`.
   f. Create any needed networks (see `SPEC/network.md`).
   g. Determine the web UI host port: start at 4096, increment by 1 until a free
      port is found (see port free-check in `SPEC/containers.md`).
   h. Generate the `construct-agents.md` file and write it to
      `/state/sessions/<short-id>/construct-agents.md`.
   i. Create the session container using a two-step process:
      1. `docker create` with all mounts, env vars, ports, and the entrypoint
         (see `SPEC/containers.md` for full container creation parameters).
      2. `docker start <container>` to start the container.
      If any step fails, the daemon cleans up all resources created so far in
      reverse order: container (`docker rm`), dind sidecar (`docker rm`, if
      created), network (`docker network rm`, if created), volume
      (`docker volume rm`). This ensures no orphaned resources are left behind.
      For example, if `docker create` succeeds but `docker start` fails, the
      daemon removes the container, then the dind sidecar and network (if dind
      mode), then the volume.
   j. If tool not installed in container: run the tool install command via
      `docker exec` (see `SPEC/tools.md`).
   k. Start the agent process (see "Agent process launch" below) or shell if `debug`.
   l. Register the session in the registry with `status: running`.
   m. Save quickstart record for the folder (written by daemon, see "Quickstart
      persistence" below).
   n. Return session record + connection info to client.
3. **Existing session, same tool/stack/docker:**
   a. If `status: stopped`: start the container (`docker start`). If
      `docker_mode = dind`, also start the dind sidecar (`docker start
      construct-dind-<short-id>`). Regenerate `construct-agents.md`, start the
      agent process, set `status: running`, return connection info. Use the
      stored `host_uid`, `host_gid`, and `opencode_config_dir` from the session
      record. If the CLI sends different values, log a warning but use the
      stored values (the container was created with the original UID/GID mapping
      and cannot be changed without recreating).
   b. If `status: running`: return connection info (pure attach; no changes).
4. **Existing session, different tool/stack/docker/debug:**
   - Return the existing session's connection info with a warning that the
     new tool/stack/docker/debug flags were ignored. The daemon does not attempt
     to change a running session's tool, stack, docker mode, or debug flag.

### Connection info response

```json
{
  "session": { ... session record ... },
  "web_url": "http://localhost:<port>",
  "tui_hint": "opencode --socket /path/to/socket",
  "warning": "tool/stack/docker/debug flags ignored; session already exists"
}
```

- `web_url` is present if the tool provides a web UI (opencode does via its built-in
  server). See `SPEC/tools.md` for per-tool web URL derivation.
- `tui_hint` is a hint for TUI attachment (informational).
- `warning` is present only when flags were ignored for an existing session.

---

## Agent process launch

The container's entrypoint is a long-running process (`sleep infinity` — see
`SPEC/containers.md` for the entrypoint script). The agent is **not** the
container's PID 1. Instead, the daemon launches the agent as a background exec:

```
docker exec -d <container> opencode --yolo --port <web-port>
```

The `--port` flag tells opencode which port to bind its web server to. The
`<web-port>` is the container-side port (always 4096 for opencode; the
host-side mapping is handled by Docker's port publishing). The command uses
`opencode` (not an absolute path) because `/agent/bin` is on `PATH` and
`npm install -g` creates a symlink there.

The `-d` flag runs the exec in detached mode. The daemon records the exec process
ID (returned by the Docker API's `ContainerExecCreate` + `ContainerExecStart`)
so it can send SIGTERM to the agent process on `session.stop` without stopping
the entire container.

### Why exec instead of CMD

- The container must stay alive independently of the agent process. The
  entrypoint (`sleep infinity`) keeps the container running so that
  `session.reset` can kill the agent, replace the volume, and start a new
  agent — all without recreating the container.
- `session.stop` first terminates the agent process (SIGTERM/SIGKILL), then
  stops the container. The two-step approach ensures clean agent shutdown.
- Debug mode replaces the exec with an interactive shell (see below).

### Agent stdout/stderr capture

The daemon attaches to the exec's stdout/stderr streams (via the Docker SDK's
`ContainerExecAttach`) and feeds them into the per-session log buffer. This is
the source of data for `session.logs`.

---

## Debug mode

When `debug` is true in `session.start`:

1. The container is created and started with the same entrypoint as normal
   (the entrypoint script runs `sleep infinity`).
2. Instead of launching the agent via `docker exec -d`, the daemon does **not**
   launch any background process.
3. The CLI receives the session record with `debug: true` in the response.
4. The CLI then runs `docker exec -it <container> /bin/bash` directly, attaching
   the user's terminal (stdin/stdout/stderr) to the interactive shell.
5. This requires the CLI's stdout to be a TTY. If not, the CLI prints an error:
   `--debug requires an interactive terminal` and exits with a non-zero code.
6. Debug containers use restart policy `no` instead of `unless-stopped` to avoid
   an infinite restart loop if the container exits unexpectedly.

### Debug mode is fixed at session creation

The `debug` flag is fixed for the lifetime of a session, just like `tool`,
`stack`, and `docker_mode`. If a session was created with `--debug`, it remains
a debug session. To switch to a normal (non-debug) session, the user must
destroy the session (`construct destroy`) and create a new one without `--debug`.
If `--debug` is passed and a non-debug session already exists for the folder
(or vice versa), the daemon ignores the flag and returns a warning (same
behaviour as conflicting tool/stack/docker flags in step 4 of session creation).

---

## Session attachment (`session.attach`)

`session.attach` is not a separate daemon protocol command. The CLI implements
`construct attach` by first calling `session.list` to verify a session exists
for the target folder or ID. If no session is found, the CLI prints an error
and exits (it does not create a session). If a session is found, the CLI sends
`session.start` to the daemon, which handles both the "already running" case
(pure attach) and the "stopped" case (restart). The distinction between
"start" and "attach" is a CLI-side UX concept, not a protocol-level one
(see `SPEC/daemon.md`).

If the session is stopped, `session.start` restarts it using the saved settings
(same as the behaviour described in step 3a above).

---

## Session stop (`session.stop`)

1. Daemon sends SIGTERM to the agent exec process inside the container (using the
   recorded exec ID from `ContainerExecInspect` to find the PID, then
   `ContainerKill` with SIGTERM targeting that process — or via `docker exec`
   running `kill -TERM <pid>`).
2. Waits up to 30 seconds for the agent to exit gracefully (poll exec status).
3. If agent has not exited: SIGKILL.
4. Calls `docker stop <container>` (stops the agent container process; Docker sends
   SIGTERM then SIGKILL after its own timeout).
5. If `docker_mode = dind`: also stops the dind sidecar container
   (`docker stop construct-dind-<short-id>`). The session network
   (`construct-net-<short-id>`) is left in place — it is only removed on destroy.
6. Sets `status: stopped` and `stopped_at` in the registry.
7. All volumes (agent layer) remain. Both containers exist in stopped state.

---

## Session destroy (`session.destroy`)

1. If `status: running`: stop first (same as `session.stop`).
2. Call `docker rm <container>`.
3. Call `docker volume rm construct-layer-<short-id>`.
4. If dind mode: call `docker rm <dind-sidecar-container>` and
   `docker network rm <session-network>`.
5. Remove the per-session working directory: `/state/sessions/<short-id>/`.
6. Remove session from the registry.
7. Delete quickstart record if it references this session's folder.

---

## Session reset (`session.reset`)

Reset loses agent-installed tools but preserves auth and global config (R-LIFE-5).
The quickstart record is not updated by reset (it already reflects the correct
settings from the original session creation).

1. If `status: running`: stop the agent process and the container (same as stop).
2. Call `docker volume rm construct-layer-<short-id>`.
3. Create a fresh empty volume: `docker volume create construct-layer-<short-id>`.
4. Regenerate the `construct-agents.md` file (in case ports or mode changed — 
   though they don't change, this ensures the file is always fresh).
5. Restart the container (same as `docker start <container>`; the container
   definition was not changed).
6. Run the tool install command again (same as first-time setup, since the agent
   layer is now empty).
7. Start the agent process.
8. Set `status: running`.

---

## Multiple simultaneous sessions (R-OBS-3)

Each session has an independent container and agent layer volume with a unique name.
Sessions do not share any Docker resources. The daemon manages them all concurrently.
The only shared resource is the host Docker daemon (socket), which Docker handles
with its own concurrency.

---

## Tool installation inside the container

After container creation (or after reset), the daemon checks whether the tool
binary is present in the container. If not (or always on a fresh agent layer), it
runs the tool's install command.

The install command runs inside the container as a `docker exec`. It installs into
a path on the agent layer volume (e.g. `/agent/bin`), which is added to `PATH`
in the container's environment. This ensures the tool persists across container
restarts and survives stack image rebuilds (R-LIFE-4).

See `SPEC/tools.md` for per-tool install commands.

---

## Quickstart persistence (R-UX-1)

After a successful `session.start` call that creates a new session or restarts
a stopped session, the **daemon** writes a quickstart record to:

```
/state/quickstart/<folder-slug>.json
```

(maps to `~/.config/construct/quickstart/<folder-slug>.json` on host)

The `<folder-slug>` derivation algorithm is defined in `SPEC/cli.md` and is
shared between the quickstart and credential modules.

The record captures the settings actually used for the session (which may differ
from what the CLI passed if an existing session overrode the flags). This ensures
`construct qs` replays what actually ran, not what was requested.

The quickstart record is written by the daemon (not the CLI) because the daemon
knows the actual settings used. The CLI never writes to the state directory.
