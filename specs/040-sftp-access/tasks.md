# Tasks: SFTP access to game servers

**Input**: Design documents from `/specs/040-sftp-access/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/api.md, quickstart.md. OD-1 to OD-7 settled by valgul on 2026-11-22; no decision is open.

**Tests**: Required. Constitution Principle I needs E2E coverage, and the coverage gates (agent 90, api 80, operator 72, web 92/76/82/92, the sftp repo's `.testcoverage.yml`) need unit, envtest and Vitest tests. Every test task adds **new** test functions or files. Changing an **existing** test needs valgul's written sign-off from T002 first (CLAUDE.md override 1). Tests run in CI only (Principle VI); locally only `go build ./...` (`GOWORK=off` inside `sftp/`) and `npx tsc --noEmit`.

**Organization**: by user story in spec priority order: US1, US2 (P1), then US3, US4, US5 (P2).

**Delivery** (plan.md, valgul 2026-11-23): one Gameplane PR on `feat/040-sftp-access` (agent, operator, API, design, web, chart, docs, E2E as separate commits) plus one `GameplanePanel/sftp` PR on `feat/initial-server`, which merges first. Implementation runs through `Workflow` scripts (Principle V): haiku agents with an explicit `model:`, then one diff review per batch one tier up.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task).
- **[Story]**: US1 to US5 from spec.md. Setup, Foundational and Polish tasks carry none.
- Paths starting `sftp/` are in the `GameplanePanel/sftp` repository (OD-7), checked out in Gameplane as the `sftp/` submodule. All other paths are in Gameplane.

---

## Phase 1: Setup

**Purpose**: branches, test sign-off, the new repository, and the one design pass every UI task depends on.

- [ ] T001 Confirm PR #1066 (spec 040 quickstart) is merged to `master`; create the two branches named in **Delivery** from fresh `master` (Gameplane) and `main` (sftp); record base SHAs in each PR description.
- [ ] T002 List every existing test that encodes behaviour this spec changes (file, test name, assertion, needed change) and get valgul's written sign-off before editing any, in `specs/040-sftp-access/T002-test-signoff.md`. Candidates: `agent/internal/files/*_test.go` (route table, upload caps), operator envtests asserting the `<name>-agent` Service selector in `operator/internal/controller/`, `api/internal/gateway/*_test.go` (operation allow-list), `api/internal/audit/*_test.go` (row shape, webhook/S3 payload), `api/internal/rbac/*_test.go` (route rules), `web/src/routes/tabs/Files*.test.tsx` (multipart upload), `test/e2e/optional_components_e2e_test.go`. Write "none needed" with the reason for each file that needs no change.
- [ ] T003 Scaffold `GameplanePanel/sftp` like `GameplanePanel/mcp`: `sftp/go.mod` (module `github.com/GameplanePanel/sftp`), `sftp/.github/workflows/ci.yaml` calling Gameplane's `go-component.yaml` pinned by SHA, `sftp/.testcoverage.yml`, `sftp/CLAUDE.md`, `sftp/AGENTS.md`, `sftp/specs.md` (empty sections), `sftp/Dockerfile` copying `sftp/...` from the Gameplane build context, `sftp/cmd/main.go` stub that builds.
- [ ] T004 Design in `design.fig` through OpenPencil (never edit the file directly): profile SFTP section (address, port, `<user>` and `<user>+<server>` username forms, host key fingerprint, keys list, add-key dialog, generate-password dialog with a 20–40 length picker and one-time display, regenerate and revoke confirms), admin SFTP panel on Users, Clusters SFTP token dialog and endpoint fields, Files tab per-file upload progress with cancel (OD-6), Files tab "files are starting" state (OD-4). Run `hack/design/fig-export.ts`, commit `assets/design-export/` with `design.fig` on `design/040-sftp`, and get valgul's sign-off before any React task (T030, T035, T036, T042, T050).

---

## Phase 2: Foundational (blocks every story)

**Purpose**: agent primitives, storage, API auth and the minimal deployable SFTP server that every story builds on.

**⚠️ CRITICAL**: no user story work starts until this phase is complete.

- [ ] T005 [P] Agent `GET /files/stat?path=` in `agent/internal/files/stat.go`: rooted walk like `list`, dotfiles refused (`errDotfile`), returns one `Entry`, 404 when missing; mount in the bounded group in `agent/internal/files/files.go`.
- [ ] T006 [P] Agent `POST /files/rename` `{from, to, overwrite}` in `agent/internal/files/rename_linux.go`: `renameat` on directory fds from the rooted walker, refuses the root, dotfiles and `overwrite=false` onto an existing name; mount in the bounded group.
- [ ] T007 [P] Agent `PUT /files/stream?path=&offset=N` in `agent/internal/files/stream_linux.go`: raw body; `offset=0` writes a new temp file in the target directory, `offset=N` (N ≤ existing size) copies the first N bytes first; `statfs` free-space check before writing; limit from new flag `--max-upload-bytes` in `agent/cmd/main.go`; fsync and `renameat` commit on success; temp removed on any error or cancel (FR-016); mount next to `upload` (no 30 s timeout).
- [ ] T008 Agent tests in new files `agent/internal/files/stat_test.go`, `agent/internal/files/rename_linux_test.go`, `agent/internal/files/stream_linux_test.go`: `..` and symlink-swap escapes via the `beforeOpen` seam, dotfile refusal, resume prefix copy, cancel mid-stream keeps the old file, free-space refusal, size limit, overwrite rules.
- [ ] T009 Document the three routes and their invariants in `agent/specs.md`.
- [ ] T010 Migration `api/internal/db/migrations/common/024_sftp_credentials.sql` per data-model.md: `sftp_ssh_keys`, `sftp_passwords`, `sftp_service_tokens`, `sftp_sessions`, `audit_events.source`; portable SQL, ids and timestamps bound from Go; `DeleteUserTx` in `api/internal/db/` deletes a user's SFTP rows.
- [ ] T011 Credential store in `api/internal/auth/sftp.go`: SSH key parse and normalize (ed25519, ecdsa-sha2-nistp256/384/521, RSA ≥ 3072 bits), SHA256 fingerprint, 20 keys per user; password generate from `crypto/rand` uniform over the 94 printable ASCII characters (space excluded), length 20–40, argon2id hash via `api/internal/auth/password.go`; one password per user, regenerate replaces in one transaction.
- [ ] T012 Service and session tokens in `api/internal/auth/sftp_tokens.go`: service tokens `gpsftps_` (SHA-256 digest, bound to one cluster, 2 active per cluster), session tokens `gpsftp_` (15 min sliding, `last_used_at` at most once a minute, `session_epoch` copied from `users.session_epoch`), revocation on key delete, password regenerate/revoke, user disable or delete; sweeper next to the MCP token sweeper.
- [ ] T013 Session-token guard in `api/internal/auth/sftp_guard.go` (pattern of `MCPTokenGuard` in `api/internal/auth/mcptokens.go`): only the routes listed in contracts/api.md, `cluster` forced to the token's cluster; wire into the auth chain in `api/cmd/main.go`.
- [ ] T014 Audit source: `api/internal/audit/audit.go` writes `source=sftp` for requests authenticated by a session token and adds `source` to the webhook and S3 payloads (`api/internal/audit/s3.go`) as an additive field.
- [ ] T015 File-access helper `api/internal/rbac/fileaccess.go` per contracts/api.md and research R8: `VisibleServers`, `Allow`, `FilterEntries`; today's implementation wraps `servers:read`, `servers:write` and the ownership/collaboration fallback from `api/internal/rbac/rbac.go`; rename checks delete on the source plus create/write on the destination. Move the existing Files tab routes in `api/internal/ws/dialer.go` onto it so no file route tests a permission key itself. Tests in new `api/internal/rbac/fileaccess_test.go` (matrix: read-only, write, owner, collaborator, none, other namespace, remote cluster).
- [ ] T016 API file routes in `api/internal/ws/dialer.go`: mount `files/stat`, `files/rename` and `files/stream`, each authorized through the T015 helper; exempt `files/stream` from the body limit in `api/cmd/main.go` and apply `files.maxUploadSize`; add the three to the gateway allow-list and mark `stream` long-running in `api/internal/gatewayprotocol/` and `api/internal/gateway/server.go`.
- [ ] T017 `POST /sftp/v1/login` and `POST /sftp/v1/sessions/check` in `api/internal/handlers/sftp_login.go` per contracts/api.md: service-token auth, split at the last `+`, target checked with the T015 helper (`Allow` list), generic 401 (`server.sftp.login_failed`), shared login rate limiter from `api/internal/auth/ratelimit.go`, audit rows for success and failure.
- [ ] T018 Self-service credential routes `GET /users/me/sftp`, `POST|DELETE /users/me/sftp/keys[/{id}]`, `POST|DELETE /users/me/sftp/password` in `api/internal/handlers/sftp.go`, mounted from `MountUsers`; refused for session-token and MCP-token requests.
- [ ] T019 API tests in new files: `api/internal/auth/sftp_test.go` (key types, password alphabet and length distribution, hash, token digests, epoch), `api/internal/auth/sftp_guard_test.go` (allow-list, cluster forcing), `api/internal/audit/audit_source_test.go`, `api/internal/handlers/sftp_login_envtest_test.go` (login matrix, rate limit, generic refusals), migration test on SQLite and PostgreSQL.
- [ ] T020 [P] API client in `sftp/internal/apiclient/client.go`: login, session check, `GET /sftp/v1/servers`, `files/list`, `files/stat`, `files/download` with `Range`, `files/stream`, `files/rename`, `files/mkdir`, `files/delete`; API errors mapped to SFTP status codes; optional CA bundle.
- [ ] T021 SSH server in `sftp/internal/sshserver/server.go` and `sftp/cmd/main.go`: host key from a mounted Secret file, password and public-key callbacks that call `/sftp/v1/login`, SFTP subsystem only (no shell, exec, port forwarding or SCP, FR-004), idle timeout, per-user session cap, 30 s `sessions/check` loop closing revoked sessions, Prometheus metrics (sessions, logins by result, bytes) (FR-022).
- [ ] T022 Minimal chart template `charts/gameplane/templates/sftp.yaml` and `values.yaml` keys `sftp.enabled` (false), `sftp.port` (2222), `sftp.image`: Deployment, ClusterIP Service, host key Secret generated once (`lookup`, `helm.sh/resource-policy: keep`), local service token Secret shared with the API; NetworkPolicy in `charts/gameplane/templates/networkpolicies.yaml` for SFTP ingress and egress to the API only.
- [ ] T023 Add the `sftp` submodule: `.gitmodules` entry with `component = true`, path filters for `hack/submodules.sh check`, `IMAGES += sftp` and `image-sftp` in `Makefile`, release image list and cosign signing.

**Checkpoint**: an SFTP server deploys, logs a seeded user in, and can call the API; user story phases can start.

---

## Phase 3: User Story 1 - Browse and download server files (Priority: P1) 🎯 MVP

**Goal**: a user connects to one cluster address and browses and downloads the files of every server they can read.

**Independent Test**: give a user `servers:read` on server A only; add a key through `POST /users/me/sftp/keys`; connect: the root lists only A; download a 1 GiB file from A, interrupt and resume, checksums match (quickstart §4, §5).

### Tests for User Story 1

- [ ] T024 [P] [US1] Component tests in `sftp/internal/vfs/read_test.go` against an `httptest` API: tree `/<server>` and `/_namespaces/<ns>/<server>`, per-server login root, dotfiles absent, root and server folders read-only, download from an offset.
- [ ] T025 [P] [US1] Component tests in `sftp/internal/sshserver/server_test.go`: shell, exec and forwarding refused; revoked session closed after the check loop; session cap.

### Implementation for User Story 1

- [ ] T026 [US1] Read-side VFS in `sftp/internal/vfs/vfs.go` implementing `pkg/sftp` `FileLister` and `FileReader`: root from `GET /sftp/v1/servers` (the helper's `VisibleServers`) (default games namespace at the top, others under `/_namespaces/`), per-server login rooted at one server, `Stat`/`Lstat` via `files/stat`, `ReadAt` via ranged `files/download`; mkdir/rename/remove at the root or a server folder return permission denied (FR-012).

**Checkpoint**: US1 works on its own against a running server.

---

## Phase 4: User Story 2 - Upload, edit, rename and delete (Priority: P1)

**Goal**: users with write access change files over SFTP and from the dashboard with large, resumable uploads (OD-6).

**Independent Test**: as a user with `servers:write` on A and `servers:read` on B: upload 2 GiB to A with an interrupt and resume, WinSCP-style temp-and-rename, mkdir, rename, delete all succeed and show in the Files tab and audit log with source `sftp`; the same on B is refused and B is unchanged (quickstart §6, §7).

### Tests for User Story 2

- [ ] T027 [P] [US2] Component tests in `sftp/internal/vfs/write_test.go`: sequential write streams once, first write at offset N resumes, out-of-order write returns `SSH_FX_OP_UNSUPPORTED`, temp-and-rename, `SETSTAT` mode/time no-op and owner refused, write on a read-only server refused, transfer cap.
- [ ] T028 [P] [US2] Envtest `api/internal/handlers/files_stream_envtest_test.go`: stream, rename and stat through the API with read-only, write, owner and collaborator users; body limit; audit `source`.

### Implementation for User Story 2

- [ ] T029 [US2] Write-side VFS in `sftp/internal/vfs/write.go` implementing `FileWriter` and `FileCmder`: one `files/stream` call per open handle fed by a pipe, resume on a first write at offset N > 0, rename (incl. `posix-rename@openssh.com`), mkdir, remove, rmdir, `SETSTAT` per FR-017, per-user transfer cap.
- [ ] T030 [US2] Dashboard streaming upload (FR-018b) after T004 sign-off: `web/src/routes/tabs/Files.tsx` uploads each file with `PUT files/stream` using `XMLHttpRequest` upload progress, per-file progress and cancel per the design; `uploadStream` in `web/src/lib/api.ts`; i18n strings in the web pack.
- [ ] T031 [US2] Vitest `web/src/routes/tabs/Files_stream.test.tsx`: progress, cancel, error states, size-limit message.
- [ ] T032 [US2] Helm `files.maxUploadSize` (default `20Gi`) in `charts/gameplane/values.yaml`, passed to the operator (agent flag `--max-upload-bytes`, built in `buildAgentContainer` in `operator/internal/controller/gameserver_controller.go`), the API and the gateway; keep the multipart `files/upload` route for one release.

**Checkpoint**: US1 and US2 both work; P1 scope is shippable.

---

## Phase 5: User Story 3 - Manage personal SFTP credentials (Priority: P2)

**Goal**: every user, SSO included, manages their own SSH keys and SFTP password in the dashboard; admins can revoke.

**Independent Test**: as an SSO user, add a key and generate a password in the profile, connect with each; revoke both: an open session closes within 60 s and new logins fail; an admin removes another user's key (quickstart §3, §9).

### Tests for User Story 3

- [ ] T033 [P] [US3] Envtest `api/internal/handlers/sftp_envtest_test.go`: add/remove keys, duplicate fingerprint across users refused, password shown once, regenerate invalidates the old one, admin list and revoke, user disable and password reset revoke sessions, session-token and MCP-token requests refused.

### Implementation for User Story 3

- [ ] T034 [US3] Admin routes `GET /users/{id}/sftp`, `DELETE /users/{id}/sftp/keys/{keyId}`, `DELETE /users/{id}/sftp/password` in `api/internal/handlers/sftp.go`, mounted with `users:manage` RBAC in `api/internal/rbac/`.
- [ ] T035 [US3] Personal SFTP page after T004 sign-off: new `web/src/routes/ProfileSftp.tsx` at `/settings/sftp` (next to `/settings/theme`), registered in `web/src/router/tree.tsx` and linked where the design places it; API methods in `web/src/lib/api.ts`; i18n strings.
- [ ] T036 [US3] Admin SFTP panel on `web/src/routes/Users.tsx` per the design.
- [ ] T037 [US3] Vitest `web/src/routes/ProfileSftp.test.tsx` and `web/src/routes/Users_sftp.test.tsx`: add/remove key, generate with length picker, one-time display, regenerate and revoke confirms, admin revoke.

**Checkpoint**: US3 works with US1/US2 credentials replaced by real dashboard-managed ones.

---

## Phase 6: User Story 4 - Admin turns SFTP on and exposes it (Priority: P2)

**Goal**: one Helm switch per cluster, configurable exposure, a stable host key, and remote clusters served through the central API (OD-3, OD-5).

**Independent Test**: default install renders no SFTP objects; enabling with a LoadBalancer on 2222 lets a client connect; on a registered remote cluster, a second endpoint lists only that cluster's servers with the same credentials (quickstart §1, §2, §10).

### Tests for User Story 4

- [ ] T038 [P] [US4] Chart checks in new `charts/gameplane/tests/sftp.sh` (pattern of `charts/gameplane/tests/mcp-server-rbac.sh`): off renders nothing; on renders Deployment, Service (ClusterIP, NodePort, LoadBalancer variants), host key Secret with keep policy, NetworkPolicy.
- [ ] T039 [P] [US4] Envtest `api/internal/handlers/sftp_cluster_envtest_test.go`: create/revoke service tokens with `cluster:manage`, endpoint address and fingerprint per cluster, a remote service token cannot list another cluster.

### Implementation for User Story 4

- [ ] T040 [US4] Chart exposure and remote mode in `charts/gameplane/templates/sftp.yaml` and `values.yaml`: `sftp.serviceType`, `sftp.nodePort`, `sftp.loadBalancerIP`, `sftp.publicAddress`, `sftp.apiURL`, `sftp.apiCASecret`, `sftp.serviceTokenSecret`, `sftp.maxSessionsPerUser` (4), `sftp.maxTransfersPerUser` (8); render on a gateway-only install.
- [ ] T041 [US4] `POST /clusters/{name}/sftp-token`, `DELETE /clusters/{name}/sftp-token/{id}` and `PUT /clusters/{name}/sftp` in `api/internal/handlers/sftp_cluster.go`; local endpoint address and fingerprint read from Helm values and the host key Secret.
- [ ] T042 [US4] Clusters SFTP dialog after T004 sign-off in `web/src/routes/Clusters.tsx`, API methods in `web/src/lib/api.ts`, Vitest `web/src/routes/Clusters_sftp.test.tsx`.

**Checkpoint**: admins can roll SFTP out to every cluster.

---

## Phase 7: User Story 5 - Edit files of a stopped or sleeping server (Priority: P2)

**Goal**: files of a stopped or asleep server are reachable through a short-lived file-only pod, over SFTP and in the Files tab, without waking the game (OD-4).

**Independent Test**: stop A, list it over SFTP: a `gameplane-files` pod serves it, A stays stopped, no player port opens; idle past the timeout removes the pod; start A while files are open: SFTP gets "server is starting" and A starts after the pod is gone; an asleep server is not woken (quickstart §8).

### Tests for User Story 5

- [ ] T043 [P] [US5] Operator envtest `operator/internal/controller/gameserver_files_envtest_test.go`: pod created only while replicas are 0 and the annotation is fresh, `<name>-agent` selector switched both ways, idle teardown, start while active waits for deletion, asleep server not woken, server delete removes the pod, no host ports.
- [ ] T044 [P] [US5] API test `api/internal/ws/ensure_files_test.go`: annotation patched at most once a minute, 60 s wait, `503 server.files.starting`.

### Implementation for User Story 5

- [ ] T045 [US5] Agent files-only mode flag `--files-only` in `agent/cmd/main.go`: mount only `/files/*`, `/healthz` and metrics.
- [ ] T046 [US5] Reconciler `operator/internal/controller/gameserver_files.go`: file-only Pod (labels `app.kubernetes.io/name: gameplane-files`, `app.kubernetes.io/instance: <name>`, server volumes, agent container from `buildAgentContainer` with `--files-only`, owned by the GameServer, no host ports) while replicas are 0 and `gameplane.local/files-requested-at` is younger than `--files-idle-timeout`; switch the `<name>-agent` Service selector in `reconcileAgentService`; delete on idle or server delete; flag in `operator/cmd/main.go`, Helm `operator.filesIdleTimeout` (`10m`).
- [ ] T047 [US5] Start ordering in `desiredReplicas` in `operator/internal/controller/gameserver_controller.go`: delete the file-only pod and requeue until it is gone before scaling up; file access never sets a wake.
- [ ] T048 [US5] NetworkPolicy in `charts/gameplane/templates/networkpolicies.yaml` allowing API, operator and gateway ingress to `gameplane-files` pods on 8090 only.
- [ ] T049 [US5] Ensure-files in the API file routes in `api/internal/ws/dialer.go` (new helper `api/internal/ws/ensure_files.go`): when the server is stopped or asleep, patch the annotation, wait up to 60 s for the agent Service, otherwise `503 server.files.starting`; works for remote clusters through the registry client.
- [ ] T050 [US5] Files tab "files are starting" state after T004 sign-off in `web/src/routes/tabs/Files.tsx`, Vitest `web/src/routes/tabs/Files_starting.test.tsx`.

**Checkpoint**: all five stories work independently.

---

## Phase 8: Polish & Cross-Cutting Concerns

- [ ] T051 [P] E2E bucket `sftp` in new `test/e2e/sftp_e2e_test.go`, registered in `test/e2e/buckets.sh`: chart off, connect, tree and per-server login, 2 GiB upload/download with resume, permission matrix, audit `source`, revoke within 60 s, stopped server; `t.Parallel()`, unique names, at most 3 admin logins.
- [ ] T052 [P] Remote endpoint E2E in new `test/e2e/multicluster_sftp_e2e_test.go` (bucket `multicluster`).
- [ ] T053 [P] Docs: new `docs/sftp.md` (enable, connect with FileZilla/WinSCP/`sftp`, host key check, credentials, per-server login, remote clusters, stopped servers), threat-model entry in `docs/security.md`, component in `docs/architecture.md`, `sftp/specs.md` and `sftp/README.md`, `operator` and `api` docs for the annotation and routes.
- [ ] T054 Changelog `changelog/unreleased/<PR>-sftp-access.md` in the chart PR per `changelog/unreleased/README.md` (new Helm values `sftp.*`, `files.maxUploadSize`, `operator.filesIdleTimeout`).
- [ ] T055 Run every scenario in `specs/040-sftp-access/quickstart.md` on a dev cluster and record results in the chart PR.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: T001 first; T002, T003, T004 then run in parallel. T004 sign-off gates every web task (T030, T035, T036, T042, T050).
- **Foundational (Phase 2)**: needs T001–T003. Agent tasks T005–T009 → API tasks T010–T019 → sftp repo T020–T021 → chart and submodule T022–T023. Blocks every story.
- **US1 (Phase 3)** and **US2 (Phase 4)**: after Phase 2. US2's VFS (T029) extends US1's (T026).
- **US3 (Phase 5)**: after Phase 2; independent of US1/US2 except for manual testing.
- **US4 (Phase 6)**: after Phase 2.
- **US5 (Phase 7)**: after Phase 2; T049 builds on T016.
- **Polish (Phase 8)**: after the stories it covers.

### User Story Dependencies

- US1 (P1): none beyond Foundational.
- US2 (P1): T029 builds on T026's VFS type.
- US3, US4, US5 (P2): none on other stories.

### Within Each User Story

- Tests are written alongside implementation in the same PR; CI must be green on both PRs before they merge (sftp repo PR first).
- API before web; design sign-off before web.

### Parallel Opportunities

- Phase 2: T005, T006, T007 (agent files) in parallel; T020 parallel to API work once contracts are fixed.
- Test tasks marked [P] inside each story.
- After Phase 2, US3, US4 and US5 can proceed in parallel with US1/US2 (different packages: `api/internal/handlers/sftp*.go`, `charts/`, `operator/`).

---

## Parallel Example: Phase 2 agent work

```text
Task: "Agent GET /files/stat in agent/internal/files/stat.go"            (T005)
Task: "Agent POST /files/rename in agent/internal/files/rename_linux.go" (T006)
Task: "Agent PUT /files/stream in agent/internal/files/stream_linux.go"  (T007)
```

## Parallel Example: User Story 5

```text
Task: "Operator envtest gameserver_files_envtest_test.go" (T043)
Task: "API test ensure_files_test.go"                     (T044)
```

---

## Implementation Strategy

### MVP first (User Story 1)

1. Phase 1 and Phase 2.
2. Phase 3 (US1): read-only SFTP for running servers.
3. Stop and validate with quickstart §4 and §5.

### Incremental delivery

1. Setup + Foundational → deployable server.
2. US1 → read-only browsing (MVP).
3. US2 → writes and large uploads for SFTP and the dashboard.
4. US3 → self-service credentials UI.
5. US4 → exposure and remote clusters.
6. US5 → stopped and sleeping servers.
7. Polish → E2E, docs, changelog, quickstart run.

Each step is one or more commits in the Gameplane PR (the sftp server in its repo PR); no step breaks the previous ones.
