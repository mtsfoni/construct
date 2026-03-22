#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="$HOME/.local/bin"
VERSION="dev"
LDFLAGS="-X github.com/construct-run/construct/internal/version.Version=${VERSION} -X github.com/construct-run/construct/internal/version.SourceDir=${REPO_DIR} -s -w"

cd "$REPO_DIR"

echo "Building construct..."
go build \
  -ldflags "${LDFLAGS}" \
  -o "${INSTALL_DIR}/construct" \
  ./cmd/construct/

echo "Building constructd..."
go build \
  -ldflags "${LDFLAGS}" \
  -o "${INSTALL_DIR}/constructd" \
  ./cmd/constructd/

echo "Installed construct and constructd to ${INSTALL_DIR}"

# Restart the daemon so it picks up the new binary embedded in the construct image.
echo "Stopping construct-daemon container (if running)..."
docker rm -f construct-daemon 2>/dev/null || true

echo "Done. Run 'construct run' to start a session (daemon will rebuild automatically)."

