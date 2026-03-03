# Spec: Docker mode (`--docker`)

## Problem

`construct` previously always started a Docker-in-Docker (DinD) sidecar, even when the user's workload has no need for Docker inside the agent container. The sidecar requires `--privileged`, takes several seconds to become ready, and adds operational complexity. Many use-cases (pure coding agents, no containerised test suites) do not need Docker at all.

At the same time, some users want Docker-outside-of-Docker (DooD) instead of DinD: they prefer the agent to share the host Docker daemon via the host socket rather than running an isolated nested daemon.

## Solution

Introduce a `--docker` flag that selects the Docker access mode. The default is `none` — no Docker — matching the principle of least privilege.

```
construct --tool <tool> --docker <mode> [...]
```

## Modes

| Value | Behaviour |
|-------|-----------|
| `none` | **Default.** No Docker access. `DOCKER_HOST` is not set. No sidecar is started. |
| `dood` | Docker-outside-of-Docker. The host socket `/var/run/docker.sock` is bind-mounted into the agent container. `DOCKER_HOST=unix:///var/run/docker.sock` is injected. No sidecar is started. |
| `dind` | Docker-in-Docker. A privileged `docker:dind` sidecar is started on an isolated bridge network. `DOCKER_HOST=tcp://dind:2375` is injected (existing behaviour). |

## AGENTS.md context

The entrypoint always writes `~/.config/opencode/AGENTS.md`. The networking section it writes depends on `CONSTRUCT_DOCKER_MODE`:

- **dind** — instructs the agent that Docker runs on a separate sidecar host (`dind`), not `localhost`.
- **dood** — instructs the agent that Docker is available via the host socket and containers share the host network.
- **none** — informs the agent that no Docker access is available.

## Persistence

The selected mode is saved alongside `--tool`, `--stack`, `--mcp`, and `--port` in `~/.construct/last-used.json` under the `"docker"` key (omitted when empty, i.e. legacy entries produced before this flag existed):

```json
{
  "/home/alice/projects/api": { "tool": "opencode", "stack": "go", "docker": "dind" },
  "/home/alice/projects/web": { "tool": "opencode", "stack": "base" }
}
```

When `qs` loads a legacy entry (no `"docker"` key), it defaults to `"none"`.

## `qs` replay

`construct qs` now prints the full set of replayed flags, including `--docker`:

```
construct qs: reusing --tool opencode --stack go --docker dind
```

## Files changed

| File | Change |
|------|--------|
| `internal/runner/runner.go` | Add `DockerMode string` to `Config`; gate dind sidecar on `"dind"`; update `buildRunArgs` to handle all three modes; update entrypoint to write mode-aware AGENTS.md |
| `internal/config/lastused.go` | Add `DockerMode string` to `LastUsed`; add `dockerMode` param to `SaveLastUsed` |
| `cmd/construct/main.go` | Add `--docker` flag to `runAgent`; validate mode; pass to `runner.Config` and `SaveLastUsed`; update `runQuickstart` to print and replay all flags |
| `internal/config/lastused_test.go` | Update `SaveLastUsed` call sites; add `DockerMode` round-trip tests |
| `internal/runner/runner_test.go` | Update `fakeConfig`; add `TestBuildRunArgs_NoneMode_*`, `TestBuildRunArgs_DoodMode_*`, `TestBuildRunArgs_DindMode_*`, `TestBuildRunArgs_DockerModeEnvAlwaysPresent` |
| `docs/spec/docker-mode.md` | This document |
| `docs/spec/quickstart-qs.md` | Update `qs` status line format and persistence example |
