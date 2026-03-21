# construct — Requirements

This document captures the requirements behind construct. It is the input to the rewrite.
The old specs (docs/spec/) are the implementation of these requirements and are being discarded.
Requirements survive rewrites; specs do not.

---

## Core purpose

Run an AI coding agent in yolo/auto-approve mode against a local folder without giving
it access to the rest of the host machine. Not a hardened sandbox — a meaningful step up
from running the agent directly on the host.

---

## Session requirements

### R-SES-1 — A session is the fundamental unit
A session is one agent process running against one folder with one tool and one
stack. It has a lifecycle: created, running, stopped, destroyed. Everything in
construct is scoped to a session. The folder does not need to be a git root — it
can be any directory (e.g. a parent folder containing multiple projects).

### R-SES-2 — Start a session
Running construct against a folder creates a session if one does not exist for that
folder, or attaches to the existing session if one is already running. Starting a
session selects a tool and stack; these are fixed for the lifetime of the session.

### R-SES-3 — One session per folder
Each folder has at most one active session at a time. Running construct against
a folder that already has a running session attaches to it rather than starting a
second one.

### R-SES-3a — Prompt when re-running a folder with different settings
When `construct run` is called against a folder that already has a running session
but the supplied flags (tool, stack, docker mode, ports) differ from what the session
was started with, the CLI must prompt the user to choose one of:

1. **Restart** — stop the existing session and start a new one with the new settings.
2. **Attach** — ignore the new settings and attach to the existing session as-is.
3. **Cancel** — do nothing and exit.

If the flags are identical to the existing session, attach silently (existing R-SES-2/3
behaviour). Opening a second concurrent session against the same folder is not
supported and must not be offered as an option.

### R-SES-4 — List sessions
I can see all currently running and stopped sessions at a glance — which folder each
belongs to, which tool and stack it uses, whether it is running or stopped, and how
long it has been active.

### R-SES-5 — Attach to a running session
I can connect to a session that is already running, including one I started earlier
or from a different terminal. The session continues uninterrupted; I am just
connecting a client to it.

### R-SES-6 — Stop a session
I can stop a running session gracefully. The agent process is shut down, the
container is stopped, but all state (installed tools, caches, history) is preserved.
I can restart it later and continue where I left off.

### R-SES-7 — Destroy a session
I can permanently destroy a session and all its state, returning to a clean stack
image. This is explicit and confirmed — not something that happens by accident.

### R-SES-8 — Sessions survive construct CLI restarts
A session is owned by the daemon, not the CLI process that started it. Closing the
terminal, restarting the CLI, or detaching from the client does not stop the session.
The agent keeps running until explicitly stopped.

---



### R-ISO-1 — Folder-only filesystem access
The agent may only access the folder it is invoked against. No other host paths
are visible inside the container.

### R-ISO-2 — Same absolute path inside and outside
The folder must be mounted at the exact same absolute path inside the container
as it has on the host. If the folder lives at `/home/claude/src/myapp` on the host,
it is available at `/home/claude/src/myapp` inside the container — not `/workspace`
or any other normalised path. This is non-negotiable: compiler output, debugger
configs, opencode session state, and tool caches all embed absolute paths.

### R-ISO-3 — No host Docker daemon exposure by default
The agent must not have access to the host Docker daemon unless explicitly opted in.
Default mode is no Docker access at all.

### R-ISO-4 — Optional Docker-in-Docker
When the agent needs Docker (e.g. for Testcontainers, building images), it gets its
own private Docker daemon via a DinD sidecar. This daemon is isolated from the host
daemon and from other sessions. The agent cannot see host containers, images, or volumes.

### R-ISO-5 — Optional Docker-outside-of-Docker
For users who explicitly need the agent to interact with the host Docker daemon, a
dood mode mounts the host socket. This is an explicit opt-in; the user accepts the
risk.

---

## Container lifecycle requirements

### R-LIFE-1 — Persistent containers, not ephemeral ones
Each folder+tool+stack combination gets a named container that is stopped and restarted
across sessions, not recreated. State (installed tools, build caches, shell history)
survives between sessions automatically.

### R-LIFE-2 — Agent can install tools without sudo
The agent must be able to install language runtimes, package manager tools, and
arbitrary binaries during a session without needing root or sudo. Installed tools
must persist to the next session (see R-LIFE-1).

### R-LIFE-3 — No host file ownership pollution
Files the agent creates or modifies in the mounted folder must be owned by the
invoking host user, not by root. The Jenkins problem (root-owned files you cannot
delete without sudo) is not acceptable.

### R-LIFE-4 — Agent-installed tools survive image rebuilds
If the agent installs a tool or runtime during a session, that installation must
survive even if the stack image is updated or rebuilt. I should not lose what the
agent set up just because I pulled a new version of the stack.

### R-LIFE-5 — Explicit full purge
I can purge all construct state in one command when I want a completely clean
slate — useful before upgrades, after breakage, or when done with construct on a
machine. Purge stops and removes all session containers and their agent layer
volumes, stops and removes the daemon container, and removes all construct Docker
images. Auth credentials (host cred files) are preserved by default so the user
does not have to re-authenticate after a purge. This is a deliberate action with
a confirmation step — not something that happens automatically.

---

## Auth and credentials requirements

### R-AUTH-1 — Credentials not visible in docker inspect
Credentials must not be passed as docker run -e flags. They are injected via
bind-mounted files so they do not appear in docker inspect output.

### R-AUTH-2 — Global auth persists across folders
OAuth tokens and interactive auth state (e.g. from opencode /connect) persist
globally across all folders and survive purge. A separate mechanism from the
per-folder container layer.

### R-AUTH-3 — Per-folder credential override
A credential set for a specific folder overrides the global default for that session.
Global credentials are the fallback.

### R-AUTH-4 — Credentials managed via CLI, not by hand
A config subcommand lets the user set, unset, and list credentials without editing
files manually. Values are masked in list output.

---

## Tool support requirements

### R-TOOL-1 — opencode is the primary tool
opencode is the tool construct is built around and optimised for.

(R-TOOL-2 was removed — it was a placeholder for multi-tool support which is not planned.)

### R-TOOL-3 — Yolo mode is always on
construct exists to run agents in auto-approve mode. Yolo is not optional.

---

## Stack requirements

### R-STACK-1 — Language stacks are pre-built images
Stacks are Docker images that pre-install a language runtime on top of a common base.
The agent container is derived from the stack image.

### R-STACK-2 — Base includes the essentials
The base stack includes Node (for tool installation), git, Docker CLI, and Python.
All stacks inherit from base.

### R-STACK-3 — Supported stacks
At minimum: base, node, dotnet, dotnet-big (multiple SDK versions), go, python, ruby.
UI variants (with Playwright/MCP) are additive on top of any stack.

---

## Home config requirements

### R-HOME-1 — Same global opencode experience inside and outside
The agent running inside construct must have access to the same global opencode
configuration as if it were running directly on the host. This covers skills,
slash commands, AGENTS.md, and tool config (model selection, theme, keybindings).
The host is always the source of truth — the agent can read but not modify global
config.

### R-HOME-2 — Per-folder agent instructions travel automatically
Any agent instruction files in the root of the mounted folder (e.g. `AGENTS.md`)
are available to the agent without any special configuration. They are inside the
folder which is already mounted. For sessions invoked against a parent folder
containing multiple projects, each project's own instruction files are also
accessible because the entire folder tree is mounted.

### R-HOME-3 — construct augments global agent instructions
construct injects additional context into the agent's global instructions — telling
it that it is running inside a construct container, what mode it is in, which ports
are published, etc. The user's own global instructions are preserved and combined
with construct's additions.



---

## Network requirements

### R-NET-1 — Outbound internet access by default
The agent needs outbound internet access to call LLM APIs and install packages.
No egress filtering by default.

### R-NET-2 — Session isolation in dind mode
In dind mode, the agent and dind sidecar share an isolated bridge network. No other
containers join it. Both are removed at session end.

---

## UX requirements

### R-UX-1 — Quickstart replays last invocation
After running construct at least once against a folder, `construct qs` replays the last
tool, stack, docker mode, ports, and client settings for that folder.

### R-UX-2 — Port forwarding
The user can publish container ports to the host for dev servers. The agent is
told which ports are published so it binds correctly.

### R-UX-3 — Web client is the primary experience, TUI is supported
The primary way to interact with a construct session is via the browser. The TUI
(opencode attach) is supported for users who prefer it but is not the focus.

### R-UX-7 — construct run returns the shell after the agent is ready
`construct run` and `construct attach` must return the shell prompt as soon as
the session is ready — they must not block streaming logs. After printing the web
URL and TUI hint, the CLI waits until the agent's web server is reachable on the
host port (readiness probe), then exits cleanly. Log streaming is a separate
explicit command (`construct logs -f`).

### R-UX-4 — Debug mode
A --debug flag drops into an interactive shell in the container instead of starting
the agent. For troubleshooting image and container state.

### R-UX-5 — Linux only
See R-PLAT-1.

### R-UX-6 — Help on bare invocation
Running `construct` with no arguments and no flags prints a help summary listing
all available commands with a one-line description of each, then exits with code 0.
The user should never be left wondering what commands exist.

---

## Daemon requirements

### R-OBS-1 — construct runs a daemon that manages all sessions
construct operates as a daemon — a lightweight container started automatically on
the first `construct` invocation and left running in the background. The CLI is a
thin client that talks to the daemon. Users never manage the daemon manually.

### R-OBS-2 — Daemon owns all session containers
The daemon is responsible for starting, stopping, and tracking all session
containers. It knows which sessions are running, which folders they belong to, and
their current state. The CLI queries the daemon rather than talking to Docker
directly.

### R-OBS-3 — Multiple simultaneous sessions are first-class
I can run construct against multiple folders at the same time. Each gets its own
session container managed by the daemon. Sessions are independent and do not
interfere with each other.

### R-OBS-4 — Session output is accessible at any time
I can check what a running session is doing — its log output, current state, and
any ports it has published — without having been attached since the start. The
daemon buffers session output so I can catch up on what happened while I was away.

### R-OBS-5 — Daemon starts automatically, stays out of the way
The daemon starts on first use and requires no manual setup, no systemd unit, and
no init configuration. It is just another container. If it is not running when the
CLI is invoked, the CLI starts it.

---

## Security posture

### R-SEC-1 — No --privileged on the agent container
The agent container never runs with --privileged. Only the dind sidecar (when used)
requires --privileged.

### R-SEC-2 — Root inside the container is acceptable
The agent runs as root inside the container. The container boundary provides the
isolation, not the Unix user. This simplifies tool installation (R-LIFE-2) and
avoids permission headaches.

### R-SEC-3 — File ownership on host is the invoking user (not root)
Despite R-SEC-2, files written to the bind-mounted repo must land with the host
user's UID/GID. The container achieves this by mapping UIDs appropriately at the
mount level, not by running the agent as a non-root user.

### R-SEC-4 — Not a hardened sandbox
construct is not a CVE-proof container escape preventer. It is a meaningful step up
from running the agent directly on the host. The threat model is documented, not
hidden.

---

## Platform requirements

### R-PLAT-1 — Linux only
construct targets Linux Docker hosts exclusively. No Windows, no macOS. Opinionated
by design.

### R-PLAT-2 — Minimum kernel and Docker versions
construct requires Linux kernel 5.12 or later and Docker Engine 25.0 or later.
These are needed for idmap mount support (UID mapping on bind mounts). Both
versions are over 2 years old (kernel 5.12: April 2021, Docker 25.0: January
2024). No fallback is provided for older versions; construct exits with an error
if the requirements are not met.

### R-PLAT-3 — Implementation language
construct is implemented in Go. The CLI and daemon are both Go binaries.

### R-PLAT-4 — Respect XDG conventions
construct respects `$XDG_CONFIG_HOME` when resolving the host opencode configuration
directory. If `$XDG_CONFIG_HOME` is set, the opencode config path is
`$XDG_CONFIG_HOME/opencode/`; otherwise it defaults to `~/.config/opencode/`.

---

## Out of scope

- Egress network filtering (nice to have, not a requirement)
- Multi-user or multi-tenant usage
- Compliance boundaries (SOC 2, ISO 27001, etc.)
- macOS Docker Desktop support (blocked upstream by microVM requirements)