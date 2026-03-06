# construct

Run [opencode](https://opencode.ai) in an isolated Docker container. The agent gets your repository and a language runtime; it cannot reach the rest of your machine. Not a perfect sandbox, but meaningfully better than running directly on your host.

A side-effect: you never need to install language runtimes locally — pick a stack and the container has everything.

> **Linux and Windows.** construct requires a Docker host (Docker Desktop, Engine, or OrbStack). Docker Desktop on macOS is not yet supported.

## How it works

construct runs the agent in a Docker container with only the repository bind-mounted. Docker access is opt-in via `--docker`:

- **`--docker none`** *(default)* — no Docker inside the container. Fastest start, minimal attack surface.
- **`--docker dood`** — Docker-outside-of-Docker: the host socket `/var/run/docker.sock` is bind-mounted so the agent shares your host daemon.
- **`--docker dind`** — Docker-in-Docker: a fresh [Docker-in-Docker](https://hub.docker.com/_/docker) sidecar is started with its own private daemon on an isolated bridge network. The agent talks to it via `DOCKER_HOST=tcp://dind:2375`. Both containers and the network are removed when the session ends.

In `dind` mode the agent:
- Cannot see your host Docker daemon or any of your existing containers/images
- Gets a clean, empty Docker environment to build and run whatever it needs

**Testcontainers works out of the box in `dind` mode.** Because the agent has its own Docker daemon, frameworks like [Testcontainers](https://testcontainers.com/) that spin up containers during tests just work — no extra configuration needed.

> **Not a security guarantee.** A sufficiently motivated agent could escape the container. This tool is about isolation and a clean workspace, not hardened sandboxing. See the [threat model](docs/threat-model.md) for a full breakdown of what is and isn't protected, and the deliberate trade-offs made.

## Installation

Download the pre-built binary:

- **Linux (x86-64):**
  ```bash
  curl -L https://github.com/mtsfoni/construct/releases/latest/download/construct-linux-amd64 -o ~/.local/bin/construct
  chmod +x ~/.local/bin/construct
  ```
- **Windows (x86-64):**
  Download `construct-windows-amd64.exe` from the [releases page](https://github.com/mtsfoni/construct/releases/latest).

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
construct [--stack <stack>] [--docker <mode>] [--rebuild] [--reset] [--debug] [--mcp] [--port <port>] [path] [-- <tool-args>]
construct config <set|unset|list> [--local] [KEY [VALUE]]
construct qs [path] [-- <tool-args>]
```

`path` defaults to the current working directory.

```bash
construct --stack dotnet /path/to/repo
construct --stack go ~/projects/myapp
construct --stack ui --mcp --port 3000 .
construct --stack go --docker dind .
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--stack` | `base` | Language stack: `base`, `dotnet`, `dotnet-big`, `dotnet-big-ui`, `dotnet-ui`, `go`, `ruby`, `ruby-ui`, `ui` |
| `--docker` | `none` | Docker access mode: `none` (no Docker), `dood` (share host socket), `dind` (isolated Docker-in-Docker sidecar) |
| `--rebuild` | `false` | Force rebuild of stack and tool images |
| `--reset` | `false` | Wipe and re-seed the per-repo agent home volume before starting. Does **not** affect the global auth volume. |
| `--debug` | `false` | Start an interactive shell instead of the agent (for troubleshooting) |
| `--mcp` | `false` | Activate MCP servers (e.g. `@playwright/mcp`); requires `--stack ui`, `--stack dotnet-ui`, `--stack dotnet-big-ui`, or `--stack ruby-ui` for browser automation |
| `--port` | *(none)* | Publish a container port to the host (repeatable). Accepts any format `docker run -p` supports: `3000`, `9000:3000`, `127.0.0.1:3000:3000`. |
| `--version` | — | Print the construct version and exit. |

## Quickstart (`qs`)

After running `construct` at least once in a repo, replay the exact same invocation with:

```bash
construct qs [path]
```

`qs` restores the last `--stack`, `--docker`, `--mcp`, and all `--port` values used for that repo. Settings are stored in `~/.construct/last-used.json`.

You can pass extra arguments to the tool after `--`. They are forwarded verbatim and are not saved to last-used:

```bash
# Resume a previous opencode session
construct qs -- -s ses_deadbeefcafe1234abcd5678

# Same with an explicit repo path
construct qs ~/projects/myapp -- -s ses_deadbeefcafe1234abcd5678
```

The `--` separator works with plain `construct` too:

```bash
construct --stack go -- -s ses_deadbeefcafe1234abcd5678
```

## Supported tools

| Tool | Package | Yolo mode |
|------|---------|-----------|
| `opencode` | `opencode-ai` (npm) | `OPENCODE_PERMISSION={"*":"allow"}` |

## Supported stacks

| Stack | Base | Additions |
|-------|------|-----------|
| `base` | Ubuntu 22.04, Node 20, Python 3, Docker CLI, buildx, git | — |
| `dotnet` | base | .NET 10 SDK |
| `dotnet-big` | base | .NET 8, 9, and 10 SDKs |
| `dotnet-big-ui` | dotnet-big | `@playwright/mcp`, Chromium |
| `dotnet-ui` | dotnet | `@playwright/mcp`, Chromium |
| `go` | base | Go 1.24 |
| `ruby` | base | Ruby (system), Bundler, Jekyll |
| `ruby-ui` | ruby | `@playwright/mcp`, Chromium |
| `ui` | base | `@playwright/mcp`, Chromium |

## Auth / config

Use `construct config` to manage credentials without editing files by hand:

```bash
# Store a credential globally (~/.construct/.env)
construct config set ANTHROPIC_API_KEY sk-ant-...

# Override a credential for one repo only (.construct/.env in the repo root)
construct config set --local ANTHROPIC_API_KEY sk-ant-...

# List all configured keys (values are masked)
construct config list

# Remove a credential
construct config unset ANTHROPIC_API_KEY
```

Credentials are stored as plain text in `~/.construct/.env`, with mode `0600`.
Use `--local` to write to `.construct/.env` in the repo root instead — useful
when a project needs different credentials than your global defaults. If you use
a per-repo file, add `.construct/.env` to the repo's `.gitignore`.

| Tool | Required credential |
|------|-------------------|
| `opencode` | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, or any other [supported provider key](https://opencode.ai/docs/providers). Alternatively, run `/connect` inside the opencode TUI to authenticate interactively — credentials are written to a **global auth volume** (`construct-auth-opencode`) that persists across repos and survives `--reset`. |

> Credentials are injected into the container via bind-mounted files, not as
> `docker run -e` flags, so values do not appear in `docker inspect` output.
> See the [threat model](docs/threat-model.md) for full security trade-offs.

## Agent instructions

The entire repo is bind-mounted at `/workspace`, so any instruction files already in the repo are available to the agent automatically — no special mounting needed. Place instructions wherever the tool expects them:

- `AGENTS.md` — picked up by opencode and other tools that follow the Agents convention

construct also injects a global `~/.config/opencode/AGENTS.md` into every session that tells the agent it is running inside a construct container. The content of this file is mode-aware: in `dind` mode it includes Docker usage guidance and Testcontainers notes; in `dood` mode it warns that the agent is sharing the host Docker daemon; in `none` mode no Docker guidance is included. When `--port` is used, this file also contains server binding rules (bind to `0.0.0.0`, use the published ports) so the agent's dev server is reachable from the host browser.

**Global opencode slash commands** are also available inside the container. If `~/.config/opencode/commands/` exists on the host, it is automatically bind-mounted read-only into the agent at startup — so any [custom commands](https://opencode.ai/docs/commands) you have defined globally on your machine work inside construct sessions without any extra configuration. Commands are simple markdown files, making them easy to write by hand or draft with the agent. The agent can create per-project commands directly in `.opencode/commands/` inside the repo; for global commands, create the file in `~/.config/opencode/commands/` on your host machine (the in-container mount is read-only).
