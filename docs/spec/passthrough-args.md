# Spec: Pass-through args (`--`)

## Problem

Some tools accept flags of their own (e.g. `opencode -s <session-id>`). Currently there is no way to hand those arguments to the tool when launching via `construct` or `construct qs`.

## Solution

Support a `--` separator in both `construct` and `construct qs`. Anything after `--` is appended verbatim to the tool's `RunCmd` inside the container.

## Behaviour

```
construct [flags] [path] -- [tool-args...]
construct qs [path] -- [tool-args...]
```

- Everything after the first bare `--` token is collected as `tool-args` and appended to the container's command after `Tool.RunCmd`.
- The `[path]` positional and all `construct` flags are parsed from the portion **before** `--`.
- If `--` is absent the behaviour is identical to today (no change in the default case).
- `--debug` mode (`/bin/bash`) still ignores pass-through args — there is nothing useful to forward to a shell.
- `--client web` is incompatible with pass-through args: headless mode requires `opencode run --attach`, so a browser-only client cannot be used. Passing both results in a fatal error:
  ```
  --client web is incompatible with passthrough args (headless requires opencode)
  ```

### Examples

```
# Resume an existing opencode session
construct qs -- -s ses_deadbeefcafe1234abcd5678

# Same, specifying the repo explicitly
construct qs ~/projects/myapp -- -s ses_deadbeefcafe1234abcd5678

# Plain construct invocation with pass-through args
construct --stack go -- -s ses_deadbeefcafe1234abcd5678
```

## Persistence

Pass-through args are **not** persisted to `~/.construct/last-used.json`. They are one-off overrides. A subsequent bare `construct qs` will replay the saved flags without the pass-through args.

## Implementation

| File | Change |
|------|--------|
| `internal/runner/runner.go` | Add `ExtraArgs []string` to `Config`; append to `RunCmd` in `buildRunArgs` (only when not in debug mode) |
| `cmd/construct/main.go` | `splitPassthrough` helper splits `args` on `--`; `runAgent` and `runQuickstart` use it to populate `runner.Config.ExtraArgs`; usage strings updated |

## Non-goals

- No persistence of pass-through args in last-used.
- No validation of the tool args — they are forwarded verbatim.
- No pass-through support in `--debug` mode (shell is the CMD; there is nothing to forward to).
