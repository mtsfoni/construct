# Spec: `--client` flag

## Problem

`construct` currently uses a hardcoded heuristic to decide how to connect the user to the running `opencode serve` server:

1. If `opencode` is on `$PATH` → run `opencode attach <url>` (TUI).
2. Otherwise → open the URL in a browser.

This is a reasonable default but gives users no control. A user who has `opencode` installed but prefers the web UI must work around the auto-detection. Conversely, a user who knows `opencode` is not on their `$PATH` gets a silent browser fallback with no error. Web and TUI should be equal citizens that the user can explicitly select.

## Solution

Add a `--client` flag to `construct` (and replayed by `construct qs`) with three valid values:

| Value | Meaning |
|---|---|
| `""` (empty, default) | Auto-detect: try `opencode attach`; fall back to browser if not found. |
| `"tui"` | Always use `opencode attach <url>`. Error if `opencode` not on `$PATH`. |
| `"web"` | Always open the browser directly; skip `opencode` check entirely. |

The flag is saved to `last-used.json` and replayed by `construct qs`. An empty string is omitted from JSON (`omitempty`), meaning old entries behave as auto (the previous default behaviour).

## Behaviour table

| `--client` | `opencode` on `$PATH`? | Passthrough args? | Result |
|---|---|---|---|
| `""` (auto) | yes | any | `opencode attach <url>` |
| `""` (auto) | no | any | browser (`xdg-open`/`open`) |
| `"tui"` | yes | any | `opencode attach <url>` |
| `"tui"` | no | any | **error**: "opencode not found on PATH; install opencode or use --client web" |
| `"web"` | either | none | browser directly |
| `"web"` | either | present | **fatal error**: "--client web is incompatible with passthrough args (headless requires opencode)" |

Any value other than `""`, `"tui"`, or `"web"` is a fatal validation error at startup.

## Persistence

`Client string \`json:"client,omitempty"\`` is added to `config.LastUsed`. An empty string is absent from JSON, so existing entries continue to behave as auto.

`SaveLastUsed` gains a `client string` parameter. All call sites are updated.

`construct qs` replays `--client <value>` in the `agentArgs` slice only when `last.Client != ""`.

## Error messages

| Condition | Message |
|---|---|
| `--client tui` and `opencode` not on PATH | `opencode not found on PATH; install opencode or use --client web` |
| `--client web` with passthrough args | `--client web is incompatible with passthrough args (headless requires opencode)` |
| Unknown `--client` value | `unknown client %q; supported values: tui, web` |

## Files changed

| File | Change |
|---|---|
| `docs/spec/client-flag.md` | This file — spec |
| `internal/config/lastused.go` | Add `Client string` to `LastUsed`; add `client string` param to `SaveLastUsed` |
| `internal/config/lastused_test.go` | Update `SaveLastUsed` call sites; add `TestSaveAndLoadLastUsed_Client` and `TestSaveAndLoadLastUsed_ClientOmittedWhenEmpty` |
| `internal/runner/runner.go` | Add `Client string` to `Config`; refactor `runLocalAttach` to accept `client string`; add validation in `Run` |
| `internal/runner/runner_test.go` | Tests for `runLocalAttach` client modes and `Run` validation |
| `cmd/construct/main.go` | Add `--client` flag; validate; wire into `SaveLastUsed` and `runner.Config`; update `runQuickstart` |
| `CHANGELOG.md` | Entry under `[Unreleased]` |
