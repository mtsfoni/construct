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
│    (exact host path, R-ISO-2, with idmap UID mapping)     │
│                                                            │
│    /state/credentials/...  → /run/construct/creds/...      │
│    (credential files, R-AUTH-1)                            │
│                                                            │
│    <opencode-config-dir>  → <opencode-config-dir>  (ro)   │
│    (host opencode config, R-HOME-1)                       │
│                                                            │
│    /state/sessions/<id>/construct-agents.md                │
│      → /agent/home/.config/opencode/construct-agents.md   │
│    (injected agent instructions, R-HOME-3)                │
│                                                            │
│  Entrypoint: /entrypoint.sh (sources creds, sleeps)       │
│  Agent: launched via docker exec -d                        │
└────────────────────────────────────────────────────────────┘
```

### Agent layer volume

The agent layer volume (`construct-layer-<short-id>`) is mounted at `/agent` inside
the container. It stores everything the agent installs during its sessions:

```
/agent/
├── bin/        # Installed tool binaries (opencode, etc.)
├── lib/        # npm global modules, Go binaries, pip user installs, etc.
├── cache/      # Build caches (npm cache, cargo registry, etc.) — optional
└── home/       # Agent home directory overlay (shell history, tool configs)
    └── .config/
        └── opencode/
            └── construct-agents.md  (bind-mounted, read-only)
```

`/agent/bin` is prepended to `PATH`. `/agent/home` is the `$HOME` for the agent
process (not the host user's home; the host opencode config is separately mounted
read-only — see `SPEC/config.md`).

This volume survives container recreation and stack image rebuilds (R-LIFE-4).
It is destroyed only on `session.destroy` or `session.reset`.

---

## Entrypoint script

The base stack image includes a startup script at `/entrypoint.sh`. This script
is set as the container's default command via `CMD ["/entrypoint.sh"]` in the
Dockerfile. (Docker's `ENTRYPOINT` is not used, so `docker exec` commands run
without entrypoint interference.) It runs as PID 1 inside the container.

```bash
#!/bin/bash
set -e

# 1. Source global credential files
for f in /run/construct/creds/global/*.env 2>/dev/null; do
  [ -f "$f" ] && set -a && source "$f" && set +a
done

# 2. Source per-folder credential files (override global)
for f in /run/construct/creds/folder/*.env 2>/dev/null; do
  [ -f "$f" ] && set -a && source "$f" && set +a
done

# 3. Ensure agent layer directories exist
mkdir -p /agent/bin /agent/lib /agent/cache /agent/home/.config

# 4. Sleep forever — the agent is launched separately via docker exec
exec sleep infinity
```

Key design points:

- The entrypoint sources credentials into the process environment. Processes
  launched via `docker exec` in the same container inherit the container's
  environment, so the agent process gets these env vars automatically.
- `sleep infinity` keeps the container alive. The agent is launched separately
  via `docker exec -d` (see `SPEC/sessions.md`). This allows the agent to be
  stopped and restarted without restarting the container.
- `set -a` / `set +a` ensures sourced variables are exported.
- The `2>/dev/null` on the glob handles the case where the credential directory
  is empty (no `.env` files).

---

## Construct-agents.md mount strategy

The `construct-agents.md` file (generated per-session by the daemon) must be
visible to the agent in a directory that opencode scans for global instructions.

### Problem

The host opencode config directory is already bind-mounted read-only at
`<opencode-config-dir>`. Mounting a file *into* a read-only bind mount does not
work in Docker — a file bind mount cannot overlay a path inside another bind
mount.

### Solution

Mount the generated file at `/agent/home/.config/opencode/construct-agents.md`
instead. This path is inside the agent layer volume (writable), not inside the
read-only host config mount. The `OPENCODE_CONFIG_DIR` environment variable
points opencode at the host config dir for reading its primary config.

opencode reads global instruction files from **both** `OPENCODE_CONFIG_DIR`
(the host config mount) and `$XDG_CONFIG_HOME/opencode/` (which resolves to
`/agent/home/.config/opencode/`). The construct-agents.md file is picked up
from the latter path.

The bind mount for this file:

```
/state/sessions/<short-id>/construct-agents.md  →  /agent/home/.config/opencode/construct-agents.md  (read-only)
```

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
The container process runs as `root` (UID 0) inside the container (R-SEC-2).
File ownership on the host is handled at the mount level (see UID mapping below).

### Mounts

| Host path / Volume | Container path | Mode | Notes |
|---|---|---|---|
| `construct-layer-<short-id>` (volume) | `/agent` | read-write | Agent layer |
| `<canonical-repo-path>` (bind) | `<canonical-repo-path>` | read-write | With idmap (see below) |
| `<host-opencode-config-dir>` (bind) | `<host-opencode-config-dir>` | read-only | Host opencode config |
| `/state/credentials/global/` (bind) | `/run/construct/creds/global/` | read-only | Global credentials |
| `/state/credentials/folders/<slug>/` (bind) | `/run/construct/creds/folder/` | read-only | Per-folder credentials |
| `/state/sessions/<short-id>/construct-agents.md` (bind) | `/agent/home/.config/opencode/construct-agents.md` | read-only | Injected instructions |

`<host-opencode-config-dir>` is the resolved host opencode config path
(`$XDG_CONFIG_HOME/opencode` or `~/.config/opencode`), passed to the daemon by
the CLI at session creation time.

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
| `OPENCODE_CONFIG_DIR` | Resolved host opencode config path |
| `CONSTRUCT_SESSION_ID` | Session UUID |
| `CONSTRUCT_REPO` | Canonical repo path |
| `CONSTRUCT_TOOL` | Tool name |
| `CONSTRUCT_STACK` | Stack name |
| `CONSTRUCT_DOCKER_MODE` | `none`, `dind`, or `dood` |
| `CONSTRUCT_PORTS` | Comma-separated `<host_port>:<container_port>` pairs (e.g. `3000:3000,4096:4096`) |
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

## UID mapping for folder bind-mount (R-LIFE-3, R-SEC-3)

The agent runs as root (UID 0) inside the container, but files written to the
bind-mounted folder must appear with the host invoking user's UID/GID on the host.

This is achieved via Docker's **idmap mount** feature, available since Linux
kernel 5.12 (April 2021) and Docker 25.0 (January 2024).

### Requirements

- **Linux kernel 5.12 or later** (released April 2021)
- **Docker Engine 25.0 or later** (released January 2024)

If either requirement is not met, construct exits with an error message
explaining the minimum versions required. No fallback mechanism is provided.
Both versions are over 2 years old at time of writing; this is a hard
requirement (R-PLAT-2).

### Version detection

The CLI checks these requirements during the bootstrap sequence (before
creating the daemon container):

- **Kernel version**: Read from `uname -r` (or `syscall.Uname` in Go).
  Parse the major.minor version and compare against 5.12.
- **Docker version**: Call `docker version --format '{{.Server.Version}}'`
  (or use the Docker SDK's `ServerVersion()` call). Parse the major version
  and compare against 25.

If either check fails, the CLI prints:

```
Error: construct requires Linux kernel >= 5.12 and Docker >= 25.0.
  Kernel: <detected> (need >= 5.12)
  Docker: <detected> (need >= 25.0)
```

and exits with code 1.

### Mechanism

The daemon is passed the invoking user's UID and GID at session creation time
(from the CLI, which reads them from `os.Getuid()` / `os.Getgid()`).

The daemon creates the session container with an idmap mount on the folder
bind. In the Docker Go SDK, this is configured via the mount spec:

```go
mount.Mount{
    Type:   mount.TypeBind,
    Source: folderPath,
    Target: folderPath,
    BindOptions: &mount.BindOptions{
        CreateMountpoint: true,
        // Idmap mount: maps container root (UID 0) to host UID
        // Available in Docker SDK since API version 1.44 (Docker 25.0)
        IDMapping: &mount.IDMapping{
            UIDMappings: []mount.IDMap{
                {ContainerID: 0, HostID: hostUID, Size: 1},
            },
            GIDMappings: []mount.IDMap{
                {ContainerID: 0, HostID: hostGID, Size: 1},
            },
        },
    },
}
```

Inside the container, the folder appears owned by root:root. Outside, files created
by root inside map to the host user's UID:GID.

This entire mechanism is transparent to the user; they never need to configure it.

### Scope of idmap

The idmap is applied **only** to the folder bind mount. The agent layer volume
(`/agent`) and credential mounts do not use idmap — they are internal to
construct and host file ownership is irrelevant for them.

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
2. The Docker socket group GID from the host is added to the container via
   `--group-add <gid>`. The daemon reads the GID of `/var/run/docker.sock` on
   the host (via `os.Stat` → `Sys().Gid`) and passes it. This ensures the agent
   (running as root) can access the socket even if the socket has restrictive
   group permissions.
3. On systems with SELinux enabled, `--security-opt label=disable` is added to
   the container to prevent SELinux from blocking access to the host Docker
   socket. The daemon detects SELinux by checking if `/sys/fs/selinux` exists
   and is mounted.
4. The user accepts the risk explicitly (R-ISO-5).
5. No special network or sidecar is created.
6. A warning is printed by the CLI at session start: "dood mode gives the agent
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
