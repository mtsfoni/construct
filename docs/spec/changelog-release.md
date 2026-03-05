# Spec: Changelog-driven release notes

## Problem

The release pipeline previously used GitHub's `generate_release_notes: true`
to auto-generate release body text from merged PR titles. This ignored the
manually curated `CHANGELOG.md` entries that describe changes in human-readable
prose.

## Solution

When a `v*` tag is pushed, the release pipeline:

1. Extracts the `## [Unreleased]` block from `CHANGELOG.md` and uses it
   verbatim as the GitHub release body.
2. Rewrites `CHANGELOG.md` in-place:
   - Renames `## [Unreleased]` → `## [<tag>] — <YYYY-MM-DD>` (UTC date).
   - Inserts a fresh empty `## [Unreleased]` block above the new versioned
     entry.
3. Commits the updated `CHANGELOG.md` back to `main` so the history stays
   tidy.

## Behaviour

### Extraction rules

- Everything between `## [Unreleased]` and the next `## [` heading is treated
  as the release body.
- Trailing blank lines and `---` horizontal-rule separators (which are
  changelog section dividers, not part of the notes) are stripped from the
  body before it is used.
- If the `[Unreleased]` section is empty (contains only whitespace and `---`),
  the script exits with code 1 and a descriptive error, failing the pipeline
  before any files are modified.

### CHANGELOG.md rewrite

Before (tag `v0.4.1` pushed):

```markdown
## [Unreleased]

### Changed

- Some change.

---

## [v0.4.0] — 2026-03-03
```

After:

```markdown
## [Unreleased]

---

## [v0.4.1] — 2026-03-05

### Changed

- Some change.

---

## [v0.4.0] — 2026-03-03
```

### Commit back to main

The changelog commit is authored by `github-actions[bot]` with the message:

```
chore: update changelog for <tag>
```

It is pushed directly to `main`. Branch protection rules that require PRs for
`main` will block this push; if the repo uses such rules, add a bypass for
`github-actions[bot]` or push to a release branch instead.

## Persistence details

No new files are written to the workspace or the repo beyond the updated
`CHANGELOG.md`. The release body is passed via a temp file on the runner
(`/tmp/release-body.md`).

## Files changed

| File | Change |
|---|---|
| `.github/extract-changelog.sh` | New script: extracts unreleased notes, rewrites changelog |
| `.github/workflows/release.yml` | Adds changelog extraction + commit steps; replaces `generate_release_notes: true` with `body_path` |
| `docs/spec/changelog-release.md` | This document |
| `CHANGELOG.md` | Entry under `## [Unreleased]` |
