# Security

> **Disclaimer:** This document was largely generated with the assistance of an
> LLM, using best-effort analysis and established security practices. AI-generated
> security documentation can contain errors or omissions — as can human-written
> documentation. Treat this as a starting point for your own assessment, not a
> definitive audit. Corrections and pull requests are welcome.

This document describes the security model for `construct`, focusing on how
credentials are handled and what guarantees (and non-guarantees) the tool
provides.

## Credential flow

```
~/.construct/.env  ─┐
.construct/.env    ─┴─► in-memory map ──► per-key temp file (0600) ──► /run/secrets/<KEY>
                                                                              │
                                                                   entrypoint wrapper
                                                                              │
                                                                        env var inside container
```

1. **At rest** — credentials are stored as plain text in `~/.construct/.env`
   (global) and/or `.construct/.env` (per-repo). Both files are created with
   `0600` permissions (owner read/write only). No encryption is applied.

2. **In transit to the container** — at run time, each `AuthEnvVar` credential
   is written to its own `0600` temp file inside a `construct-secrets-*` temp
   directory. The entire directory is bind-mounted read-only at `/run/secrets`
   inside the container. The temp directory is removed with `os.RemoveAll` once
   the agent container exits.

3. **Inside the container** — credentials are mounted as read-only files at
   `/run/secrets/<KEY>` via a bind mount (`-v secretsDir:/run/secrets:ro`).
   A generic entrypoint wrapper (`/usr/local/bin/construct-entrypoint`) reads
   each file and exports it as an environment variable before `exec`-ing the
   tool command.

## What is protected

| Threat | Status |
|---|---|
| Credential values visible in `docker inspect <container>` | ✅ Protected — secrets are not env vars at the Docker layer |
| Credential values in `docker run` process args (`ps aux`) | ✅ Protected — `--secret src=file` is used instead of `-e KEY=val` |

## What is NOT protected

| Threat | Status |
|---|---|
| Credentials at rest on the host | ❌ Plain text in `.env` files; protected only by filesystem permissions |
| Credentials visible inside the container via `/proc/1/environ` | ❌ The entrypoint exports them as env vars; any process inside the container can read them |
| Host user with Docker socket access | ❌ Anyone who can run `docker` on the host can inspect volumes and networks |
| DinD daemon TLS | ⚠️  Only applies in `--docker dind` mode. The inner Docker daemon runs without TLS on port 2375; accessible to anything on the session bridge network. Not present in `--docker none` or `--docker dood` modes |
| Outer Docker socket (`--docker dood`) | ❌ In `--docker dood` mode, `/var/run/docker.sock` is mounted into the agent container, giving the agent full control over the host Docker daemon |
| Temp secret files during the session | ⚠️  Written as `0600` and removed on exit; a root process or another process running as the same user on the host could read them during the session |

## Recommendations for users

- Restrict access to the Docker socket (`/var/run/docker.sock`) to trusted users only.
- Set `0600` on `~/.construct/.env` (done automatically by `construct config set`).
- Do not commit `.construct/.env` to version control. Add it to `.gitignore`.
- Rotate credentials if the host is shared or considered compromised.
- Prefer short-lived credentials (e.g. temporary tokens) where the tool supports them.

## Further reading

- [Threat model](threat-model.md) — full catalogue of threats, mitigations, residual risks, and trade-offs
- [ADR 001 — Docker secrets for credentials](adr/001-docker-secrets-for-credentials.md)
