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

The trust boundary diagram below shows the **`--docker dind`** mode (the most
expansive configuration). In `--docker none` (no inner daemon) and `--docker dood`
(Docker-outside-of-Docker) the layout differs — see the notes beneath the diagram.

```
┌─ Host ──────────────────────────────────────────────────────────────┐
│  ~/.construct/.env (credentials)                                    │
│  ~/projects/myrepo/  (workspace)                                    │
│  /var/run/docker.sock (outer Docker daemon)                         │
│                                                                     │
│  ┌─ Isolated bridge network (dind mode only) ────────────────────┐  │
│  │                                                               │  │
│  │  ┌─ agent container ─────────────────┐  ┌─ dind sidecar ──┐   │  │
│  │  │  opencode                         │  │  inner dockerd  │   │  │
│  │  │  /workspace  (bind-mount, rw)     │◄─►  port 2375      │   │  │
│  │  │  /run/secrets (bind-mount, ro)    │  │  no TLS         │   │  │
│  │  │  /home/agent  (named volume, rw)  │  └─────────────────┘   │  │
│  │  └───────────────────────────────────┘                        │  │
│  └───────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

**`--docker none`** — no dind sidecar is started, no bridge network is created,
and no `DOCKER_HOST` is set. The agent has no access to any Docker daemon.

**`--docker dood`** — no dind sidecar. Instead, `/var/run/docker.sock` is
bind-mounted into the agent container and `DOCKER_HOST` is set to
`unix:///var/run/docker.sock`. The agent talks directly to the **host** Docker
daemon (see T9 below).

The agent container has access to:

- The **workspace repo** (read/write bind-mount) — intentional; this is why you
  run the agent.
- **Credentials** for the AI tool via `/run/secrets` — intentional; the tool
  needs them to call the LLM API.
- A **Docker daemon** — present only in `--docker dind` (inner daemon) and
  `--docker dood` (host daemon). Not present in `--docker none`.
- A **persistent home volume** — intentional; preserves shell history, tool
  caches, and config across sessions.

The agent does **not** have access to:

- The host's outer Docker daemon or any existing host containers/images — except
  in `--docker dood`, where this is explicitly opted into.
- Any part of the host filesystem outside the workspace repo.
- Other network interfaces on the host (isolated bridge network only, in `dind`
  mode; no dedicated network in `dood` / `none` modes).

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
| **Mitigation** | No `--privileged` flag on the agent container. In `--docker dind` mode, only the dind sidecar runs privileged. In `--docker none` and `--docker dood` modes, no privileged container is started at all. Containers run as the host user (not root inside the container). |
| **Residual risk** | Container escapes are a known class of vulnerability. construct does not claim to be a hardened sandbox. A sufficiently motivated or compromised model could attempt this. |
| **vs. host baseline** | On the host, no escape is needed — the agent already is on the host. construct raises the bar significantly. `--docker none` offers the smallest privileged-container footprint since no sidecar is involved. |

---

### T3 — Agent hijacks the inner Docker daemon

| | |
|---|---|
| **Scenario** | The agent uses a Docker daemon to do something harmful — mounting host paths, running containers with `--privileged`, etc. |
| **Mitigation** | In `--docker dind` mode, the inner daemon is completely isolated. It cannot reach the host daemon or any host resources. In `--docker none`, no daemon is available to the agent at all. |
| **Residual risk** | In `--docker dind` mode, the agent can do arbitrary things **within the inner daemon**: spin up crypto miners, exfiltrate data via the network, etc. The inner daemon has outbound internet access (the host's network stack is reachable via the bridge). In `--docker dood` mode the agent talks directly to the host daemon — see T9. |
| **vs. host baseline** | On the host the agent could hijack the real Docker daemon with all existing volumes and networks. `--docker dind` limits this to the isolated inner daemon; `--docker none` removes daemon access entirely. |

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
| **Mitigation** | In `--docker dind` (default) and `--docker none` modes, the Docker socket is not mounted into the agent container. `DOCKER_HOST` points to the inner dind daemon only (`dind` mode) or is unset (`none` mode). |
| **Residual risk** | In `--docker dood` mode the outer Docker socket **is** explicitly mounted into the agent container. The agent can use it to inspect or modify any container, image, volume, or network on the host daemon — including mounting host paths, running privileged containers, etc. This mode should only be used when the agent genuinely needs access to the host daemon and the risk is understood and accepted. |
| **vs. host baseline** | On the host, the agent inherits the user's Docker socket access. `--docker dind` and `--docker none` eliminate this; `--docker dood` is equivalent to the host baseline for Docker access. |

---

### T10 — Agent pushes to git remotes using injected credentials

| | |
|---|---|
| **Scenario** | The agent uses an injected credential (e.g. `ANTHROPIC_API_KEY`) to make API calls — or discovers credentials in its environment and uses them for unintended operations such as a `git push`. |
| **Mitigation** | No git credential helper is wired up by construct. The agent would have to discover the token from its environment and construct the remote URL manually (e.g. `https://x-access-token:$TOKEN@github.com/...`). This is non-trivial but not difficult for a capable model. SSH keys are **not** mounted into the container, so SSH-based remotes are unreachable. |
| **Residual risk** | No token with git push scope is injected by default. If a user supplies a GitHub token via `config set`, the agent could use it for HTTPS git operations. |
| **Recommendation** | Use a token scoped to the minimum required permissions. A GitHub fine-grained PAT with read-only or repo-specific access limits blast radius. Avoid using tokens that have push access to repositories beyond the one you are actively working on. Note: recommending SSH remotes is **not** a useful mitigation here — SSH keys are not mounted, so the attack surface is HTTPS+token only. |
| **Attribution note** | construct injects the host user's real git identity (`user.name` / `user.email`) as `GIT_AUTHOR_*` and `GIT_COMMITTER_*`. Any commits the agent makes — including any it pushes — will carry the developer's real name and email. Users should be aware that agent-authored commits are attributable to them in git history. |
| **vs. host baseline** | On the host, the agent has access to the user's full git credential store (`~/.gitconfig`, credential helper, SSH keys, `~/.netrc`), giving push access to every repository the user can reach. construct limits this to explicitly injected tokens only. |

---

### T11 — DinD inner daemon without TLS

| | |
|---|---|
| **Scenario** | Another container on the same bridge network connects to the inner Docker daemon (port 2375) and performs unauthorized Docker operations. |
| **Mitigation** | This threat only applies in `--docker dind` mode. The bridge network is created fresh per session and contains only the agent container and the dind sidecar. No other containers join it. Both are removed at session end. In `--docker none` and `--docker dood` modes no inner daemon is started, so this threat does not apply. |
| **Residual risk** | In `--docker dind` mode, the inner daemon has no TLS and no authentication. Any container on the bridge could connect to it. In practice, no other containers join the session bridge. |
| **vs. host baseline** | Not applicable. |

---

### T12 — opencode HTTP server port exposure

| | |
|---|---|
| **Scenario** | Another process on the host (or a local network attacker if loopback filtering fails) connects to the opencode HTTP server port (`127.0.0.1:<serve-port>`) and issues API calls — reading or writing session state, injecting prompts, or accessing work in progress. |
| **Mitigation** | The opencode server is bound to `0.0.0.0` inside the container (necessary so the host can reach it through the published port), but the port is published **loopback-only**: `127.0.0.1:<serve-port>:<serve-port>`. It is not reachable from the LAN. No password is required by default (opencode does not currently enforce auth on its HTTP API). |
| **Residual risk** | Any process running as the same Unix user on the host can connect to `127.0.0.1:<serve-port>` during the session. An attacker with local code execution (e.g. a malicious npm package in a different terminal) could read or manipulate the active session. The port is open for the full duration of the session. |
| **Recommendation** | Use `--serve-port` to pick a non-default port if you are concerned about predictable port targeting. A `--serve-password` flag for HTTP bearer auth is a future enhancement. |
| **vs. host baseline** | Running opencode directly on the host exposes the same HTTP port. construct does not worsen this — the port is loopback-only in both cases. |

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

| Risk | On host (yolo) | `--docker none` | `--docker dind` | `--docker dood` |
|---|---|---|---|---|
| Agent reads arbitrary host files | Full access | Repo only | Repo only | Repo only |
| Agent reads credentials/keys on host | All of `~` | Explicitly injected keys only | Explicitly injected keys only | Explicitly injected keys only |
| Agent hijacks host Docker daemon | Yes (if user has socket) | No | No | **Yes** (socket is mounted) |
| Agent persists malicious config | Real `~/.bashrc` etc. | Isolated named volume | Isolated named volume | Isolated named volume |
| Agent accesses local services (localhost ports) | Yes | No | No | No |
| Agent exfiltrates data over network | Yes | Yes | Yes | Yes |
| Agent pushes to git remotes | Full credential store (SSH + HTTPS) | Injected token only (HTTPS); no SSH | Injected token only (HTTPS); no SSH | Injected token only (HTTPS); no SSH |
| Agent escapes to host | Already there | Possible but requires exploit; no privileged sidecar | Possible but requires exploit | Possible but requires exploit |
| Agent modifies repo files | Yes | Yes | Yes | Yes |
| Privileged container on host | N/A | None | dind sidecar only | None |
| opencode HTTP server reachable by local processes | Yes (direct) | Yes (`127.0.0.1:<serve-port>`) | Yes (`127.0.0.1:<serve-port>`) | Yes (`127.0.0.1:<serve-port>`) |

The last two rows of the original table — agent escapes and repo modification — remain the honest limits of what construct provides. Everything else is a meaningful improvement over the baseline.

---

## Further reading

- [ADR 001 — Docker secrets for credential injection](adr/001-docker-secrets-for-credentials.md)
- [Security — credential flow detail](security.md)
- [Spec — serve mode and local client selection](spec/serve-mode.md)
