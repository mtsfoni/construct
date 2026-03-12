# Serve mode

## Problem

The current architecture runs the opencode TUI inside the container via `docker run -it ... opencode`. This couples the UI experience to the container environment — the TUI is rendered inside a Docker pseudo-terminal, which produces subtle rendering issues and prevents users from using their normal local opencode setup (themes, keybinds, commands, etc.).

## Solution

Replace the TUI-in-container approach with `opencode serve` running headlessly inside the container, and connect a local client to it.

**Before:**
```
construct run → docker run -it ... opencode
               (TUI runs inside container)
```

**After:**
```
construct run → docker run -d ... opencode serve --hostname 0.0.0.0 --port 4096
              → opencode attach http://localhost:4096    (local TUI, fallback: browser)
               (server runs inside container, UI runs locally)
```

The sandbox remains what it always was — isolated filesystem, isolated Docker daemon, stack dev environment — but the agent now runs headlessly via the opencode HTTP server. The user interacts through their own local opencode client.

## Behaviour

### Interactive mode (`construct run [path]`)

1. Build stack + tool images (unchanged).
2. Start the container **detached** (`docker run -d`) running:
   ```
   opencode serve --hostname 0.0.0.0 --port <serve-port>
   ```
3. Poll `GET http://localhost:<serve-port>/global/health` until `{"healthy":true}` is returned, or until a 15-second timeout (error printed and container stopped on timeout).
4. Connect a local client according to the `--client` flag (see below).
5. When the local client process exits (user quits TUI or closes browser), stop and remove the container (same cleanup as today).
6. SIGINT/SIGTERM: same interrupt handling as before — print "interrupted — cleaning up…", stop container, exit 1.

### Headless mode (`construct run [path] -- "message"`)

When `ExtraArgs` (passthrough args after `--`) are non-empty, the run is treated as headless:

1. Steps 1–3 identical to interactive mode.
2. Run locally (blocks, streams to stdout/stderr):
   ```
   opencode run --attach http://localhost:<serve-port> <extra-args...>
   ```
3. When `opencode run` exits, stop and remove the container.

`--client web` is incompatible with headless mode (passthrough args require `opencode`). Passing both is a fatal error.

### Debug mode (`construct run --debug`)

`--debug` keeps the old interactive shell behaviour:
```
docker run -it ... /bin/bash
```
The container is still run interactively with a TTY. This mode is unchanged.

## New flag: `--serve-port`

| Flag | Default | Description |
|------|---------|-------------|
| `--serve-port` | `4096` | Port for the opencode HTTP server. Published `host:container` as `<serve-port>:<serve-port>`. |

The `--port` flag is unchanged — it continues to publish application ports (e.g. `--port 3000 --port 8080`).

`--serve-port` is saved to `~/.construct/last-used.json` and replayed by `construct qs`.

## Auto-port fallback (no `--serve-port` given)

When `--serve-port` is **not** specified and the default port (4096) is already
bound on the host, `construct` automatically picks the next free higher port and
prints a yellow diagnostic to stderr:

```
construct: port 4096 is already in use; using port 4097 instead
```

The fallback port is chosen by probing `127.0.0.1:<port>` sequentially from
4096 upward until a free one is found. If `--serve-port` **is** specified
explicitly, no fallback occurs — the user-supplied port is used as-is.

## Local client selection (`--client`)

The `--client` flag controls how the host connects to the opencode server once it is ready:

| Value | Behaviour |
|---|---|
| `""` (default) | Auto-detect: run `opencode attach <url>` if `opencode` is on `$PATH`; otherwise open the URL in the system default browser. |
| `"tui"` | Always run `opencode attach <url>`. Error if `opencode` is not on `$PATH`. |
| `"web"` | Always open the URL in the system default browser. Incompatible with passthrough args (headless mode). |

`--client` is saved to `~/.construct/last-used.json` and replayed by `construct qs`. An absent/empty value means auto-detect, so old entries continue to behave correctly.

## Persistence

`last-used.json` gains a `"serve_port"` key:

```json
{
  "stack": "go",
  "docker": "dind",
  "mcp": false,
  "ports": ["3000"],
  "serve_port": 4096,
  "client": "web"
}
```

Legacy entries without `"serve_port"` default to `4096`. Legacy entries without `"client"` default to auto-detect.

## AGENTS.md update

The networking section of the auto-generated `~/.config/opencode/AGENTS.md` gains a note about the opencode server port:

```
## opencode server

This opencode instance is running in server mode on port <serve-port>.
The host connects to it via http://localhost:<serve-port>.
```

## Security

The opencode server is bound to `0.0.0.0` inside the container so the host can reach it via the published port. This is acceptable because:

- The port is published only on the host's loopback interface (`127.0.0.1:<serve-port>:<serve-port>`) by default, not exposed to the network.
- No `OPENCODE_SERVER_PASSWORD` is set by default; password support can be added as a future enhancement via a `--serve-password` flag.

## Files changed

| File | Change |
|---|---|
| `docs/spec/serve-mode.md` | This spec |
| `internal/config/lastused.go` | Add `ServePort int` and `Client string` to `LastUsed` |
| `internal/runner/runner.go` | `Config.ServePort`; `Config.Client`; detached container start; `waitForServer`, `runLocalAttach(url, client)`, `runLocalHeadless` helpers; debug mode unchanged; `isPortFree`/`findFreePort` for auto-port fallback |
| `cmd/construct/main.go` | `--serve-port` and `--client` flags, pass to `runner.Config`, save to last-used |
| `internal/runner/runner_test.go` | Tests for `buildServeArgs`, health-poll timeout behaviour, `runLocalAttach` client modes, `isPortFree`/`findFreePort` |
| `CHANGELOG.md` | Entry under `[Unreleased]` |
