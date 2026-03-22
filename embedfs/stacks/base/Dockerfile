FROM debian:bookworm-slim

# Install base dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    wget \
    unzip \
    tar \
    git \
    bash \
    zsh \
    ca-certificates \
    gnupg \
    python3 \
    python3-pip \
    python3-venv \
    build-essential \
    sudo \
    gosu \
    && rm -rf /var/lib/apt/lists/*

# Grant passwordless sudo to any user — this is a single-tenant container and
# the container boundary is the security perimeter. This lets the agent run
# apt-get install and other root operations without a password.
RUN echo 'ALL ALL=(ALL:ALL) NOPASSWD: ALL' > /etc/sudoers.d/agent \
    && chmod 0440 /etc/sudoers.d/agent

# Install Docker CLI (no daemon)
RUN install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod a+r /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" \
       > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js LTS
RUN curl -fsSL https://deb.nodesource.com/setup_lts.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install opencode into the image (NPM_CONFIG_PREFIX is not set yet, so npm
# uses the default prefix /usr/local — binary lands at /usr/local/bin/opencode).
RUN npm install -g opencode-ai

# Create /agent with world-writable permissions so the named volume that mounts
# here is writable by whichever host UID the container runs as (--user uid:gid).
RUN mkdir -p /agent && chmod 0777 /agent

# Agent layer environment variables — /agent is for agent-installed tools only.
# opencode itself lives in /usr/local/bin (baked into the image above).
ENV HOME=/agent/home
ENV XDG_CONFIG_HOME=/agent/home/.config
ENV PATH=/agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV NPM_CONFIG_PREFIX=/agent

# Ensure the construct PATH survives login shells (which source /etc/profile
# and reset PATH to a Debian default).
RUN echo 'export PATH="/agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"' \
    > /etc/profile.d/construct-path.sh

# Ensure the construct PATH survives login shells (which source /etc/profile
# and reset PATH to a Debian default). Any stack-specific paths prepended via
# ENV PATH in a child Dockerfile are picked up via the $PATH expansion here
# because this RUN executes after those ENV instructions are applied.
RUN echo 'export PATH="'"$PATH"'"' > /etc/profile.d/construct-path.sh

# Label
LABEL io.construct.stack=base

# Entrypoint script
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

CMD ["/entrypoint.sh"]
