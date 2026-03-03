# construct – Copilot Instructions

## Workflow

**Tests first.** Before implementing any feature or fix, write the tests that define the expected behaviour. Only then write the code to make them pass. Run `go test ./...` to verify the full suite stays green.

## Build & test

```bash
# Build the binary
go build -o construct ./cmd/construct

# Run all tests
go test ./...

# Run a single package's tests
go test ./internal/config/...

# Run a single test by name
go test ./internal/config/... -run TestSet_UpdatesExistingKey
go test ./cmd/construct/... -run TestConfigSet_WritesToGlobalFile
```

The integration tests in `cmd/construct/config_test.go` compile the binary themselves via `TestMain`; no manual build step is needed before running them.

## Architecture

`construct` is a thin orchestration layer that runs AI coding agents inside Docker containers with an isolated Docker-in-Docker daemon.

**Execution flow** (`runner.Run`):
1. Build the stack image (embedded Dockerfiles in `internal/stacks/dockerfiles/`) if not cached
2. Build a tool image on top of the stack by generating a minimal Dockerfile from `Tool.InstallCmds`
3. Create/reuse a persistent named Docker volume for the agent's home dir (keyed by SHA256 of repo path + tool name)
4. Start a privileged `docker:dind` sidecar container on an isolated bridge network
5. Run the agent container on the same network with `DOCKER_HOST` pointed at the dind daemon
6. On exit (clean or SIGINT/SIGTERM), stop and remove both containers and the network

**Key types:**
- `tools.Tool` (`internal/tools/tools.go`) — defines install commands, auth env vars, run command, extra env, and home dir seed files for each AI tool
- `runner.Config` (`internal/runner/runner.go`) — top-level options passed from `main`
- `dind.Instance` (`internal/dind/dind.go`) — lifecycle handle for the dind sidecar
- `config` package (`internal/config/`) — manages `~/.construct/.env` and `.construct/.env` credential files

**Image naming convention:** stack images are `construct-<stack>`, tool images are `construct-<stack>-<tool>` (e.g. `construct-node-copilot`).

## Key conventions

**Any change that alters product behaviour** (new commands, new flags, new stacks/tools, changed defaults, new persistence) **must be accompanied by a spec document** in `docs/spec/` and an entry in `CHANGELOG.md` under `## [Unreleased]`. Name the file after the feature (e.g. `docs/spec/quickstart-qs.md`). The spec should cover: the problem, the solution/behaviour, persistence details if applicable, and a table of files changed.

**Adding a new tool** — create a new file in `internal/tools/` that calls `register(&Tool{...})` in `init()`. No other registration is needed.

**Adding a new stack** — add a `Dockerfile` under `internal/stacks/dockerfiles/<name>/` and add the name to `validStacks` in `stacks.go`. The `//go:embed dockerfiles` directive picks it up automatically.

**Env file precedence** — `~/.construct/.env` is loaded first; `.construct/.env` in the repo root overrides it. Both files are parsed by the same `mergeEnvFile` function that strips surrounding quotes (single or double) from values.

**Home volume persistence** — the agent's `/home/agent` is a named Docker volume (`construct-home-<tool>-<8-byte-hex>`). It survives container restarts, preserving shell history, tool caches, and any seeded config files defined in `Tool.HomeFiles`. The volume is only initialised once; `--rebuild` does not reset it.

**No external Go dependencies** — `go.mod` declares no `require` directives. Everything is standard library + `os/exec` shelling out to `docker`.

**SELinux hosts (Fedora, RHEL, etc.)** — all host bind mounts must carry the `:z` relabeling suffix so SELinux grants the container access. This applies to `/workspace`, `/run/secrets`, and the home volume seed dir. Named Docker volumes do not need `:z`. If a container silently fails to read a bind-mounted path, a missing `:z` is the first thing to check.
