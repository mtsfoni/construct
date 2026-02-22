# ADR 001 — Docker secrets for credential injection

**Status:** Accepted

## Context

`construct` passes AI tool credentials (API keys, tokens) from the host's
`.env` files into agent containers. The original implementation used
`docker run -e KEY=val` flags for each auth credential.

This approach has two visibility problems:

1. **`docker inspect <container>`** exposes all env vars, including credential
   values, to anyone with Docker socket access on the host.
2. **The `docker run` command-line** briefly appears in the host process list
   (e.g. `ps aux`), making credential values visible to other local users and
   to audit logs.

## Decision

Replace `-e KEY=val` injection with Docker secrets:

- At run time, each credential is written to a `0600` temp file inside a
  `construct-secrets-*` temp directory.
- The entire directory is bind-mounted read-only at `/run/secrets` inside the
  container (`-v secretsDir:/run/secrets:ro`). Values do **not** appear in
  `docker inspect` output (only the host path is recorded, not the file
  contents).
- A generic entrypoint script (`construct-entrypoint.sh`) is baked into every
  tool image at build time. It reads each file under `/run/secrets/` and
  exports it as an environment variable before `exec`-ing the tool command.
  This keeps tool compatibility unchanged — tools still see normal env vars.
- The temp directory holding secret files is removed (`os.RemoveAll`) once the
  agent container exits.

## Consequences

### Positive

- Credential values are no longer visible in `docker inspect <container>`.
- Credential values are no longer visible in the `docker run` process args.
- No changes are required to any tool definition or `.env` file format.

### Negative / trade-offs

- **Credentials still become env vars inside the container.** The entrypoint
  wrapper exports them so the tool can use them. A compromised process inside
  the container can still read `/proc/1/environ`.
- **Temp files on the host.** Credentials live as `0600` files for the
  duration of the session before being cleaned up. This is not materially
  different from the existing `.env` files already on disk.
- **Tool images must be rebuilt** (`construct --rebuild`) after this change to
  pick up the new `ENTRYPOINT` layer.

## Alternatives considered

| Alternative | Reason rejected |
|---|---|
| `--env-file` flag | Values still appear in `docker inspect` (same as `-e`) |
| Docker Swarm secrets | Requires Swarm mode; far heavier than needed for a local dev tool |
| `docker run --secret` flag | Not universally available across Docker builds; bind mount (`-v`) achieves the same `docker inspect` protection without any version constraint |
| Encryption at rest for `.env` | Out of scope for this change; would not address the `docker inspect` problem |
