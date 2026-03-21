# construct — Tools Spec

Covers R-TOOL-1, R-TOOL-3.

---

## opencode

opencode is the only supported tool (R-TOOL-1).

| Property | Value |
|---|---|
| npm package | `opencode-ai` |
| Install command | `npm install -g opencode-ai` |
| Install path | `/agent/bin/opencode` (symlinked by npm from `/agent/lib/node_modules/opencode-ai/bin/opencode`) |
| Invoke command | `opencode serve --hostname 0.0.0.0 --port 4096` |
| Subcommand | `serve` — starts a headless opencode server (no TUI, just the web API) |
| Hostname flag | `--hostname 0.0.0.0` — required so the server binds to all interfaces, not just loopback |
| Port flag | `--port <port>` (tells opencode which port to bind its web server to) |
| Web UI | Yes — opencode exposes a local web server |
| Web UI port | `4096` (default). If port 4096 is in use, increment by 1 until a free port is found. |
| Web URL | `http://localhost:<assigned-port>` (auto-published if not user-specified) |
| TUI attach | `opencode` (attaches to running session via IPC) |

### Web UI behaviour

opencode's built-in web server is the primary client interface (R-UX-3). The
daemon starts opencode with port 4096. If port 4096 is already in use (by
another session or another process), the daemon increments the port number by 1
until a free port is found.

The daemon returns `web_url` **optimistically** — it includes the URL in the
session response immediately after starting the agent process, without waiting
for the web server to become ready. If the agent takes a moment to start, the
URL may not be reachable for a brief period. This is acceptable; the CLI prints
the URL and the user can refresh.

The CLI prints this URL and optionally opens it in the default browser.

---

## Yolo mode (R-TOOL-3)

Yolo (auto-approve) mode is always enabled. The yolo flag is added to the invoke
command unconditionally; there is no way to start a session without it.

---

## Installation flow

At session creation (or after reset), the daemon checks whether the opencode binary
is present in the agent layer:

```
/agent/lib/node_modules/.bin/opencode
```

If not present, the daemon runs the install command inside the container via
`docker exec`. This is a blocking operation with progress streamed to the client.

Installation only runs once per agent layer (once per session, or once after reset).
Subsequent `stop`/`start` cycles skip it.

---

## Web UI port management

The daemon needs to know which host port maps to opencode's web port. Two cases:

1. **User specified `--port`:** The daemon matches the container port against
   opencode's known web port (4096). If a match is found, that host port is
   used in `web_url`.

2. **Auto-publish (default):** The daemon automatically publishes opencode's web
   port with a host port starting at 4096. If 4096 is occupied (by another
   session or process), the daemon increments by 1 until a free port is found.
   The assigned mapping is stored in the session record and shown in
   `construct ls` output.

In both cases, the host port is stored in the session record and retrievable at
any time via `construct ls` or `construct attach`.

The `web_url` is returned optimistically at session start without waiting for
the web server to become ready. The URL may not be immediately reachable if the
agent process takes time to initialise. This is a known trade-off for faster
CLI response times.
