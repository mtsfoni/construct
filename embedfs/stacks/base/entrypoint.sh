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

# 5. Sleep forever — the agent is launched separately via docker exec
exec sleep infinity
