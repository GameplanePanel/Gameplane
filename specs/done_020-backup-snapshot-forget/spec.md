# Feature Specification: Forget Restic Snapshot on Backup Delete

**Feature Branch**: `feat/backup-snapshot-forget`

**Created**: 2026-10-05

**Status**: Implemented (merged in PR #567, 2026-10-05)

**Input**: Finding #6 from the backup review: deleting a `Backup` (by hand, through the API, or via `BackupSchedule` retention) removes only the Kubernetes object. The restic snapshot it created stays in the repository forever, so retention never reclaims repository space and the repository accumulates orphaned snapshots (two were already orphaned in the e2e `gameplane-test-restic` repository). Nothing in the Backup delete path touched the repository.

## Clarifications

### Session 2026-10-05 (maintainer, in chat)

The maintainer accepted the proposed default for every open question in the design. The rulings are recorded in `OPEN-DECISIONS.md` (Q1-Q8); in short:

- Q1: complete the kubebuilder RBAC markers and hand-edit `operator/config/rbac/role.yaml` to match (controller-gen style); the chart's RBAC already grants everything needed.
- Q2: `restic forget <id> --prune` per deleted Backup (no batched or periodic prune).
- Q3: a Backup with no scraped snapshot id is skipped, with a warning.
- Q4: accept, and document, that rolling back to an operator without the finalizer leaves a finalizer nothing removes.
- Q5: forget Job `backoffLimit` 3, `activeDeadlineSeconds` 1800, hard cap of 45 minutes via annotation; on failure give up (Warning event, orphaned snapshot) rather than block deletion.
- Q6: add `--retry-lock 10m` to the backup Job args and update the envtests that pin the old args.
- Q7: no change to the dashboard's delete-confirmation copy.
- Q8: no cleanup of snapshots already orphaned before this change.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Deleting a Backup reclaims its repository space (Priority: P1)

An administrator deletes a Backup they no longer need. They expect the restic repository to stop holding that backup's data.

**Why this priority**: This is the whole feature; without it the repository grows without bound regardless of retention settings.

**Independent Test**: Create two Backups against one repository, wait for both to succeed, delete one, and confirm the deleted Backup's snapshot id is gone from `restic snapshots` while the other's remains.

**Acceptance Scenarios**:

1. **Given** a Succeeded restic Backup with a snapshot id, **When** it is deleted, **Then** a Job `<backup>-forget` runs `restic forget <id> --prune`, the Backup stays `Terminating` until the Job succeeds, and the Backup then disappears.
2. **Given** a `BackupSchedule` with retention, **When** retention deletes older Backups, **Then** their snapshots are forgotten and pruned the same way, and the surviving Backups' snapshots are untouched.
3. **Given** a Backup whose snapshot was already removed out of band, **When** the Backup is deleted, **Then** the delete still completes.

---

### User Story 2 - A broken repository never blocks deleting a Backup (Priority: P1)

An administrator deletes a Backup whose repository is unreachable, whose credentials Secret is gone, or whose namespace is being torn down.

**Why this priority**: A finalizer that can wedge forever would make Backups, GameServers and namespaces undeletable.

**Independent Test**: Delete a Backup whose repo Secret no longer exists; it is released immediately with a Warning event naming the snapshot.

**Acceptance Scenarios**:

1. **Given** the repo Secret is missing or lacks `repo`/`password`, **When** the Backup is deleted, **Then** it is released at once with a `SnapshotForgetAbandoned` warning and no Job is created.
2. **Given** the forget Job fails permanently, **When** its retries are exhausted, **Then** the Backup is released with a `SnapshotForgetAbandoned` warning and `SnapshotForgotten=False`.
3. **Given** the forget stage has run for more than 45 minutes (vanished Job, unpullable image, stuck Restore), **When** the operator next reconciles, **Then** the Backup is released with the same warning.
4. **Given** the namespace is terminating and the apiserver refuses the new Job, **When** the operator tries to create it, **Then** the Backup is released with the same warning.

---

### User Story 3 - Deleting a Backup never harms a running backup or restore (Priority: P2)

**Independent Test**: Delete a Backup while its own Job is running; its Job is deleted first and no snapshot is written after the forget. Delete a Backup while a Restore is using it; the forget waits for the Restore to finish.

**Acceptance Scenarios**:

1. **Given** a Backup deleted while its restic Job is running, **When** the finalizer runs, **Then** the Job is deleted (foreground, awaited) before anything is forgotten; if no snapshot id had been recorded, the Backup is released with a `SnapshotForgetSkipped` warning.
2. **Given** a non-terminal Restore whose `spec.backupRef.name` is the Backup, or whose `status.snapshotID` equals the Backup's, **When** the Backup is deleted, **Then** no forget Job is created until the Restore is Succeeded or Failed.
3. **Given** a quiesced Backup, **When** it is deleted, **Then** the unquiesce stage completes before the snapshot stage starts.

---

### Edge Cases

- **Volume-snapshot Backups** have no `repoRef`; their `VolumeSnapshot` is garbage-collected through its owner reference. They never get the snapshot finalizer.
- **Failed Backups** never produced a snapshot id and are not given the finalizer; a Backup that fails after receiving it is released immediately on delete (empty snapshot id, no warning).
- **Backups that predate this change** receive the finalizer on the first reconcile after the operator upgrade, terminal ones included, so a later delete forgets their snapshots. The first retention pass after an upgrade may therefore start many forget Jobs at once; `--retry-lock 10m` serializes them on the repository lock.
- **Malformed snapshot id**: anything not matching `^[0-9a-f]{8,64}$` is never passed to restic; the Backup is released with a `SnapshotForgetSkipped` warning.
- **Retention accounting**: a Backup held by its finalizer still reads `Succeeded`. `trimBackups` skips Backups with a `deletionTimestamp` so they neither shift the keep window nor get a second Delete.
- **Foreground deletion** of a Backup (`propagationPolicy=Foreground`) may make the garbage collector delete the forget Job (a dependent) while the finalizer is still running; the operator would recreate it, and the 45-minute cap bounds that loop. Default (background) deletes are unaffected.
- **Append-only repositories** (for example rest-server `--append-only`) reject forget; every delete there ends in the abandoned path and the repository must be pruned externally.
- **Operator downtime** during a delete: the finalizer persists, and the `forget-started-at` annotation survives so the 45-minute cap still applies after a restart.
- **A Restore created after the forget began** is not blocked (the interlock runs once per pass); the Restore Job may then fail against a pruned snapshot and the Restore ends Failed.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Every Backup with a `repoRef` and a strategy other than `volume-snapshot`, whose phase is not `Failed`, MUST carry the finalizer `gameplane.local/backup-snapshot-finalizer`, added by the operator on reconcile (including for terminal Backups that predate it).
- **FR-002**: On delete the operator MUST run, after the unquiesce stage, a one-shot Job `<backup>-forget` (name shortened with a hash when over 56 characters) executing `restic forget <snapshotID> --prune --retry-lock 10m` against the Backup's repo Secret, then release the finalizer when the Job succeeds.
- **FR-003**: The forget Job MUST need only the repo Secret (no PVC, GameServer or GameTemplate), run non-root with a read-only root filesystem, all capabilities dropped and the RuntimeDefault seccomp profile, carry the pod label `app.kubernetes.io/name=gameplane-backup-restore`, use `backoffLimit` 3 and `activeDeadlineSeconds` 1800, and request 50m CPU / 128Mi with a 512Mi memory limit.
- **FR-004**: The snapshot id MUST reach the container through the environment variable `FORGET_SNAPSHOT_ID` only, and MUST match `^[0-9a-f]{8,64}$`; otherwise the Backup is released with a `SnapshotForgetSkipped` warning and no Job is created.
- **FR-005**: A snapshot that is already gone MUST count as success (restic ignoring an unknown id, or the script's `no matching ID` / `could not find snapshot` / `id not found` match).
- **FR-006**: The operator MUST release the finalizer with a `SnapshotForgetAbandoned` Warning event and `SnapshotForgotten=False` when the repo Secret is missing or lacks `repo`/`password`, the namespace is terminating, the Job fails permanently, or 45 minutes have passed since the annotation `backup.gameplane.local/forget-started-at`.
- **FR-007**: On success the operator MUST set `SnapshotForgotten=True` and emit a Normal `SnapshotForgotten` event before releasing the finalizer.
- **FR-008**: A Backup deleted before it is terminal MUST have its own Job deleted (foreground) and awaited before any forget; a Backup with no recorded snapshot id MUST be released, with a `SnapshotForgetSkipped` warning when it may have written a snapshot.
- **FR-009**: The forget MUST NOT start while a non-terminal Restore in the namespace references the Backup by `spec.backupRef.name` or by `status.snapshotID`.
- **FR-010**: Backup Jobs MUST pass `--retry-lock 10m` to `restic backup` so a backup waits for, rather than fails on, the repository lock held by a prune.
- **FR-011**: `BackupScheduleReconciler.trimBackups` MUST ignore Backups with a `deletionTimestamp` when choosing candidates and computing the keep set.
- **FR-012**: The `+kubebuilder:rbac` markers on `backup_controller.go` MUST grant `backups` update/patch, `backups/status` get/update/patch, `backups/finalizers` update and `jobs` create/update/patch/delete, and `operator/config/rbac/role.yaml` MUST match what controller-gen generates from them.
- **FR-013**: No CRD schema change is made: the finalizer is a Go constant, state lives in an annotation, and the outcome is a condition (`SnapshotForgotten`) plus Events.
- **FR-014**: `docs/architecture.md` and `operator/specs.md` MUST describe the finalizer, the forget Job, the give-up policy and the caveats.

### Key Entities

- **Backup** (`operator/api/v1alpha1/backup_types.go`): gains the finalizer constant `BackupSnapshotFinalizer`, the annotation `backup.gameplane.local/forget-started-at`, and the condition type `SnapshotForgotten`. No new spec or status fields.
- **Forget Job** (`<backup>-forget`, namespace of the Backup): controller-owned by the Backup, labelled `gameplane.local/backup=<backup>`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After a Succeeded restic Backup is deleted, its snapshot id is absent from `restic snapshots` on the same repository (e2e `TestBackup_DeleteForgetsSnapshot`).
- **SC-002**: After `BackupSchedule` retention trims a Backup, its snapshot id is absent from the repository (e2e `TestBackupSchedule_RetentionTrimsPast`).
- **SC-003**: No Backup stays `Terminating` for more than the 45-minute cap, whatever state the repository is in.
- **SC-004**: Deleting a Backup never alters another Backup's snapshot.

## Assumptions

- **Credentials.** The forget Job uses exactly the credentials the backup Job uses (the destination Secret's `repo` and `password` keys); credentials for S3/B2-style backends are embedded in the repo URL, as today. No new constraint is introduced.
- **Operator authority.** The finalizer and Job live in the operator; `api/` needs no change because its generic delete handler only deletes the CR. `charts/gameplane` needs no change: the `operator-manage` Role already grants Job create/update/patch/delete, `backups` update/patch/delete, `backups/finalizers` update and Secret get, and the NetworkPolicy `allow-backup-restore-egress` selects the pod label the forget Job carries.
- **Restic version.** The pinned `restic/restic:0.17.1` supports `--retry-lock` (added in 0.16).
- **E2E repository.** `rest-server` in the e2e cluster runs `--no-auth` and is not append-only, so forget and prune work.

## Out of Scope

- Cleaning up snapshots already orphaned before this change (Q8).
- A batched or periodic prune Job per repository (Q2).
- Tagging snapshots with the Backup UID to forget by tag (Q3).
- Changing the dashboard's Backup delete confirmation copy (Q7).
- Completing the RBAC markers of `BackupSchedule` and `Restore` (only Backup and Job markers are completed here).
- Failing a new Restore whose source Backup is already `Terminating`.
