# Spec: Ruby stack

## Problem

Projects using Ruby — in particular [Jekyll](https://jekyllrb.com/) static sites — need a stack image with Ruby, Bundler, and Jekyll pre-installed so the agent can build, serve, and modify the site without manual gem installation at runtime.

## Solution

Add a `ruby` stack built on top of `construct-base` that installs Ruby via the Ubuntu system package (`ruby-full`), then installs Bundler and Jekyll as system gems.

## Behaviour

```
construct --tool <tool> --stack ruby [path]
```

Produces a `construct-ruby` Docker image that extends `construct-base` with:

- `ruby` / `gem` / `irb` available on `PATH`
- `bundle` (Bundler) available on `PATH`
- `jekyll` available on `PATH`

Typical Jekyll workflow inside the container:

```bash
# Build the site
bundle exec jekyll build

# Serve with live-reload (pair with --port 4000 on the host)
bundle exec jekyll serve --host 0.0.0.0
```

## Persistence

No additional persistence beyond the standard per-repo home volume. Bundler's cache (`~/.bundle`) lives inside the agent home volume and survives container restarts. `--reset` wipes it along with the rest of the home volume.

## Implementation

| File | Change |
|------|--------|
| `internal/stacks/dockerfiles/ruby/Dockerfile` | New — installs `ruby-full`, `ruby-dev`, `build-essential`, `zlib1g-dev` via apt; installs `bundler` and `jekyll` gems |
| `internal/stacks/stacks.go` | Added `"ruby"` to `validStacks` |
| `internal/stacks/stacks_test.go` | Tests: `TestIsValid_RubyStack`, `TestEmbeddedDockerfiles_RubyExists`, `TestEmbeddedDockerfiles_RubyContent`, `TestAll_ContainsRuby` |
| `README.md` | `ruby` row in Supported stacks table; `--stack` flag description updated |
| `CHANGELOG.md` | Entry under `[Unreleased]` |

## Non-goals

- No Ruby version manager (e.g. `rbenv`, `rvm`). A single system-installed Ruby version per image.
- No pre-installed project-specific gems beyond Bundler and Jekyll. Run `bundle install` inside the container for project dependencies.
