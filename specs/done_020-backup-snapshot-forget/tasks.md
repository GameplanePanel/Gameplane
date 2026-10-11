---
description: "Task list for Feature 020: Forget Restic Snapshot on Backup Delete"
---

# Tasks: Forget Restic Snapshot on Backup Delete

**Input**: Design documents from `specs/done_020-backup-snapshot-forget/`: `spec.md`, `plan.md`, `OPEN-DECISIONS.md` (Q1-Q8 settled 2026-10-05).

**Prerequisites**: plan.md (required), spec.md (required), OPEN-DECISIONS.md (every task below cites the ruling it implements; nothing here re-decides one).

**Worktree**: `/home/valgul/project/wt-s2-restic`, branch `feat/backup-snapshot-forget` (from `origin/master`).

**Local verification (rule 8)**: nothing is built, vetted, linted or tested on the workstation; `gofmt` and `git` only. CI is the sole verification authority.

**Format**: `T### [P?] [Group] Description`
- **[P]**: different files, no dependency on another incomplete task.
- **[Group]**: Operator, Test, E2E, Docs.
- Every description carries a repo-relative path.

---

## Phase 1: Spec

- [X] T001 [Docs] Create `specs/done_020-backup-snapshot-forget/` (`spec.md`, `plan.md`, `tasks.md`, `OPEN-DECISIONS.md`) recording Q1-Q8 as decided by the maintainer on 2026-10-05.

---

## Phase 2: Operator

- [X] T002 [Operator] Add `BackupSnapshotFinalizer = "gameplane.local/backup-snapshot-finalizer"` to `operator/api/v1alpha1/backup_types.go` (Go constant only; no CRD change, FR-013).
- [X] T003 [Operator] In `operator/internal/controller/backup_controller.go`: add the snapshot finalizer in `Reconcile` (no early return, so pre-existing terminal Backups get it), rename the old `finalizeDelete` to `finalizeUnquiesce`, factor `resticEnv` / `resticContainerSecurityContext` out of `buildBackupPodSpec`, add `--retry-lock 10m` to the backup args (Q6), and complete the RBAC markers (Q1).
- [X] T004 [Operator] Create `operator/internal/controller/backup_forget.go`: `finalizeDelete` (two stages), `finalizeSnapshot`, `clearBackupJob`, `pinnedByRestore`, `runForgetJob`, `abandonSnapshotForget`, `forgetJobName`, `buildForgetPodSpec`, the forget script, and the constants (Q2, Q3, Q5). Depends on T002, T003.
- [X] T005 [Operator] In `operator/internal/controller/retention.go`, skip Backups with a `deletionTimestamp` in `trimBackups` (FR-011).
- [X] T006 [Operator] Hand-edit `operator/config/rbac/role.yaml` to the controller-gen shape of the new markers (Q1). Depends on T003.

---

## Phase 3: Unit and envtest tests

- [X] T007 [Test] Create `operator/internal/controller/backup_forget_test.go` (fake client): finalizer predicate and Reconcile add/skip, release-without-Job table, Job shape, Job outcomes, unusable repo Secret, 45-minute cap, pinning Restore, in-flight backup Job, terminating namespace, unquiesce-before-snapshot ordering, `forgetJobName`, snapshot id pattern. Depends on T004.
- [X] T008 [Test] Create `operator/internal/controller/retention_terminating_test.go` proving a terminating Backup does not shift the keep window. Depends on T005.
- [X] T009 [Test] Create `operator/internal/controller/backup_forget_envtest_test.go` (envtest): finalizer only on restic Backups, delete runs and completes the forget Job, failed Job releases with a warning, hard cap, missing Secret, malformed id, pinning Restore. Depends on T004.
- [X] T010 [Test] Update the two args assertions in `operator/internal/controller/backup_envtest_test.go` (`TestBackup_CreatesJobWithExpectedSpec`, `TestBackup_PassesTagsToRestic`) for `--retry-lock 10m` (Q6 sign-off). Depends on T003.

---

## Phase 4: E2E

- [X] T011 [E2E] Create `test/e2e/backup_forget_e2e_test.go` with `TestBackup_DeleteForgetsSnapshot` and the restic helpers (`runResticJob`, `resticSnapshotIDs`, `assertSnapshotsAbsent`, `waitBackupGone`, `recordBackupSnapshotIDs`, `snapshotListed`, `deleteBackupCR`).
- [X] T012 [E2E] Extend `TestBackupSchedule_RetentionTrimsPast` in `test/e2e/backupschedule_e2e_test.go` to record snapshot ids and assert the trimmed ones are gone from the repository. Depends on T011.
- [X] T013 [E2E] Register `TestBackup_DeleteForgetsSnapshot` in `bucket_operator` in `test/e2e/buckets.sh`. Depends on T011.

---

## Phase 5: Docs

- [X] T014 [Docs] Add "Backup deletion and snapshot cleanup" to `docs/architecture.md`, including the append-only and rollback caveats (Q4).
- [X] T015 [Docs] Update `operator/specs.md` (BackupReconciler "Snapshot cleanup on delete", BackupScheduleReconciler retention).

---

## Phase 6: Verification and ship

- [X] T016 Push `feat/backup-snapshot-forget`; CI (lint including `check-specs`, unit, envtest, e2e bucket `operator`) is the only verification. Confirm the `make manifests` generated-file check accepts the hand-edited `role.yaml` (Q1); if CI reorders it, take CI's output. Done: CI green on PR #567.
- [X] T017 Open the PR with labels `type: feature` and `area: operator`, `area: e2e`, `area: specs` (REST only, CLAUDE.md rule 14). A human approves and merges (rule 12). Done: PR #567 carried these labels and was merged 2026-10-05.
- [X] T018 After merge, archive per rule 16: `git mv specs/020-backup-snapshot-forget specs/done_020-backup-snapshot-forget` and update in-repo references in the same commit. Done in this commit.

---

## Dependencies & Execution Order

- T001 first. T002 and T005 are independent. T003 needs T002; T004 needs T002 and T003; T006 needs T003.
- Tests: T007, T009 need T004; T008 needs T005; T010 needs T003.
- E2E: T011 first; T012 and T013 after it.
- Docs (T014, T015) can run any time after T004's design is fixed.
- T016-T018 are sequential and human-gated.

## Notes on tests that change

- `backup_envtest_test.go`: the two args assertions are updated, not deleted or loosened, per the Q6 sign-off in `OPEN-DECISIONS.md`.
- Every other existing test is untouched: the schedule envtests run only the schedule reconciler against Backups without the finalizer, and the fake-client Backup tests use Backups with no `repoRef`, so they never receive the snapshot finalizer.
