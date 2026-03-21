FROM construct-stack-base:latest

# Install additional Node.js tooling
RUN npm install -g pnpm yarn

# Build essentials for native Node addons
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3-dev \
    && rm -rf /var/lib/apt/lists/*

LABEL io.construct.stack=node
