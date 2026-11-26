# Implementation Plan: Downloadable backup archives

**Branch**: `spec-041/downloadable-backup-archives` (docs) | **Date**: 2026-11-26 | **Spec**: [spec.md](spec.md)

**Input**: `specs/041-downloadable-backup-archives/spec.md`; decisions OD-1 to OD-7 in [OPEN-DECISIONS.md](OPEN-DECISIONS.md), all settled by valgul on 2026-11-26.

## Summary

Every finished restic backup can be downloaded as `.tar.gz` or `.zip` with a manifest, and a server can be restored from an uploaded archive (Gameplane or plain). Restic stays the only store. Download: the API creates a short-lived `BackupExport` CR; the operator runs a worker Pod that pipes `restic dump` through a Go transcoder and serves it over mTLS; the API or cluster gateway streams it to the browser. Upload: `Restore` gains an `upload` source; the operator receives and checks the file on a temporary staging PVC while the server keeps running, waits for the user's confirmation, then reuses the existing suspend, fence and resume path with a copy Pod. Details and rejected options: [research.md](research.md).

## Technical Context

**Language/Version**: Go (workspace `go.work`), TypeScript strict / React 19 (web)

**Primary Dependencies**: controller-runtime (operator), chi (API), `archive/tar`, `archive/zip`, `compress/gzip` (stdlib), restic 0.17.1 (`restic/restic` image), TanStack Query, HeroUI v3

**Storage**: restic repositories (unchanged); staging PVC per upload restore (OD-7); no database change

**Testing**: Go unit + envtest (operator, API), Vitest (web), Kind E2E (`test/e2e/`, new bucket `backup-archives`). CI only (CLAUDE.md rule 8).

**Target Platform**: Kubernetes (k3s single node and multi-node, local and remote clusters)

**Project Type**: operator + API gateway + agent binary + web dashboard

**Performance Goals**: download/upload throughput limited by network and restic, not the panel; API memory per transfer ≤ 64 MiB (SC-003)

**Constraints**: no full archive in API memory or disk; 60 s app timeout exemption for the stream routes; upload ≤ `backups.maxUploadSize` (20Gi default)

**Scale/Scope**: ≤ 3 concurrent exports per namespace by default; one upload restore per server at a time (existing restore ownership)

## Constitution Check

| Principle | Status |
|---|---|
| I. E2E-tested delivery | New `backup-archives` bucket: download round trip (tar.gz and zip, checksum), upload restore of Gameplane and plain archives, unsafe archive set (SC-005), remote cluster download. Registered in `test/e2e/buckets.sh`. |
| II. Design-first | Dashboard work waits for the `design.fig` pass (T005) and valgul's sign-off; prompt in `/mnt/project-files/notes/design-prompts/041-backup-archives.md`. |
| III. Best practice | Go errors wrapped `%w`; no `//nolint` / `eslint-disable`; strict TS. |
| IV. Spec-driven | This folder; tasks in `tasks.md`; `operator/specs.md`, `api` and `agent` `specs.md` updated in the same PRs as behaviour. |
| V. Workflows | Implementation through `Workflow` scripts, haiku first, explicit `model:`, one diff review per batch one tier up. |
| VI. CI | Locally only `go build ./...` and `npx tsc --noEmit`. |
| Core rule 9/10 | CRD + Pod + Service + PVC only; all volume and repository access in operator-owned Pods; API only streams bytes. |
| Core rule 7 | `BackupExport` and `Restore` changes regenerate deepcopy, CRD and RBAC YAML, chart copies. |

No violations; Complexity Tracking not needed.

## Project Structure

### Documentation (this feature)

```text
specs/041-downloadable-backup-archives/
├── spec.md
├── OPEN-DECISIONS.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/api.md
├── checklists/requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
operator/api/v1alpha1/backupexport_types.go        new CRD
operator/api/v1alpha1/restore_types.go             upload source, phases, status
operator/internal/controller/backupexport_controller.go   new reconciler
operator/internal/controller/restore_upload.go     receive → check → confirm → copy
operator/internal/controller/archive_worker_pods.go       shared Pod/Service/cert builders
operator/cmd/main.go                               register reconciler, flags
agent/cmd/main.go                                  `archive-worker` subcommand
agent/internal/archive/                            transcoder, manifest, safe unpack, servers
api/internal/handlers/backup_archives.go           download, upload, confirm routes
api/internal/gateway/backup_archives.go            remote forwarding
api/internal/gatewayprotocol/backup_archives.go    targets
api/internal/rbac/rbac.go                          verify segment rules cover new routes
api/cmd/main.go                                    isLargeTransferPath, flags
i18n/packs/*                                       message keys
charts/gameplane/values.yaml, templates             backups.* values, flags
web/src/routes/tabs/Backups.tsx                    Download, Upload backup, confirm
web/src/lib/api.ts, web/src/types.ts               client + types
test/e2e/backup_archives_e2e_test.go, buckets.sh   E2E
docs/backups.md (or existing backup section)       user docs; website docs page later
```

## Delivery

One docs PR for this folder (`spec-041/downloadable-backup-archives`). Implementation in one Gameplane PR on `feat/041-backup-archives` with separate commits per layer (CRDs, operator, agent, API/gateway, chart, design, web, docs, E2E) plus a changelog file `changelog/unreleased/<PR>-backup-archives.md`. A website docs PR follows after merge (website refresh task picks it up). No component submodule changes.

## Risks

- **Final copy failure (OD-7)**: data may be partly replaced; surfaced in status and before confirmation.
- **Topology**: staging PVC and target volume must be co-mountable; receiver Pod uses the target volume's node affinity (same logic as restore workers, `operator/specs.md` "existing volume topology"). If the staging class cannot bind there, the restore fails before the server stops.
- **Prune during download**: `restic forget --prune` from Backup deletion waits on the dump's shared lock (`--retry-lock`); a deleted Backup cascades the BackupExport, which aborts the stream (FR-006).
- **CRD count**: docs that say "9 CRDs" (`CLAUDE.md`, `operator/specs.md`) become 10.
