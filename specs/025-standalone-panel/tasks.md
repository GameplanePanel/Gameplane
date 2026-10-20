---
description: "Task list for Feature 025: Standalone panel and API (retroactive, PR #630)"
---

# Tasks: Standalone Panel and API

**Input**: `specs/025-standalone-panel/` (spec.md, plan.md, research.md, data-model.md, contracts/, quickstart.md, OPEN-DECISIONS.md)

**Retroactive**: Tasks T001 to T049 were done by PR #630 (contributor Baylem, merge commit `6ed25c1`, 2026-10-14). Each names the files and the PR commits that did it (`git log 6ed25c1^1..6ed25c1^2`). T050 and T051 are bookkeeping: T050 records the merge, T051 is the follow-up documentation change on branch `claude/project-thread-gzh0dk`. No new work is listed; requirements the code does not fully meet are in [spec.md Known gaps](spec.md#known-gaps).

**Tests**: PR #630 added tests with each change; they are listed in the task that introduced them.

**Format**: `[ID] [P?] [Story] Description`

## Phase 1: Setup

- [X] T001 Add migration `api/internal/db/migrations/common/016_standalone_management.sql` (table `management_objects`) — `0182e16`
- [X] T002 Add migration `api/internal/db/migrations/common/017_standalone_key_state.sql` (table `management_key_state`) — `c01a963`

## Phase 2: Foundational

- [X] T003 Define the management storage interface (`SecretStore`, `ClusterStore`, `ClusterRegistrar`, `Client.Secrets`, `Client.Clusters`, `Client.IsStandalone`) in `api/internal/kube/management.go` and `api/internal/kube/client.go`; route handlers, `api/internal/auth/registry.go` and `api/internal/registry/keys.go` through it — `0182e16`, `c01e3bb`, `3d8a62e`
- [X] T004 Implement the SQL management store with compare-and-swap, label selectors, continue tokens and dry-run refusal in `api/internal/controlplane/store.go` and `api/internal/controlplane/resources.go`; tests `store_test.go`, `store_postgres_test.go` — `0182e16`, `9f698da`, `3035757`
- [X] T005 Separate management clients from workload clients and keep a management backend apart from the default cluster in `api/cmd/management.go` and `api/internal/kube/registry.go` (`Registry.SetManagement`) — `0182e16`, `3d8a62e`
- [X] T006 Run `./internal/controlplane/...` tests against PostgreSQL in the `api-postgres` job of `.github/workflows/ci.yaml` — `9f698da`, `b73bbbc`

## Phase 3: User Story 1 - Run a panel with no local cluster (P1)

**Goal**: The API and dashboard start and work with no Kubernetes on the host.

**Independent Test**: CI `standalone-compose` job (`deploy/standalone/smoke.py`): bootstrap, login, empty clusters and fleet, restart.

- [X] T007 [US1] Add `--standalone` / `GAMEPLANE_STANDALONE` and refuse cluster-ops and local credential minting with it in `api/cmd/main.go` and `api/cmd/management.go`; test `api/cmd/management_test.go` — `0182e16`, `3d8a62e`, `6564774`
- [X] T008 [P] [US1] Omit the synthetic `local` cluster from `/clusters`, fleet candidates and `/cluster` inventory in `api/internal/handlers/clusters.go`, `fleet.go`, `cluster.go`; test `standalone_test.go` — `0182e16`, `28dd876`
- [X] T009 [P] [US1] Return `501` with container-runtime guidance from Kubernetes system logs in standalone in `api/internal/handlers/systemlogs.go` — `0182e16`
- [X] T010 [P] [US1] Exclude the synthetic local cluster from the telemetry cluster count in `api/internal/telemetry/collect.go` — `0182e16`
- [X] T011 [US1] Add the Docker Compose profile with only `gameplane-api` and `web`, separate `panel-data` and `panel-keys` volumes and a hardened API container in `deploy/standalone/compose.yaml`; pre-create `/data` and `/keys` for the non-root user in `api/Dockerfile` — `0182e16`, `3d8a62e`, `7e6d247`
- [X] T012 [US1] Add the Compose smoke test `deploy/standalone/smoke.py` and the `standalone-compose` CI job with its `standalone` path filter in `.github/workflows/ci.yaml` — `0182e16`, `9f698da`, `a4cb5fe`

**Checkpoint**: A Compose panel starts, logs in and persists with zero clusters.

## Phase 4: User Story 2 - Register, rotate and remove remote clusters (P1)

**Goal**: Remote clusters are registered with encrypted credentials, checked for health, rotated and removed safely.

**Independent Test**: `smoke.py` registration, ciphertext snapshot, kubeconfig replacement, delete and re-register; handler and kube unit tests.

- [X] T013 [US2] Encrypt credentials with AES-256-GCM, the `gpk1.<keyID>.` header and record-identity associated data in `api/internal/controlplane/key_ciphertext.go` and `store.go`; tests `store_test.go` — `0182e16`, `9f698da`, `c01a963`
- [X] T014 [US2] Commit standalone registration and its credential in one transaction, adopting only an exact API-owned orphan, in `api/internal/controlplane/registrations.go` and `api/internal/handlers/clusters.go`; tests `registrations_test.go`, `clusters_registration_test.go`, `standalone_storage_test.go` — `0182e16`, `c01e3bb`, `c01a963`
- [X] T015 [US2] Validate standalone kubeconfigs (HTTPS, verified TLS, no proxy, no userinfo, safe destination) and refuse redirects in `api/internal/kube/remote_access.go` and `loader.go`; tests `remote_access_test.go`, `clusters_remote_policy_test.go` — `3d8a62e`, `cc21cfa`
- [X] T016 [US2] Add dial-time destination checks with an optional `--remote-allowed-cidrs` allowlist in `api/internal/kube/remote_access.go`, `api/cmd/main.go`, `api/cmd/management.go` — `3d8a62e`, `cc21cfa`
- [X] T017 [US2] Bound decompressed responses (8 MiB, 64 KiB for `/version`) and watch frames (1 MiB) and reject unsolicited upgrades in `api/internal/kube/remote_response.go`; test `remote_response_test.go` — `8d94492`, `ca65c90`, `a42796f`, `cc21cfa`
- [X] T018 [US2] Sequence registry updates per cluster and reload credentials by UID in `api/internal/kube/registry.go`, `watch.go`, `loader.go`; test `watch_rotation_test.go` — `55f1bcf`, `cc21cfa`, `a0dc7c4`
- [X] T019 [US2] Poll remote health every 30 s and persist a fixed status projection in `api/internal/kube/watch_standalone.go`; tests `watch_standalone_test.go`, `api/internal/controlplane/remote_health_fixture_test.go` — `0182e16`, `8d94492`, `0828690`, `cc21cfa`
- [X] T020 [US2] Replace a registered kubeconfig in place (`PUT /clusters/{name}/kubeconfig`) preserving gateway and grants in `api/internal/handlers/clusters_kubeconfig.go`; tests `clusters_kubeconfig_test.go`, `credential_review_test.go` — `55f1bcf`, `ca5cbac`, `6564774`
- [X] T021 [US2] Configure and remove agent-gateway credentials (`PUT`/`DELETE /clusters/{name}/gateway`) in `api/internal/handlers/clusters_gateway.go`; test `clusters_gateway_test.go` — `28dd876`, `67fd660`, `6564774`
- [X] T022 [US2] Remove registrations and API-owned credentials with UID and version preconditions, keeping unrelated Secret keys, in `api/internal/handlers/clusters.go` and `secrets_managed.go`; test `secrets_managed_test.go` — `0182e16`, `ca5cbac`, `ca0f49b`
- [X] T023 [US2] Add the dashboard register and remove dialogs and empty states in `web/src/components/RegisterClusterDialog.tsx`, `web/src/routes/Clusters.tsx`, `web/src/lib/endpoints.ts`; test `web/src/routes/Standalone.test.tsx` — `0182e16`, `3741ae5`, `be6f0e6`

**Checkpoint**: Registration, rotation and removal work with encrypted storage.

## Phase 5: User Story 3 - Work on remote clusters from the dashboard (P2)

**Goal**: Every module, inventory and agent request names an explicit registered cluster and respects grants on it.

**Independent Test**: `web/src/routes/Standalone.test.tsx`, `api/internal/handlers/modules_cluster_test.go`, `api/internal/auth/perms_test.go`.

- [X] T024 [US3] Add the authenticated `GET /admin/installation` in `api/internal/handlers/installation.go`, `api/cmd/main.go`, `api/internal/rbac/rbac.go`; test `installation_test.go` — `0182e16`, `9f698da`, `6564774`
- [X] T025 [US3] Route standalone `/modules` to one explicit registered cluster in `api/internal/handlers/modules_cluster.go`; test `modules_cluster_test.go` — `0182e16`
- [X] T026 [US3] Check modules and templates permissions as cluster-wide grants on the target, and allow them for remote bindings, in `api/internal/auth/perms.go`, `api/internal/rbac/rbac.go`, `api/internal/handlers/binding_permissions.go`, `fleet.go`; tests `perms_test.go`, `fleet_test.go` — `0182e16`
- [X] T027 [US3] Guard pod exec, attach and stdin with a policy-aware SPDY executor in `api/internal/kube/remote_spdy.go`, `stdin.go`, `api/internal/ws/attach.go`; test `remote_spdy_test.go` — `a42796f`, `cda9658`, `068eb4c`, `cc21cfa`
- [X] T028 [US3] Apply the destination guard to agent-gateway connections in `api/internal/ws/gateway_client.go`; tests `gateway_client_test.go`, `gateway_private_fixture_test.go` — `0182e16`, `3d8a62e`, `6564774`
- [X] T029 [US3] Take bounded per-cluster notification snapshots and tag events with a cluster in `api/internal/notify/poll_standalone.go`, `registry.go`, `watch.go`, `notify.go`, `format.go`; tests `poll_standalone_test.go`, `registry_test.go`, `format_test.go` — `0182e16`, `28dd876`, `8d94492`, `cc21cfa`
- [X] T030 [P] [US3] Read installation capabilities and drop the implicit `local` in `web/src/lib/useInstallation.ts`, `web/src/lib/cluster.ts`, `web/src/components/ClusterSelector.tsx`, `web/src/components/AppLayout.tsx`, `web/src/routes/Cluster.tsx`; tests `AppLayout.test.tsx`, `ClusterSelector.test.tsx`, `cluster.test.ts` — `0182e16`, `30a194f`, `3741ae5`
- [X] T031 [P] [US3] Target modules, sources, builder and uploads at an explicit cluster in `web/src/components/ModuleTarget.tsx`, `web/src/routes/Modules.tsx`, `web/src/components/modules/*`, `web/src/lib/endpoints.ts`, `web/src/lib/api.ts`; tests `moduleTargets.test.ts`, `ModuleCard.test.tsx`, `Modules.test.tsx` — `0182e16`, `30a194f`, `2e5e46a`, `3741ae5`
- [X] T032 [P] [US3] Adapt admin screens (backup destinations, About version, updates, system logs, notifications bell, user grants) in `web/src/routes/AdminSettings.tsx`, `AdminLogs.tsx`, `Users.tsx`, `web/src/components/ui/NotificationsPanel.tsx`; tests `AdminLogs.test.tsx`, `Users.test.tsx`, `NotificationsPanel.test.tsx` — `0182e16`, `30a194f`, `3741ae5`
- [X] T033 [US3] Design and export the 19 standalone frames in `design.pen` and `assets/design-export/` (MANIFEST section "Standalone panel (PR #630, 2026-10-08)") — `6eb7ac3`, `b73bbbc`

**Checkpoint**: The dashboard works with zero, one or many registered clusters.

## Phase 6: User Story 4 - Keep the panel key safe and rotate it (P2)

**Goal**: The key lives apart from the database, is checked before use, fails closed and can be rotated offline.

**Independent Test**: `api/internal/controlplane/key_*_test.go`, `api/cmd/panel_key_test.go`, `smoke.py` rotation and read-only provisioned-key run.

- [X] T034 [US4] Add `--panel-key-file` and `--panel-key-provisioned` in `api/cmd/main.go` and `api/internal/controlplane/key_options.go`; tests `key_options_test.go`, `api/cmd/management_test.go` — `0182e16`, `7e6d247`
- [X] T035 [US4] Validate key ownership, mode, type, size, links and ACLs on the opened inode in `api/internal/controlplane/key_file_unix.go`, `key_acl_linux.go`, `key_acl_unsupported.go`, `key_file_unsupported.go`; tests `key_file_unix_test.go`, `key_acl_linux_test.go`, `key_review_test.go` — `2414e1a`, `7bca550`, `3035757`
- [X] T036 [US4] Publish generated keys crash-durably under a lock file in `api/internal/controlplane/store.go`, `key_directory.go`, `key_publication_unix.go`; test `key_durability_test.go` — `6dffc99`, `076fa85`, `e0e3595`, `3035757`
- [X] T037 [US4] Fail closed on a missing or wrong key and lock key state on every credential write in `api/internal/controlplane/key_state.go`; tests `store_test.go`, `key_rotation_test.go` — `9f698da`, `c01a963`
- [X] T038 [US4] Add offline `api rotate-panel-key` with environment-only DSN in `api/cmd/panel_key.go`, `api/cmd/main.go`, `api/internal/controlplane/key_rotation.go`; tests `panel_key_test.go`, `key_rotation_test.go`, `key_rotation_postgres_test.go` — `c01a963`, `3035757`, `6564774`
- [X] T039 [US4] Return typed, payload-free credential errors in `api/internal/controlplane/credential_error.go`; test `key_review_test.go` — `3035757`
- [X] T040 [US4] Add the provisioned-key Compose overlay `deploy/standalone/compose.provisioned-key.yaml` — `7e6d247`

**Checkpoint**: Key custody and rotation work on SQLite and PostgreSQL.

## Phase 7: User Story 5 - Install a panel-only Helm release (P3)

**Goal**: A panel-only Helm profile next to unchanged combined and remote profiles.

**Independent Test**: `charts/tests/check_standalone.py` in the chart-template CI job.

- [X] T041 [US5] Add `operator.enabled` and guard every operator resource with `gameplane.operatorEnabled` in `charts/gameplane/values.yaml`, `templates/_helpers.tpl` and the operator templates — `0182e16`
- [X] T042 [US5] Add `api.standalone`, `api.panelKey.*` and `api.remoteAllowedCIDRs`, the key PVC and Secret mount, and standalone pod settings (`Recreate`, no service-account token) in `charts/gameplane/values.yaml`, `templates/api.yaml`, `templates/panel-key.yaml`; drop the service-account token from web pods in every profile in `templates/web.yaml` (see spec Known gaps) — `0182e16`, `7e6d247`, `391ca03`, `0ece9ae`, `3d8a62e`
- [X] T043 [US5] Fail invalid standalone renders in `charts/gameplane/templates/validate.yaml` — `0182e16`, `a4cb5fe`
- [X] T044 [US5] Add the panel-only example `charts/gameplane/examples/standalone-panel-values.yaml` and standalone `templates/NOTES.txt` — `0182e16`, `a4cb5fe`
- [X] T045 [US5] Add the chart matrix check `charts/tests/check_standalone.py` to the chart-template job in `.github/workflows/ci.yaml` — `0182e16`, `a4cb5fe`, `b73bbbc`

**Checkpoint**: Combined, remote-only and standalone profiles render; invalid combinations fail.

## Phase 8: Polish & Cross-Cutting Concerns

- [X] T046 [P] Write the operator guide `docs/standalone-panel.md` and update `docs/install.md`, `docs/security.md`, `docs/architecture.md`, `docs/gateway-install.md`, `docs/multicluster-*.md`, `README.md`, `CHANGELOG.md`, `deploy/standalone/README.md` — `a60c4d3`, `6952098`, `b73bbbc`
- [X] T047 [P] Update module specs `api/specs.md` and `web/specs.md` (Constitution IV) — `0182e16`, `b73bbbc`
- [X] T048 [P] Keep one-time share-link tokens across Settings refreshes in `web/src/routes/tabs/Settings.tsx`; test `Settings.test.tsx` (fix bundled in PR #630, not standalone-specific) — `834c3ca`
- [X] T049 Verify remote template writes need a remote grant in `test/e2e/multicluster_e2e_test.go` (combined-mode RBAC, not a standalone test) — `28dd876`
- [X] T050 CI green on PR #630 head `117caf35` (97 checks), human approval, merge into `master` as `6ed25c1` (2026-10-14)
- [X] T051 Renumber this folder from `specs/023-standalone-panel/` to `specs/025-standalone-panel/`, update `api/specs.md`, and complete the spec-kit artifacts (this change)

## Requirement Coverage

| Requirement | Tasks |
|---|---|
| FR-001 | T007, T034 |
| FR-002 | T008 |
| FR-003 | T003, T004, T005 |
| FR-004 | T013, T034, T042 |
| FR-005 | T034, T040 |
| FR-006 | T035, T036 |
| FR-007 | T037 |
| FR-008 | T002, T037, T038 |
| FR-009 | T014 |
| FR-010 | T018, T020 |
| FR-011 | T020, T021, T022, T039 |
| FR-012 | T015, T016, T027, T028 |
| FR-013 | T017, T029 |
| FR-014 | T019 |
| FR-015 | T028 |
| FR-016 | T025, T026, T027, T029, T030, T031, T032 |
| FR-017 | T024, T030 |
| FR-018 | T023, T030, T031 |
| FR-019 | T041 to T045 |
| FR-020 | T011, T040 |
| FR-021 | T042, T043 (Helm only; Compose does not enforce one container, see spec Known gaps) |
| SC-001 | T011, T012 |
| SC-002 | T012, T013, T018 |
| SC-003 | T037 |
| SC-004 | T038 |
| SC-005 | T043, T045 |
| SC-006 | T020, T021, T022, T039 |

## Dependencies & Execution Order

- Phase 1 and Phase 2 came first: every story needs the tables (T001, T002) and the storage interface (T003 to T005).
- US1 and US2 are both P1. US2 needs T013 (encryption) and US4's key loading (T034); in PR #630 they landed together in `0182e16`, then were hardened.
- US3 depends on US2 (it needs registered clusters). US4 hardens storage from US2. US5 is independent of the API work except for the flags it passes.

## Parallel Opportunities

- T008, T009, T010 touch separate handlers.
- T030, T031, T032 touch separate dashboard areas.
- T046, T047, T048 touch separate files.

## Implementation Strategy

As delivered: one large PR. The first commit `0182e16` built the MVP across US1, US2, US3 and US5; later commits hardened key custody (US4), remote access (US2), and the dashboard after review.

## Notes

- Every task is ticked and PR #630 is merged, so Constitution IV and CLAUDE.md rule 16 call for renaming this folder to `done_025-standalone-panel`. valgul decided to keep the `025-` name until a standalone E2E test exists, because Constitution I also requires one and there is none (OD-5, spec Known gaps).
- Follow-ups outside this feature: telemetry marker for standalone installs (OD-2); `--skip-crds` issue (OD-3).
