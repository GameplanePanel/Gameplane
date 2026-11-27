# Tasks: Downloadable backup archives

**Input**: Design documents from `specs/041-downloadable-backup-archives/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/api.md, quickstart.md. OD-1 to OD-7 settled by valgul on 2026-11-26; no decision is open.

**Tests**: Required (Constitution Principle I and the coverage gates: agent 90, api 80, operator 72, web 92/76/82/92). Test tasks add new test functions or files. Editing an existing test so it matches this spec needs no sign-off (valgul 2026-11-24), but every such edit gets a peer review from a fresh agent given only the test diff; deleting, weakening or bypassing a test needs valgul's written sign-off naming the change. Tests run in CI only; locally only `go build ./...` and `npx tsc --noEmit`.

**Organization**: US1 (download, P1), then US2 (restore from upload, P2).

**Delivery**: one Gameplane PR on `feat/041-backup-archives`, separate commits per layer, through `Workflow` scripts (haiku first, explicit `model:`, one diff review per batch one tier up). Before every CI wait, check the PR still merges cleanly with `master`.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task).

---

## Phase 1: Setup

- [ ] T001 Create `feat/041-backup-archives` from fresh `master` after the docs PR for this folder merges; record the base SHA in the PR body.
- [ ] T002 List existing tests that encode behaviour this spec changes (file, test, assertion, needed change) in `specs/041-downloadable-backup-archives/T002-test-edits.md`: candidates `operator/internal/controller/restore_*_test.go` (required `backupRef`, phase enum), `api/internal/rbac/*_test.go` (route rules), `api/cmd/*_test.go` (large-transfer paths), `api/internal/gateway/*_test.go` (operation allow-list), `web/src/routes/tabs/Backups*.test.tsx`. Mark each "matches spec, peer review" or "needs valgul sign-off".
- [X] T003 Write the design prompt `/mnt/project-files/notes/design-prompts/041-backup-archives.md` (done with the docs PR; see T005).

## Phase 2: Foundational

- [X] T004 [P] CRDs: add `operator/api/v1alpha1/backupexport_types.go` and the `Restore` upload fields, phases and status per data-model.md (CEL: exactly one of `backupRef`/`upload`; immutability except `upload.confirmed` false→true); run `make generate && make manifests`; commit deepcopy, `operator/config/{crd,rbac}/`, `charts/gameplane/charts/gameplane-crds/crds/`, `charts/gameplane/crd-manifests/`.
- [X] T005 Design pass in `design.fig` through OpenPencil from the T003 prompt; run `hack/design/fig-export.ts`; commit `assets/design-export/` with it; get valgul's sign-off before T020 and T031.
- [X] T006 [P] Agent `archive-worker` subcommand skeleton in `agent/cmd/main.go` and `agent/internal/archive/`: mTLS server (agent CA, cert from mounted Secret), single-route mux keyed on `EXPORT_UID`/`RESTORE_UID`, idle timeout, termination-log writer.
- [X] T007 [P] `agent/internal/archive/manifest.go`: manifest type, JSON encode/decode, `formatVersion` check, layout mapping (`volumes/<logicalName>`, legacy `data/`) per research R3.
- [X] T008 [P] Operator shared builders `operator/internal/controller/archive_worker_pods.go`: worker Pod (agent image, restic init-copy into `emptyDir`, spec 030 security context, resource defaults from `--backup-resources`), ClusterIP Service, per-worker server cert Secret signed by the agent CA (reuse `signAgentServerCert`).
- [X] T009 [P] Chart: `backups.maxUploadSize`, `backups.uploadStagingStorageClass`, `backups.maxConcurrentExports`, `backups.exportTTL` in `charts/gameplane/values.yaml` and the API/operator flag wiring in templates; operator RBAC for `backupexports`.
- [X] T010 Verify operator ClusterRole covers pods, services, secrets, PVCs needed by T008 and T024 (`operator/config/rbac/role.yaml` via kubebuilder markers).

## Phase 3: User Story 1 - Download a backup (P1)

**Independent test**: back up a server, download `.tar.gz` and `.zip`, unpack, compare checksums with the server data at backup time.

- [X] T011 [P] [US1] `agent/internal/archive/transcode.go`: read restic's tar stream, write manifest first, remap paths, write `.tar.gz` or streaming `.zip` (R4); refuse device files; tests with crafted tar input (`transcode_test.go`).
- [X] T012 [US1] Export server in `agent/internal/archive/export.go`: run `/tools/restic dump <snapshot> / --archive tar --retry-lock 10m` with repo env, pipe to T011, stream to the one client, non-zero exit on any error so the API can abort; tests.
- [X] T013 [US1] `operator/internal/controller/backupexport_controller.go`: validate (data-model.md), create cert/Pod/Service, set `Ready`, track Pod end → `Completed`/`Failed`, TTL → `Expired`, delete children; owner reference to the Backup; register in `operator/cmd/main.go`.
- [X] T014 [US1] Envtest `backupexport_envtest_test.go`: refusal cases (not Succeeded, volume-snapshot, UID mismatch, missing repo keys), Ready path, TTL expiry, cascade on Backup delete.
- [X] T015 [P] [US1] API `api/internal/handlers/backup_archives.go` download route per contracts/api.md (export create, wait, stream, abort on error, delete, audit, 429 limit); mount in `api/cmd/main.go`; add to `isLargeTransferPath`.
- [X] T016 [P] [US1] Gateway: `gatewayprotocol` `BackupExportTarget` and `api/internal/gateway/backup_archives.go` forwarding with identity checks like `capture.go`; panel side forwards for remote clusters.
- [X] T017 [US1] API tests: `backup_archives_envtest_test.go` (permissions via `backups:read`, refusals, audit rows, timeout exemption), gateway tests (wrong identity → 404).
- [X] T018 [P] [US1] i18n keys for download messages in the shared packs; `make check-i18n` runs in CI.
- [X] T019 [P] [US1] `web/src/lib/api.ts` `backupDownloadURL`, types in `web/src/types.ts`.
- [X] T020 [US1] `web/src/routes/tabs/Backups.tsx`: Download action with format choice and the "contains every server file" note (FR-019), hidden for volume-snapshot backups and without `backups:read`; tests in a new `Backups_download.test.tsx`. Needs T005 sign-off.
- [X] T021 [US1] E2E `test/e2e/backup_archives_e2e_test.go`: download tar.gz and zip, checksum against data; viewer permission; CSI refusal; remote cluster download in the `multicluster` bucket if a remote fixture is needed there. Register bucket `backup-archives` in `test/e2e/buckets.sh`.

## Phase 4: User Story 2 - Restore from an uploaded archive (P2)

**Independent test**: upload a downloaded archive to an empty server of the same game and restore; refuse another game; reject unsafe archives with the server untouched.

- [X] T022 [P] [US2] `agent/internal/archive/unpack.go`: safe unpack of tar.gz and zip onto the staging root (no absolute paths, `..`, outside links, devices, hard links outside; unpacked-size cap; strip one wrapping folder for plain archives; zip permission reset); test set covering SC-005.
- [X] T023 [US2] Receiver in `agent/internal/archive/receive.go`: accept one `PUT`, enforce declared size, write file to staging, run T022, write summary JSON to the termination log; copy mode `agent archive-worker copy` that replaces each target volume's contents from staging; tests.
- [X] T024 [US2] `operator/internal/controller/restore_upload.go`: staging PVC (class from flag or default, refuse `NoStagingStorageClass`, size per R5, target node topology), receiver Pod/Service, read summary, FR-013 checks (game, volumes, version warnings), `AwaitingConfirmation` with 30 min timeout, then existing suspend/fence path, copy Pod, resume; cleanup on every terminal phase; final-copy failure message (OD-7).
- [X] T025 [US2] Envtest `restore_upload_envtest_test.go`: no storage class, wrong game refused before suspend, plain archive needs template, confirmation timeout, client disconnect cleanup, copy failure leaves server stopped with message, staging PVC deleted in all paths.
- [X] T026 [P] [US2] API upload and confirm routes per contracts/api.md in `backup_archives.go`; size limit; delete Restore on disconnect; audit; `isLargeTransferPath` for `POST /restores:upload`.
- [X] T027 [P] [US2] Gateway forwarding for the upload stream and confirm.
- [X] T028 [US2] API tests: `backups:restore` required, 411/413, busy server, disconnect cleanup, audit rows.
- [X] T029 [P] [US2] i18n keys for upload and restore messages.
- [X] T030 [P] [US2] `web/src/lib/api.ts` `uploadRestore` (XHR progress), `confirmRestore`; types.
- [X] T031 [US2] Backups tab Upload backup flow: file picker, format detect, "not a Gameplane archive" + game select, upload progress, check summary with warnings, overwrite and partial-copy risk confirmation, result; tests in new `Backups_upload.test.tsx`. Needs T005 sign-off.
- [X] T032 [US2] E2E in `backup_archives_e2e_test.go`: Gameplane archive restore, wrong game, plain archive, unsafe set, interrupted upload leaves server running and no staging PVC.

## Phase 5: Polish

- [X] T033 [P] Update `operator/specs.md` (BackupExport, Restore upload lifecycle, 10 CRDs), `api` and `agent` `specs.md`, `CLAUDE.md` CRD count, user docs for backups (find the page via `docs/agent-architecture.md`).
- [X] T034 [P] `changelog/unreleased/<PR>-backup-archives.md`.
- [ ] T035 Browser smoke test via Chrome MCP at `sonnet` on a dev cluster, parallel with the diff review.
- [ ] T037 User docs page for backup download and upload on the website (`GameplanePanel/website`, docs collection), then bump the `website` pointer; split from T033, which covered the in-repo docs (`docs/operator-operations.md`).
- [ ] T036 valgul's quickstart walk (`quickstart.md`), then archive the folder to `specs/done_041-downloadable-backup-archives` (core rule 16).

## Dependencies

- T004 blocks T008, T013, T024. T005 blocks T020, T031. T006 and T007 block T011, T012, T022, T023.
- US1 (T011–T021) can ship and be tested before US2 (T022–T032).
- T033–T036 after both stories.
