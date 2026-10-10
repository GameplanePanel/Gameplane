# agent — Specification

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/agent`

## Purpose

The agent is a per-pod HTTP/HTTPS sidecar that runs inside every game pod to expose the game server's operations to the control plane and dashboard. It translates dashboard requests into game-protocol actions (RCON, file I/O, container logs, player queries) and reports liveness, resource usage, and player metrics back to the operator via Kubernetes API patches, decoupling game-specific behavior from the cluster control plane.

## Responsibilities

- **Request authentication**: Verify incoming requests via mTLS (preferred) or shared-secret bearer token.
- **Console & RCON**: Duplex WebSocket forwarding user commands to the game via RCON and echoing responses; supports Valve/Source, Telnet, WebSocket, BattlEye, Satisfactory, and Palworld protocols.
- **File I/O**: List, read, download, upload, write, mkdir, delete within the `/data` volume; reject path traversal and any symlink traversal; every operation runs through directory descriptors rooted at the data directory, so it cannot be redirected by a concurrent filesystem change.

  File uploads and downloads have no router-level total-duration timeout. The
  remaining file operations retain a 30-second timeout. Authentication, body
  size limits and path checks apply to transfers as well as short operations.
- **Logs**: Tail the game container's log file over WebSocket (text frames per line); supports streaming from start or end.
- **Players**: Query online count, names, ban lists, and moderation actions (kick, ban, unban) via RCON; capabilities advertise per-game support.
- **Quiesce**: Pause auto-saves and flush in-flight state before snapshots; run module-declared sequences over RCON and handle games that don't support it gracefully.
- **Lifecycle**: Run module-declared stop sequences over RCON before the operator scales the server to zero.
- **Actions**: Execute module-declared operator actions (templated RCON commands with user parameters) without agent code changes.
- **Status metrics**: Query live game metrics (TPS, world time, etc.) via RCON and regex extraction; module-declared in `capabilities.status.metrics[]`.
- **Mods**: List, install, and uninstall game mods from a registry; track per-volume metadata; guard downloads with strict SSRF egress validation.
- **Heartbeat**: Periodically patch the owning GameServer's status with lastHeartbeat, playersOnline, playersMax, gameVersion, and resource usage (CPU, memory, disk).

## Non-goals / boundaries

- The agent does **not** expose a PTY or attach the game container directly. Games with consoleMode "pty" (e.g., Unity servers) are handled by the Gameplane API bridging the browser WebSocket to Kubernetes pod-attach; the agent is uninvolved.
- The agent does **not** manage game processes or containers — all process/container control is the operator's job (scaling, restarts, resource limits).
- The agent does **not** validate RCON responses for correctness. It forwards raw game output to the UI, so game-specific parsing happens on the dashboard (e.g., player-list regex rendering).
- The agent does **not** require a cluster metrics pipeline for its own resource-usage reporting (no metrics-server dependency); usage is sourced in-pod from `/proc` or cgroups. A Prometheus scrape of `/metrics` (via the chart's optional `serviceMonitors.enabled` PodMonitor) is supported but not required.
- The agent does **not** implement game-specific business logic. All protocol handlers are per-game and declared in the module's template (`spec.capabilities`); new games require no agent code change.

## Directory & package layout

```
agent/
├── cmd/main.go              # Entry point: flag parsing, auth setup, chi router, heartbeat goroutine
├── cmd/metrics.go           # Separate plain-HTTP /metrics listener (--metrics-addr), off the mTLS control mux
├── internal/
│   ├── actions/             # Module-declared operator actions (templated RCON)
│   ├── auth/                # Request authenticator (mTLS + bearer token)
│   ├── caps/                # Mirrors GameTemplate.spec.capabilities schema (JSON unmarshaling)
│   ├── console/             # Duplex WebSocket console bridge (RCON stdin/stdout)
│   ├── files/               # File-browser HTTP API (list, read, write, upload, mkdir, delete)
│   ├── heartbeat/           # GameServer status patcher (lastHeartbeat, players, version, usage)
│   ├── lifecycle/           # Stop sequence execution before scale-to-zero
│   ├── logs/                # Log file streaming over WebSocket (tail from start or end)
│   ├── mods/                # Mod list, install, uninstall (manifest tracking, SSRF guard)
│   ├── players/             # Player count, names, ban lists, moderation (kick, ban, unban)
│   ├── quiesce/             # Pause auto-saves and flush state before snapshots
│   ├── rcon/                # RCON wire protocols (source, telnet, websocket, battleye, satisfactory, palworld, nuclearoption, rest, cli)
│   ├── status/              # Live metrics extraction via RCON + regex
│   └── usage/               # Resource usage reader (/proc or cgroups; disk via statfs)
├── openapi.yaml             # Partial machine-readable HTTP contract (subset of routes)
└── .testcoverage.yml        # Coverage gate (90% total, unit-only)
```

Per-package roles:

- **`actions`**: Renders module-declared RCON command templates with user parameters; validates via `gameaction` package.
- **`auth`**: Two modes: mTLS (agent listens TLS, requires client cert signed by `--tls-client-ca`), or shared-secret bearer token (fallback for dev).
  mTLS validates the serving certificate/key and client CA at startup and
  reloads them for every new TLS handshake. The operator mounts all three
  files in one projected Secret; the loader pins its `..data` generation
  before reading, so a concurrent projection update cannot mix material.
  Invalid, missing or mismatched rotated material rejects new handshakes
  until repaired, without falling back to cached credentials or trust.
  Every decoded `CERTIFICATE` block in the client CA bundle must contain
  valid DER, even when other CA certificates in that bundle are valid.
  Fully valid bundles may contain multiple trusted CAs.
  TLS remains at least 1.2 with a required verified client certificate.
  The control server advertises HTTP/2 and HTTP/1.1 across TLS renewal.
  Session tickets are disabled to reverify client trust on every new
  connection. Established connections keep their existing TLS state.
  Regular PEM files are also supported; replace the set consistently to
  avoid temporary handshake failures during updates.
- **`caps`**: Unmarshals JSON capabilities blob from `GAMEPLANE_CAPABILITIES` env; exposes `Spec` with `Players`, `Quiesce`, `Lifecycle`, `Actions`, `Status`, `Mods`.
- **`console`**: Accepts `{ kind: "cmd", body: "<rcon cmd>" }` JSON over WebSocket, runs it via RCON, replies with `{ kind: "out"|"err", body: "<response>" }`. On an RCON failure the `err` body is the generic `upstream unavailable`; the detailed error is logged with every occurrence of the submitted command replaced by `<redacted>`, since RCON clients embed the command in their errors and it may carry a secret.
- **`files`**: Walks the filesystem under `--data-root`, validates paths lexically, then performs every operation relative to a directory descriptor on the root with `openat(O_NOFOLLOW)` per component (no `..`, no symlink at any component), handles multipart uploads.
- **`heartbeat`**: Runs a background goroutine that every 20 seconds patches `gameservers/<name>/status` with `status.agent.lastHeartbeat`, `status.agent.playersOnline`, `status.agent.playersMax`, `status.agent.gameVersion`, and resource usage via the pod's ServiceAccount. `gameVersion` is currently always patched `null` ("unknown") — the agent has no source for the game's actual running version; it must never be filled in with the game/template identifier (e.g. `minecraft-java`), which is a different value.
- **`lifecycle`**: HTTP handler for the operator's `/lifecycle/stop` call; runs module-declared stop commands over RCON before the game process terminates.
- **`logs`**: Tails a game log file (path from `--game-log-path`) over WebSocket; supports streaming from end (default, "live") or start ("backlog"). Absolute paths must be inside `--data-root`; relative paths resolve beneath it. Downloads and all tail opens, rotation probes, and reopens use regular-file descriptors confined to the trusted data root. Symlinks in any component below the root and nonregular files are rejected; FIFO rejection does not block. Downloads serve the opened descriptor even if the path changes. Ordinary rename rotation and missing-file retries remain supported. Filesystem failures use generic client errors.
- **`mods`**: Tracks installed mods in a per-volume manifest (`.gameplane-mods.json`); downloads from registry with strict egress validation via `netguard.IsPublic`.
- **`players`**: Queries player count, names, ban lists, and runs moderation actions over RCON; a single template-driven `commander` renders each moderation command from the module's declared `capabilities.players` templates (no per-game Go implementations), and reports capabilities from what the template declares.
- **`quiesce`**: Runs module-declared sequences (e.g., Minecraft's `save-off` + `save-all flush`) over RCON; responds `quiesced: false` + reason when unsupported (not an error).
- **`rcon`**: Factory pattern for wire-protocol clients (`Valve/Source`, `Telnet`, `WebSocket`, `BattlEye`, `Satisfactory`, `Palworld`, `NuclearOption`, `REST`, `CLI`, `Disabled`); `Exec(cmd) (string, error)` interface. RCON reply packets are accepted up to 16394 bytes (a 4096-character Minecraft chunk at its worst-case UTF-8 byte length, plus the 10-byte packet header) and rejected as malformed outside `[10, 16394]` (F-104).
- **`status`**: Runs module-declared metrics queries over RCON; each metric specifies a command and a regex with named group `"value"` for extraction.
- **`usage`**: Reads CPU/memory from `/proc` (proc mode, default) or cgroup v2; disk via `statfs`; exposes `Sample` with `Known` flags so callers distinguish "unknown" from "zero".

## External interface / contracts

### Entry point

Entry: `agent/cmd/main.go`  
Mode: In-pod HTTP/HTTPS sidecar (runs as a container sidecar or as a pod share-process-namespace helper)

### Command-line flags

| Flag | Default | Env var | Purpose |
|------|---------|---------|---------|
| `--addr` | `:8090` | — | HTTP listen address for the mTLS control mux (e.g., `:8090` for all interfaces) |
| `--metrics-addr` | `:9090` | `GAMEPLANE_METRICS_ADDR` | Listen address for the separate Prometheus metrics listener, plain HTTP with no auth (empty disables it); never shares `--addr`'s mTLS control mux, so a scraper needs no client cert |
| `--data-root` | `/data` | — | Root path for file operations (agent restricts all I/O here) |
| `--rcon-host` | `127.0.0.1` | — | RCON server host (loopback in-pod) |
| `--rcon-port` | `25575` | — | RCON server port (game-specific default) |
| `--rcon-password-file` | `` | — | Path to file holding the RCON password |
| `--rcon-enabled` | `true` (from env) | `GAMEPLANE_RCON_ENABLED` | Whether the game exposes RCON; `false` degrades RCON-backed endpoints gracefully |
| `--rcon-protocol` | `source` (from env) | `GAMEPLANE_RCON_PROTOCOL` | RCON wire protocol: `source` (Valve/Minecraft), `telnet` (7 Days to Die), `websocket` (Rust), `battleye` (DayZ/Arma), `satisfactory` (Satisfactory), `palworld` (Palworld), `nuclearoption` (Nuclear Option), `rest` (generic HTTP/JSON admin API, e.g. FiveM txAdmin or Farming Simulator 25), `cli` (container stdin/PTY); unrecognized falls back to `source` |
| `--game-log-path` | `` | — | Regular game log file for `/logs/tail` and `/logs/download`; absolute inside `--data-root`, or relative to it; no symlinks below root |
| `--tls-cert` | `` | — | Server TLS cert (PEM); if set, requires `--tls-key` and enables HTTPS + mTLS; exits with error if set without `--tls-key` or vice versa |
| `--tls-key` | `` | — | Server TLS key (PEM); must be set together with `--tls-cert` |
| `--tls-client-ca` | `` | — | CA bundle that signs API client certs (required if `--tls-cert` is set) |
| `--api-token-file` | `` | — | Fallback shared-secret auth (used when TLS is not configured); file contents become the bearer token |
| `--server-uid` | `` | `GAMEPLANE_SERVER_UID` | Immutable owning GameServer UID; required for versioned gateway routes |
| `--server-name` | `` | `GAMEPLANE_SERVER_NAME` | Owning GameServer name (for status patches) |
| `--template` | `` | `GAMEPLANE_TEMPLATE` | GameTemplate name |
| `--game` | `` | `GAMEPLANE_GAME` | Game identifier (e.g., `minecraft`, `rust`, `satisfactory`) |
| `--capabilities` | `` | `GAMEPLANE_CAPABILITIES` | Declared game capabilities (JSON, from `GameTemplate.spec.capabilities`) |
| `--log-level` | `info` (from env) | `GAMEPLANE_LOG_LEVEL` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `--cli-pipe` | `/var/run/gameplane/console.pipe` (from env) | `GAMEPLANE_CLI_PIPE` | Named FIFO pipe used by the `cli` RCON protocol to drive commands via container stdin/PTY |

Resource usage env vars (set by the operator):

| Env var | Purpose |
|---------|---------|
| `GAMEPLANE_USAGE_PROC` | When `"1"`, enables proc mode (reads game process CPU/memory from `/proc`); otherwise falls back to cgroup mode |
| `GAMEPLANE_CPU_LIMIT_MILLICORES` | Game container's CPU limit (for usage percentage calculation) |
| `GAMEPLANE_MEM_LIMIT_BYTES` | Game container's memory limit |

### Endpoint groups (from mounted routes in cmd/main.go; openapi.yaml documents a subset)

**Outside the auth middleware, on the `--addr` control mux:**
- `GET /healthz` — Liveness probe; returns `200 ok`. Registered outside the auth middleware. In mTLS mode (always used by the operator) the TLS handshake requires a verified client certificate for every path, including this one. In bearer-token fallback mode it answers without a token.

**Unauthenticated, on the separate `--metrics-addr` listener — not the control mux above, and never TLS:**
- `GET /metrics` — Prometheus metrics exposition. (F-216, canonical rationale;
  other mentions of F-216 elsewhere in the repo point back here.) The
  agent's only listener used to be the mTLS control port, so a plain-HTTP
  PodMonitor scrape of that port always failed the TLS handshake and every
  agent target showed "down" in Prometheus. An earlier fix instead handed
  Prometheus the same mTLS client cert the API presents to agents — rejected,
  because `RequireAndVerifyClientCert` accepts that cert for every control
  route (console, files, RCON), not just `/metrics`, which is more trust
  than a scraper needs. The fix here is this separate, unauthenticated
  `--metrics-addr` listener: a scraper never needs the client cert that
  unlocks console/files/RCON on the control mux. `buildAgentContainer`
  (`operator/internal/controller/gameserver_controller.go`) declares this
  listener's port as a named `metrics` containerPort (9090), and the chart's
  agent `PodMonitor` targets it by that name rather than a bare
  `portNumber`, so the scrape works on any Prometheus-Operator CRD version.
  The games-namespace `default-deny-ingress` policy admits this port only
  when an operator opts in via `serviceMonitors.scrapeNamespaceSelector`
  (see `charts/gameplane/templates/networkpolicies.yaml`'s
  `allow-prometheus-to-agent` policy and `docs/security.md`).

**Protected (all require mTLS cert or bearer token):**

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/files/list` | GET | List directory entries; query param `path` (default `/`) |
| `/files/read` | GET | Read file inline (small files only); query param `path` |
| `/files/download` | GET | Download file as attachment; query param `path` |
| `/files/write` | POST | Overwrite or create file; query param `path`; body is raw file content |
| `/files/upload` | POST | Upload one or more files to a directory; query param `path`; body is `multipart/form-data` with `files[]`; returns HTTP 400 if the destination directory or a destination file name is, or is reached through, a symlink |
| `/files/mkdir` | POST | Create directory (recursive); query param `path` |
| `/files/delete` | DELETE | Delete file or directory; query param `path`, optional `recursive` (boolean) |
| `/logs/tail` | GET | Tail game log over WebSocket; query params `from` (enum: `start`, `end`, default `end`) and `tail` (optional positive integer, capped at 20000 lines and 4 MiB bytes; replays last N lines before following; no replay after rotation) |
| `/console` | GET | Duplex console over WebSocket; client sends `{ kind: "cmd", body: "<command>" }` (JSON), server replies `{ kind: "out"\|"err", body: "<response>" }` |
| `/players` | GET | Current online player count and names; response: `{ online, max, players[], asOf, capabilities }`. `online`/`max` are `-1` when unknown (RCON disabled, or no recognized player-list format) — the sole "unknown" representation, never `0`. |
| `/players/kick` | POST | Kick a player; request: `{ name, reason? }`; response: `{ ok, raw? }`; 501 if unsupported; reason is validated with the gameaction input policy (no control characters, no ``; & \| $ ` \ " '``), max 256 bytes (excess truncated); 400 on violation |
| `/players/ban` | POST | Ban a player; request: `{ name, reason? }`; response: `{ ok, raw? }`; 501 if unsupported; reason is validated with the gameaction input policy (no control characters, no ``; & \| $ ` \ " '``), max 256 bytes (excess truncated); 400 on violation |
| `/players/unban` | POST | Lift a ban; request: `{ name }`; response: `{ ok, raw? }`; 501 if unsupported |
| `/players/banned` | GET | Currently banned players; response: `[ { name, reason?, source? } ]`; 502 if RCON unavailable |
| `/quiesce` | POST | Pause auto-saves before snapshot; response: `{ quiesced, reason? }` (boolean, reason optional); unsupported games return `quiesced: false` with a reason |
| `/unquiesce` | POST | Resume auto-saves after snapshot; response: `{ quiesced, reason? }` |
| `/lifecycle/stop` | POST | Run stop sequence before scale-to-zero; RCON connection drop is the expected outcome |
| `/actions/run` | POST | Execute module-declared operator action; request: `{ id, params }`; response: `{ ok, raw? }` |
| `/status` | GET | Live game metrics (module-declared); response: bare JSON array `[ { id, displayName?, value, unit? } ]` |
| `/mods` | GET | List installed mods; response: bare JSON array `[ { name, size, modTime, meta? } ]` |
| `/mods/install` | POST | Install a mod; request: `{ url, name?, replaces?, meta? }` (no `version` field; `meta` carries the registry identity) |
| `/mods` | DELETE | Uninstall a mod; query param `?name=` (not a JSON body) |
| `/mods/upload` | POST | Upload a mod archive; body is multipart/form-data |
| `/logs/download` | GET | Download the full game log file as an attachment |
| `/players/whitelist` | GET | Currently whitelisted players; response: bare JSON array of names (`[]` if whitelist management isn't supported or RCON is disabled) |
| `/players/whitelist/add` | POST | Add a player to the whitelist; request: `{ name }`; response: `{ ok, raw? }` |
| `/players/whitelist/remove` | POST | Remove a player from the whitelist; request: `{ name }`; response: `{ ok, raw? }` |

All endpoints on the `--addr` control mux, except `/healthz`, return `401 Unauthorized` if the request lacks a valid cert or token. `/healthz` is outside the auth middleware: in mTLS mode (always used by the operator) the TLS handshake still requires a verified client certificate for it, as for every path; in bearer-token fallback mode it answers without a token. `/metrics` is not on that mux at all — it lives on the separate `--metrics-addr` listener, which has no auth of its own.

## Key invariants

- **Every protected request is authenticated**: The `auth` package gates all protected game-data routes (`/files`, `/logs`, `/console`, `/players`, RCON) with either mTLS verification or bearer-token matching. The documented public endpoints (`/healthz` on the control mux, `/metrics` on its separate listener) are the only exceptions; see above.
- **File write access**: `files.write` and `files.savePart` publish replacements owned by the unprivileged agent. For a different original owner, a Linux POSIX access ACL preserves that exact UID's owner permissions. The original group and all existing effective named-user, named-group and other permissions are retained; widening the ACL mask never revives previously masked grants. Subsequent edits retain this ACL. New files use `0o644` and do not inherit extra named grants. Replacing another UID's file requires POSIX ACL support; preserving the original group requires that group to be available to the agent. Unsupported access metadata, a refused group change, or a detected concurrent regular-file replacement returns a conflict and leaves the previous file intact. This does not add privileges or grant access to other users. As with ordinary rename-based writes, an external process can still replace the destination after the final metadata check; callers must coordinate concurrent writers.
- **Cross-UID file access regression**: The amd64 agent CI job runs `TestFileAccessAcrossUIDs` from a compiled test binary under a root harness. The harness launches capability-free agent UID 65532 and game UID 1000 subprocesses, checks writes and multipart uploads over real game-owned `0600`/`0644` files, verifies masked named-user/group access, and verifies atomic failure. The normal unprivileged suite skips this harness; production privileges are unchanged.
- **Direct mod file permissions**: Non-extracted mod uploads and downloads are set to `mods.moduleFileMode` (`0o644`) before rename. Extracted mod files remain covered by the existing world-readable invariant.
- **RCON is a lower-trust boundary**: The agent uses `netguard.IsAllowed()` for WebRcon dial operations (permissive, allows loopback and private addresses on the assumption game servers run inside the cluster), and `netguard.IsPublic()` (strict, permissive only for well-known registries) for mod-install downloads, assuming modules are less trusted than the operator.
- **Gameaction validation is independent**: Both the API (stdin pod-attach) and the agent (RCON) call `gameaction.Resolve()` independently to validate action inputs (no control characters, 512-char cap, required-ness checks, etc.). Neither trusts the other. The players moderation endpoints (kick/ban reason) also call gameaction.CheckText, so moderation text follows the same character policy as actions.
- **No persistent storage**: The agent has no database. GameServer status patches flow through the operator; all transient state (WebSocket streams, RCON sessions) is in-memory.
- **Path confinement via per-component validation**: The `files` package validates the requested path lexically in `resolve()` (direct traversal: `..`, `/`, absolute paths; any dot-prefixed component is rejected as a dotfile) and then performs every operation relative to a directory descriptor opened on the data root (`rooted_linux.go`). Each component is opened with `openat(O_NOFOLLOW)` (directories also `O_DIRECTORY`), so a symlink at any component of the path, ancestor or final, is refused with HTTP 400 (`dotfile access denied` when the link's target lies below a dot-prefixed component, otherwise `path escapes root (symbolic link in path)`). Reads, writes, creates, deletes and listings use the descriptors they obtained (`fstatat`, `mkdirat`, `unlinkat`, `renameat`, `ServeContent` on the opened fd) and never re-open a path string, so replacing a checked directory or file with a symlink between validation and use cannot move the operation outside the root; a delete removes a symlink as the link it is and a recursive delete never follows links. The data-root path itself is the one trusted, followed component (it may be a symlink). `savePart` likewise rejects dot-prefixed upload filenames; `/files/list` excludes dot-prefixed entries from its output and a delete refuses trees containing them. The `mods` package uses `ConfinePath(rootDir, untrustedName)` as the authoritative guard for single-component mod paths (upload, download, removal, archive swap), and `ConfineRelPath(root, relPath)` for multi-component archive entry paths during extraction. ConfinePath returns a cleaned, absolute path guaranteed to be confined within rootDir (or raises an error if escape is attempted), validating against both direct traversal and symlink escape. The prior `safeName()` function remains in use as caller-side defense-in-depth (pre-filtering before ConfinePath), but ConfinePath is the authoritative guard within the function boundary where the archive operation occurs.
- **WebRcon dials through netguard**: `rcon.websocket.go`'s `ensureLocked()` method dials the Rust WebSocket using `netguard.IsAllowed()` dial policy for defense-in-depth, allowing loopback and private addresses (since game servers legitimately run inside the cluster) while blocking the ranges `netguard.IsAllowed` rejects (link-local, multicast, NAT64/6to4).
- **WebRcon dial errors are redacted**: On dial failure, `ensureLocked()` strips the URL path (which carries the escaped password) from any `*url.Error` via `redactURLErr`. If the redacted message still contains the password in raw, path-escaped, or query-escaped form (`errorLeaksSecret`; passwords shorter than three characters are not checked), it returns a generic `connection failed` error without the original cause; otherwise it wraps the redacted error with `%w` so `errors.Is`/`errors.As` still work.
- **Agent-created directories are 0755**: `files` (mkdir, upload and write ancestors) and `mods` (mods dir, archive extraction dirs) create directories 0o755, matching the 0o644 files, because the game container runs as a different uid and on templates without an fsGroup reaches the shared volume only through the "other" bits. The gosec G301 finding for these two files is scoped in `.golangci.yml`.
- **Partial uploads never linger, and never destroy an existing file**: `files.savePart` writes to a temp file in the destination directory and renames it over the final name only once the copy succeeds. A save that fails after the temp file is opened — a source read error, an `io.ErrUnexpectedEOF` from a truncated multipart body, or an over-the-limit part — removes only the temp file, so a client abort mid-upload can't leave a half-written file where a later `/files/read` or `/files/download` would serve it as complete, and can't delete a file that already existed at that name.
- **Symlink confinement on upload**: `files.savePart` confines the upload destination the same way as other file operations: an existing symlink at the destination name (pointing inside the root, outside it, or dangling) is rejected with HTTP 400 (`errLinkTraversal`, which wraps `errPathOutOfRoot`), leaving the symlink and its target untouched. Regular (non-symlink) files are overwritten by a successful upload through an atomic `renameat` in the same directory descriptor.
- **Extracted mod files stay world-readable**: `mods.moduleFileMode` is `0o644`, not a tighter `0o600`, because the mods volume is shared with the game container, which runs as whatever uid its image needs — a different uid than the agent's own. At `0o600` the game process couldn't read its own mods. This is the rationale behind the `.golangci.yml` gosec G302 exclusion scoped to `agent/internal/mods/mods.go`.
- **Resource usage is in-pod**: The `usage` package reads from `/proc` or cgroups; no external metrics pipeline required. Cgroup mode is a fallback for older clusters; proc mode (default in production) requires the operator to set `ShareProcessNamespace: true`.
- **Module capabilities drive behavior**: Every game-specific handler (players, quiesce, lifecycle, status, actions) reads its config from `--capabilities` (JSON unmarshaled into `caps.Spec`). New games require no agent code change.
- **RCON connection errors are graceful**: A lost RCON connection does not crash the agent. `console`, `players`, `quiesce`, and `lifecycle` handlers catch connection errors and return appropriate HTTP status (e.g., `502 Bad Gateway`).
- **Source RCON sends complete frames**: Each AUTH or command frame is submitted in one socket write, with its existing byte-size limit, request ID, type, body, and two NUL terminators preserved. This avoids separate header-only writes rejected by vanilla Minecraft's RCON reader. A short write is an error and drops the connection without replaying the command; response grace and healthy connection reuse remain unchanged.
- **Log streams are tail-only**: The `logs` package does not support random-access reads. It streams from the current end (live mode) or from file start (backlog mode); clients must handle partial output and reconnection.

## Dependencies

### Internal

- **`netguard`** (sibling module): SSRF dial-guard for egress validation. Agent uses `IsPublic()` for mod downloads and `IsAllowed()` for WebRcon dials. Operator uses `IsAllowed()` (permissive for git/http ModuleSource fetches).
- **`gameaction`** (sibling module): Console-injection guard and command-template renderer. Agent calls `Resolve()` on RCON action inputs independently.

### External

| Module | Version | Purpose |
|--------|---------|---------|
| `github.com/go-chi/chi/v5` | v5.3.2 | HTTP router |
| `github.com/coder/websocket` | v1.8.15 | WebSocket library for console, logs, player queries |
| `k8s.io/apimachinery` | v0.37.1 | Kubernetes types for status patches |
| `k8s.io/client-go` | v0.37.1 | Kubernetes client for heartbeat (GameServer status patches) |
| `github.com/prometheus/client_golang` | v1.24.1 | Prometheus metrics (`/metrics` endpoint) |
| `golang.org/x/sys` | v0.48.0 | System-level utilities (used by client-go) |

The agent and operator modules use `k8s.io/apimachinery`/`k8s.io/client-go` v0.37.1 and the api module v0.37.0; each module resolves its own version, so patch versions can drift between them as Dependabot bumps one module at a time.

## Data & persistence

- **Game data volume** (`--data-root`, typically `/data`): A PVC mounted read-write. The agent's file I/O operations are confined here; no access to system paths or other volumes.
- **GameServer status**: Patched periodically by the `heartbeat` goroutine via the Kubernetes API. The agent holds no local copy; the operator is the source of truth.
- **RCON sessions**: In-memory only. Each RCON protocol implementation holds a connection pool or singleton:
  - `source`: Single TCP stream with Valve request ID sequencing.
  - `telnet`: Line-based TCP console.
  - `websocket`: Rust WebRcon protocol.
  - `battleye`: UDP-based CRC32 packet framing.
  - `satisfactory`: HTTPS JSON function-call API with session token.
  - `palworld`: HTTP Basic REST admin API.
  - `nuclearoption`: Dedicated TCP stream for Nuclear Option remote command protocol.
  - `rest`: Generic HTTP/JSON REST console client with lazy auth (`PassFn`), 1 MiB bounded body read, configurable timeouts, auth failure cooldown, and loopback TLS protection. Provides adapters for FiveM (txAdmin: `POST /fxserver/commands`, bearer/X-TxAdmin-Token, `{"action":"console","parameter":"<cmd>"}`), Farming Simulator 25 (`POST /api/console`, Basic auth, `{"command":"<cmd>"}`), and generic REST (`POST /api/command`).
  - `cli`: Container stdin/PTY / local execution client (Option A in OPEN-DECISIONS.md). Drives commands via named FIFO pipe (`GAMEPLANE_CLI_PIPE`) or local process shell execution without requiring remote TCP networking or password secrets, while web console continues to attach via pod-attach PTY.

## Security considerations

- **mTLS + token auth**: All protected endpoints require either a valid mTLS client cert (signed by `--tls-client-ca`) or a bearer token (from `--api-token-file`).
- **SSRF guard on mod downloads**: `netguard.IsPublic()` gates every mod download dial; private or loopback registries are rejected regardless of `allowedHosts`, which only narrows the host allowlist.
- **Console-injection guard on RCON actions**: `gameaction.Resolve()` validates action inputs independently on the agent side; control characters, oversized inputs, and required-parameter validation prevent blind command injection. Kick/ban reasons go through the same guard (gameaction.CheckText) before being rendered into the module's .Reason template variable.
- **Path-traversal protection**: The `files` package rejects `..`, any symlink component below `--data-root`, and dotfile access; all I/O goes through descriptors rooted at the data directory, so it is also safe against a path being swapped for a symlink after validation (race-oriented tests cover this).
- **No half-written uploads, and no lost files on a failed write**: `files.write` and `files.savePart` write to a temp file in the destination directory first and rename it over the target only once the copy succeeds. A failure partway through (truncated body, read error, over-limit part, out of space) removes only the temp file — a pre-existing file at that path is never truncated or deleted by a failed write or upload. The temp file is created, chmodded, fsynced and renamed through the same parent directory descriptor.
- **Extracted mod files are 0o644, not 0o600**: `mods.moduleFileMode` grants group+world read so the game container — which runs under whatever uid its own image needs (not the agent's uid) — can read its shared mods volume; `FSGroup` alone doesn't help here since it only changes group ownership, not mode bits. The tradeoff is scoped: the `.golangci.yml` gosec G302 exclusion applies to `agent/internal/mods/mods.go` only.
- **Low privilege within pod**: The agent is a sidecar container (not privileged, not root unless the game container is). It reads `/proc` only for the game process and the pause process; it cannot access other pods' data.
- **No unauthenticated game-data exposure**: `/metrics` (its own separate, unauthenticated listener) is public, and `/healthz` is outside the auth middleware on the control mux (in mTLS mode the TLS handshake still requires a verified client certificate for it; in bearer-token fallback mode it answers without a token); all game data (`/files`, `/logs`, `/console`, `/players`) requires authentication. `/metrics` is deliberately never reachable through the mTLS control mux, so a Prometheus scraper is never handed the client cert that would also unlock those authenticated routes.
- **Network context**: The agent runs inside the game pod and is reached via service DNS (e.g., `gameserver-pod-0.gameplane-games.svc.cluster.local:8090`). The pod's NetworkPolicy may restrict egress (e.g., games namespace has default-deny-egress); the agent's mod downloads and heartbeat calls must be compatible with that policy.

## Testing & coverage

- **Unit tests**: All packages have `*_test.go` files covering happy paths, error cases, and protocol edge cases (e.g., RCON packet framing, BattlEye CRC32, Satisfactory HTTP auth).
- **e2e tests**: The `api-agent` bucket in `test/e2e/` runs real agent instances on a kind cluster, testing console duplex, file I/O, moderation, quiesce/lifecycle, and heartbeat against live game servers (Minecraft, Satisfactory, Palworld).
- **Coverage gate**: `agent/.testcoverage.yml` enforces **90% total line coverage** (re-baselined down from 91% after the SSRF dial guard moved to `netguard`). Excluded: `cmd/` (flag/signal wiring) and `proto/` (if present). The remaining ~10% gap is in `heartbeat.Run()`'s in-cluster `rest.InClusterConfig` path (only meaningful in a real pod with ServiceAccount) and a few bookkeeping branches in `logs.streamFile` (sleep/return logic), both exercised by the e2e tier.

## References

- **`agent/openapi.yaml`** — Partial machine-readable HTTP contract documenting a subset of the routes (security schemes, endpoint paths, request/response schemas).
- **`docs/architecture.md`** — System overview, data flow, and the "operator is authoritative" design principle.
- **`docs/security.md`** — Auth model, threat boundaries, pod security defaults, and the module-trust relationship.
- **`go.mod`** (agent) — Dependency versions and workspace references.


## Versioned gateway targets

The optional private cluster gateway uses `/v1/targets/{uid}` followed by an
existing protected agent route. These endpoints apply the same authentication,
input validation, and operation handlers as local routes, then reject any UID
other than the immutable `GAMEPLANE_SERVER_UID` injected by the operator.
An empty configured UID also fails closed. Legacy unprefixed routes remain
available to local API/operator callers during upgrades.

A gateway must never retry a failed versioned request through the legacy path:
older agents deliberately return 404. This binds the final network hop to the
actual server incarnation even if the name-based agent Service changes between
the gateway's Kubernetes lookup and connection. Existing operator-owned game
Pod templates gain the UID environment variable on reconciliation, which can
roll existing game workloads during an operator upgrade.
