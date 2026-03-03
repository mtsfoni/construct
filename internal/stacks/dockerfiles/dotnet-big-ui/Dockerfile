FROM construct-dotnet-big

USER root

# Install Playwright browsers to a fixed path (same convention as construct-ui).
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright

# Install @playwright/mcp globally and Chromium via the bundled playwright binary.
RUN npm install -g @playwright/mcp && \
    $(npm root -g)/@playwright/mcp/node_modules/.bin/playwright install --with-deps chromium && \
    chmod -R a+w /ms-playwright

USER agent
