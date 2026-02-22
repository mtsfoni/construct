# Spec: Playwright browser skill (superseded)

This spec described a planned approach where browser automation would be exposed
to the agent as an opencode skill rather than an MCP server. That approach was
not implemented.

The current implementation uses `@playwright/mcp` as an MCP server seeded via
`opencode.json` in the agent's home volume. See `playwright-mcp-config.md` for
the implemented behaviour and `ui-stack.md` for the full `ui` stack spec.
