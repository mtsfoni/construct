# Spec: `ruby-ui` stack

## Problem

Ruby/Jekyll projects that include a web front-end or need browser-based
end-to-end testing require both Ruby tooling and a browser available inside
the construct container. Neither `ruby` (no browser) nor `ui` (no Ruby)
covers this combination.

## Solution

Add a `ruby-ui` stack that layers `@playwright/mcp` and Chromium on top of
`construct-ruby`. This gives agents full access to the Ruby/Jekyll toolchain
and browser automation in a single session.

## Behaviour

```
construct --tool opencode --stack ruby-ui --mcp --port 4000 .
```

Produces a `construct-ruby-ui` Docker image. The agent has:

- `ruby`, `gem`, `irb`, `bundle`, and `jekyll` available on `PATH`
- `@playwright/mcp` globally installed and Chromium at `/ms-playwright`
- MCP activated when `--mcp` is passed (same mechanism as `--stack ui`)

## Dependency chain

```
construct-base
  └─ construct-ruby
       └─ construct-ruby-ui
```

`EnsureBuilt("ruby-ui", ...)` resolves this via `stackDeps`:

```go
"ruby-ui": {"base", "ruby"},
```

Both `construct-base` and `construct-ruby` are built (if not cached) before
`construct-ruby-ui`.

## Why not merge ruby and ui into one image?

Keeping `ruby` and `ui` as separate images preserves the existing build cache
for projects that only need one or the other. `ruby-ui` pays the extra layer
cost only when both are explicitly required.

## Files changed

| File | Change |
|---|---|
| `internal/stacks/dockerfiles/ruby-ui/Dockerfile` | New — `FROM construct-ruby`; installs `@playwright/mcp` and Chromium |
| `internal/stacks/stacks.go` | Add `"ruby-ui"` to `validStacks`; add `stackDeps["ruby-ui"] = ["base", "ruby"]` |
| `internal/stacks/stacks_test.go` | Tests for `IsValid`, embedded Dockerfile content, `stackDeps` |
| `README.md` | Add `ruby-ui` row to stacks table; update `--mcp` flag description |
| `CHANGELOG.md` | Add to unreleased section |
| `docs/spec/ruby-ui-stack.md` | This document |
