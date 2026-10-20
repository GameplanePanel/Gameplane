# Implementation Plan: Forget Restic Snapshot on Backup Delete

**Branch**: `feat/backup-snapshot-forget` | **Date**: 2026-10-05 | **Spec**: [./spec.md](./spec.md)

**Input**: Feature specification from `specs/done_020-backup-snapshot-forget/spec.md`; every open question (Q1-Q8) was decided by the maintainer on 2026-10-05 and is recorded in `OPEN-DECISIONS.md`.

## Summary

Add a second Backup finalizer, `gameplane.local/backup-snapshot-finalizer`, to restic-strategy Backups. On delete, after the existing unquiesce stage, the operator runs a one-shot Job `<backup>-forget` that executes `restic forget <snapshotID> --prune --retry-lock 10m` against the Backup's repo Secret, and releases the Backup when it succeeds. The stage never blocks forever: a missing Secret, a terminating namespace, a failed Job, or a 45-minute hard cap (annotation `backup.gameplane.local/forget-started-at`) releases the Backup with a Warning event and leaves the snapshot in the repository. `BackupSchedule` retention needs no new deletion logic (it already deletes Backups) beyond ignoring Backups that are already being deleted. Backup Jobs gain `--retry-lock 10m` so a backup waits for a prune instead of failing. No CRD schema change.

---

## Technical Context

**Language/Version**: Go 1.26 (`operator/`), POSIX shell (the forget container script), restic 0.17.1 (`DefaultResticImage`)

**Primary Dependencies**: controller-runtime (finalizers, `CreateOrUpdate`, fake client in unit tests, envtest), `k8s.io/client-go/tools/events` (fake recorder in tests)

**Storage**: none new. State is a finalizer, one annotation and one condition on the Backup object.

**Testing**: unit tests with the fake client (`backup_forget_test.go`, `retention_terminating_test.go`), envtest (`backup_forget_envtest_test.go`), Kind e2e in bucket `operator` (`TestBackup_DeleteForgetsSnapshot`, extended `TestBackupSchedule_RetentionTrimsPast`). CI is the only verification (CLAUDE.md rule 8).

**Target Platform**: the operator Deployment; the forget Job runs in the Backup's namespace.

**Constraints**: no CRD change; no `//nolint`; Go errors wrapped with `%w`; unused parameters named `_`; the chart's RBAC and NetworkPolicy are already sufficient.

---

## Constitution Check

| Principle | Status | Justification & Verification Plan |
|---|---|---|
| **I. E2E-Tested Delivery** | **PASS** | `TestBackup_DeleteForgetsSnapshot` is new and registered in `test/e2e/buckets.sh` (bucket `operator`, dynamic client, no login budget); `TestBackupSchedule_RetentionTrimsPast` is extended to assert trimmed snapshots leave the repository. Both use `ensureResticRepo(t)` and the shared `e2e-restic-creds` Secret. |
| **II. Design-First for User-Facing Change** | **N/A** | No dashboard change (Q7). |
| **III. Language & Ecosystem Best Practice** | **PASS** | Kubernetes primitives only (finalizer, Job, Secret, Events, condition). Errors wrapped with `%w`; no lint suppression. |
| **IV. Spec-Driven Development** | **PASS** | This folder; `operator/specs.md` and `docs/architecture.md` updated in the same changeset. |
| **V. Delegate to Workflows & Subagents** | **PASS** | Implemented from a blind-apply brief by small models; reviewed against the diff one tier up. |
| **VI. CI Bears the Heavy Lifting** | **PASS** | Nothing is built, linted or tested locally; the branch is pushed and CI decides. |

---

## Design

### Reconcile and finalizer

`BackupReconciler.Reconcile` adds `BackupSnapshotFinalizer` right after the quiesce-finalizer step whenever `wantsSnapshotFinalizer(b)` (strategy not `volume-snapshot`, `repoRef` set, phase not `Failed`). Unlike the quiesce step it does not return after the metadata Update, so terminal Backups that predate the finalizer still fall through to their normal handling in the same pass.

`finalizeDelete` (now in `backup_forget.go`) is two stages: `finalizeUnquiesce` (the former `finalizeDelete` body, unchanged) while `BackupFinalizer` is present, then `finalizeSnapshot` while `BackupSnapshotFinalizer` is present.

### finalizeSnapshot

1. Volume-snapshot strategy or nil `repoRef`: release.
2. Stamp `forget-started-at` on first entry; if more than 45 minutes old (or malformed): abandon.
3. `clearBackupJob`: for a non-terminal Backup, delete its own Job with foreground propagation and requeue every 5s until it is gone.
4. Empty snapshot id: release (Warning `SnapshotForgetSkipped` when the Backup ran). Malformed id: release with the same warning.
5. A non-terminal Restore pins the Backup (by `backupRef` or `snapshotID`): requeue every 15s.
6. Repo Secret missing or lacking keys: abandon.
7. `CreateOrUpdate` Job `<backup>-forget` (controller-owned, pod label for the egress NetworkPolicy, no PVC). A `NamespaceTerminating` create error abandons; `AlreadyExists` (stale cache) requeues in 2s.
8. Job Succeeded: set `SnapshotForgotten=True`, emit Normal `SnapshotForgotten`, release. Job Failed: abandon. Otherwise requeue in 10s.

`abandonSnapshotForget` records a Warning `SnapshotForgetAbandoned` event and `SnapshotForgotten=False` (best effort) and releases.

### Forget container

`/bin/sh -c` with `restic unlock || true` (stale locks only), then `restic forget "$FORGET_SNAPSHOT_ID" --prune --retry-lock 10m`; a non-zero exit whose output matches `no matching ID|could not find snapshot|id not found` is success. The id is only ever read from the environment.

### Shared builders

`resticEnv(secretName)` and `resticContainerSecurityContext()` are factored out of `buildBackupPodSpec` so the backup and forget Jobs cannot drift apart. `buildRestorePodSpec` is left alone.

### Retention

`trimBackups` skips Backups with a `deletionTimestamp`. `selectKept` is unchanged.

### RBAC

Markers on `backup_controller.go` add `backups` update/patch, `backups/status`, `backups/finalizers` and `jobs` create/update/patch/delete. `operator/config/rbac/role.yaml` is edited by hand to the exact shape controller-gen emits (rules sorted by group then resources, resources with identical verbs merged, verbs sorted); CI's generated-file check is the confirmation. The chart is unchanged.

---

## Project Structure

```text
operator/
├── api/v1alpha1/backup_types.go                 # + BackupSnapshotFinalizer const (no CRD change)
├── config/rbac/role.yaml                         # hand-edited to match the new markers
└── internal/controller/
    ├── backup_controller.go                      # finalizer add, finalizeUnquiesce rename, resticEnv helpers, --retry-lock, markers
    ├── backup_forget.go                          # NEW: finalizeDelete, finalizeSnapshot, forget Job, helpers
    ├── retention.go                              # skip Backups being deleted
    ├── backup_forget_test.go                     # NEW: fake-client unit tests
    ├── retention_terminating_test.go             # NEW
    ├── backup_forget_envtest_test.go             # NEW (envtest)
    └── backup_envtest_test.go                    # two args assertions updated for --retry-lock
test/e2e/
├── backup_forget_e2e_test.go                     # NEW: TestBackup_DeleteForgetsSnapshot + restic helpers
├── backupschedule_e2e_test.go                    # RetentionTrimsPast extended
└── buckets.sh                                    # register the new test in bucket_operator
docs/architecture.md                              # "Backup deletion and snapshot cleanup"
operator/specs.md                                 # BackupReconciler / BackupScheduleReconciler
specs/done_020-backup-snapshot-forget/                 # this folder
```

## Complexity Tracking

| Aspect | Justification | Simpler Alternative Rejected Because |
|---|---|---|
| Second finalizer instead of extending `BackupFinalizer` | Keeps the unquiesce semantics untouched and lets the two stages be ordered and tested separately | One finalizer would conflate "release the world" with "forget the snapshot" and would add the quiesce path to every restic Backup |
| Forget Job instead of calling restic from the operator | The operator image has no restic and no repo network access; a Job reuses the backup Job's image, credentials, NetworkPolicy label and security context | Embedding restic in the operator would widen its attack surface and egress |
| Give up after 45 minutes instead of blocking | An unreachable repository must not make Backups, GameServers or namespaces undeletable (Q5) | Blocking forever turns a repository outage into a cluster-wide delete outage |
| `--prune` per Backup | Simplest correct behaviour (Q2) | A repo-level batched prune needs a repo-level lock and a scheduler |
