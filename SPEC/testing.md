# construct — Testing Spec

---

## Goals

1. Every package has tests that can run without Docker (fast, CI-friendly).
2. Integration tests verify real Docker behaviour (container creation, UID
   mapping, port publishing, credential mounting, dind/dood).
3. An implementing agent can run `go test ./...` at any point during
   development and know whether what they built works.
4. Tests are written alongside the code they test, not as an afterthought.

---

## Test layers

### Layer 1 — Unit tests (no Docker, no network)

These tests run in-process with no external dependencies. They use interfaces
and fakes to isolate the code under test from Docker, the filesystem, and the
network. Every package must have unit tests.

**What they cover:**

| Package | What to test |
|---|---|
| `slug` | Slug derivation from various paths (leading slash, deep paths, truncation at 200 chars, no-underscore edge case) |
| `version` | Version comparison logic, dev-build sentinel handling |
| `stacks` | Embedded FS contains expected Dockerfile paths, image name derivation, version label matching (with stubbed `imageLabel`) |
| `tools` | Install command string, invoke command string, web port constant |
| `auth` | Provider derivation from key (first word before `_`, lowercased), `.env` file read/write/delete, key masking, per-folder override resolution |
| `config` | `construct-agents.md` template rendering with various inputs (ports, docker modes, tool names) |
| `quickstart` | Record serialisation/deserialisation, slug-based file path derivation |
| `client` | Request envelope serialisation, response envelope deserialisation, error handling for malformed JSON |
| `daemon/registry` | Add/remove/lookup by ID and by repo path, atomic JSON serialisation to a temp dir, reconciliation logic (given a mock Docker state) |
| `daemon/logbuffer` | Ring buffer: write N lines, read back, overflow eviction, follow-mode channel delivery, concurrent read/write safety |
| `daemon/server` | Request dispatch to correct handler (with stub handlers), unknown command → error response, malformed JSON → error response |
| `daemon/session` | Port spec parsing (`"3000:3000"`, `"8080"`, `"8080:9000"`), session state machine transitions (created→running→stopped→running, running→destroyed), flag-conflict warning generation |
| `network` | Port free-check logic (with a mock listener), network name derivation |
| `cli` | Canonical path resolution (with temp dirs and symlinks), argument disambiguation (path vs session ID vs prefix), quickstart replay flag mapping |

**Rules:**

- No `docker` CLI calls. No Docker SDK calls. No network listeners (except
  in-process ones for port-check tests).
- Use `t.TempDir()` for any filesystem operations.
- Use interfaces for Docker operations; provide fake/mock implementations in
  test files. The Docker wrapper (`daemon/docker`) should be defined behind an
  interface so session lifecycle logic can be tested with a fake.
- Tests must pass in under 10 seconds total for the entire unit suite.

### Layer 2 — Integration tests (require Docker)

These tests run real Docker operations. They are gated behind a build tag
or an environment variable check so they don't fail in environments without
Docker.

**Gate mechanism:**

```go
func skipWithoutDocker(t *testing.T) {
    t.Helper()
    if os.Getenv("CONSTRUCT_TEST_DOCKER") == "" {
        t.Skip("set CONSTRUCT_TEST_DOCKER=1 to run Docker integration tests")
    }
    // Also verify Docker is reachable
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    cli, err := client.NewClientWithOpts(client.FromEnv)
    if err != nil {
        t.Skipf("Docker client error: %v", err)
    }
    if _, err := cli.Ping(ctx); err != nil {
        t.Skipf("Docker not reachable: %v", err)
    }
}
```

**What they cover:**

| Test | What it verifies |
|---|---|
| `TestContainerCreate_BasicSession` | Create a container from a known image (e.g. `debian:bookworm-slim`), verify it starts, exec a command inside, stop and remove it. Validates the two-step create/start flow. |
| `TestContainerCreate_UIDMapping` | Create a container with an idmap bind mount. Write a file inside the container as root. Verify the file is owned by the host user's UID on the host side. Skip if kernel < 5.12 or Docker < 25. |
| `TestContainerCreate_AgentLayerVolume` | Create a container with a named volume at `/agent`. Write a file to `/agent/bin/test`. Stop, remove, and recreate the container with the same volume. Verify the file persists. |
| `TestContainerCreate_CredentialMount` | Write a test `.env` file to a temp dir. Create a container with the dir bind-mounted at `/run/construct/creds/global/`. Exec `env` inside and verify the variable is present (sourced by entrypoint). |
| `TestContainerCreate_PortPublish` | Create a container that listens on a port. Publish it to a host port. Verify connectivity from the host side via TCP dial. |
| `TestContainerCreate_Entrypoint` | Build a minimal image with the entrypoint script. Start a container. Verify PID 1 is `sleep infinity` (via `docker exec ps`). Verify credential env vars are set. |
| `TestDinD_SidecarLifecycle` | Create a dind sidecar, session network, and agent container. Verify agent can reach the sidecar via `docker info` using `DOCKER_HOST`. Stop and destroy all resources. Verify cleanup (no orphaned containers/networks). |
| `TestDooD_SocketAccess` | Create a container with the host Docker socket mounted. Verify `docker ps` works inside the container. |
| `TestDaemon_Bootstrap` | Build the daemon image from the embedded Dockerfile. Start the daemon container. Verify the Unix socket becomes connectable. Stop and remove. |
| `TestDaemon_SessionLifecycle` | Start the daemon. Send `session.start` via the protocol. Verify the session container exists. Send `session.stop`. Verify the container is stopped. Send `session.destroy`. Verify cleanup. |
| `TestDaemon_Reconciliation` | Start the daemon with a pre-populated `daemon-state.json` that references containers in various states (running, stopped, missing). Verify the registry is corrected after reconciliation. |
| `TestReset_VolumeFreshness` | Create a session, write a file to the agent layer, reset. Verify the file is gone and the tool install runs again. |

**Rules:**

- Each test cleans up after itself (remove containers, volumes, networks,
  temp dirs). Use `t.Cleanup()` to guarantee cleanup even on failure.
- Use unique names per test run (include `t.Name()` or a random suffix) to
  avoid collisions with parallel test runs.
- Container and image names use a `construct-test-` prefix so they can be
  identified and bulk-cleaned if a test crashes.
- Integration tests may take up to 2 minutes each. Total integration suite
  target: under 10 minutes.
- Integration tests must not interfere with any real construct daemon or
  sessions that may be running on the same machine.

### Layer 3 — CLI end-to-end tests

These tests compile the `construct` binary, run it as a subprocess, and verify
its output and exit codes. They exercise the full path from CLI argument
parsing through daemon communication to Docker operations.

**Gate:** Same as integration tests (`CONSTRUCT_TEST_DOCKER=1`).

**What they cover:**

| Test | What it verifies |
|---|---|
| `TestCLI_Version` | `construct --version` prints version string and exits 0. |
| `TestCLI_RunAndAttach` | `construct run --folder <tmpdir> --stack base` starts a session. `construct ls` shows it. `construct attach <tmpdir>` connects. `construct stop` stops it. `construct destroy --force` removes it. |
| `TestCLI_Quickstart` | Run a session with specific flags. Destroy it. Run `construct qs` for the same folder. Verify it uses the saved flags. |
| `TestCLI_CredentialSetListUnset` | `construct config cred set KEY` (piped value). `construct config cred list` shows masked value. `construct config cred unset KEY`. `construct config cred list` shows nothing. |
| `TestCLI_DebugMode` | `construct run --debug --folder <tmpdir>` with a pseudo-TTY. Verify interactive shell starts (or verify error when not a TTY). |
| `TestCLI_FlagConflictWarning` | Start a session with `--stack base`. Run again with `--stack node`. Verify warning is printed and stack is unchanged. |
| `TestCLI_PlatformCheck` | (Only if running on a system that doesn't meet requirements) Verify the CLI prints the version requirement error and exits 1. |

**Rules:**

- Compile the binary once in `TestMain`, reuse across tests.
- Each test uses an isolated `HOME` / `XDG_CONFIG_HOME` (temp dir) and a
  unique `--daemon-socket` path so tests don't conflict with each other or
  with a real construct installation.
- Use a 60-second timeout per subprocess invocation.
- Capture stdout and stderr for assertion. Use `t.Logf` to dump them on failure.

---

## Docker interface for testability

The `daemon/docker` package wraps all Docker SDK calls behind an interface:

```go
type DockerClient interface {
    ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkConfig *network.NetworkingConfig, platform *specs.Platform, name string) (container.CreateResponse, error)
    ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
    ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
    ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
    ContainerInspect(ctx context.Context, containerID string) (types.ContainerJSON, error)
    ContainerExecCreate(ctx context.Context, container string, config container.ExecOptions) (types.IDResponse, error)
    ContainerExecStart(ctx context.Context, execID string, config container.ExecStartOptions) error
    ContainerExecAttach(ctx context.Context, execID string, config container.ExecAttachOptions) (types.HijackedResponse, error)
    ContainerExecInspect(ctx context.Context, execID string) (container.ExecInspect, error)
    ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error)
    ContainerLogs(ctx context.Context, container string, options container.LogsOptions) (io.ReadCloser, error)
    ImageBuild(ctx context.Context, buildContext io.Reader, options types.ImageBuildOptions) (types.ImageBuildResponse, error)
    ImageInspectWithRaw(ctx context.Context, imageID string) (types.ImageInspect, []byte, error)
    NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error)
    NetworkRemove(ctx context.Context, networkID string) error
    NetworkList(ctx context.Context, options network.ListOptions) ([]network.Summary, error)
    VolumeCreate(ctx context.Context, options volume.CreateOptions) (volume.Volume, error)
    VolumeRemove(ctx context.Context, volumeID string, force bool) error
    ServerVersion(ctx context.Context) (types.Version, error)
    Ping(ctx context.Context) (types.Ping, error)
    Close() error
}
```

The real implementation wraps `*client.Client` from the Docker SDK. Tests use
a fake that records calls and returns pre-configured responses. This fake
lives in an `internal/daemon/docker/dockertest` package (or as unexported
types in `_test.go` files).

---

## Test naming conventions

- Unit test functions: `TestFunctionName_Scenario` (e.g. `TestSlug_DeepPath`,
  `TestLogBuffer_OverflowEviction`).
- Integration test functions: `TestIntegration_Feature` (e.g.
  `TestIntegration_UIDMapping`).
- CLI end-to-end test functions: `TestCLI_Command_Scenario` (e.g.
  `TestCLI_Run_NewSession`).

---

## Running tests

```bash
# Unit tests only (fast, no Docker required)
go test ./...

# All tests including Docker integration
CONSTRUCT_TEST_DOCKER=1 go test ./... -timeout 15m

# Specific package
go test ./internal/daemon/registry/

# Verbose with race detector
go test -v -race ./...
```

---

## Test fixtures

Test fixtures (sample config files, credential files, quickstart records) are
stored as Go constants or literals in `_test.go` files, not as separate fixture
files. This keeps tests self-contained and avoids a `testdata/` directory tree
that is hard to maintain.

Exception: if a fixture is large (e.g. a complete Dockerfile for an
integration test), it may be placed in `testdata/` within the relevant package.

---

## Coverage expectations

- Unit test coverage target: 80%+ line coverage for logic-heavy packages
  (`slug`, `auth`, `registry`, `logbuffer`, `session` state machine, `config`
  template, `client` protocol).
- Integration tests are not measured by coverage — they verify correctness of
  Docker interactions that cannot be unit tested.
- No coverage enforcement in CI initially, but the target guides what to test.

---

## CI considerations

The CI pipeline should run two stages:

1. **Fast stage** (every push): `go test ./...` — unit tests only, no Docker.
   Should complete in under 30 seconds.
2. **Full stage** (merge to main, or manually triggered):
   `CONSTRUCT_TEST_DOCKER=1 go test ./... -timeout 15m` — includes Docker
   integration tests. Requires a CI runner with Docker available.

Both stages run with `-race` to catch data races.

---

## What NOT to test

- Docker engine internals (e.g. whether `docker create` actually creates a
  container — Docker's own test suite covers that).
- opencode's behaviour inside the container (we test that the container is
  set up correctly; what the agent does is opencode's responsibility).
- Exact Dockerfile build output (we test that the embedded Dockerfiles are
  present and that `docker build` succeeds; we don't parse build logs).
- Network egress behaviour (explicitly out of scope per requirements).
