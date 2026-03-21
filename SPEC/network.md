# construct — Network Spec

Covers R-NET-1, R-NET-2, R-UX-2, R-ISO-4.

---

## Network modes by docker mode

The network configuration of a session depends on the docker mode.

### `none` mode (default)

The agent container uses Docker's default bridge network. This provides:
- Full outbound internet access for LLM API calls and package installation (R-NET-1).
- Port publishing to the host (user-specified and auto-published web UI ports).
- No special session isolation network is created; Docker's default bridge is fine
  since there is no dind sidecar to isolate.

No `--network` flag is passed explicitly; Docker's `bridge` default is used.

### `dind` mode

A private bridge network is created for the session to isolate the agent and the
dind sidecar from each other and from other containers (R-NET-2).

**Network name:** `construct-net-<short-id>`

**Members:**
- Agent container (`construct-<short-id>`) — connected to both this network and
  the default bridge (for outbound internet access).
- DinD sidecar (`construct-dind-<short-id>`) — connected to this network only.
  It does not need direct internet access; the agent container proxies Docker
  client commands to it.

**DNS:** Docker's embedded DNS resolves `construct-dind-<short-id>` to the sidecar's
IP address within the session network. The agent container sets
`DOCKER_HOST=tcp://construct-dind-<short-id>:2375` to point at this address.

**Lifecycle:** The network is created before the containers are started and is
removed as part of `session.destroy`. It is not removed on `session.stop`; the
containers remain attached and the network persists until destroy.

### `dood` mode

No special session network. The agent container uses the default bridge network
and has the host Docker socket mounted. Network setup is identical to `none` mode.

---

## Port forwarding (R-UX-2)

### User-specified ports

The user specifies ports via the `--port` flag:

```
--port 3000              # Map container:3000 to a random host port
--port 3000:3000         # Map container:3000 to host:3000
--port 8080:9000         # Map container:9000 to host:8080
```

These are passed to Docker as `--publish` flags at container creation. Port
bindings are fixed at container creation time.

Port specs are stored in the session record and replayed by quickstart.

### Auto-published web UI port

For tools that expose a web UI (opencode), the daemon automatically publishes the
tool's known web port if the user did not explicitly publish it. The daemon
assigns a host port starting at 4096; if port 4096 is already in use (by another
session or another process), it increments by 1 until a free port is found.

The port free-check mechanism is described in `SPEC/containers.md`. The daemon
attempts a TCP listen on `0.0.0.0:<port>` (possible because the daemon container
runs with `--network host`), closes the listener if successful, and uses that
port.

After the container starts, the assigned host port is stored in the session
record.

### Agent awareness of ports (R-HOME-3)

The `CONSTRUCT_PORTS` environment variable in the container is set to
comma-separated `<host_port>:<container_port>` pairs (e.g.
`3000:3000,4096:4096`). The injected `construct-agents.md` instructs the
agent to:

- Bind dev servers to `0.0.0.0` (not `127.0.0.1`) so they are accessible via
  the published port.
- Use the correct container port numbers listed in `CONSTRUCT_PORTS`.

This allows the agent to automatically configure dev servers to be accessible
from the host browser.

---

## Outbound internet access (R-NET-1)

No egress filtering is applied. The agent container has full outbound internet
access by default. Egress filtering is explicitly out of scope (see REQS Out of
scope section).

---

## Port visibility in `construct ls`

The `construct ls` output includes PORTS and URL columns for each session. The
columns use standard Docker `host:container` format for ports.

Example row from `construct ls`:

```
ID          REPO                  TOOL       STACK  DOCKER  STATUS   PORTS              URL                       AGE
a1b2c3d4    /home/alice/src/app   opencode   node   none    running  3000:3000          http://localhost:4096      2h 14m
                                                                     4096:4096
```

The PORTS column shows all published port mappings. Multiple mappings are shown
on separate lines (aligned under the column). The URL column shows the web UI
URL if the tool provides one and the session is running. Both columns are empty
for stopped sessions with no ports.

The full column set is defined in `SPEC/cli.md`.

The web URL is also printed when attaching (`construct run`, `construct attach`).

---

## Network resource cleanup

| Event | Network action |
|---|---|
| `session.stop` | No network changes; network and containers remain |
| `session.destroy` (none/dood mode) | No session network to remove |
| `session.destroy` (dind mode) | Remove `construct-net-<short-id>` |
| Daemon startup (reconcile) | Remove orphaned `construct-net-*` networks with no associated containers |
