FROM construct-stack-dotnet:latest

# Install Playwright dependencies and Chromium for browser automation
RUN apt-get update && apt-get install -y --no-install-recommends \
    xvfb \
    libglib2.0-0 \
    libnss3 \
    libnspr4 \
    libdbus-1-3 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libcups2 \
    libdrm2 \
    libxkbcommon0 \
    libxcomposite1 \
    libxdamage1 \
    libxfixes3 \
    libxrandr2 \
    libgbm1 \
    libasound2 \
    libpango-1.0-0 \
    libcairo2 \
    && rm -rf /var/lib/apt/lists/*

# Install Playwright and Chromium browser (no MCP server)
RUN npm install -g playwright \
    && npx playwright install chromium --with-deps || true

LABEL io.construct.stack=dotnet-ui
