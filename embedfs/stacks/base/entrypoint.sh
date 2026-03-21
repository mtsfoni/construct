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

# 3. Ensure agent layer directories exist
mkdir -p /agent/bin /agent/lib /agent/cache /agent/home/.config

# 4. Sleep forever — the agent is launched separately via docker exec
exec sleep infinity
