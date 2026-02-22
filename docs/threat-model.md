# Threat Model

> **Disclaimer:** This document was largely generated with the assistance of an
> LLM, using best-effort analysis and established threat-modelling practices.
> AI-generated security analysis can contain errors or omissions — as can
> human-written analysis. Treat this as a starting point for your own
> assessment, not a definitive audit. Corrections and pull requests are welcome.

This document describes what `construct` protects against, what it does not, and
the deliberate trade-offs made in its design. The goal is transparency, not
perfection. There is rarely a clear right or wrong — but every user deserves to
understand what risks they are accepting.

The baseline comparison throughout this document is **running an AI coding agent
directly on your host in yolo/auto-approve mode**. `construct` is not a
hardened sandbox; it is a meaningful step up from that baseline.

---

## Trust boundaries

```
┌─ Host ──────────────────────────────────────────────────────────────┐
│  ~/.construct/.env (credentials)                                    │
│  ~/projects/myrepo/  (workspace)                                    │
│  /var/run/docker.sock (outer Docker daemon)                         │
│                                                                     │
│  ┌─ Isolated bridge network ─────────────────────────────────────┐  │
│  │                                                               │  │
│  │  ┌─ agent container ─────────────────┐  ┌─ dind sidecar ──┐   │  │
│  │  │  AI tool (copilot / opencode)     │  │  inner dockerd  │   │  │
│  │  │  /workspace  (bind-mount, rw)     │◄─►  port 2375      │   │  │
│  │  │  /run/secrets (bind-mount, ro)    │  │  no TLS         │   │  │
│  │  │  /home/agent  (named volume, rw)  │  └─────────────────┘   │  │
│  │  └───────────────────────────────────┘                        │  │
│  └───────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

The agent container has access to:

- The **workspace repo** (read/write bind-mount) — intentional; this is why you
  run the agent.
- **Credentials** for the AI tool via `/run/secrets` — intentional; the tool
  needs them to call the LLM API.
- The **inner Docker daemon** (dind) — intentional; the agent needs Docker to
  build and run containers as part of its work.
- A **persistent home volume** — intentional; preserves shell history, tool
  caches, and config across sessions.

The agent does **not** have access to:

- The host's outer Docker daemon or any existing host containers/images.
- Any part of the host filesystem outside the workspace repo.
- Other network interfaces on the host (isolated bridge network only).

---

## Threat catalogue

### T1 — Agent modifies files outside the workspace

| | |
|---|---|
| **Scenario** | The agent deletes or exfiltrates files from `~/Documents`, SSH keys, etc. |
| **Mitigation** | Only the workspace repo directory is bind-mounted. No other host paths are accessible. |
| **Residual risk** | None for files outside the mount. Files inside the repo are fully accessible and writable. |
| **vs. host baseline** | Running on the host gives the agent access to the entire home directory and all mounted drives. construct eliminates this. |

---

### T2 — Agent escapes the container

| | |
|---|---|
| **Scenario** | A vulnerability in the container runtime, kernel, or tool allows the agent to execute code on the host. |
| **Mitigation** | No `--privileged` flag on the agent container; only the dind sidecar runs privileged. Containers run as the host user (not root inside the container). |
| **Residual risk** | Container escapes are a known class of vulnerability. construct does not claim to be a hardened sandbox. A sufficiently motivated or compromised model could attempt this. |
| **vs. host baseline** | On the host, no escape is needed — the agent already is on the host. construct raises the bar significantly. |

---

### T3 — Agent hijacks the inner Docker daemon

| | |
|---|---|
| **Scenario** | The agent uses the inner dind Docker daemon to do something harmful — mounting host paths, running containers with `--privileged`, etc. |
| **Mitigation** | The inner daemon is completely isolated. It cannot reach the host daemon or any host resources. |
| **Residual risk** | The agent can do arbitrary things **within the inner daemon**: spin up crypto miners, exfiltrate data via the network, etc. The inner daemon has outbound internet access (the host's network stack is reachable via the bridge). |
| **vs. host baseline** | On the host the agent could hijack the real Docker daemon with all existing volumes and networks. construct limits this to the isolated inner daemon. |

---

### T4 — Credential theft from the container

| | |
|---|---|
| **Scenario** | The agent reads LLM API keys or tokens from inside the container and exfiltrates them. |
| **Mitigation** | Credentials are delivered via files in `/run/secrets` (not env vars at the Docker layer), so they don't appear in `docker inspect`. See [ADR 001](adr/001-docker-secrets-for-credentials.md). |
| **Residual risk** | The entrypoint script exports secrets as environment variables before starting the tool. Any process inside the container can read them from `/proc/1/environ`. An agent that chooses to exfiltrate credentials over the network can do so. |
| **vs. host baseline** | On the host, the agent can read `.env` files, shell history, git credentials, SSH keys, and every other credential on the machine. construct limits exposure to the credentials explicitly passed to the tool. |

---

### T5 — Credential theft from the host during a session

| | |
|---|---|
| **Scenario** | Another process on the host reads the temp secret files while a session is running. |
| **Mitigation** | Temp files are created with `0600` permissions. The temp directory is removed with `os.RemoveAll` on session exit (clean or SIGINT/SIGTERM). |
| **Residual risk** | A root process, or another process running as the same Unix user, can read the files during the session window. |
| **vs. host baseline** | No material difference from the `.env` files already on disk. |

---

### T6 — Agent network access

| | |
|---|---|
| **Scenario** | The agent exfiltrates code or data to an external server, phones home, or attacks third-party services. |
| **Mitigation** | None. The agent container has unrestricted outbound internet access. This is necessary for the agent to call the LLM API and install packages. |
| **Residual risk** | Full outbound internet access. Inbound connections from the host are blocked (isolated bridge), but the agent can reach any public IP. |
| **vs. host baseline** | No difference in outbound access. On the host the agent additionally has access to local services bound to localhost (databases, dev servers, etc.) — construct blocks that. |

---

### T7 — Workspace repo contamination

| | |
|---|---|
| **Scenario** | The agent writes malicious code, backdoors, or secrets into the repo. |
| **Mitigation** | None — this is intentional write access. The agent is supposed to edit the repo. Use version control (`git diff`, `git stash`, `git reset`) to review and revert changes. |
| **Residual risk** | The agent can write anything to the repo, including `.github/workflows/`, Dockerfiles, dependency manifests, etc. |
| **vs. host baseline** | No difference. |

---

### T8 — Home volume persistence across sessions

| | |
|---|---|
| **Scenario** | A previous session plants a malicious file in `/home/agent` (shell init scripts, tool configs, etc.) that activates in a future session. |
| **Mitigation** | The home volume is keyed by a SHA256 of the repo path + tool name. It is not shared across different repos or tools. `--rebuild` does not reset the volume (by design — to preserve history); use `docker volume rm` to reset it manually. |
| **Residual risk** | If the agent modifies `/home/agent/.bashrc` or a tool's config file during a session, that change persists in future sessions for the same repo+tool pair. |
| **vs. host baseline** | On the host, the agent can modify the real home directory of the invoking user, affecting all future terminal sessions. construct isolates this to the named volume. |

---

### T9 — Outer Docker socket access

| | |
|---|---|
| **Scenario** | The agent container gets access to `/var/run/docker.sock` and takes over the host Docker daemon. |
| **Mitigation** | The Docker socket is not mounted into the agent container. The `DOCKER_HOST` env var points to the inner dind daemon only. |
| **Residual risk** | None from inside the container. On the host, anyone with Docker socket access can already do this — that is a host-level concern. |
| **vs. host baseline** | On the host, the agent inherits the user's Docker socket access. construct eliminates this. |

---

### T10 — Agent pushes to git remotes using injected credentials

| | |
|---|---|
| **Scenario** | The agent uses an injected credential (e.g. `GH_TOKEN` for the `copilot` tool) to authenticate a `git push` to a remote repository — without the user's knowledge or intent. |
| **Mitigation** | No git credential helper is wired up by construct. The agent would have to discover the token from its environment and construct the remote URL manually (e.g. `https://x-access-token:$GH_TOKEN@github.com/...`). This is non-trivial but not difficult for a capable model. SSH keys are **not** mounted into the container, so SSH-based remotes are unreachable. |
| **Residual risk** | For the `copilot` tool: `GH_TOKEN` is present as an env var and could be used for HTTPS git operations against any GitHub repo the token has push access to. For other tools (`opencode`): no token with git push scope is injected by default. |
| **Recommendation** | Use a token scoped to the minimum required permissions. A GitHub fine-grained PAT with read-only or repo-specific access limits blast radius. Avoid using tokens that have push access to repositories beyond the one you are actively working on. Note: recommending SSH remotes is **not** a useful mitigation here — SSH keys are not mounted, so the attack surface is HTTPS+token only. |
| **vs. host baseline** | On the host, the agent has access to the user's full git credential store (`~/.gitconfig`, credential helper, SSH keys, `~/.netrc`), giving push access to every repository the user can reach. construct limits this to explicitly injected tokens only. |

---

### T11 — DinD inner daemon without TLS

| | |
|---|---|
| **Scenario** | Another container on the same bridge network connects to the inner Docker daemon (port 2375) and performs unauthorized Docker operations. |
| **Mitigation** | The bridge network is created fresh per session and contains only the agent container and the dind sidecar. No other containers join it. Both are removed at session end. |
| **Residual risk** | The inner daemon has no TLS and no authentication. Any container on the bridge could connect to it. In practice, no other containers join the session bridge. |
| **vs. host baseline** | Not applicable. |

---

## What this tool is not

- **Not a CVE-proof container escape preventer.** Kernel vulnerabilities exist.
  construct reduces attack surface; it does not eliminate it.
- **Not a DLP (data loss prevention) tool.** Outbound network is not filtered.
- **Not a secrets vault.** Credentials are stored as plain text in `.env` files,
  protected only by filesystem permissions.
- **Not a compliance boundary.** Do not rely on this tool alone to meet SOC 2,
  ISO 27001, or similar requirements.

---

## Summary: construct vs. running on the host

| Risk | On host (yolo) | In construct |
|---|---|---|
| Agent reads arbitrary host files | Full access | Repo only |
| Agent reads credentials/keys on host | All of `~` | Explicitly injected keys only |
| Agent hijacks host Docker daemon | Yes (if user has socket) | No |
| Agent persists malicious config | Real `~/.bashrc` etc. | Isolated named volume |
| Agent accesses local services (localhost ports) | Yes | No |
| Agent exfiltrates data over network | Yes | Yes |
| Agent pushes to git remotes | Full credential store (SSH + HTTPS) | Injected token only (HTTPS); no SSH |
| Agent escapes to host | Already there | Possible but requires exploit |
| Agent modifies repo files | Yes | Yes |

The last two rows are the honest limits of what construct provides. Everything
else is a meaningful improvement over the baseline.

---

## Further reading

- [ADR 001 — Docker secrets for credential injection](adr/001-docker-secrets-for-credentials.md)
- [Security — credential flow detail](security.md)
