# Spec: `config` command

## Problem

Users need to supply API keys and tokens to the AI tools that construct runs.
The naive approach — editing `~/.construct/.env` by hand — is error-prone
(quoting, permissions, typos) and undiscoverable. There is also no built-in way
to override a credential for a single repository without touching the global
file.

## Solution

Introduce a `config` subcommand that reads and writes `.env` credential files
safely, with scoped targeting (global vs. per-repo).

## Behaviour

```
construct config <set|unset|list> [--local] [KEY [VALUE]]
```

### Subcommands

| Subcommand | Arguments | Description |
|---|---|---|
| `set` | `KEY VALUE` | Write or update a credential. Creates the file and parent directory if absent. |
| `unset` | `KEY` | Remove a credential. No-ops silently if the key or file does not exist. |
| `list` | — | Print all configured keys. Values are masked (`****`). |

### `--local` flag

By default all commands operate on the **global** file (`~/.construct/.env`).
Pass `--local` to operate on the **per-repo** file (`.construct/.env` in the
current working directory). The flag must appear after the subcommand name:

```bash
construct config set --local ANTHROPIC_API_KEY sk-ant-...
```

### Precedence

At agent launch time, the global file is loaded first; the per-repo file is
merged on top, so per-repo values override global ones for that invocation.

## Persistence

| Scope | Path | Created with |
|---|---|---|
| Global | `~/.construct/.env` | dir `0700`, file `0600` |
| Per-repo | `<repoDir>/.construct/.env` | dir `0700`, file `0600` |

File format is `KEY=VALUE`, one per line. Blank lines and `#` comments are
preserved on `set`/`unset`. Values are written without quotes; surrounding
single or double quotes are stripped when reading.

Writes are atomic: content is written to `<path>.tmp` then renamed into place,
preventing partial writes.

## Examples

```bash
# Set credentials globally
construct config set ANTHROPIC_API_KEY sk-ant-xyz

# Override for a single repo
cd ~/projects/myapp
construct config set --local ANTHROPIC_API_KEY sk-ant-different

# Review what is configured globally
construct config list
# Keys configured in /home/alice/.construct/.env:
#   ANTHROPIC_API_KEY=****

# Remove a key
construct config unset ANTHROPIC_API_KEY
```

## Implementation

| File | Role |
|---|---|
| `internal/config/config.go` | `Set`, `Unset`, `List` — read/write helpers for `.env` files |
| `cmd/construct/main.go` | `runConfig` — routes `set`/`unset`/`list`, resolves target file via `targetEnvFile` |
| `internal/runner/runner.go` | `loadEnv` — merges global then per-repo file at launch time; `writeSecretFiles` — writes each `AuthEnvVar` value to a `0600` temp file for injection |

## Non-goals

- No encryption at rest. Files are plain text, protected by filesystem permissions only.
- No `config get KEY` command that prints a value in plaintext — intentional, to avoid leaking credentials in terminal history or CI logs.
- No interactive prompt mode.
- No support for env var expansion inside values.
