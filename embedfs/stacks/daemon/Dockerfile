# construct daemon image
# The constructd binary is cross-compiled by the bootstrap process and
# copied into this build context before `docker build` is invoked.
FROM debian:bookworm-slim

# Install Docker CLI for managing session containers
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    gnupg \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod a+r /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" \
       > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli \
    && rm -rf /var/lib/apt/lists/*

COPY constructd /usr/local/bin/constructd
RUN chmod +x /usr/local/bin/constructd

LABEL io.construct.component=daemon

ENTRYPOINT ["/usr/local/bin/constructd"]
