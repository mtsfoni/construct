# Spec: `dotnet-ui` stack

## Problem

.NET projects that include a web front-end (e.g. Blazor, ASP.NET + React/Angular,
end-to-end tests with Playwright) need both the .NET 10 SDK and a browser
available inside the construct container. Neither `dotnet` (no browser) nor
`ui` (no .NET) covers this combination.

## Solution

Add a `dotnet-ui` stack that layers `@playwright/mcp` and Chromium on top of
`construct-dotnet`. This gives agents full access to the .NET toolchain and
browser automation in a single session.

## Behaviour

```
construct --tool opencode --stack dotnet-ui --mcp --port 5000 .
```

Produces a `construct-dotnet-ui` Docker image. The agent has:

- The full .NET 10 SDK (`dotnet build`, `dotnet test`, `dotnet run`, etc.)
- `@playwright/mcp` globally installed and Chromium at `/ms-playwright`
- MCP activated when `--mcp` is passed (same mechanism as `--stack ui`)

## Dependency chain

```
construct-base
  └─ construct-dotnet
       └─ construct-dotnet-ui
```

`EnsureBuilt("dotnet-ui", ...)` resolves this via `stackDeps`:

```go
"dotnet-ui": {"base", "dotnet"},
```

Both `construct-base` and `construct-dotnet` are built (if not cached) before
`construct-dotnet-ui`.

## Why not merge dotnet and ui into one image?

Keeping `dotnet` and `ui` as separate images preserves the existing build cache
for projects that only need one or the other. `dotnet-ui` pays the extra layer
cost only when both are explicitly required.

## Files changed

| File | Change |
|---|---|
| `internal/stacks/dockerfiles/dotnet-ui/Dockerfile` | New — `FROM construct-dotnet`; installs `@playwright/mcp` and Chromium |
| `internal/stacks/stacks.go` | Add `"dotnet-ui"` to `validStacks`; add `stackDeps["dotnet-ui"] = ["base", "dotnet"]` |
| `internal/stacks/stacks_test.go` | Tests for `IsValid`, embedded Dockerfile content, `stackDeps` |
| `README.md` | Add `dotnet-ui` row to stacks table |
| `CHANGELOG.md` | Add to unreleased section |
| `docs/spec/dotnet-ui-stack.md` | This document |
