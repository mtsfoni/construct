# Changelog

## [Unreleased]

### Added
- **Serve mode** — `construct run` now starts `opencode serve` headlessly inside the container (`docker run -d`) and connects a local client from the host. The local client is `opencode attach <url>` when `opencode` is on `$PATH`, or the system default browser as a fallback. This eliminates TUI-in-container rendering issues and lets users interact through their own local opencode setup.
- **Headless mode** — when passthrough args are provided (`construct [path] -- "message"`), `opencode run --attach <url> <args...>` is run locally instead of launching an interactive TUI.
- **`--serve-port` flag** — sets the port for the opencode HTTP server inside the container (default `4096`). Distinct from `--port` (application ports). Saved to `last-used.json` and replayed by `construct qs`.
- **Pass-through args (`--`)** — both `construct [flags] [path] -- <tool-args>` and `construct qs [path] -- <tool-args>` now forward everything after the bare `--` separator verbatim to the tool inside the container (e.g. `construct qs -- continue-session <session-id>`). Pass-through args are not persisted to last-used settings. Debug mode (`--debug`) ignores them.
- **`--client` flag** — explicitly choose the local client that connects to the opencode server: `tui` (always `opencode attach`; errors if opencode not on PATH), `web` (always opens browser directly), or omit for auto-detect (default: `opencode attach` if on PATH, browser otherwise). `--client web` is incompatible with passthrough args (headless mode requires opencode). Saved to `last-used.json` and replayed by `construct qs`.

### Fixed
- **Container startup "Permission denied" errors** — the entrypoint script's heredoc that writes `~/.config/opencode/AGENTS.md` used an unquoted delimiter, causing the shell to treat backtick-wrapped paths (`` `/workspace` ``, `` `/home/agent` ``) as command substitutions. The delimiter is now quoted (`<< 'AGENTSEOF'`), preventing the errors `/workspace: Permission denied` and `/home/agent: Permission denied` on startup.

### Removed
- **copilot tool support dropped** — `opencode` is now the only supported tool. The `copilot` tool registration, its `GH_TOKEN` auth requirement, and all copilot-specific home-file seeding have been removed. See `docs/adr/002-opencode-as-sole-tool.md`.

---

## [v0.6.3] — 2026-03-05

### Added
- **Richer agent context in AGENTS.md** — the generated `~/.config/opencode/AGENTS.md` now includes a **Workspace** section (explaining that `/workspace` is the user's repo, bind-mounted and immediately visible) and an **Isolation** section (explaining that the rest of the container is isolated, and that `/home/agent` persists across sessions via a named volume).

---

## [v0.6.0] — 2026-03-05

### Added
- **`ruby` stack** — `construct-base` + Ruby (system package), Bundler, and Jekyll. Use `--stack ruby` for Jekyll sites and Ruby projects.
- **`ruby-ui` stack** — `construct-ruby` + `@playwright/mcp` + Chromium. Use `--stack ruby-ui --mcp` for Jekyll/Ruby projects that also need browser automation.
- **Global opencode slash commands** — when `~/.config/opencode/commands/` exists on the host, it is automatically bind-mounted read-only into the opencode agent container. Custom slash commands defined globally on the host are now available inside the container without any extra flags.
- **`--version` flag** — prints the construct version (e.g. `construct v0.6.0`) and exits. Reports `construct dev` when built without ldflags.
- **ARM64 support for the `go` stack** — the `go` stack Dockerfile now uses `TARGETARCH` to select the correct Go tarball, so `construct-go` builds correctly on both `linux/amd64` and `linux/arm64` hosts.
- **Automatic image rebuild on version mismatch** — stack and tool images are now stamped with an `io.construct.version` label at build time. On startup, construct compares the label against the running binary version and automatically rebuilds any stale image (one built by a different version, or one that predates this feature and carries no label). Dev builds (no ldflags version) skip the check entirely so local iteration is unaffected.

### Fixed
- **`--stack` default corrected to `base`** — the default was incorrectly left as `node` (a stack removed in v0.3.0), which would cause an error for users who omitted `--stack`. It now correctly defaults to `base`.

---

## [v0.5.0] — 2026-03-05

### Added

- **Changelog-driven release notes** — the release pipeline now extracts the
  `## [Unreleased]` block from `CHANGELOG.md` and uses it as the GitHub
  release body. After tagging, the changelog is automatically updated:
  `[Unreleased]` is renamed to the tag version with today's date, and a fresh
  empty `[Unreleased]` block is inserted above it. The commit is pushed back
  to `main` by `github-actions[bot]`.

### Changed

- Agent commits now carry the host user's real git identity instead of the
  synthetic `construct agent` identity. Author and committer are resolved
  independently using git's own precedence: `GIT_AUTHOR_*` / `GIT_COMMITTER_*`
  host env vars take priority, then `git config user.name` / `user.email`, then
  the committer falls back to the author (matching git's default behaviour). The
  synthetic fallback (`construct user <user@construct.local>`) is only used when
  no author identity at all is available on the host, and triggers a warning.
- A `commit-msg` hook is injected into the container at startup via
  `core.hooksPath`. It appends a `Generated by construct` git trailer to every
  commit message (idempotent — safe with amend and rebase).

---

## [v0.4.0] — 2026-03-03

### Added

- **`--docker` flag** — selects Docker access mode for the agent container. `--docker none` (default) starts no sidecar and sets no `DOCKER_HOST`, following the principle of least privilege. `--docker dood` bind-mounts the host socket (`/var/run/docker.sock`) for Docker-outside-of-Docker access. `--docker dind` starts an isolated privileged `docker:dind` sidecar (previous default behaviour). The mode is saved in `~/.construct/last-used.json` and replayed by `qs`. The injected `AGENTS.md` networking section is tailored to the active mode.
- **`dotnet-big` stack** — new `construct-dotnet-big` image extending `construct-base` with the .NET 8, 9, and 10 SDKs installed side-by-side. Use when a project targets multiple .NET generations or must verify cross-version compatibility.
- **`dotnet-big-ui` stack** — new `construct-dotnet-big-ui` image extending `construct-dotnet-big` with `@playwright/mcp` and Chromium. Use with `--mcp` for projects that need multi-version .NET support and browser automation in the same session.

---

## [v0.3.2] — 2026-03-01

### Added

- **Static `dind` network alias** — the dind sidecar now registers the alias `dind` on its session-scoped bridge network. `DOCKER_HOST` is always `tcp://dind:2375`, matching the hostname already documented in the injected `AGENTS.md`. This makes the alias real rather than implied and keeps it stable across sessions.

### Fixed

- **`dotnet`/`dotnet-ui` stack** — `libicu70` is now installed in the image, resolving a runtime crash when .NET applications use globalization (ICU mode). Previously .NET would abort on startup with `Couldn't find a valid ICU package`.

---

## [v0.3.0] — 2026-02-23

### Added

- **`--port` flag** — publish container ports to the host. Repeatable; accepts any format `docker run -p` supports (`3000`, `9000:3000`, `127.0.0.1:3000:3000`). A bare port number is automatically expanded to `host:container`.
- **`--mcp` flag** — activate MCP servers at container startup. When passed, the entrypoint writes `~/.config/opencode/opencode.json` registering `@playwright/mcp`; without it the file is removed. Requires `--stack ui` or `--stack dotnet-ui` for full browser automation support.
- **`dotnet-ui` stack** — new `construct-dotnet-ui` image combining the .NET 10 SDK with `@playwright/mcp` and Chromium. Extends `construct-dotnet`; use with `--mcp` for Blazor/ASP.NET projects that need browser automation.
- **Automatic AGENTS.md injection** — the entrypoint always writes `~/.config/opencode/AGENTS.md` so opencode knows it is running inside a construct container. When `--port` is used the file also contains server binding rules (bind to `0.0.0.0`, use the ports listed in `$CONSTRUCT_PORTS`), preventing the common mistake of agents starting dev servers on `127.0.0.1` which is unreachable from the host.
- **Networking context in AGENTS.md** — the injected file now always explains that Docker runs on a separate sidecar host (`dind`), not `localhost`. Containers started via Docker are reachable at `dind:<port>`, not `127.0.0.1`. Also clarifies that the user can access ports on the agent machine directly but not ports inside Docker containers, so agents should run services on the machine itself.
- **`OPENCODE_EXPERIMENTAL_DISABLE_COPY_ON_SELECT=true`** — injected into the opencode container to prevent unwanted clipboard interference from terminal text selection.
- **`CONSTRUCT=1` env var** — always injected into the agent container so tools can detect they are running inside construct.
- **`CONSTRUCT_PORTS` env var** — injected when `--port` is used; contains the comma-separated list of container-side port numbers.
- **`qs` now replays `--mcp` and `--port`** — the quickstart command restores the full previous invocation, not just `--stack`. `~/.construct/last-used.json` now stores `mcp` and `ports` alongside `stack`.
- **`install.sh`** — convenience script that builds the binary from source and installs it to `~/.local/bin/construct`.

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
