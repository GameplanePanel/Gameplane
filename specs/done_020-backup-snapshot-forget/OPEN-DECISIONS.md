# Open Decisions: 020-backup-snapshot-forget

Rulings on the design's open questions, plus items deliberately left unsettled.

Status: every question below was **Settled by the maintainer on 2026-10-05** (in chat), by accepting the design's proposed default. Nothing in this file is Open except the follow-ups at the end, which are not decisions.

## Rulings (maintainer, 2026-10-05)

- **Q1 - RBAC markers. Settled 2026-10-05:** complete the `+kubebuilder:rbac` markers on `backup_controller.go` (`backups` update/patch, `backups/status`, `backups/finalizers`, `jobs` create/update/patch/delete) and hand-edit `operator/config/rbac/role.yaml` to match, in the style controller-gen produces. The chart's `operator-manage` Role already grants everything the operator needs, so the chart is unchanged and stays consistent with the markers. This knowingly overrides the "do not hand-edit" note in `operator/specs.md` for this one generated file, because the maintainer ruled it; `make manifests` in CI must reproduce the file byte for byte.
- **Q2 - Prune strategy. Settled 2026-10-05:** `restic forget <id> --prune` per deleted Backup. No `forget`-only plus a periodic or batched prune Job, and no per-pass throttle in `trimBackups`.
- **Q3 - Backups without a scraped snapshot id. Settled 2026-10-05:** skip and warn. A Backup deleted while still running, or Succeeded with no scraped id, is released with a `SnapshotForgetSkipped` warning (only when it may have written a snapshot); no tag-based forget (`backup-uid=<uid>`) is introduced.
- **Q4 - Rollback caveat. Settled 2026-10-05:** accept and document. After rolling back to an operator release without this finalizer, Backups that carry it cannot be deleted without `kubectl patch backup <name> --type=merge -p '{"metadata":{"finalizers":null}}'`. Documented in `docs/architecture.md`. No "finalizer-aware no-op release" is shipped in a prior version.
- **Q5 - Limits and timing. Settled 2026-10-05:** forget Job `backoffLimit` 3, `activeDeadlineSeconds` 1800, `--retry-lock 10m`, requests 50m CPU / 128Mi and a 512Mi memory limit; a hard cap of 45 minutes on the whole delete stage, measured from the annotation `backup.gameplane.local/forget-started-at`. Policy: when the repository cannot be reached, give up (Warning event `SnapshotForgetAbandoned`, orphaned snapshot) rather than block the deletion.
- **Q6 - Test sign-off. Settled 2026-10-05:** approved. `--retry-lock 10m` is added to the backup Job args, and the two envtest assertions that pin the old args are updated (CLAUDE.md rule 1 sign-off recorded here). The assertions changed, in `operator/internal/controller/backup_envtest_test.go`:
  - `TestBackup_CreatesJobWithExpectedSpec`: expected args `backup /data --json --tag gameplane` become `backup /data --json --retry-lock 10m --tag gameplane`.
  - `TestBackup_PassesTagsToRestic`: expected args gain the same `--retry-lock 10m` after `--json`.
  - `test/e2e/test_helpers_e2e_test.go` `waitBackupCount` no longer counts Backups with a `deletionTimestamp` (a trimmed Backup lingers `Terminating` while its forget Job runs), and `TestBackupSchedule_RetentionTrimsPast` waits for a trimmed Backup to be gone before asserting its snapshot left the repository.
  No test is deleted or weakened. Audit result: apart from the e2e `waitBackupCount` helper above, no other existing test depends on Backup deletion being immediate (the retention envtests run only the schedule reconciler against Backups without the finalizer; the envtest namespaces have no namespace controller, so namespace cleanup never waits on a finalizer).
- **Q7 - UI copy. Settled 2026-10-05:** no change to the Backup delete confirmation.
- **Q8 - Existing orphans. Settled 2026-10-05:** no cleanup of snapshots already orphaned (including the two known ones in `gameplane-test-restic`). Not in this change.

## Follow-ups (not decisions; recorded so they are not lost)

- The `+kubebuilder:rbac` markers of `BackupScheduleReconciler` and `RestoreReconciler` are still incomplete (get/list/watch only). Only the Backup and Job markers were completed here. A later change can complete them under the same Q1 reasoning.
- A new Restore whose source Backup is already `Terminating` is not rejected. The forget interlock only sees Restores that exist when it runs.
- `docs/install.md` has no note about snapshot cleanup or append-only repositories; `docs/architecture.md` carries it for now.
