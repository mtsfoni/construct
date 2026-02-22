# Spec: Quickstart (`qs`) command

## Problem

Running `construct` requires remembering the `--tool` and `--stack` flags used last time for a given repository. For frequent users this is repetitive friction.

## Solution

Introduce a `qs` subcommand that replays the last `--tool` and `--stack` used in a repository without requiring the user to re-type them.

## Behaviour

```
construct qs [path]
```

- `path` defaults to the current working directory.
- Prints `construct qs: reusing --tool <tool> --stack <stack>` to stderr before launching.
- Errors with a clear message if no previous run has been recorded for the given path.

## Persistence

Last-used settings are stored in `~/.construct/last-used.json` as a JSON object keyed by the absolute repository path:

```json
{
  "/home/alice/projects/myapp": { "tool": "copilot", "stack": "node" },
  "/home/alice/projects/api":   { "tool": "opencode", "stack": "go" }
}
```

The file is written atomically (write to `.tmp`, then rename) with mode `0600`. The directory is created with mode `0700` if it does not exist.

Settings are saved automatically at the end of argument validation in every normal `construct --tool … --stack …` invocation, before the agent is launched. A failure to save is logged as a warning and does not abort the run.

## Implementation

| File | Change |
|------|--------|
| `internal/config/lastused.go` | New — `SaveLastUsed`, `LoadLastUsed`, JSON read/write helpers |
| `cmd/construct/main.go` | `main()` routes `qs` → `runQuickstart`; `runAgent` calls `SaveLastUsed`; help lists `qs` under Subcommands |
| `README.md` | New **Quickstart (qs)** section |

## Non-goals

- No flag to disable auto-saving.
- No `qs --tool` / `qs --stack` overrides (just use the full command instead).
- No TTY prompt to confirm before launching; the reused settings are printed to stderr.
