# Serve-mode health-check timeout diagnostics

## Problem

When `construct` starts opencode in serve mode (`docker run -d`), it waits up to
15 seconds for `GET /global/health` to return `{"healthy":true}`. If the server
never becomes ready — because opencode crashed, couldn't write to a root-owned
directory, hit a missing API key, encountered a corrupt database, or for any
other reason — the user sees:

```
server did not become ready: timed out after 15s waiting for http://127.0.0.1:4096/global/health
```

The container is stopped immediately after this message. The user has no idea
**why** opencode failed to start, and no idea **what to do next**. Common causes
(stale home volume after an upgrade, stale image, port conflict) each have a
known remedy, but none are surfaced.

## Solution

Two improvements, applied together at the timeout site in `runner.go`:

### 1. Retrieve and print container logs before stopping

Before calling `stopContainer`, run `docker logs <containerName>` and print the
output to stderr under a clear header. This exposes the opencode process's own
error output, which almost always names the root cause directly (e.g.
`EACCES: permission denied, mkdir '/home/agent/.local/share/opencode/bin'` or
a missing API key complaint).

The log retrieval is best-effort: if `docker logs` itself fails (e.g. the
container already exited and Docker cleaned it up), the error is ignored and the
timeout error is returned as before.

Output format:

```
construct: server did not become ready — container logs:
--- begin container logs ---
<output of docker logs>
--- end container logs ---
```

If the container produced no output, the section is omitted (to avoid printing
an empty block that could confuse users).

### 2. Append a recovery hint to the error

After the container log block, print a recovery hint that names the three most
likely remedies in order of how commonly they apply:

```
construct: hint: if opencode just updated, try: construct --rebuild
construct: hint: if the home volume is corrupt or stale, try: construct --reset
construct: hint: to inspect the container interactively, try: construct --debug
```

These are written to stderr as plain lines (not part of the `error` return
value), so they appear even when the caller wraps the error.

## Behaviour details

- Log retrieval and hint printing happen only on a health-check timeout, not on
  other errors (image build failure, port conflict, dind timeout, etc.).
- `docker logs` is called with no flags — it captures both stdout and stderr of
  the container process, which is sufficient for diagnosing opencode crashes.
- The log output is written to `os.Stderr` so it is not captured by callers
  that only capture the returned `error`.
- The recovery hint lines are written to `os.Stderr` unconditionally (no
  `--quiet` flag exists today).
- The returned error string is unchanged: `server did not become ready: timed
  out after 15s waiting for http://127.0.0.1:4096/global/health`. This keeps
  integration tests that assert on the error string working without changes.

## Files changed

| File | Change |
|------|--------|
| `internal/runner/runner.go` | Add `containerLogs(name string) string` helper; update the `waitForServer` timeout branch to call it and print the hint |
| `internal/runner/runner_test.go` | Tests for `containerLogs` helper and the timeout diagnostic output |
| `CHANGELOG.md` | Entry under `[Unreleased]` |
