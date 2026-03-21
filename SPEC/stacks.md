# construct — Stacks Spec

Covers R-STACK-1, R-STACK-2, R-STACK-3, R-LIFE-2.

---

## What a stack is

A stack is a Docker image that pre-installs a language runtime on top of a common
base image. The agent container is derived from a stack image (R-STACK-1). Users
select a stack at session creation time; the choice is fixed for the session lifetime.

Stacks do not contain opencode. It is installed
at runtime into the agent layer volume so they survive stack image rebuilds (R-LIFE-4).

---

## Image naming convention

```
construct-stack-<name>:<version>
```

Examples:
- `construct-stack-base:latest`
- `construct-stack-node:latest`
- `construct-stack-go:latest`

A `:latest` tag always points to the most recently built stable version.
Pinned version tags are also available for reproducibility.

---

## Dockerfile embedding

All stack Dockerfiles (and the daemon Dockerfile) are embedded in **both**
the CLI binary and the daemon binary via Go's `go:embed` directive. The
`internal/stacks` package exposes an `embed.FS` containing the `stacks/`
directory tree from the source repository:

```
stacks/
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

When the daemon needs to build a stack image, it extracts the relevant
subdirectory from its own embedded FS to a temporary directory and runs
`docker build -t construct-stack-<name>:latest .` against it. The temporary
directory is removed after the build completes.

The CLI embeds the same Dockerfiles but only uses the daemon Dockerfile
during bootstrap. The daemon binary uses the stack Dockerfiles for building
stack images. Both binaries are self-contained with no external file
dependencies.

---

## Base image (`construct-stack-base`)

All stacks inherit from the base image. The base image provides (R-STACK-2):

| Component | Version policy |
|---|---|
| OS | Debian Bookworm (slim) |
| Node.js | LTS (for tool installation) |
| npm | Bundled with Node |
| git | Distribution package |
| Docker CLI | Latest stable (no daemon) |
| Python 3 | Distribution package (`python3`, `pip3`) |
| curl, wget, unzip, tar | Distribution packages |
| bash, zsh | Both available |
| sudo | Distribution package — passwordless for any user |
| gosu | Distribution package — used by entrypoint for privilege drop |

### Why these are in base

- **Node + npm**: opencode is an npm package. Node must be in base so the tool
  install step works on every stack.
- **git**: Required for the agent to operate on the folder contents.
- **Docker CLI**: Required for `dind` and `dood` modes; present but non-functional
  in `none` mode (no daemon socket). Does not add meaningful risk.
- **Python 3**: Baseline for a large class of build tools and scripts.

### Startup script

The base image includes `/entrypoint.sh` (copied into the image by the
Dockerfile). This script is set as the container's default command via
`CMD ["/entrypoint.sh"]` (Docker's `ENTRYPOINT` is not used). It:

1. Registers the host UID/GID (from `CONSTRUCT_UID` / `CONSTRUCT_GID` env
   vars) in `/etc/passwd` and `/etc/group` so that `sudo` can resolve the user.
2. Sources all credential `.env` files from `/run/construct/creds/global/` and
   `/run/construct/creds/folder/` (per-folder overrides global).
3. Creates agent layer directories (`/agent/bin`, `/agent/lib`, `/agent/cache`,
   `/agent/home/.config/opencode`) and chowns them to the host user.
4. Writes `/agent/home/.config/opencode/opencode.json` with construct context
   injection (`instructions: ["/run/construct/agents.md"]`) and
   `autoupdate: false`.
5. Drops to the host user via `gosu uid:gid` and runs `exec sleep infinity` to
   keep the container alive.

The agent process is launched separately via `docker exec -d --user uid:gid` by
the daemon (see `SPEC/sessions.md`). The full startup script is defined in
`SPEC/containers.md`.

### Agent layer hooks in base

The base image defines `$HOME=/agent/home` and `XDG_CONFIG_HOME=/agent/home/.config`
in the Dockerfile's `ENV` directives. `/agent/bin` is prepended to `PATH`.
`NPM_CONFIG_PREFIX=/agent` is set so that `npm install -g` installs packages to
`/agent/lib/node_modules/` with executable symlinks in `/agent/bin/` — both on the
persistent agent layer volume.

---

## Language stacks

### `construct-stack-node`

Adds to base:
- Node.js (latest LTS, installed alongside the base Node; managed via `nvm` baked
  into the agent layer or via a version manager in the image)
- pnpm, yarn (global installs)
- Build essentials for native addons (`build-essential`, `python3-dev`)

### `construct-stack-go`

Adds to base:
- Go (latest stable)
- Standard Go toolchain (`go`, `gofmt`, `gopls`)
- `$GOPATH` set under `/agent/lib/go`

### `construct-stack-python`

Adds to base:
- Python (latest stable, managed via `pyenv` baked into the image)
- `pip`, `pipx`, `poetry`, `uv`
- Common build deps (`libffi-dev`, `libssl-dev`)

### `construct-stack-dotnet`

Adds to base:
- .NET SDK (latest LTS)
- `dotnet` CLI

### `construct-stack-dotnet-big`

Adds to base:
- Multiple .NET SDK versions (current LTS, previous LTS, and current STS)
- Suitable for multi-targeting projects

### `construct-stack-ruby`

Adds to base:
- Ruby (latest stable, via `rbenv`)
- `bundler`, `gem`

---

## UI variants (additive)

UI variants add browser automation / MCP tooling on top of any base stack. They
are named with a `-ui` suffix:

- `construct-stack-base-ui`
- `construct-stack-node-ui`
- etc.

What a UI variant adds:
- Playwright and its browser dependencies
- `@playwright/mcp` package pre-installed in the system npm prefix
- Chromium, Firefox (or a subset, depending on image size constraints)
- `xvfb` (for headless display server, if needed)

UI variants are larger images. Users opt in explicitly by choosing a `-ui` stack.

---

## Stack image build and distribution

Stack images are built from Dockerfiles embedded in the CLI binary (see
"Dockerfile embedding" above). Images are built locally using `docker build`
during development.

The daemon builds stack images on first use. When a `session.start` request
references a stack image that is not present locally, the daemon extracts the
embedded Dockerfile and build context to a temp directory and runs `docker build`.

### Image update policy

- Images are tagged with both `:latest` and a version tag matching the construct
  binary version (e.g. `:0.3.0`).
- All images are stamped with the label `io.construct.version=<build-version>`
  (see `SPEC/daemon.md`).
- The daemon uses `:latest` by default.
- If a newer `:latest` is available (or the Dockerfile has changed — detected via
  the `io.construct.version` label not matching the daemon binary version), the
  daemon will rebuild it before creating a **new** session. Existing session
  containers are not affected (they continue using the image layer they started
  with).
- Agent-installed tools in the agent layer volume are unaffected by image updates
  (R-LIFE-4).

---

## Tool installation (R-LIFE-2)

The agent runs as the host user's UID inside the container, but has full
passwordless `sudo` access (granted to any user via `/etc/sudoers.d/agent`).
This means:

- Standard package manager commands work without sudo, directed to the agent
  layer volume by environment variables:
  - `npm install -g <pkg>` → `/agent/lib/node_modules/.bin` (via `NPM_CONFIG_PREFIX=/agent`)
  - `pip install --user <pkg>` → `/agent/home/.local/bin` (via `HOME=/agent/home`)
  - `go install <pkg>` → `/agent/lib/go/bin` (via `GOPATH`)
- System package installation works via sudo:
  - `sudo apt-get install <pkg>` — installs system-wide (image layer, not persistent)

All agent layer installs land in the agent layer volume and survive restarts and
image rebuilds (R-LIFE-4). System packages installed via `sudo apt-get` do not
persist across container recreation (they live in the container's writable layer),
but persist across stop/start cycles.

The `sudo` / `gosu` binaries are installed in the base image. Passwordless sudo
is safe here because this is a single-tenant container: the container boundary is
the security perimeter (R-SEC-4).
