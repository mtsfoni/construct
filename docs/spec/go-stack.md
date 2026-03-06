# Spec: Go stack

## Problem

Projects written in Go need a stack image that has the Go toolchain pre-installed so the agent can build, test, and run Go code (including frameworks such as Testcontainers).

## Solution

Add a `go` stack built on top of `construct-base` that installs the Go toolchain.

## Behaviour

```
construct --stack go [path]
```

Produces a `construct-go` Docker image that extends `construct-base` with Go installed at `/usr/local/go`. `go`, `gofmt`, and all standard toolchain binaries are available on `PATH`.

Because every stack inherits the Docker-in-Docker daemon from `construct-base`, frameworks like [Testcontainers](https://testcontainers.com/) work without any extra configuration — containers started during tests run inside the agent's isolated daemon and never touch the host.

## Implementation

| File | Change |
|------|--------|
| `internal/stacks/dockerfiles/go/Dockerfile` | New — installs Go 1.24 via official tarball |
| `internal/stacks/stacks.go` | Added `"go"` to `validStacks`; added `All()` function |
| `README.md` | `go` row in Supported stacks table; Testcontainers callout |

## Go version

Go 1.24 installed from `https://go.dev/dl/go1.24.0.linux-amd64.tar.gz` into `/usr/local/go`. Update the URL in the Dockerfile when upgrading.

## Non-goals

- No automatic Go version management (e.g. `asdf`, `goenv`). Pin a single version per image.
