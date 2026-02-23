# Changelog

## [Unreleased] — v0.3.0

### Added

- **`--port` flag** — publish container ports to the host. Repeatable; accepts any format `docker run -p` supports (`3000`, `9000:3000`, `127.0.0.1:3000:3000`). A bare port number is automatically expanded to `host:container`.
- **`--mcp` flag** — activate MCP servers at container startup. When passed, the entrypoint writes `~/.config/opencode/opencode.json` registering `@playwright/mcp`; without it the file is removed. Requires `--stack ui` or `--stack dotnet-ui` for full browser automation support.
- **`dotnet-ui` stack** — new `construct-dotnet-ui` image combining the .NET 10 SDK with `@playwright/mcp` and Chromium. Extends `construct-dotnet`; use with `--mcp` for Blazor/ASP.NET projects that need browser automation.
- **Automatic AGENTS.md injection** — the entrypoint always writes `~/.config/opencode/AGENTS.md` so opencode knows it is running inside a construct container. When `--port` is used the file also contains server binding rules (bind to `0.0.0.0`, use the ports listed in `$CONSTRUCT_PORTS`), preventing the common mistake of agents starting dev servers on `127.0.0.1` which is unreachable from the host.
- **Networking context in AGENTS.md** — the injected file now always explains that Docker runs on a separate sidecar host (`dind`), not `localhost`. Containers started via Docker are reachable at `dind:<port>`, not `127.0.0.1`. Also clarifies that the user can access ports on the agent machine directly but not ports inside Docker containers, so agents should run services on the machine itself.
- **`OPENCODE_EXPERIMENTAL_DISABLE_COPY_ON_SELECT=true`** — injected into the opencode container to prevent unwanted clipboard interference from terminal text selection.
- **`CONSTRUCT=1` env var** — always injected into the agent container so tools can detect they are running inside construct.
- **`CONSTRUCT_PORTS` env var** — injected when `--port` is used; contains the comma-separated list of container-side port numbers.
- **`qs` now replays `--mcp` and `--port`** — the quickstart command restores the full previous invocation, not just `--tool` and `--stack`. `~/.construct/last-used.json` now stores `mcp` and `ports` alongside `tool` and `stack`.

### Changed

- **Stack consolidation** — `node` and `python` stacks are removed. Python 3, pip, and venv are now included in the `base` image alongside Node.js 20. The `ui` stack now extends `base` directly. Any invocation using `--stack node` or `--stack python` should switch to `--stack base` (or a more specific stack).
- **Default stack changed from `node` to `base`** — reflects the consolidation above.
- **MCP activation decoupled from stack** — `@playwright/mcp` is installed in the `ui` and `dotnet-ui` stack images at build time but is only activated at runtime when `--mcp` is passed. Previously the MCP config was seeded unconditionally into the home volume.

---

## [v0.2.0]

### Added

- **`qs` subcommand** — replays the last `--tool` and `--stack` used for a given repo. Settings are stored atomically in `~/.construct/last-used.json` (mode `0600`), keyed by absolute repo path. A failure to save is logged as a warning and never aborts the run.
- **`go` stack** — new `construct-go` image extending `construct-base` with Go 1.24.
- **`ui` stack** — new `construct-ui` image extending `construct-base` with `@playwright/mcp` and Chromium installed at build time, enabling browser automation for front-end work.
- **`--reset` flag** — wipes and re-seeds the per-repo agent home volume before starting. Useful when home volume contents are stale. Does not affect the global auth volume or rebuild images.
- **Global auth volume** (`construct-auth-<tool>`) — opencode OAuth tokens are stored in a named Docker volume that is shared across all repos and is not wiped by `--reset`. Previously tokens lived inside the per-repo home volume and were lost on reset or when switching repos.
- **Home volume labelling** — all construct-managed volumes are labelled `io.construct.managed=true` so `docker volume prune` does not silently remove them.
- **SELinux support** — secrets bind mount now carries the `:z` relabelling suffix, allowing construct to run on Fedora, RHEL, and other SELinux-enforcing hosts.

### Fixed

- Secrets temp directory is now explicitly removed on `SIGINT`/`SIGTERM`. Previously `os.Exit` bypassed deferred cleanup, leaving credentials on disk until the next run.
- `.construct/.env` is added to `.gitignore` to prevent accidental credential commits.

---

## [v0.1.0]

Initial release.

### Features

- Run AI coding agents (`copilot`, `opencode`) inside isolated Docker containers with Docker-in-Docker.
- `--tool`, `--stack`, `--rebuild`, `--debug` flags.
- Stacks: `base`, `node`, `python`, `dotnet`.
- `construct config set|unset|list [--local]` — manage credentials in `~/.construct/.env` or a per-repo `.construct/.env`, injected into the container via bind-mounted secret files (not `docker run -e`).
- Per-repo persistent home volume (`construct-home-<tool>-<hash>`) preserving shell history, tool caches, and seeded config files.
- Testcontainers works out of the box.
