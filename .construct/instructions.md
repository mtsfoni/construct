# Construct agent instructions

## Visual iteration with Playwright (ui stack)

When running on the `ui` stack, Playwright is pre-installed and Chromium is
available headlessly. Use the `playwright-browser` skill to drive browser
automation for visual feedback during component development.

### Workflow

1. **Start the dev server** — launch `npm run dev` (or the appropriate command
   for the project) as a background process before taking any screenshots.
   ```
   npm run dev &
   ```
   Wait a moment for it to bind before navigating.

2. **Load the skill** — invoke the `playwright-browser` skill to get the
   execution patterns and script templates.

3. **Screenshot first, code second** — after writing or modifying a component,
   write a Playwright script to `/tmp/` and run it to confirm the visual result
   before moving on.
   ```bash
   node /tmp/screenshot.js
   ```

4. **Iterate visually** — use the screenshot output to spot layout issues,
   missing styles, or broken states. Fix the component, screenshot again, and
   repeat until it looks correct.

5. **Write tests from the working state** — once the component renders as
   expected, generate Playwright tests that assert the visible behaviour you
   just verified. Prefer `getByRole` and `getByText` selectors over CSS
   selectors for resilience.

6. **Run the tests headlessly** — confirm the generated tests pass inside the
   container:
   ```
   npx playwright test
   ```

### Tips

- Chromium runs as a process inside the agent container — no extra service or
  sidecar is needed.
- The Vite dev server also runs as a process in the same container. Both are
  managed by the agent.
- Always use `headless: true` — there is no display in the container.
- Write all temporary scripts to `/tmp/` — never to `/workspace` — so the
  user's repo stays clean.
- For multi-page flows, write a single script that navigates between steps and
  takes a screenshot at each stage to track state visually.
- If a screenshot shows a blank page, the dev server may still be starting.
  Wait a second and retry.
- Prefer `localhost` over `127.0.0.1` to avoid IPv4/IPv6 resolution surprises.
