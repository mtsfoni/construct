#!/bin/bash
set -e

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

# 4. Copy construct-agents.md into the opencode config dir
#    The source is bind-mounted at /run/construct/agents.md (read-only) so
#    that Docker never touches /agent/home when setting up the bind mount.
if [ -f /run/construct/agents.md ]; then
  cp /run/construct/agents.md /agent/home/.config/opencode/construct-agents.md
fi

# 5. Sleep forever — the agent is launched separately via docker exec
exec sleep infinity
