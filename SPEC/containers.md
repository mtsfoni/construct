# construct — Containers Spec

Covers R-ISO-1 through R-ISO-5, R-LIFE-1 through R-LIFE-4, R-SEC-1 through
R-SEC-3, R-STACK-1 through R-STACK-3, R-PLAT-2.

---

## Session container anatomy

Each session has one primary container (the agent container) and optionally one
sidecar container (dind). Both are managed by the daemon.

### Agent container

```
┌────────────────────────────────────────────────────────────┐
│  Agent container  (construct-<short-id>)                   │
│                                                            │
│  Stack image layer   (read-only, from stack image)         │
│  Agent layer volume  (read-write, construct-layer-<id>)    │
│                                                            │
│  Bind mounts:                                              │
│    /home/alice/src/myapp  ←→  /home/alice/src/myapp       │
│    (exact host path, R-ISO-2)                             │
│                                                            │
│    /state/credentials/...  → /run/construct/creds/...      │
│    (credential files, R-AUTH-1)                            │
│                                                            │
│    <opencode-config-dir>  → <opencode-config-dir>  (rw)   │
│    (host opencode config, R-HOME-1)                       │
│                                                            │
│    <opencode-data-dir>  → <opencode-data-dir>  (rw)       │
│    (host opencode data dir, auth.json)                    │
│                                                            │
│    /state/sessions/<id>/construct-agents.md                │
│      → /run/construct/agents.md  (read-only)              │
│    (entrypoint copies this to opencode config on startup) │
│                                                            │
│  Entrypoint: /entrypoint.sh (sources creds, copies        │
│              agents.md, sleeps)                           │
│  Agent: launched via docker exec -d                        │
└────────────────────────────────────────────────────────────┘
```

### Agent layer volume

The agent layer volume (`construct-layer-<short-id>`) is mounted at `/agent` inside
the container. It stores everything the agent installs during its sessions:

```
/agent/
├── bin/        # Installed tool binaries (on PATH)
├── lib/        # npm global modules, Go binaries, pip user installs, etc.
├── cache/      # Build caches (npm cache, cargo registry, etc.) — optional
└── home/       # Agent home directory overlay (shell history, tool configs)
    └── .config/
        └── opencode/
            └── opencode.json  (written by entrypoint; references /run/construct/agents.md)
```

`/agent/bin` is prepended to `PATH`. `/agent/home` is the `$HOME` for the agent
process (not the host user's home; the host opencode config is separately mounted
read-only — see `SPEC/config.md`).

This volume survives container recreation and stack image rebuilds (R-LIFE-4).
It is destroyed only on `session.destroy` or `construct purge`.

---

## Entrypoint script

The base stack image includes a startup script at `/entrypoint.sh`. This script
is set as the container's default command via `CMD ["/entrypoint.sh"]` in the
Dockerfile. (Docker's `ENTRYPOINT` is not used, so `docker exec` commands run
without entrypoint interference.) It runs as PID 1 inside the container **as
root**, because it needs to register the host UID in `/etc/passwd` before
dropping privileges.

```bash
#!/bin/bash
set -e

AGENT_UID=${CONSTRUCT_UID:-1000}
AGENT_GID=${CONSTRUCT_GID:-1000}

# Register the group and user so that sudo can resolve the UID.
if ! getent group "$AGENT_GID" > /dev/null 2>&1; then
    groupadd --gid "$AGENT_GID" agent
fi
if ! getent passwd "$AGENT_UID" > /dev/null 2>&1; then
    useradd --uid "$AGENT_UID" --gid "$AGENT_GID" \
        --home /agent/home --no-create-home --shell /bin/bash agent
fi

# Source global and per-folder credential files.
for f in /run/construct/creds/global/*.env; do
  [ -f "$f" ] && set -a && source "$f" && set +a
done 2>/dev/null || true
for f in /run/construct/creds/folder/*.env; do
  [ -f "$f" ] && set -a && source "$f" && set +a
done 2>/dev/null || true

# Ensure agent layer directories exist, owned by the agent user.
mkdir -p /agent/bin /agent/lib /agent/cache /agent/home/.config/opencode
chown -R "$AGENT_UID:$AGENT_GID" /agent/home /agent/bin /agent/lib /agent/cache 2>/dev/null || true

# Write opencode.json with construct context and autoupdate disabled.
cat > /agent/home/.config/opencode/opencode.json <<'EOF'
{
  "$schema": "https://opencode.ai/config.json",
  "autoupdate": false,
  "instructions": ["/run/construct/agents.md"]
}
EOF
chown "$AGENT_UID:$AGENT_GID" /agent/home/.config/opencode/opencode.json

# Drop to the host user and sleep forever — the agent is launched separately
# via docker exec (with --user uid:gid). gosu handles the privilege drop.
exec gosu "$AGENT_UID:$AGENT_GID" sleep infinity
```

Key design points:

- The entrypoint runs as root initially. This is required to call `useradd` and
  register the host UID in `/etc/passwd`. `sudo` on Debian performs a `getpwuid`
  lookup and refuses to run if the UID is unknown.
- `CONSTRUCT_UID` and `CONSTRUCT_GID` are injected as env vars by the daemon so
  the entrypoint knows which UID/GID to register (see env var table below).
- After setup, `gosu uid:gid sleep infinity` drops to the host user for the
  parked process. All subsequent `docker exec` commands (including the agent)
  run as the host user via `--user uid:gid`.
- `gosu` is used instead of `su` or `sudo` because it does a clean exec without
  spawning a shell, making it suitable for PID 1.
- `set -a` / `set +a` ensures sourced credential variables are exported.
- The `2>/dev/null || true` on the glob handles empty credential directories.
- The opencode.json `instructions` field references `/run/construct/agents.md`
  directly (the bind-mounted construct context file). No copy is needed; opencode
  reads the file at runtime via this config.

---

## Construct-agents.md injection strategy

The `construct-agents.md` file (generated per-session by the daemon) must be
visible to the agent in a way that opencode includes it in its global
instructions.

### Approach

The file is bind-mounted at a neutral path outside the agent layer volume:

```
/state/sessions/<short-id>/construct-agents.md  →  /run/construct/agents.md  (read-only)
```

The entrypoint writes `/agent/home/.config/opencode/opencode.json` (inside the
agent layer volume) with an `instructions` field that references this path
directly:

```json
{
  "instructions": ["/run/construct/agents.md"]
}
```

opencode reads this config file at startup and loads the construct context from
the bind-mounted path. No copy is needed.

### Why not bind-mount directly into the opencode config dir

Docker cannot overlay a bind mount on top of a volume mount target. Since the
agent layer volume is mounted at `/agent`, individual files within `/agent`
cannot also be bind-mounted. The neutral path `/run/construct/agents.md` avoids
this constraint. opencode accesses it via the `instructions` reference in the
config, not by directory scanning.

### How opencode picks up both host config and construct context

opencode merges configuration from multiple sources. The container has:
- `OPENCODE_CONFIG_DIR` pointing at the host opencode config dir (bind-mounted)
  — picks up the user's models, skills, slash commands, AGENTS.md, etc.
- `OPENCODE_CONFIG_CONTENT={"permission":"allow"}` — enforces yolo/auto-approve
  mode regardless of host config.
- `/agent/home/.config/opencode/opencode.json` (written by entrypoint) — adds
  the construct instructions and disables auto-update.

All three are merged by opencode; the user's global configuration is preserved.

---

## Container creation parameters

The daemon calls Docker with the following effective configuration when creating
a session container. Container creation uses a two-step process: `docker create`
followed by `docker start` (see `SPEC/sessions.md` for error cleanup).

### Name
`construct-<short-id>` (8-char prefix of the session UUID)

### Image
The stack image, e.g. `construct-stack-node:latest`. See `SPEC/stacks.md`.

### Restart policy
`unless-stopped` — the container restarts automatically after host reboots,
keeping `status: running` sessions alive (R-LIFE-1, R-SES-8).

Exception: debug sessions use restart policy `no` (see `SPEC/sessions.md`).

### User

The container is created **without** a `User` field — it starts as root so the
entrypoint can register the host UID in `/etc/passwd` (required for `sudo` to
work) and then drop privileges via `gosu`. The host UID/GID are passed via the
`CONSTRUCT_UID` / `CONSTRUCT_GID` env vars (see env var table below).

The agent process (and all `docker exec` operations) run as the host user's
UID:GID by setting `User: "<host_uid>:<host_gid>"` on the `docker exec` call,
not on the container itself. This ensures files written to the bind-mounted repo
appear with the correct ownership on the host.

### Mounts

| Host path / Volume | Container path | Mode | Notes |
|---|---|---|---|
| `construct-layer-<short-id>` (volume) | `/agent` | read-write | Agent layer |
| `<canonical-repo-path>` (bind) | `<canonical-repo-path>` | read-write | With idmap (see below) |
| `<host-opencode-config-dir>` (bind) | `<host-opencode-config-dir>` | read-write | Host opencode config (writable so /connect can persist tokens) |
| `<host-opencode-data-dir>` (bind) | `<host-opencode-data-dir>` | read-write | Host opencode data dir (auth.json lives here) |
| `/state/credentials/global/` (bind) | `/run/construct/creds/global/` | read-only | Global credentials |
| `/state/credentials/folders/<slug>/` (bind) | `/run/construct/creds/folder/` | read-only | Per-folder credentials |
| `/state/sessions/<short-id>/agents.md` (bind) | `/run/construct/agents.md` | read-only | Injected instructions |

`<host-opencode-config-dir>` is the resolved host opencode config path
(`$XDG_CONFIG_HOME/opencode` or `~/.config/opencode`), passed to the daemon by
the CLI at session creation time.

`<host-opencode-data-dir>` is the resolved host opencode data path
(`$XDG_DATA_HOME/opencode` or `~/.local/share/opencode`), also passed to the
daemon by the CLI. opencode writes `auth.json` here; mounting it read-write
ensures tokens written by an in-container auth flow persist to the host and are
shared across all sessions.

The per-folder credentials directory is always mounted, even if empty. The daemon
creates an empty directory for the folder slug at session start if it doesn't
exist, so the mount source is always valid.

No other host paths are mounted (R-ISO-1).

### Environment variables

Only non-secret env vars are passed via `-e`. Secrets come from credential files
sourced by the entrypoint (R-AUTH-1). Standard env vars set in the container:

| Variable | Value |
|---|---|
| `HOME` | `/agent/home` |
| `PATH` | `/agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin` |
| `XDG_CONFIG_HOME` | `/agent/home/.config` |
| `XDG_DATA_HOME` | Parent of the host opencode data dir (e.g. `~/.local/share`) |
| `OPENCODE_CONFIG_DIR` | Resolved host opencode config path |
| `OPENCODE_CONFIG_CONTENT` | `{"permission":"allow"}` — enforces yolo/auto-approve mode |
| `CONSTRUCT_SESSION_ID` | Session UUID |
| `CONSTRUCT_REPO` | Canonical repo path |
| `CONSTRUCT_TOOL` | Tool name |
| `CONSTRUCT_STACK` | Stack name |
| `CONSTRUCT_DOCKER_MODE` | `none`, `dind`, or `dood` |
| `CONSTRUCT_PORTS` | Comma-separated `<host_port>:<container_port>` pairs (e.g. `3000:3000,4096:4096`) |
| `CONSTRUCT_UID` | Host user UID — read by entrypoint to register the user in `/etc/passwd` |
| `CONSTRUCT_GID` | Host user GID — read by entrypoint to register the group in `/etc/group` |
| `DOCKER_HOST` | `tcp://construct-dind-<short-id>:2375` — **dind mode only**; not set in `none` or `dood` modes |
| `NPM_CONFIG_PREFIX` | `/agent` — directs `npm install -g` to install into `/agent/lib/node_modules/` with binaries symlinked to `/agent/bin/` |

### Capabilities and security

- `--privileged` is **never** used for the agent container (R-SEC-1).
- No additional Linux capabilities beyond the Docker default set are granted.
- No seccomp profile override (Docker's default seccomp profile applies).
- `--network`: depends on docker mode (see `SPEC/network.md`).

### Published ports

For each `--port` spec provided by the user, a `--publish` flag is added to the
container. The auto-published web UI port is also included (see
`SPEC/tools.md`). Port bindings are fixed at container creation time and cannot
be changed without destroying and recreating the container.

---

## File ownership on the host

Because the agent exec runs as the host user's UID:GID (set via `User` on
`docker exec`, not on the container), files written to the bind-mounted repo
folder appear with the correct host ownership automatically. No idmap or
user-namespace remapping is needed.

The CLI reads `os.Getuid()` and `os.Getgid()` and passes these to the daemon in
`host_uid` / `host_gid`. The daemon stores them in the session record and:
1. Passes them as `CONSTRUCT_UID` / `CONSTRUCT_GID` env vars to the container
   so the entrypoint can register the user in `/etc/passwd`.
2. Sets `User: "<host_uid>:<host_gid>"` on all `docker exec` calls (agent
   launch, log tail, etc.) so processes run as the host user.

If the stored UID/GID differ from the current caller's values (e.g. the session
was created by a different user), the daemon recreates the container with the new
values rather than reusing the old one.

---

## Docker-in-Docker (dind) sidecar

When `docker_mode = dind`:

1. Daemon creates a sidecar container named `construct-dind-<short-id>`.
   - Image: `docker:27-dind` (pinned to major version 27 for stability).
   - `--privileged` (required for dind, R-SEC-1 only excludes the agent container).
   - TLS disabled via environment variable `DOCKER_TLS_CERTDIR=""` — the agent
     and sidecar communicate over a private bridge network with no external
     exposure, so TLS adds complexity without security benefit.
   - Connected to a session-private bridge network `construct-net-<short-id>`.
   - No mounts of the repo or host credentials.
   - Restart policy: `unless-stopped` (same lifecycle as agent container).
2. The agent container is also attached to `construct-net-<short-id>`.
3. The environment variable `DOCKER_HOST=tcp://construct-dind-<short-id>:2375` is
   set in the agent container, pointing it at the private Docker daemon.
4. The agent cannot see host containers, images, or volumes (R-ISO-4).
5. On `session.destroy`, the dind container and the bridge network are removed.
6. On `session.stop`, both the agent and dind containers are stopped. On restart,
   both are started.

---

## Docker-outside-of-Docker (dood) mode

When `docker_mode = dood`:

1. The host Docker socket `/var/run/docker.sock` is bind-mounted into the agent
   container at `/var/run/docker.sock`.
2. `--security-opt label=disable` is added to the container unconditionally to
   prevent SELinux from blocking access to the host Docker socket on systems
   where SELinux is enforcing.
3. The user accepts the risk explicitly (R-ISO-5).
4. No special network or sidecar is created.
5. A warning is printed by the CLI at session start: "dood mode gives the agent
   full access to the host Docker daemon."

---

## Port free-check mechanism

The daemon needs to find available host ports for auto-publishing the web UI port
(and potentially for other auto-assigned ports). Since the daemon container runs
with `--network host`, it can check port availability directly:

1. Attempt to open a TCP listener on `0.0.0.0:<port>`.
2. If the listen succeeds, the port is free. Close the listener immediately.
3. If the listen fails (EADDRINUSE), increment the port and try again.
4. Start at port 4096 for the web UI, increment by 1 until a free port is found.

This is done at session creation time, just before `docker create`. There is a
small TOCTOU race (the port could be taken between the check and the Docker
publish), but this is acceptable — Docker will return an error, and the daemon
can retry with the next port.

---

## Isolation summary

| What | Isolated? | How |
|---|---|---|
| Host filesystem (outside folder) | Yes | No mounts except the folder (R-ISO-1) |
| Exact folder path preservation | Yes | Bind at same path (R-ISO-2) |
| Host Docker daemon (default) | Yes | No socket mount in `none` mode (R-ISO-3) |
| DinD daemon from host | Yes | Private daemon in sidecar (R-ISO-4) |
| DooD host socket (opt-in) | No | User explicitly opted in (R-ISO-5) |
| Privileged container | Never for agent | `--privileged` not used (R-SEC-1) |
