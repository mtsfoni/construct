# Construct agent instructions

## Visual iteration with Playwright (ui stack)

When running on the `ui` stack, Playwright is available as an MCP server.
Use the `playwright-browser` MCP tools to drive browser automation for visual
feedback during component development.

### Workflow

1. **Start the dev server** — launch `npm run dev` (or the appropriate command
   for the project) as a background process before taking any screenshots.
   ```
   npm run dev &
   ```
   Wait a moment for it to bind before navigating.

2. **Screenshot first, code second** — after writing or modifying a component,
   use the `playwright-browser` MCP tools to navigate and take a screenshot to
   confirm the visual result before moving on.

3. **Iterate visually** — use the screenshot output to spot layout issues,
   missing styles, or broken states. Fix the component, screenshot again, and
   repeat until it looks correct.

4. **Write tests from the working state** — once the component renders as
   expected, generate Playwright tests that assert the visible behaviour you
   just verified. Prefer `getByRole` and `getByText` selectors over CSS
   selectors for resilience.

5. **Run the tests** — confirm the generated tests pass:
   ```
   npx playwright test
   ```

### Tips

- The Vite dev server runs as a process in the agent container and is
  managed by the agent.
- For multi-page flows, navigate between steps and take a screenshot at each
  stage to track state visually.
- If a screenshot shows a blank page, the dev server may still be starting.
  Wait a second and retry.
- Prefer `localhost` over `127.0.0.1` to avoid IPv4/IPv6 resolution surprises.
