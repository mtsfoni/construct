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
construct --tool opencode --stack ui --port 3000 .
construct --tool opencode --stack ui --port 3000 --port 8080 .
construct --tool opencode --stack ui --port 9000:3000 .
construct --tool opencode --stack ui --port 127.0.0.1:3000:3000 .
```

The flag is repeatable: each `--port` adds one `-p` to the underlying
`docker run` call. Formats follow standard Docker port mapping syntax:

| `--port` value | Docker flag | CONSTRUCT_PORTS |
|---|---|---|
| `3000` | `-p 3000` | `3000` |
| `3000:3000` | `-p 3000:3000` | `3000` |
| `9000:3000` | `-p 9000:3000` | `3000` |
| `127.0.0.1:3000:3000` | `-p 127.0.0.1:3000:3000` | `3000` |

`CONSTRUCT_PORTS` always carries the **container-side** port (the last
colon-delimited segment), which is what the agent's server must bind to.

## Environment variables injected

When `--port` is used, two env vars are added to the container:

| Variable | Value | Purpose |
|---|---|---|
| `CONSTRUCT` | `1` | Signals to the agent it is running inside construct |
| `CONSTRUCT_PORTS` | comma-separated container-side ports | Tells the agent which port(s) to listen on |

Neither variable is injected when `--port` is not used, so existing sessions
are unaffected.

### Why only when ports are set

`CONSTRUCT=1` could be always-injected, but tying it to `--port` keeps the
signal meaningful: the agent only needs to change its behaviour (bind address,
port selection) when the user has explicitly said "I want to reach this app".
An unconditional `CONSTRUCT=1` with no port information would add noise without
actionable information.

## Agent awareness

For an agent to make use of these env vars it needs context. The recommended
approach (not yet implemented) is to add a system-prompt note via opencode's
`instructions` config field or a seeded `AGENTS.md` in the home volume:

```
If CONSTRUCT=1 is set, you are running inside a construct container.
Bind any servers to 0.0.0.0 (not 127.0.0.1).
Use the port numbers in CONSTRUCT_PORTS.
Print a clear message when the server is ready so the user knows to open their browser.
```

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
| `cmd/construct/port_test.go` | New — integration tests: usage mentions `--port`; missing `--tool` still errors; multiple `--port` values parse without error |
