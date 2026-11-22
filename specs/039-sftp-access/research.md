# Research: SFTP access to game servers

Facts read from `origin/master` at `71a2db19` on 2026-11-22. Each item states what exists, what this spec does with it, and why.

## R1. The agent file API and its gaps

- `agent/internal/files/files.go:81-99` mounts `/files/{download,archive,upload,list,read,write,create,mkdir,delete}`. Paths are resolved component by component below the data root with symlinks refused (`rooted_linux.go`); dotfiles are refused (`hasDotComponent`).
- `download` already serves `Range` through `http.ServeContent` (`files.go:241`), so download resume needs no agent change. The gateway forwards `Range` (`api/internal/gateway/server.go:139`).
- Missing for SFTP: **stat** of one path (SFTP `STAT`/`LSTAT`/`FSTAT`), **rename** (SFTP `RENAME`, `posix-rename@openssh.com`; WinSCP uploads to `*.filepart` and renames), and an **offset/streaming upload** (`upload` is multipart with a 64 MiB per-file cap, `files.go:249-253`, `maxFormSize` 64 MiB at `files.go:300`).
- `list` returns `Entry{Name,Path,Size,Mode,Dir,ModTime}` (`files.go:101-109`), enough for SFTP `READDIR` attributes.

**Decision**: add `GET /files/stat`, `POST /files/rename` and `PUT /files/stream` (raw body, `offset` query, temp file committed atomically on success) to the agent. The streaming route has its own limit from OD-6 and is long-running like `upload`.

## R2. How the API reaches an agent, locally and remotely

- Both paths dial the per-server Service `<name>-agent.<ns>.svc.cluster.local:8090`: the local proxy (`api/internal/ws/agent_client.go:171`) and the remote gateway (`api/internal/gateway/server.go:128`). The gateway checks that the Service is controlled by the target GameServer (`server.go:171-198`).
- The Service selects `app.kubernetes.io/name: gameplane-game`, `app.kubernetes.io/instance: <name>` with `PublishNotReadyAddresses` (`operator/internal/controller/gameserver_controller.go:1301-1318`).
- 17 other selectors in the operator use the same name/instance pair (player Services, NetworkPolicies, PDBs).

**Decision (OD-4 file-only pod)**: the file-only pod gets its own labels (`app.kubernetes.io/name: gameplane-files`, `app.kubernetes.io/instance: <name>`), so no player Service, PDB or game NetworkPolicy selects it. While file-only access is active the operator points the `<name>-agent` Service selector at those labels, and back at the game labels when it ends. API and gateway routing stay unchanged, and nothing rolls the StatefulSet. A new NetworkPolicy allows API and gateway ingress to `gameplane-files` pods on 8090 only.

## R3. Stop, sleep and start in the operator

- `desiredReplicas` (`gameserver_controller.go:1504-1610`) scales the StatefulSet to 0 for `spec.suspend` and idle sleep (`idleAsleep`). An asleep server may have a sentinel pod holding its ports (`sentinel/specs.md`).
- Wake requests are annotation patches the operator acts on (sentinel pattern), keeping business logic in the reconciler (core rule 10).

**Decision**: the API requests file-only access by patching annotation `gameplane.local/files-requested-at` (RFC 3339) on the GameServer, at most once a minute while operations continue. The reconciler, when replicas are 0 and the annotation is younger than the idle timeout, creates the file-only pod (owned by the GameServer, same volumes, agent container built by `buildAgentContainer` with the files-only flag, no game container, no host ports); otherwise it deletes it. Before scaling a server up, the reconciler deletes the file-only pod and waits for it to be gone (FR-013c). The API waits up to 60 s for the agent Service to answer before returning "files are starting".

## R4. Credentials and the API session for SFTP

- Spec 037 added personal MCP tokens: `api/internal/auth/mcptokens.go` (prefix `gpmcp_`, SHA-256 digest, `session_epoch`, route allow-list guard `MCPTokenGuard`), table `mcp_tokens` (`migrations/common/023_mcp_tokens.sql`).
- Passwords are argon2id (`api/internal/auth/password.go`); login rate limiting lives in `api/internal/auth/ratelimit.go`.
- Usernames match `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$` (`api/internal/handlers/validate.go:17`), so `+` never appears in one.

**Decision**: new tables `sftp_ssh_keys` and `sftp_passwords` (migration `024_sftp_credentials.sql`). SFTP passwords are argon2id hashes (they are user-held secrets typed by hand, unlike 256-bit tokens). The SFTP server authenticates itself to the API with a per-cluster **SFTP service token** (`gpsftps_…`) and calls `POST /sftp/v1/login`; the API checks the credential, rate limits, and returns a short-lived **SFTP session token** (`gpsftp_…`, 15 min, renewed while active) scoped to one user and one cluster. File operations use the normal `/servers/{name}/files/*` routes with `?cluster=<cluster>` and that bearer token; a guard like `MCPTokenGuard` allows only the file routes and the server list. Revocation, disable and password reset (`session_epoch`) invalidate session tokens at once; the SFTP server checks its sessions every 30 s through `POST /sftp/v1/sessions/check` and closes revoked ones (FR-007).

## R5. Per-cluster endpoints and the central API (OD-3, OD-5)

- A remote cluster runs the operator and the gateway (`docs/gateway-install.md`); the central API reaches it, never the reverse.

**Decision**: the SFTP chart template renders on any install with `sftp.enabled`. On the panel's own cluster it reaches the API through the in-cluster Service and the chart generates its service token Secret. On a remote cluster, values give `sftp.apiURL`, an optional CA bundle and the name of a Secret holding a service token that an admin creates for that cluster in the central panel (`POST /clusters/{name}/sftp-token`, `cluster:manage`). The service token binds the endpoint to its cluster, so a remote endpoint cannot list another cluster's servers.

## R6. SFTP library

- `golang.org/x/crypto/ssh` is already a dependency (`api/go.mod:42`). `github.com/pkg/sftp` provides an SFTP server with a pluggable `Handlers` interface (`FileGet`, `FilePut`, `FileCmd`, `FileList`), which maps onto the API calls without touching a local filesystem.

**Decision**: use both, in the new component (OD-7). Writes map to one streaming upload per open handle: sequential writes stream; a first write at offset N > 0 resumes (agent copies the first N bytes of the existing file into the temp file); out-of-order writes return `SSH_FX_OP_UNSUPPORTED`. `SETSTAT` is accepted as a no-op for mode and times, and refused for owner (FR-017).

## R7. Audit

- `audit.Event` has `Actor, Method, Path, Target, Status, IP, Reason` (`api/internal/audit/audit.go:820-830`), written by `audit.Middleware` for mutating requests and fanned out to webhook, syslog bridge and S3.

**Decision**: add a nullable `source` column (migration 024) set to `sftp` when the request carries an SFTP session token, and include it in webhook/S3 payloads as an additive field. SFTP logins and failures are audited as `POST /sftp/v1/login` rows, with failures using the generic wording (core rule 3).
