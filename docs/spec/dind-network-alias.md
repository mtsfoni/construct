# Spec: dind network alias

## Problem

The dind sidecar container was reachable from the agent container only by its
session-specific name (`construct-dind-<sessionID>`). `DOCKER_HOST` was set to
`tcp://construct-dind-<sessionID>:2375`, which worked for the Docker client, but
left the agent without a stable, predictable hostname for the dind daemon.

The `AGENTS.md` context injected into every container already told the agent
that *"containers you start are reachable at hostname **dind**"*, but that
hostname didn't actually resolve — the alias was implied, not declared.

## Solution

Attach the static network alias `dind` to the sidecar when it is started:

```
docker run ... --network-alias dind ... docker:dind
```

Because each construct session creates its own isolated bridge network
(`construct-net-<sessionID>`), the alias `dind` is scoped to that network.
Multiple concurrent construct instances each have their own `dind` that doesn't
conflict with any other session.

`DockerHost()` now returns the static value `tcp://dind:2375` instead of the
session-specific container name. The agent container always has
`DOCKER_HOST=tcp://dind:2375`, matching the hostname the `AGENTS.md` context
already documented.

## Network topology

```
construct-net-<sessionID>  (isolated bridge)
  ├── construct-agent-<sessionID>   DOCKER_HOST=tcp://dind:2375
  └── construct-dind-<sessionID>    alias: dind   port 2375
```

Each session's bridge is independent, so `dind` resolves unambiguously within
every session regardless of how many construct instances are running in parallel.

## Files changed

| File | Change |
|---|---|
| `internal/dind/dind.go` | Extracted `buildStartArgs()` helper; added `--network-alias dind` to the sidecar `docker run` args; `DockerHost()` now returns the static `tcp://dind:2375` |
| `internal/dind/dind_test.go` | New — `TestDockerHost_ReturnsStaticAlias` and `TestStart_IncludesNetworkAlias` |
| `internal/runner/runner_test.go` | Added `TestBuildRunArgs_DockerHostUsesStaticDindAlias` |
