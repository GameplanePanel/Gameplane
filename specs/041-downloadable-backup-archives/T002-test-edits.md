# T002: existing tests touched by spec 041

Editing an existing test so it matches the spec needs no sign-off (valgul 2026-11-24), but each edit gets a peer review from a fresh agent given only the test diff. Deleting, weakening or bypassing a test needs valgul's written sign-off naming the change.

## Edits made

| Files | Change | Why | Peer review (test diff only) |
|---|---|---|---|
| `operator/internal/controller/backup_forget_test.go`, `helpers_envtest_test.go`, `lifecycle_operation_envtest_test.go`, `recovery_controller_test.go`, `restore_lifecycle_test.go`, `restore_volumesnapshot_dispatch_test.go`, `restore_volumesnapshot_ownership_test.go`, `restore_volumesnapshot_requeue_test.go` (10 literals) | `BackupRef: v1alpha1.LocalObjectRef{…}` → `BackupRef: &v1alpha1.LocalObjectRef{…}` | `Restore.spec.backupRef` became optional (pointer) because exactly one of `backupRef` and `upload` is set (data-model.md) | 2026-11-26, sonnet: every hunk "only replaced", nothing weakened or deleted |
| `agent/internal/archive/main_test.go` | `"export"` removed from `TestMainValidModesNotImplemented`; new `TestMainExportModeFailsWithoutConfig` asserts exit 1 and a written termination log | export mode is implemented by T012, so it no longer returns 3 | 2026-11-26, sonnet: removal covered by the added test, nothing weakened or deleted |
| `charts/tests/check_gateway.py` | expected `gameplane-gateway-read` rules extended from `[gameservers, networkcaptures]` to `[gameservers, networkcaptures, backups, backupexports]` | the gateway reads Backup and BackupExport to forward downloads (FR-007) | 2026-11-26, sonnet: list extended, nothing weakened or deleted |
| `api/internal/rbac/rbac_test.go` | three `TestAllow` cases added for `POST /restores:upload` | new route needs `backups:restore` (OD-6) | 2026-11-26, sonnet: added only |
| `charts/tests/check_standalone.py`, `test/e2e/helm_install_e2e_test.go`, `.github/workflows/ci.yaml` (reuse-values check) | `backupexports.gameplane.local` added to the expected CRD lists; hook CRD count 9 → 10 | the chart ships the new BackupExport CRD (data-model.md) | 2026-11-26, sonnet: list extended and count corrected, nothing weakened or deleted |

## Candidates still to check when their layer lands

| Area | Files | Expected |
|---|---|---|
| RBAC route rules | `api/internal/rbac/*_test.go` | none needed: new routes fall under the existing `backups` and `restores` segment rules |
| Large-transfer paths | `api/cmd/*_test.go` | new cases in new test functions; existing ones unchanged |
| Gateway operations | `api/internal/gateway/*_test.go` | new test files; check any allow-list table test |
| Restore phases | `operator/internal/controller/restore_*_test.go` | none expected: new phases are only used by upload restores |
| Backups tab | `web/src/routes/tabs/Backups*.test.tsx` | new test files; check snapshot or action-count assertions |

Tests first written on this branch (not on `master`) were also corrected when CI showed a wrong expectation; the same peer review found nothing weakened or deleted (2026-11-26).
