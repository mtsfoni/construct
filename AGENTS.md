# construct – Copilot Instructions

## Workflow

**Spec first, tests second, code third.** For any change that alters product behaviour, track progress by creating a todo list with the following steps before starting:

1. **Write or update the spec** in `docs/spec/` before touching implementation code. The spec defines the intended behaviour and serves as the source of truth.
2. **Write the tests** that encode the behaviour described in the spec.
3. **Write the code** to make the tests pass. Run `go test ./...` to verify the full suite stays green.
4. **Verify alignment** — after implementation, re-read the spec and confirm every stated behaviour is reflected in the code. Update the spec if the implementation diverged for good reason; do not leave spec and code contradicting each other.
5. **Update `CHANGELOG.md`** — add an entry under `## [Unreleased]` for every change that is significant to the user (new commands, new flags, new stacks/tools, changed defaults, bug fixes, behaviour changes). Internal refactors with no user-visible effect do not need an entry.

Mark each todo complete as you finish it. Do not move on to the next step until the current one is done.

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

**Any change that alters product behaviour** (new commands, new flags, new stacks/tools, changed defaults, new persistence) **must follow the workflow above**: spec first, then tests, then code, then alignment check, then changelog. Name the spec file after the feature (e.g. `docs/spec/quickstart-qs.md`). The spec should cover: the problem, the solution/behaviour, persistence details if applicable, and a table of files changed.

**Adding a new tool** — create a new file in `internal/tools/` that calls `register(&Tool{...})` in `init()`. No other registration is needed.

**Adding a new stack** — add a `Dockerfile` under `internal/stacks/dockerfiles/<name>/` and add the name to `validStacks` in `stacks.go`. The `//go:embed dockerfiles` directive picks it up automatically.

**Env file precedence** — `~/.construct/.env` is loaded first; `.construct/.env` in the repo root overrides it. Both files are parsed by the same `mergeEnvFile` function that strips surrounding quotes (single or double) from values.

**Home volume persistence** — the agent's `/home/agent` is a named Docker volume (`construct-home-<tool>-<8-byte-hex>`). It survives container restarts, preserving shell history, tool caches, and any seeded config files defined in `Tool.HomeFiles`. The volume is only initialised once; `--rebuild` does not reset it.

**No external Go dependencies** — `go.mod` declares no `require` directives. Everything is standard library + `os/exec` shelling out to `docker`.

**SELinux hosts (Fedora, RHEL, etc.)** — all host bind mounts must carry the `:z` relabeling suffix so SELinux grants the container access. This applies to `/workspace`, `/run/secrets`, and the home volume seed dir. Named Docker volumes do not need `:z`. Unix sockets (e.g. `/var/run/docker.sock` in DooD mode) **cannot** be relabeled with `:z` — use `--security-opt label=disable` on the container instead. If a container silently fails to read a bind-mounted path, a missing `:z` is the first thing to check; if it fails to access a socket, add `--security-opt label=disable`.
