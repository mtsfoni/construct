# `--version` flag

## Problem

There is no way to check which version of construct is installed without reading
the binary metadata. This makes it harder to debug issues and report bugs.

## Solution

Add a `--version` / `-version` flag that prints the construct version and exits 0.

## Behaviour

- `construct --version` and `construct -version` both print `construct <version>`
  to stdout and exit 0.
- When built without ldflags (e.g. `go build ./...` during development), the
  version reports as `construct dev`.
- When built by the release pipeline with
  `-ldflags "-X main.version=<tag>"`, the version reports as
  `construct v0.6.0` (or whatever the tag is).
- `--version` is handled before subcommand dispatch, so it works regardless of
  what other arguments are present.
- `--version` is documented in the `--help` / usage output.

## Files changed

| File | Change |
|---|---|
| `cmd/construct/main.go` | Add `var version string`; handle `--version` before subcommand dispatch; document in usage |
| `cmd/construct/version_test.go` | Integration tests: exit code, output format, `dev` fallback, presence in usage |
| `docs/spec/version-flag.md` | This spec |
| `README.md` | `--version` entry in flags table |
| `CHANGELOG.md` | Entry under `[Unreleased]` |
