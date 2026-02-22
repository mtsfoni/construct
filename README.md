# construct

Run AI coding agents in yolo/auto-approve mode without giving them access to your actual machine. Not a perfect sandbox, but meaningfully better than running directly on your host. The agent gets its own isolated Docker daemon so it can build, test, and run containers freely without touching yours.

> **Linux only (for now).** construct requires a Linux Docker host. Docker Desktop on macOS and Windows is not yet supported.

## How it works

Each session spins up a fresh [Docker-in-Docker](https://hub.docker.com/_/docker) (dind) sidecar container with its own private Docker daemon. The agent container joins the same isolated network and talks to that daemon via `DOCKER_HOST`. When the session ends — cleanly or via Ctrl-C — both containers and the network are removed.

This means the agent:
- Cannot see your host Docker daemon or any of your existing containers/images
- Gets a clean, empty Docker environment to build and run whatever it needs
- Is isolated from your host filesystem — it can only access the repo you point it at

**Testcontainers works out of the box.** Because the agent has its own Docker daemon, frameworks like [Testcontainers](https://testcontainers.com/) that spin up containers during tests just work — no extra configuration needed.

> **Not a security guarantee.** A sufficiently motivated agent could escape the container. This tool is about isolation and a clean workspace, not hardened sandboxing. See the [threat model](docs/threat-model.md) for a full breakdown of what is and isn't protected, and the deliberate trade-offs made.

## Installation

Download the pre-built binary (Linux x86-64):

```bash
curl -L https://github.com/mtsfoni/construct/releases/latest/download/construct-linux-amd64 -o ~/.local/bin/construct
chmod +x ~/.local/bin/construct
```

Or install via Go:

```bash
go install github.com/mtsfoni/construct/cmd/construct@latest
```

Or clone and build locally:

```bash
git clone https://github.com/mtsfoni/construct
cd construct
go build -o construct ./cmd/construct
```

## Usage

```
construct --tool <tool> [--stack <stack>] [--rebuild] [--debug] [path]
construct config <set|unset|list> [--local] [KEY [VALUE]]
construct qs [path]
```

`path` defaults to the current working directory.

```bash
construct --tool opencode --stack dotnet /path/to/repo
construct --tool copilot --stack node .
construct --tool opencode --stack python ~/projects/myapp
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--tool` | *(required)* | AI tool to run: `copilot`, `opencode` |
| `--stack` | `node` | Language stack: `base`, `node`, `dotnet`, `python`, `go` |
| `--rebuild` | `false` | Force rebuild of stack and tool images |
| `--debug` | `false` | Start an interactive shell instead of the agent (for troubleshooting) |

## Quickstart (`qs`)

After running `construct` at least once in a repo, replay the same tool and stack with:

```bash
construct qs [path]
```

The last-used tool/stack per repo is stored in `~/.construct/last-used.json`.

## Supported tools

| Tool | Package | Yolo mode |
|------|---------|-----------|
| `copilot` | `@github/copilot` (npm) | `copilot --yolo` |
| `opencode` | `opencode-ai` (npm) | `OPENCODE_PERMISSION={"*":"allow"}` |

## Supported stacks

| Stack | Base | Additions |
|-------|------|-----------|
| `base` | Ubuntu 22.04, Node 20, Docker CLI, buildx, git | — |
| `node` | base | — |
| `dotnet` | base | .NET 10 SDK |
| `python` | base | Python 3, pip |
| `go` | base | Go 1.24 |

## Auth / config

Use `construct config` to manage credentials without editing files by hand:

```bash
# Store a credential globally (~/.construct/.env)
construct config set GH_TOKEN ghp_...
construct config set ANTHROPIC_API_KEY sk-ant-...

# Override a credential for one repo only (.construct/.env in the repo root)
construct config set --local ANTHROPIC_API_KEY sk-ant-...

# List all configured keys (values are masked)
construct config list

# Remove a credential
construct config unset GH_TOKEN
```

Credentials are stored as plain text in `~/.construct/.env`, with mode `0600`.
Use `--local` to write to `.construct/.env` in the repo root instead — useful
when a project needs different credentials than your global defaults. If you use
a per-repo file, add `.construct/.env` to the repo's `.gitignore`.

| Tool | Required credential |
|------|-------------------|
| `copilot` | `GH_TOKEN` — a GitHub PAT with Copilot access |
| `opencode` | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, or any other [supported provider key](https://opencode.ai/docs/providers). Alternatively, run `/connect` inside the opencode TUI to authenticate interactively — credentials are written to the persistent home volume and survive across sessions. |

> Credentials are injected into the container via bind-mounted files, not as
> `docker run -e` flags, so values do not appear in `docker inspect` output.
> See the [threat model](docs/threat-model.md) for full security trade-offs.

## Agent instructions

- `.github/copilot-instructions.md` in the repo — mounted automatically for both tools
- `.construct/instructions.md` in the repo — takes precedence over the above; mounted as the tool's native instruction file
