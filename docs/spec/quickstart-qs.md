# Spec: Quickstart (`qs`) command

## Problem

Running `construct` requires remembering the `--stack` flag used last time for a given repository. For frequent users this is repetitive friction.

## Solution

Introduce a `qs` subcommand that replays the last `--stack`, `--docker`, `--mcp`, `--port`, `--serve-port`, and `--client` flags used in a repository without requiring the user to re-type them.

## Behaviour

```
construct qs [path] [-- <tool-args>]
```

- `path` defaults to the current working directory.
- Prints all replayed flags to stderr before launching, e.g.:
  `construct qs: reusing --stack go --docker dind --mcp --port 3000 --client web`
- Errors with a clear message if no previous run has been recorded for the given path.
- Replays `--docker`, `--mcp`, all `--port` values, `--serve-port`, and `--client` that were used in the last recorded invocation.
- For entries recorded before `--docker` was introduced (no `"docker"` key), defaults to `--docker none`.
- `--client` is only replayed when it was explicitly set (non-empty); absent/empty means auto-detect and is not added to the args.
- Anything after a bare `--` separator is forwarded verbatim to the tool inside the container and is **not** saved to last-used (see `docs/spec/passthrough-args.md`).

## Persistence

Last-used settings are stored in `~/.construct/last-used.json` as a JSON object keyed by the absolute repository path:

```json
{
  "/home/alice/projects/myapp": { "stack": "base" },
  "/home/alice/projects/api":   { "stack": "go", "docker": "dind" },
  "/home/alice/projects/web":   { "stack": "ui", "mcp": true, "ports": ["3000", "8080:8080"], "docker": "dood" },
  "/home/alice/projects/srv":   { "stack": "go", "serve_port": 4096, "client": "web" }
}
```

The `mcp` key is omitted when `false`; the `ports` key is omitted when empty; the `docker` key is omitted when empty (legacy entries without a docker mode default to `none` at replay time); `serve_port` is omitted when zero (defaults to `4096`); `client` is omitted when empty (defaults to auto-detect).

The file is written atomically (write to `.tmp`, then rename) with mode `0600`. The directory is created with mode `0700` if it does not exist.

Settings are saved automatically at the end of argument validation in every normal `construct --stack …` invocation, before the agent is launched. A failure to save is logged as a warning and does not abort the run.

## Implementation

| File | Change |
|------|--------|
| `internal/config/lastused.go` | New — `SaveLastUsed`, `LoadLastUsed`, JSON read/write helpers; `DockerMode` field added |
| `cmd/construct/main.go` | `main()` routes `qs` → `runQuickstart`; `runAgent` calls `SaveLastUsed`; `runQuickstart` prints and replays all flags; `runQuickstart` uses `splitPassthrough` to forward `--` args without saving them; help lists `qs` under Subcommands |
| `README.md` | New **Quickstart (qs)** section |

## Non-goals

- No flag to disable auto-saving.
- No `qs --stack` overrides (just use the full command instead).
- No TTY prompt to confirm before launching; the reused settings are printed to stderr.
- Pass-through args after `--` are not persisted; a subsequent bare `construct qs` replays only the saved flags.
