FROM construct-base

USER root

# Install Playwright browsers to a fixed path so both the build-time root user
# and the runtime agent user can find them without relying on $HOME.
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright

# Install @playwright/mcp globally so opencode can start it as an MCP server.
# Use the playwright binary bundled inside @playwright/mcp to install Chromium,
# ensuring the exact browser revision it expects is present in /ms-playwright.
RUN npm install -g @playwright/mcp && \
    $(npm root -g)/@playwright/mcp/node_modules/.bin/playwright install --with-deps chromium && \
    chmod -R a+w /ms-playwright

USER agent
