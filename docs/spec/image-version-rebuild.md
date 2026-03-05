# Automatic image rebuild on version mismatch

## Problem

When a new version of construct ships with changes to a stack Dockerfile, the
generated entrypoint script, or a tool's install commands, users' locally cached
Docker images are silently stale. The only remedy is to pass `--rebuild`
manually, which users must know to do.

Affected scenarios:
- Stack Dockerfile updated (e.g. Go version bump, new system package)
- Entrypoint script changed (new `AGENTS.md` content, MCP config changes)
- Tool install command updated (e.g. newer `opencode-ai` npm package pinned)

## Solution

Stamp every construct-built image with a Docker label recording the construct
version that built it:

```
io.construct.version=v0.6.0
```

Before using a cached image, inspect its label and compare against the running
binary's version. If they differ, trigger a rebuild automatically — exactly as
if `--rebuild` had been passed for that image.

## Behaviour

### Version match
The cached image was built by this version of construct. Use it as-is. No
change from current behaviour.

### Version mismatch (or label absent)
The image was built by a different (older) version, or by a construct binary
that predates this feature (no label). Rebuild automatically and print a
diagnostic:

```
construct: stack image construct-go was built by v0.5.0, rebuilding for v0.6.0…
construct: tool image construct-go-opencode was built by v0.5.0, rebuilding for v0.6.0…
```

### Dev builds (`version == ""`)
When the binary was built without ldflags (local development), the version
string is empty. In this case the version check is skipped entirely and the
existing `imageExists` logic applies — no forced rebuild on every `go run`.

### `--rebuild` flag
Explicit `--rebuild` continues to force a rebuild unconditionally, bypassing
the version check. Behaviour is unchanged.

### Label absent (image predates this feature)
Treated as a version mismatch: rebuild once. On subsequent runs the new image
carries the label and the check passes.

## Implementation

### `internal/buildinfo/buildinfo.go` (new package)

```go
package buildinfo

var Version string
```

Set via `-ldflags "-X github.com/mtsfoni/construct/internal/buildinfo.Version=<tag>"`.
Replaces the package-local `var version string` in `cmd/construct/main.go`.

### Image labelling

Both `stacks.build()` and `runner.buildToolImage()` pass `--label` to
`docker build` when `buildinfo.Version != ""`:

```
docker build --label io.construct.version=v0.6.0 -t construct-go <dir>
```

### Version check helpers

`imageVersionCurrent(name string) bool` — calls `docker image inspect` to read
the `io.construct.version` label and compares to `buildinfo.Version`. Returns
`true` if the label matches the running version, or if `buildinfo.Version == ""`
(dev build).

One copy lives in each package (`stacks` and `runner`) since the packages
cannot import each other without a cycle.

### `EnsureBuilt` (stacks)

Replace:
```go
if rebuild || !imageExists(name) {
```
With:
```go
if rebuild || !imageExists(name) || !imageVersionCurrent(name) {
```

Same change for each dependency image.

### `Run` (runner)

Replace:
```go
if cfg.Rebuild || !toolImageExists(toolImage) {
```
With:
```go
if cfg.Rebuild || !toolImageExists(toolImage) || !toolImageVersionCurrent(toolImage) {
```

## Files changed

| File | Change |
|---|---|
| `docs/spec/image-version-rebuild.md` | This spec |
| `internal/buildinfo/buildinfo.go` | New — `var Version string` |
| `cmd/construct/main.go` | Import `buildinfo`; remove local `var version string`; read `buildinfo.Version` |
| `internal/stacks/stacks.go` | Import `buildinfo`; label in `build()`; `imageVersionCurrent()` helper; update `EnsureBuilt` |
| `internal/runner/runner.go` | Import `buildinfo`; label in `buildToolImage()`; `toolImageVersionCurrent()` helper; update `Run` |
| `internal/stacks/stacks_test.go` | Tests for `imageVersionCurrent` logic |
| `internal/runner/runner_test.go` | Tests for `buildRunArgs` version label pass-through |
| `.github/workflows/release.yml` | Update ldflags target to `buildinfo.Version` |
| `CHANGELOG.md` | Entry under `[Unreleased]` |
