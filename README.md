# construct

Run AI coding agents (opencode) in isolated Docker containers. The agent gets its own environment — its own filesystem, its own tool installs, its own home directory — without touching your machine. Your repo is bind-mounted at its exact host path so paths, git worktrees, and relative references all work correctly.

## How it works

`construct run` starts a long-lived daemon (`constructd`) in a Docker container on your host. The daemon manages session containers — one per repo. When you run `construct` for a folder, it either creates a new session container or attaches to the existing one.

Each session container:
- Has a persistent **agent layer volume** (`/agent`) for tool installs, home directory, and build caches — survives container restarts
- Bind-mounts your **repo at its exact host path** (e.g. `/home/user/src/myapp`)
- Bind-mounts your **opencode config** so your models, skills, and slash commands work identically inside the container
- Runs the agent as **your host UID:GID** so files written to the repo have correct ownership
- Has **passwordless sudo** so the agent can install system packages (`apt-get install`)

> **Not a security guarantee.** A sufficiently motivated agent could escape the container. This tool is about giving the agent a clean, persistent workspace and keeping it from accidentally modifying unrelated parts of your system.

## Requirements

- Docker (running on the host)
- Go 1.22+ (to build from source)

## Installation

Download a binary from the [releases page](https://github.com/mtsfoni/construct/releases) and put it on your `PATH`:

```bash
curl -L https://github.com/mtsfoni/construct/releases/latest/download/construct-linux-amd64 \
  -o ~/.local/bin/construct && chmod +x ~/.local/bin/construct
```

Or build from source:

```bash
git clone https://github.com/mtsfoni/construct
cd construct
bash install.sh
```

`install.sh` builds both `construct` (CLI) and `constructd` (daemon) into `~/.local/bin`.

## Usage

```
construct [flags] [path]
construct <command> [flags] [args]
```

`path` defaults to the current working directory.

```bash
# Start a session for the current folder (base stack)
construct

# Start a session with a specific stack
construct --stack dotnet /path/to/repo

# Publish ports for dev servers
construct --stack node --port 3000 --port 5173 .

# Replay the last invocation for a folder
construct qs

# List all sessions
construct ls

# View session logs
construct logs

# Destroy a session and all its state
construct destroy

# Remove everything construct has created
construct purge
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--stack` | `base` | Language stack (see below) |
| `--docker` | `none` | Docker mode: `none`, `dind`, `dood` |
| `--port` | — | Publish a container port (repeatable, e.g. `--port 3000`) |
| `--no-web` | — | Don't auto-open the web UI |
| `--debug` | — | Drop into a shell instead of starting the agent |

## Supported stacks

| Stack | Contents |
|-------|----------|
| `base` | Debian bookworm, Node LTS, Python 3, git, sudo, Docker CLI |
| `node` | base |
| `dotnet` | base + .NET 10 SDK |
| `go` | base + Go toolchain |
| `python` | base + pip, venv |
| `ruby` | base + Ruby, Bundler, Jekyll |
| `base-ui` | base + Playwright MCP + Chromium |
| `dotnet-ui` | dotnet + Playwright MCP + Chromium |

## Credentials

Credentials are stored as `.env` files and sourced into the container at startup. They are never passed as Docker env vars.

```bash
# Set a global credential (available in all sessions)
construct config cred set ANTHROPIC_API_KEY=sk-ant-...

# Set a per-folder credential (only for sessions in this folder)
construct config cred set OPENAI_API_KEY=sk-... --folder /path/to/repo

# List credentials
construct config cred list

# Remove a credential
construct config cred unset ANTHROPIC_API_KEY
```

Global credentials live in `~/.config/construct/credentials/global/`. Per-folder credentials live in `~/.config/construct/credentials/folders/<slug>/`.

## Docker modes

| Mode | Description |
|------|-------------|
| `none` (default) | No Docker access inside the container |
| `dind` | Private Docker-in-Docker daemon — the agent gets its own isolated Docker environment |
| `dood` | Docker-outside-of-Docker — the agent shares your host Docker socket. Gives full host Docker access; use with caution |

## Project structure

```
cmd/construct/          CLI entry point
cmd/constructd/         Daemon entry point
embedfs/stacks/         Embedded Dockerfiles for each stack
internal/
  auth/                 Credential storage (.env files)
  bootstrap/            Daemon startup and lifecycle
  cli/                  CLI command implementations
  client/               Unix socket client (daemon IPC)
  config/               Config dir resolution
  daemon/
    session/            Session lifecycle (create, start, stop, destroy)
    server/             Unix socket server (newline-delimited JSON)
    registry/           Session state persistence
  stacks/               Stack image names and build context
SPEC/                   Design specs
REQS/                   Requirements
```
