# Windows Support

## Problem
`construct` was previously limited to Linux Docker hosts. Users on Windows (via Docker Desktop) were not officially supported and the documentation explicitly stated it would not work.

## Solution
This change officially enables and documents Windows support. Windows-specific path handling and permission checks have been integrated and verified via tests.

- Updated `README.md` to remove the Linux-only restriction and add Windows installation instructions.
- Updated `.github/workflows/release.yml` to include a `windows/amd64` build in releases.
- Updated `.github/workflows/ci.yml` to verify that the project still builds for Windows on every push.

## Persistence
No changes to persistence.

## Files changed
| File | Change |
|------|--------|
| `README.md` | Removed Linux-only restriction; added Windows install instructions. |
| `.github/workflows/release.yml` | Added Windows build step. |
| `.github/workflows/ci.yml` | Added Windows build check. |
