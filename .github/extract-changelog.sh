#!/usr/bin/env bash
# extract-changelog.sh <version> [changelog-file]
#
# Extracts the [Unreleased] block from CHANGELOG.md, prints its content to
# stdout (for use as a GitHub release body), and rewrites the changelog:
#   - renames ## [Unreleased] → ## [<version>] — <today>
#   - inserts a fresh empty ## [Unreleased] block above it
#
# Usage:
#   body=$(bash .github/extract-changelog.sh v1.2.3)
#   body=$(bash .github/extract-changelog.sh v1.2.3 path/to/CHANGELOG.md)
#
# Exit codes:
#   0  success
#   1  bad usage or no [Unreleased] section found

set -euo pipefail

VERSION="${1:-}"
CHANGELOG="${2:-CHANGELOG.md}"

if [[ -z "$VERSION" ]]; then
  echo "usage: extract-changelog.sh <version> [changelog-file]" >&2
  exit 1
fi

if [[ ! -f "$CHANGELOG" ]]; then
  echo "error: $CHANGELOG not found" >&2
  exit 1
fi

TODAY=$(date -u +%Y-%m-%d)

# ---------------------------------------------------------------------------
# Extract the body of the [Unreleased] section.
# The section starts on the line after "## [Unreleased]" and ends just before
# the next "## [" heading or end-of-file.
# ---------------------------------------------------------------------------
body=$(awk '
  /^## \[Unreleased\]/ { in_section=1; next }
  in_section && /^## \[/  { exit }
  in_section             { print }
' "$CHANGELOG")

if [[ -z "$(echo "$body" | tr -d '[:space:]-')" ]]; then
  # Nothing meaningful under [Unreleased]; skip rewrite and emit nothing.
  exit 0
fi

# Trim leading/trailing blank lines and trailing horizontal rules (---) that
# are changelog section separators, not part of the release notes.
body=$(echo "$body" | \
  sed -e '/./,$!d' \
      -e 's/[[:space:]]*$//' | \
  awk '
    { lines[NR] = $0 }
    END {
      # Strip trailing blank lines and bare "---" separators.
      last = NR
      while (last > 0 && (lines[last] ~ /^[[:space:]]*$/ || lines[last] ~ /^---[[:space:]]*$/)) {
        last--
      }
      for (i = 1; i <= last; i++) print lines[i]
    }
  ')

# ---------------------------------------------------------------------------
# Rewrite the changelog in-place.
# The existing "## [Unreleased]" line (and everything in its section up to but
# not including the next "## [" heading) is replaced with:
#   1. A fresh empty ## [Unreleased] block  (just the heading + blank line)
#   2. A --- separator
#   3. The versioned heading with today's date
#   4. The original section content (minus the trailing ---)
# ---------------------------------------------------------------------------
tmp=$(mktemp)

awk -v version="$VERSION" -v today="$TODAY" '
  # State: 0 = before unreleased, 1 = inside unreleased, 2 = after unreleased
  state == 0 && /^## \[Unreleased\]/ {
    # Emit the new empty Unreleased block.
    print "## [Unreleased]"
    print ""
    print "---"
    print ""
    # Emit the versioned heading in place of ## [Unreleased].
    print "## [" version "] \xe2\x80\x94 " today
    state = 1
    next
  }
  state == 1 && /^## \[/ {
    # We have left the old unreleased section; switch to passthrough.
    state = 2
  }
  # Print every line that is not the old "## [Unreleased]" heading itself.
  state != 0 || !/^## \[Unreleased\]/ { print }
' "$CHANGELOG" > "$tmp"

mv "$tmp" "$CHANGELOG"

# ---------------------------------------------------------------------------
# Print the body to stdout — captured by the caller as the release body.
# ---------------------------------------------------------------------------
printf '%s\n' "$body"
