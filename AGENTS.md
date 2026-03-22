# Construct — Agent Guidelines

This is a Go project. The CLI (`construct`) communicates with a daemon (`constructd`) over a Unix socket using newline-delimited JSON. Agents run inside Docker containers managed by the daemon.

## Build Commands

```bash
# Build the CLI binary
go build ./cmd/construct/

# Build the daemon binary
go build ./cmd/constructd/

# Build both with version stamp
go build -ldflags "-X github.com/construct-run/construct/internal/version.Version=<ver> -s -w" -o construct ./cmd/construct/
go build -ldflags "-X github.com/construct-run/construct/internal/version.Version=<ver> -s -w" -o constructd ./cmd/constructd/

# Build and install to ~/.local/bin (also stamps SourceDir so the daemon can
# recompile constructd from the correct source tree regardless of cwd):
bash install.sh
```

## Test Commands

```bash
# Run all unit tests
go test ./...

# Run a single package's tests
go test ./internal/daemon/session/

# Run a single test by name
go test ./internal/daemon/session/ -run TestSession_Start_CreatesNewSession -v

# Run Docker integration tests (requires Docker)
CONSTRUCT_TEST_DOCKER=1 go test ./internal/integration/ -v -timeout 10m

# Run a single integration test
CONSTRUCT_TEST_DOCKER=1 go test ./internal/integration/ -run TestIntegration_ContainerBasicSession -v -timeout 10m
```

The general pattern for running a single test: `go test <package_path> -run <TestFunctionName> -v`

Integration tests are gated by the `CONSTRUCT_TEST_DOCKER=1` environment variable. Unit tests do not require Docker and call `skipWithoutDocker(t)` to skip if unset.

## Format and Lint

```bash
# Format all code
go fmt ./...

# Vet all code
go vet ./...
```

Inline `//nolint:errcheck` and `//nolint:noctx` directives (golangci-lint style) are used where intentional. There is no `.golangci.yml` config file in the repo — use inline directives only where genuinely necessary.

## Project Structure

```
cmd/construct/          CLI entry point
cmd/constructd/         Daemon entry point
embedfs/                Embedded Docker build contexts (go:embed)
internal/
  auth/                 Credential storage in .env files
  bootstrap/            CLI-side daemon bootstrap logic
  cli/                  CLI command implementations
  client/               Unix socket client for daemon IPC
  config/               Config dir resolution + AGENTS.md generation
  daemon/
    docker/             Docker client interface + real implementation
    logbuffer/          Ring buffer for agent logs
    registry/           Session state persistence
    server/             Unix socket server (newline-delimited JSON)
    session/            Session lifecycle logic
  integration/          Docker integration tests
  network/              Port utilities
  platform/             Kernel/Docker version checks
  quickstart/           Last-used session params store
  slug/                 Path → slug conversion
  stacks/               Docker image names + build context extraction
  tools/                Agent tool constants (install/invoke/paths)
  version/              Version constant (set by ldflags)
SPEC/                   Specification markdown documents
REQS/                   Requirements documents
```

## Documentation flow: REQS → SPEC → impl

Changes follow a three-layer hierarchy:

1. **`REQS/REQUIREMENTS.md`** — the *why*. Durable, implementation-agnostic requirements
   (R-SES-1, R-LIFE-4, etc.). Update this first when adding or changing behaviour.
   Requirements survive rewrites; they do not describe how, only what and why.

2. **`SPEC/`** — the *how*. Markdown specs that translate requirements into concrete
   design decisions: data structures, wire formats, algorithms, Docker container
   configuration. Each spec references the requirements it implements. Update specs
   when the design changes, before or alongside the code.

3. **`internal/` (implementation)** — the code. Must stay consistent with the relevant
   spec. If code diverges from the spec, either the spec or the code is wrong — fix
   both together.

When making a non-trivial change:
- Add or update the relevant requirement in `REQS/REQUIREMENTS.md`
- Add or update the relevant spec in `SPEC/`
- Implement in code

## Code Style

### Imports

Use three import groups separated by blank lines: stdlib, then third-party, then internal:

```go
import (
    "context"
    "fmt"
    "os"

    "github.com/docker/docker/api/types"
    "github.com/google/uuid"

    "github.com/construct-run/construct/internal/auth"
    "github.com/construct-run/construct/internal/stacks"
)
```

Use import aliases only when needed to resolve ambiguity, keeping them short and descriptive:
```go
dockeriface "github.com/construct-run/construct/internal/daemon/docker"
netpkg      "github.com/construct-run/construct/internal/network"
```

### Naming Conventions

- **Exported:** PascalCase — `Manager`, `StartParams`, `NewManager`, `StatusRunning`
- **Unexported:** camelCase — `stateDir`, `logBuffers`, `newLogBuffer`
- **Receivers:** single letter or very short — `m *Manager`, `s *Server`, `r *Registry`
- **Constructors:** `New(...)` pattern — `New(socketPath string) *Server`
- **Go constants:** PascalCase — `StackBase`, `StatusRunning`, `DefaultSize`
- **Env vars:** ALL_CAPS — `CONSTRUCT_TEST_DOCKER`
- **Test helpers:** `newTest...` prefix — `newTestServer`, `newFakeDocker`

### Types

- Define interfaces close to where they are consumed, not in a separate `interfaces.go`
- Use typed string constants for status/enum-like fields: `type Status string` with `const StatusRunning Status = "running"`
- Use JSON struct tags on all serialized types: `json:"field_name,omitempty"`
- Use pointer receivers for all methods on non-trivial structs
- Always pass `context.Context` as the first parameter in any function doing I/O
- No generics — keep types simple and explicit

### Error Handling

- Wrap errors with context using `fmt.Errorf`: `fmt.Errorf("start session: %w", err)`
- Use early return on error — avoid deeply nested `if` blocks
- Ignore errors explicitly with `_ =` or `//nolint:errcheck`, never silently
- `main()` uses `log.Fatalf("...: %v", err)` for fatal startup errors
- CLI and server code uses `fmt.Fprintf(os.Stderr, "error: %v\n", err)` and returns exit code 1

```go
// Good
if err := os.MkdirAll(dir, 0o700); err != nil {
    return fmt.Errorf("create state dir: %w", err)
}

// Good — intentional ignore
_ = os.RemoveAll(sessionDir) // best-effort cleanup
```

### Package Documentation

Every package must have a `// Package foo ...` doc comment explaining its purpose:

```go
// Package session manages the lifecycle of agent sessions running inside
// Docker containers.
package session
```

### File Organization

Use `// --- section name ---` dividers to group related code within large files:

```go
// --- helpers ---

func (m *Manager) resolveStateDir() (string, error) { ... }

// --- session.start ---

func (m *Manager) Start(ctx context.Context, p StartParams) (*Session, error) { ... }
```

### Testing

- Use the standard library `testing` package only — no testify or other assertion libraries
- Use `t.Helper()` in helper functions, `t.Fatalf()` / `t.Errorf()` for assertions
- Use `t.TempDir()` for temporary directories (auto-cleaned)
- Use `t.Cleanup(func(){...})` for deferred cleanup
- Use `t.Run(name, func)` for table-driven subtests
- Define fakes/stubs at the top of `_test.go` files under a `// --- fakes ---` header
- Use external test packages (`package foo_test`) for black-box tests, internal (`package foo`) for white-box
- Integration tests must call `skipWithoutDocker(t)` as their first line

Table-driven test pattern:
```go
tests := []struct {
    name  string
    input string
    want  string
}{
    {"empty string", "", ""},
    {"basic case", "foo", "foo"},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        got := myFunc(tt.input)
        if got != tt.want {
            t.Errorf("myFunc(%q) = %q, want %q", tt.input, got, tt.want)
        }
    })
}
```

### General Go Conventions

- Prefer short, focused functions; extract helpers rather than nesting logic
- Do not use `init()` functions
- Prefer `os.MkdirAll` with `0o700` (octal literal) for directory creation
- Use `encoding/json` for all JSON serialization; prefer `omitempty` on optional fields
- Use `sync.Mutex` for state shared between goroutines; document which fields a mutex protects
- Embed file assets with `//go:embed` directives in the `embedfs` package, not scattered throughout the codebase
