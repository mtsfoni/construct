#!/usr/bin/env sh
set -e

INSTALL_DIR="$HOME/.local/bin"
BINARY="construct"

echo "Building $BINARY..."
go build -o "$BINARY" ./cmd/construct

mkdir -p "$INSTALL_DIR"
mv "$BINARY" "$INSTALL_DIR/$BINARY"

echo "Installed to $INSTALL_DIR/$BINARY"
