# Spec: `dotnet-big` and `dotnet-big-ui` stacks

## Problem

Some projects require multiple .NET SDK generations simultaneously — for example
to verify cross-version compatibility, run SDKs targeting net8.0 and net9.0
side-by-side, or maintain libraries that must build against several TFMs. The
existing `dotnet` stack only carries .NET 10, which is insufficient for these
cases.

Similarly, projects combining multi-version .NET work with web front-end testing
need both the multi-SDK environment and a browser available in a single session.

## Solution

Add two new stacks:

- **`dotnet-big`** — extends `construct-base` with the .NET 8, 9, and 10 SDKs
  installed side-by-side under `/usr/share/dotnet`.
- **`dotnet-big-ui`** — extends `construct-dotnet-big` with `@playwright/mcp`
  and Chromium, mirroring the `dotnet-ui` pattern.

## Behaviour

```
construct --tool opencode --stack dotnet-big .
construct --tool opencode --stack dotnet-big-ui --mcp --port 5000 .
```

### `dotnet-big`

Produces a `construct-dotnet-big` image. The agent has:

- .NET 8 SDK (`dotnet build`, `dotnet test`, `dotnet run` targeting net8.0)
- .NET 9 SDK (targeting net9.0)
- .NET 10 SDK (targeting net10.0)
- All SDKs share `/usr/share/dotnet`; `dotnet --list-sdks` will show all three.

### `dotnet-big-ui`

Produces a `construct-dotnet-big-ui` image. The agent additionally has:

- `@playwright/mcp` globally installed and Chromium at `/ms-playwright`
- MCP activated when `--mcp` is passed (same mechanism as `--stack ui`)

## Dependency chains

```
construct-base
  └─ construct-dotnet-big
       └─ construct-dotnet-big-ui
```

`EnsureBuilt("dotnet-big", ...)` uses the implicit base dependency:

```go
// No explicit entry needed — non-base stacks without a stackDeps entry
// automatically depend on "base".
```

`EnsureBuilt("dotnet-big-ui", ...)` resolves via `stackDeps`:

```go
"dotnet-big-ui": {"base", "dotnet-big"},
```

Both `construct-base` and `construct-dotnet-big` are built (if not cached)
before `construct-dotnet-big-ui`.

## Why not extend `dotnet`?

`dotnet-big` starts fresh from `construct-base` rather than layering on top of
`construct-dotnet`. This keeps the image minimal: the existing `dotnet` image
installs only .NET 10 and its layer cache is unaffected by the new stacks.
`dotnet-big` pays its own layer cost only when all three SDKs are explicitly
required.

## Files changed

| File | Change |
|---|---|
| `internal/stacks/dockerfiles/dotnet-big/Dockerfile` | New — `FROM construct-base`; installs .NET 8, 9, and 10 SDKs |
| `internal/stacks/dockerfiles/dotnet-big-ui/Dockerfile` | New — `FROM construct-dotnet-big`; installs `@playwright/mcp` and Chromium |
| `internal/stacks/stacks.go` | Add `"dotnet-big"` and `"dotnet-big-ui"` to `validStacks`; add `stackDeps["dotnet-big-ui"] = ["base", "dotnet-big"]` |
| `internal/stacks/stacks_test.go` | Tests for `IsValid`, embedded Dockerfile content, `stackDeps` |
| `README.md` | Add `dotnet-big` and `dotnet-big-ui` rows to stacks table; update `--stack` and `--mcp` flag descriptions |
| `CHANGELOG.md` | Add to unreleased section |
| `docs/spec/image-build-layers.md` | Add both stacks to contents table and dependency chain diagram |
| `docs/spec/mcp-flag.md` | Add `dotnet-big-ui` to MCP capability table and installation note |
| `docs/spec/playwright-mcp-config.md` | Add `dotnet-big-ui` to list of stacks with Playwright pre-installed |
| `docs/spec/dotnet-big-stack.md` | This document |
