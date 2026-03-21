# construct — Spec Overview

This document describes the system architecture of construct. It is the entry point
for all other spec documents. Every major design decision here traces back to a
requirement in `REQS/REQUIREMENTS.md`.

---

## What construct is

construct is a tool that runs an AI coding agent (primarily opencode) against a local
folder inside an isolated Docker container, in auto-approve (yolo) mode. The folder
does not need to be a git root — it can be any directory, including a parent folder
containing multiple projects. It is not a hardened sandbox — it is a meaningful step
up from running the agent directly on the host (R-SEC-4).

---

## High-level architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  Host machine                                                   │
│                                                                 │
│  ┌───────────────┐       Unix socket / HTTP      ┌──────────┐  │
│  │  construct    │ ──────────────────────────────▶  daemon  │  │
│  │  CLI          │                               │ container│  │
│  └───────────────┘                               └────┬─────┘  │
│                                                       │        │
│                                          Docker API   │        │
│                                                       ▼        │
│                                  ┌──────────────────────────┐  │
│                                  │  session container(s)    │  │
│                                  │  (one per folder)        │  │
│                                  └──────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

There are three distinct processes / containers:

| Component | What it is | Lives where |
|---|---|---|
| **CLI** | User-facing binary, thin client | Host, invoked by user |
| **Daemon** | Long-running manager, owns sessions | Docker container on host |
| **Session container** | Agent runtime, one per folder | Docker container on host |

---

## Component responsibilities

### CLI (`construct`)

- Entry point for all user interactions.
- Detects whether the daemon is running; starts it if not (R-OBS-5).
- Communicates with the daemon over a Unix domain socket (or loopback HTTP).
- Does not talk to the Docker daemon directly — all Docker operations go through
  the daemon — **except** during daemon bootstrap and debug mode `docker exec`
  (see `SPEC/daemon.md` and `SPEC/sessions.md`).
- Renders output to the terminal (session list, logs, status, confirmations).

See `SPEC/cli.md` for full command specification.

### Daemon

- A single lightweight container that starts automatically on first use (R-OBS-5).
- Owns the Docker socket and manages all session containers (R-OBS-2).
- Tracks session state: repo path, tool, stack, docker mode, ports, running/stopped
  status, creation time (R-SES-4).
- Buffers session log output so late-connecting clients can catch up (R-OBS-4).
- Exposes a simple API (Unix socket, JSON protocol) to the CLI.
- Persists session state to disk inside its own volume so sessions survive daemon
  restarts (R-SES-8).

See `SPEC/daemon.md` for full daemon specification.

### Session container

- One container per repo, named `construct-<short-id>` where `<short-id>` is the
  first 8 characters of the session UUID (R-SES-3).
- Derived from a stack image (R-STACK-1).
- Has the folder bind-mounted at its exact host path (R-ISO-2).
- Runs the agent process (opencode or other tool) in yolo mode (R-TOOL-3).
- Stopped and restarted across sessions rather than recreated (R-LIFE-1).
- A separate overlay volume (the "agent layer") holds agent-installed tools so
  they survive stack image rebuilds (R-LIFE-4).

See `SPEC/sessions.md` and `SPEC/containers.md` for details.

---

## Directory and file layout on the host

construct stores its state under a config directory derived from XDG conventions:
if `$XDG_CONFIG_HOME` is set, state lives under `$XDG_CONFIG_HOME/construct/`;
otherwise it defaults to `~/.config/construct/`. All paths shown below use the
`~/.config/construct/` shorthand; substitute as appropriate when `$XDG_CONFIG_HOME`
is set.

```
~/.config/construct/                 (or $XDG_CONFIG_HOME/construct/)
├── daemon.sock          # Unix socket for CLI↔daemon communication
├── daemon-state.json    # Persisted session registry (written by daemon)
├── credentials/
│   ├── global/          # Global credentials (R-AUTH-2)
│   │   └── <provider>.env
│   └── folders/
│       └── <folder-slug>/ # Per-folder credential overrides (R-AUTH-3)
│           └── <provider>.env
├── quickstart/
│   └── <folder-slug>.json # Last-used settings per folder (R-UX-1)
└── sessions/
    └── <short-id>/      # Per-session daemon-side working directory
        └── construct-agents.md  # Generated agent instructions for this session

~/.config/opencode/      # Host opencode config (or $XDG_CONFIG_HOME/opencode/)
                         # — read-only mounted into sessions (R-HOME-1)
```

Nothing outside `~/.config/construct/` is written by the daemon or CLI.
The session containers may write inside the mounted folder and their own volumes.

The `sessions/<short-id>/` directories are created by the daemon at session start
and removed at session destroy.

---

## Implementation language

construct is implemented in **Go**. The CLI and daemon are both Go binaries.
Go is chosen for its single-binary distribution, strong Docker SDK support,
and suitability for systems tooling.

### Go module and binary layout

```
Module path:  github.com/construct-run/construct

cmd/
├── construct/       # CLI binary (main package)
└── constructd/      # Daemon binary (main package)

internal/
├── cli/             # CLI command definitions and dispatch
├── client/          # Daemon protocol client
├── daemon/
│   ├── server/      # Unix socket listener, request dispatch
│   ├── registry/    # In-memory + on-disk session state
│   ├── docker/      # Docker SDK wrapper (create, start, stop, rm, inspect, logs, exec)
│   ├── session/     # Session lifecycle logic
│   └── logbuffer/   # Per-session ring buffer
├── stacks/          # Stack image names, embedded Dockerfiles
├── tools/           # opencode install and invoke descriptor
├── auth/            # Credential file management, env injection
├── config/          # Host opencode config path resolution, construct-agents.md
├── network/         # Bridge network creation, port-forward management
├── quickstart/      # Last-invocation persistence per folder
├── slug/            # Folder-slug derivation (shared by quickstart and auth)
└── version/         # Build version (ldflags), version-mismatch checks

stacks/              # Dockerfile build contexts (embedded via go:embed)
├── daemon/
│   └── Dockerfile
├── base/
│   ├── Dockerfile
│   └── entrypoint.sh
├── node/
│   └── Dockerfile
├── go/
│   └── Dockerfile
├── python/
│   └── Dockerfile
├── dotnet/
│   └── Dockerfile
├── dotnet-big/
│   └── Dockerfile
├── ruby/
│   └── Dockerfile
├── base-ui/
│   └── Dockerfile
└── ...
```

Both binaries are compiled with `go build -ldflags "-X .../version.Version=<ver>"`.
Dev builds (no ldflags) use the sentinel value `dev`.

### Dockerfile embedding

All Dockerfiles (daemon and stacks) are embedded in **both** binaries via
`go:embed`. The `internal/stacks` package exposes an `embed.FS` containing
the `stacks/` directory tree.

- The **CLI** binary uses the embedded daemon Dockerfile during bootstrap
  (building the daemon image). It also embeds the stack Dockerfiles but does
  not use them directly — they are compiled into both binaries from the same
  source package.
- The **daemon** binary uses the embedded stack Dockerfiles to build stack
  images on first use. When a `session.start` request references a stack image
  not present locally, the daemon extracts the build context to a temporary
  directory and runs `docker build`.

This ensures both binaries are self-contained with no external file
dependencies. The daemon image is built via a multi-stage Dockerfile: the
first stage compiles `cmd/constructd/` and the second stage copies the
resulting binary into a minimal runtime image (see `stacks/daemon/Dockerfile`).

## Module map
The implementation is structured around the following logical modules.

| Module | Responsibility |
|---|---|
| `cli` | Argument parsing, command dispatch, terminal rendering |
| `client` | Speaks the daemon protocol from the CLI side |
| `daemon/server` | Listens on the Unix socket, dispatches requests |
| `daemon/registry` | In-memory + on-disk session state |
| `daemon/docker` | Wraps Docker API calls (create, start, stop, remove, inspect, logs, exec) |
| `daemon/session` | Session lifecycle logic (create, attach, stop, destroy, reset) |
| `daemon/logbuffer` | Per-session ring-buffer of agent output (R-OBS-4) |
| `stacks` | Stack image names, embedded Dockerfiles (shared by CLI and daemon binaries), build context extraction |
| `tools` | opencode install and invoke descriptor |
| `auth` | Credential file management, env injection |
| `config` | Host opencode config path resolution, construct-injected AGENTS.md |
| `network` | Bridge network creation, port-forward management |
| `quickstart` | Last-invocation persistence per folder |
| `slug` | Folder-slug derivation (shared by quickstart and auth) |
| `version` | Build version via ldflags, version-mismatch checks |

---

## Testing

See `SPEC/testing.md` for the full testing strategy. Key points:

- All Docker operations go through a `DockerClient` interface so session
  lifecycle logic can be unit-tested with a fake (no Docker required).
- Unit tests (`go test ./...`) run without Docker and must pass in under 10s.
- Integration tests (`CONSTRUCT_TEST_DOCKER=1 go test ./...`) verify real
  Docker behaviour: container creation, UID mapping, credential mounting,
  dind/dood, daemon bootstrap, and full session lifecycle.
- CLI end-to-end tests compile the binary and run it as a subprocess.

---

## Key design constraints

1. **Minimal CLI → Docker direct calls.** The CLI talks to Docker directly only
   for daemon bootstrap (inspect, build, run, start) and for debug mode
   (`docker exec -it`). All other Docker operations go through the daemon.
   This is what makes R-SES-8 (sessions survive CLI restarts) possible.

2. **Session identity = folder canonical path.** The daemon keys sessions on the
   canonical absolute path of the folder. Two CLI invocations with
   the same resolved path are the same session (R-SES-3).

3. **Agent runs as root inside the container, UID-mapped at the mount.** This
   satisfies R-LIFE-2 (no sudo needed), R-SEC-2 (root inside is acceptable), and
   R-SEC-3 / R-LIFE-3 (host files are owned by the invoking user). See
   `SPEC/containers.md` for the UID-mapping mechanism.

4. **Credentials are bind-mounted files, not env vars.** Docker inspect must not
   expose secrets (R-AUTH-1). The daemon writes credential files into a tmpfs or
   a secrets directory and bind-mounts them into the session container.

5. **Daemon is itself a container.** It runs as a named Docker container
   (`construct-daemon`), started with `docker run -d` by the CLI bootstrap. This
   means the daemon also needs access to the Docker socket — it is mounted in.
   The daemon container is the only container that ever touches the host Docker
   socket directly.
