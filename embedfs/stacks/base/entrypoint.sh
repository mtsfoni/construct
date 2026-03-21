#!/bin/bash
set -e

# The entrypoint runs as root so it can register the host UID/GID in
# /etc/passwd and /etc/group. This is required for sudo to work — sudo does a
# getpwuid(3) lookup and refuses to proceed for unknown UIDs.
# After setup we drop to the host user via gosu for sleep infinity.

AGENT_UID=${CONSTRUCT_UID:-1000}
AGENT_GID=${CONSTRUCT_GID:-1000}

# Register the group if it doesn't already exist
if ! getent group "$AGENT_GID" > /dev/null 2>&1; then
    groupadd --gid "$AGENT_GID" agent
fi

# Register the user if it doesn't already exist
if ! getent passwd "$AGENT_UID" > /dev/null 2>&1; then
    useradd --uid "$AGENT_UID" --gid "$AGENT_GID" \
        --home /agent/home --no-create-home \
        --shell /bin/bash \
        agent
fi

# 1. Source global credential files
for f in /run/construct/creds/global/*.env; do
  [ -f "$f" ] && set -a && source "$f" && set +a
done 2>/dev/null || true

# 2. Source per-folder credential files (override global)
for f in /run/construct/creds/folder/*.env; do
  [ -f "$f" ] && set -a && source "$f" && set +a
done 2>/dev/null || true

# 3. Ensure agent layer directories exist (owned by the running user)
mkdir -p /agent/bin /agent/lib /agent/cache /agent/home/.config/opencode
chown -R "$AGENT_UID:$AGENT_GID" /agent/home /agent/bin /agent/lib /agent/cache 2>/dev/null || true

# 4. Write opencode.json into the agent layer's opencode config dir.
#    - instructions: load construct context from the bind-mounted agents.md
#      directly; no cp needed, and it combines with the user's AGENTS.md.
#    - autoupdate: false — never auto-update inside the container.
#    This file lives in XDG_CONFIG_HOME (/agent/home/.config/opencode/) so it
#    is the "global" opencode config inside the container. It will be merged
#    with the user's host opencode.json (loaded via OPENCODE_CONFIG_DIR).
cat > /agent/home/.config/opencode/opencode.json <<'EOF'
{
  "$schema": "https://opencode.ai/config.json",
  "autoupdate": false,
  "instructions": ["/run/construct/agents.md"]
}
EOF
chown "$AGENT_UID:$AGENT_GID" /agent/home/.config/opencode/opencode.json

# 5. Drop to the host user and sleep forever — the agent is launched separately
#    via docker exec (with --user uid:gid). gosu handles the privilege drop
#    cleanly and does not require the user to have a password.
exec gosu "$AGENT_UID:$AGENT_GID" sleep infinity
