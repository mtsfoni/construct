# agentbox

Run AI coding agents in yolo/auto-approve mode without giving them access to your actual machine. Not a perfect sandbox, but meaningfully better than running directly on your host. The agent gets its own isolated Docker daemon so it can build, test, and run containers freely without touching yours.

## How it works

Each session spins up a fresh [Docker-in-Docker](https://hub.docker.com/_/docker) (dind) sidecar container with its own private Docker daemon. The agent container joins the same isolated network and talks to that daemon via `DOCKER_HOST`. When the session ends — cleanly or via Ctrl-C — both containers and the network are removed.

This means the agent:
- Cannot see your host Docker daemon or any of your existing containers/images
- Gets a clean, empty Docker environment to build and run whatever it needs
- Is still contained inside a Linux container with no special host mounts (beyond the repo)

> **Not a security guarantee.** A sufficiently motivated agent could escape the container. This tool is about isolation and a clean workspace, not hardened sandboxing.

## Installation

```bash
go install github.com/mtsfoni/construct/cmd/agentbox@latest
```

Or clone and build locally:

```bash
git clone https://github.com/mtsfoni/construct
cd construct
go build -o agentbox ./cmd/agentbox
```

## Usage

```
agentbox --tool <tool> [--stack <stack>] [--rebuild] [path]
```

`path` defaults to the current working directory.

```bash
agentbox --tool opencode --stack dotnet /path/to/repo
agentbox --tool copilot --stack node .
agentbox --tool opencode --stack python ~/projects/myapp
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--tool` | *(required)* | AI tool to run: `copilot`, `opencode` |
| `--stack` | `node` | Language stack: `base`, `node`, `dotnet`, `python` |
| `--rebuild` | `false` | Force rebuild of stack and tool images |

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

## Auth / config

Create `~/.agentbox/.env` with your API keys:

```env
# For copilot
GH_TOKEN=ghp_...

# For opencode (one or more providers)
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
```

A `.agentbox/.env` file in the repo root overrides the global config.

## Agent instructions

- `.github/copilot-instructions.md` in the repo — mounted automatically for both tools
- `.agentbox/instructions.md` in the repo — mounted as the tool's instruction file

## Project structure

```
agentbox/
  cmd/agentbox/main.go              # entrypoint, flag parsing
  internal/
    tools/                          # tool definitions (copilot, opencode)
    stacks/                         # stack images + embedded Dockerfiles
      dockerfiles/
        base/Dockerfile
        dotnet/Dockerfile
        node/Dockerfile
        python/Dockerfile
    dind/                           # dind lifecycle management
    runner/                         # container orchestration
  go.mod
  README.md
```

## Non-goals for v1

- No plugin system
- No remote image registry or image pushing
- No Windows support
- No TLS for dind (port 2375, network-scoped — not exposed to the host)
- Not a security guarantee