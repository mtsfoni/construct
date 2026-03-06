# Spec: `--port` flag

## Problem

Agents running inside construct containers can start dev servers, but there was
no way to reach them from the host. Without port publishing the "vibe coding"
loop — start an agent, let it build and run the app, view it in a browser — was
impossible.

## Solution

Add a repeatable `--port` flag that publishes one or more container ports to the
host. Each `--port` value is passed directly to `docker run -p`, so all formats
docker supports are accepted. When any ports are published, two env vars are
also injected into the container so the agent knows it is in construct and which
ports to bind to.

## Usage

```
construct --stack ui --port 3000 .
construct --stack ui --port 3000 --port 8080 .
construct --stack ui --port 9000:3000 .
construct --stack ui --port 127.0.0.1:3000:3000 .
```

The flag is repeatable: each `--port` adds one `-p` to the underlying
`docker run` call. Formats follow standard Docker port mapping syntax:

| `--port` value | Docker flag | CONSTRUCT_PORTS |
|---|---|---|
| `3000` | `-p 3000:3000` | `3000` |
| `3000:3000` | `-p 3000:3000` | `3000` |
| `9000:3000` | `-p 9000:3000` | `3000` |
| `127.0.0.1:3000:3000` | `-p 127.0.0.1:3000:3000` | `3000` |

A bare port number (e.g. `3000`) is automatically expanded to `3000:3000` so
that the same port is used on both the host and the container, matching user
expectations.

## Environment variables injected

| Variable | Value | Purpose |
|---|---|---|
| `CONSTRUCT` | `1` | Always injected — signals to the agent it is running inside construct |
| `CONSTRUCT_PORTS` | comma-separated container-side ports | Only injected when `--port` is used; tells the agent which port(s) to listen on |

`CONSTRUCT=1` is always present regardless of whether `--port` is used.
`CONSTRUCT_PORTS` is only set when at least one `--port` was passed.

## Agent awareness

When `--port` is used, the entrypoint script injects a section into
`~/.config/opencode/AGENTS.md` that instructs the agent to bind dev servers to
`0.0.0.0` (not `127.0.0.1`) and to use the port numbers listed in
`$CONSTRUCT_PORTS`. This note is written automatically on every container start
by `generatedEntrypoint()` in `internal/runner/runner.go` — no user action is
required. See `docs/spec/construct-agents-md.md` for details.

## Network topology note

The agent container is on an isolated bridge network shared only with the dind
sidecar. Published ports go directly from the host to the agent container:

```
host:3000  →  agent container:3000  →  (server process)
```

If the agent spawns the server **inside dind** (via `docker run`), an extra
layer of port forwarding is required inside the container that construct does
not manage. For direct `npm run dev` / `go run` / etc. invocations, the above
is sufficient.

## Files changed

| File | Change |
|---|---|
| `cmd/construct/main.go` | Added `portFlag` type (repeatable `flag.Value`); added `--port` flag; passes `Ports` to `runner.Config`; updated usage/examples |
| `internal/runner/runner.go` | Added `Ports []string` to `Config`; emit `-p` flags and `CONSTRUCT`/`CONSTRUCT_PORTS` env vars in `buildRunArgs` |
| `internal/runner/runner_test.go` | Added 5 unit tests covering bare port, colon mapping, multiple ports, three-part mapping, and absent-when-empty |
| `cmd/construct/port_test.go` | New — integration tests: usage mentions `--port`; multiple `--port` values parse without error |
