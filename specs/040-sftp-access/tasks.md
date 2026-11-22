# Tasks: SFTP access to game servers

**Input**: Design documents from `/specs/040-sftp-access/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/api.md. OD-1 to OD-6 settled by valgul on 2026-11-22. OD-7 settled A (new repository `GameplanePanel/sftp`) the same day. No decision is open. No implementation starts while any decision is open.

**Tests**: Required (Constitution I and coverage gates: agent 90, api 80, operator 72, web 92/76/82/92, new component `.testcoverage.yml`). Every test task adds **new** test functions or files. Changing an **existing** test needs valgul's written sign-off from T002 first (CLAUDE.md override 1). Tests run in CI only; locally only `go build ./...` (`GOWORK=off` in the component) and `npx tsc --noEmit`.

**Organization**: by user story in spec priority order: US1, US2 (P1), US3, US4, US5 (P2).

**Delivery** (plan.md): stacked PRs: agent `feat/040-sftp-agent`, operator `feat/040-sftp-files-pod`, API `feat/040-sftp-api`, component repo `GameplanePanel/sftp` `feat/initial-server`, design `design/040-sftp`, web `feat/040-sftp-ui`, chart/docs/E2E `feat/040-sftp-chart`. Implementation through `Workflow` scripts with explicit models (haiku first), one diff review per batch one tier up.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task).
- Paths starting `sftp/` are in the GameplanePanel/sftp repository (OD-7 A).

---

## Phase 1: Setup

- [ ] T001 Confirm the spec docs PR is merged to `master`; create the branches in **Delivery** from fresh `master`; record base SHAs in each PR description.
- [ ] T002 List every existing test that encodes behaviour this spec changes (file, test, assertion, needed change) and get valgul's written sign-off before editing any, in `specs/040-sftp-access/T002-test-signoff.md`. Candidates: `agent/internal/files/*_test.go` (route table, upload caps), `operator/internal/controller/*agent*_test.go` and envtests asserting the `<name>-agent` Service selector, `api/internal/gateway/*_test.go` (operation allow-list), `api/internal/audit/*_test.go` (row shape, webhook/S3 payload), `api/internal/rbac/*_test.go` (route rules), `charts` template checks, `test/e2e/optional_components_e2e_test.go`.
- [ ] T003 (OD-7 A) Once valgul has created `GameplanePanel/sftp`: scaffold it like `GameplanePanel/mcp` (`go.mod`, `.github/workflows/ci.yaml` calling `go-component.yaml` pinned by SHA, `.testcoverage.yml`, `CLAUDE.md`, `AGENTS.md`, `specs.md`, `Dockerfile` copying `sftp/…` from the Gameplane build context).

## Phase 2: Foundational (blocks every story)

- [ ] T004 [P] Agent `GET /files/stat` in `agent/internal/files/stat.go`, rooted like `list`, dotfiles refused, returns `Entry`; mount in `files.go` bounded group.
- [ ] T005 [P] Agent `PUT /files/stream` in `agent/internal/files/stream_linux.go`: raw body, `offset` query (0 or ≤ existing size, copy prefix), temp file in the target dir, free-space check (`statfs`) before writing, limit from flag `--max-upload-bytes` (OD-6, Helm `files.maxUploadSize`, default 20 GiB, wired by the operator), fsync + rename commit on success, temp removed on any failure (FR-016); long-running group.
- [ ] T006 [P] Agent `POST /files/rename` in `agent/internal/files/rename_linux.go` using `renameat` on directory fds opened by the rooted walker; refuses root, dotfiles, `overwrite=false` onto an existing name.
- [ ] T007 Tests for T004–T006 in new files `stat_test.go`, `stream_linux_test.go`, `rename_linux_test.go`: escapes via `..` and symlink swaps (use `beforeOpen` seam), resume prefix copy, cancel mid-stream leaves old file intact, free-space refusal, size limit.
- [ ] T008 Update `agent/specs.md` with the three routes and their invariants.
- [ ] T009 Migration `api/internal/db/migrations/common/024_sftp_credentials.sql` per data-model.md (four tables, `audit_events.source`), portable SQL; `DeleteUserTx` deletes the user's SFTP rows.
- [ ] T010 API store and auth in `api/internal/auth/sftp.go`: key parse/normalize/fingerprint (allowed types per data-model), password generate (crypto/rand, uniform over the 94 printable ASCII chars, length 20–40) and argon2id hash, service tokens (`gpsftps_`), session tokens (`gpsftp_`, 15 min sliding), `session_epoch` binding, revoke on key delete/password regenerate/user disable.
- [ ] T011 Session-token guard (like `MCPTokenGuard`) allowing only the routes in contracts/api.md and forcing `cluster` to the token's cluster; audit middleware sets `source=sftp`; webhook/S3 payload gains `source`.
- [ ] T012 API mounts `files/stat`, `files/rename`, `files/stream` in `api/internal/ws/dialer.go` with the read/write RBAC of `read`/`write`; body-limit exemption for `stream` in `api/cmd/main.go`; gateway protocol allows the three operations, `stream` long-running with the OD-6 limit (`api/internal/gatewayprotocol`, `api/internal/gateway/server.go`).
- [ ] T013 Tests for T009–T012 (new files): migration on SQLite and PostgreSQL, password alphabet/length distribution, key types refused/accepted, guard allow-list, cluster forcing, audit `source`.

## Phase 3: US1 Browse and download (P1)

- [ ] T014 [US1] `POST /sftp/v1/login` and `POST /sftp/v1/sessions/check` in `api/internal/handlers/sftp_login.go`: username split at last `+`, target `servers:read` check, generic 401, shared login rate limiter, audit rows.
- [ ] T015 [US1] Envtest `api/internal/handlers/sftp_login_envtest_test.go`: permission matrix (read-only, write, owner, collaborator, none, other namespace, remote cluster service token), target login refusal identical to wrong password.
- [ ] T016 [P] [US1] `sftp/internal/apiclient`: login, session check, server list, list/stat/download (Range) calls with the session token; errors mapped to SFTP status codes.
- [ ] T017 [US1] `sftp/internal/vfs`: `pkg/sftp` `Handlers` for the tree (`/<server>`, `/_namespaces/<ns>/<server>`) and the per-server login root; root and server folders read-only (FR-012); download with offset.
- [ ] T018 [US1] `sftp/internal/sshserver` and `sftp/cmd/main.go`: host key from Secret file, password and public-key auth via the API, SFTP subsystem only (no shell/exec/forwarding, FR-004), per-user session cap, idle timeout, 30 s session check closing revoked sessions, metrics (FR-022).
- [ ] T019 [US1] Component tests against an `httptest` API: tree listing, per-server login, refused shell/exec, revoked session closed, resume download.

## Phase 4: US2 Upload, edit, rename, delete (P1)

- [ ] T020 [US2] `sftp/internal/vfs` writes: one `stream` call per open handle; first write at offset N > 0 resumes; out-of-order write → `SSH_FX_OP_UNSUPPORTED`; rename, mkdir, remove, rmdir; `SETSTAT` mode/times no-op, owner refused (FR-017); per-user transfer cap.
- [ ] T021 [US2] Component tests: WinSCP-style temp-and-rename, interrupted upload resume, write refused on read-only server, top-level folder changes refused.

## Phase 5: US3 Personal SFTP credentials (P2)

- [ ] T022 [US3] Routes in `api/internal/handlers/sftp.go` per contracts/api.md (self and admin), mounted with RBAC; session-token requests refused.
- [ ] T023 [US3] Envtest `api/internal/handlers/sftp_envtest_test.go`: add/remove keys, duplicate fingerprint refused, password shown once and regenerate invalidates old, admin revoke, user disable ends sessions.
- [ ] T024 [US3] Design in `design.fig`: profile SFTP section (address, port, username forms, host key fingerprint, keys list, add key dialog, generate password dialog with length 20–40 and one-time display), admin SFTP panel on Users, cluster SFTP token dialog, Files tab upload progress and cancel for large files (OD-6), Files tab "files are starting" state; run `hack/design/fig-export.ts`; get valgul's sign-off.
- [ ] T025 [US3] Web after T024 sign-off: profile section, Users admin panel, `web/src/lib/api.ts` methods, i18n strings, Vitest tests.

## Phase 6: US4 Admin enables SFTP (P2)

- [ ] T026 [US4] Chart `charts/gameplane/templates/sftp.yaml`: Deployment, Service (type/port values), host key Secret generated once (lookup, `helm.sh/resource-policy: keep`), local service token Secret, values `sftp.*` (enabled false, port 2222, serviceType, publicAddress, apiURL, apiCASecret, serviceTokenSecret, limits) and `files.maxUploadSize` (default 20Gi, used by operator, API and gateway); NetworkPolicy allowing SFTP ingress and egress to the API.
- [ ] T027 [US4] `POST /clusters/{name}/sftp-token`, `PUT /clusters/{name}/sftp` and the Clusters page dialog (design from T024).
- [ ] T028 [US4] Submodule `sftp` (`.gitmodules` `component = true`, `hack/submodules.sh` path filters), `Makefile` `IMAGES += sftp`, release image list and signing.
- [ ] T029 [US4] E2E bucket `sftp` in `test/e2e/sftp_test.go` registered in `test/e2e/buckets.sh`: chart off renders nothing; on: connect, list, 2 GiB upload/download with resume, permission matrix, audit `source`, revoke within 60 s; `multicluster` addition for a remote endpoint.

## Phase 7: US5 Stopped or sleeping servers (P2)

- [ ] T030 [US5] Operator `operator/internal/controller/gameserver_files.go`: file-only pod (labels `gameplane-files`, server volumes, agent container in files-only mode, no game container, no host ports, owned by the GameServer) while replicas are 0 and `files-requested-at` is younger than `--files-idle-timeout`; Service selector switch; deletion on idle or server delete.
- [ ] T031 [US5] Start ordering in `desiredReplicas`: delete the file-only pod and wait for it to be gone before scaling up; never wake on file access.
- [ ] T032 [US5] Agent files-only mode flag (only `/files/*` and `/healthz` mounted).
- [ ] T033 [US5] API ensure-files in the file routes for stopped/asleep servers: patch the annotation (at most once a minute), wait up to 60 s, `503 server.files.starting`.
- [ ] T034 [US5] NetworkPolicy for `gameplane-files` pods (API, operator, gateway → 8090 only).
- [ ] T035 [US5] Operator envtest `gameserver_files_envtest_test.go`: create/teardown, selector switch both ways, start while active, asleep server not woken, delete server; Files tab "files are starting" state (design from T024) and Vitest.

- [ ] T035a Files tab streaming upload (OD-6, FR-018b): `web/src/routes/tabs/Files.tsx` uploads each file with `PUT files/stream` and shows progress and cancel per the T024 design; `web/src/lib/api.ts` method; Vitest tests. Multipart route kept for one release.

## Phase 8: Polish

- [ ] T036 [P] Docs: `docs/sftp.md` (enable, connect with FileZilla/WinSCP/`sftp`, host key check, credentials, remote clusters), `docs/security.md` threat model entry, `docs/architecture.md`, `sftp/specs.md`; website docs page via the website repo if the docs list needs it.
- [ ] T037 Changelog `changelog/unreleased/<PR>-sftp-access.md` in the chart PR.
- [ ] T038 Run quickstart scenarios from the spec's user stories on a dev cluster and record results in the chart PR.
