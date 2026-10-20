# Implementation Plan: Standalone Panel and API

**Branch**: `feat/standalone-panel` (PR #630, merged as `6ed25c1`) | **Date**: 2026-10-14 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/025-standalone-panel/spec.md`

**Note**: Written retroactively. This plan describes the design PR #630 merged; it plans no new work.

## Summary

The API gains a standalone mode that runs without any local Kubernetes cluster. Management records (cluster registrations and labelled credentials) move behind a narrow storage interface. The combined install backs it with Kubernetes; standalone backs it with two new SQL tables, with credentials sealed by AES-256-GCM under a panel key kept on separate storage. Remote clusters are reached through hardened clients (verified HTTPS, no proxy, no redirects, dial-time destination checks, bounded responses). A health poller and a notification poller replace the informers a central operator would otherwise feed. The dashboard reads an authenticated installation endpoint and stops assuming a `local` cluster. Helm gains `operator.enabled` and `api.standalone`, and a two-service Docker Compose profile ships under `deploy/standalone/`.

## Technical Context

**Language/Version**: Go (API, `api/go.mod`), TypeScript strict with React 19 (dashboard), Helm templates, Docker Compose, Python 3 (CI smoke and chart checks)

**Primary Dependencies**: chi, client-go and its SPDY executor, controller-runtime (combined path only), TanStack Query, HeroUI v3

**Storage**: Panel SQL database (SQLite, or experimental PostgreSQL) with migrations `016_standalone_management.sql` and `017_standalone_key_state.sql`; panel key file on separate storage (`/keys/panel.key`)

**Testing**: Go unit tests (including PostgreSQL-tagged tests run by the CI `api-postgres` job), Vitest dashboard tests, `charts/tests/check_standalone.py`, `deploy/standalone/smoke.py` (CI `standalone-compose` job). No `test/e2e/` test (see Constitution Check I)

**Target Platform**: Linux host with Docker Compose, or a Kubernetes cluster for the panel-only Helm profile; remote game clusters on Kubernetes

**Project Type**: Web service (API and dashboard) plus deployment profiles

**Performance Goals**: None stated. Fixed bounds instead: 8 MiB decompressed REST response, 64 KiB `/version`, 1 MiB per watch frame, 1024 objects per kind per notification snapshot, 30 s health poll with 4 workers and 10 s timeout, 5 s notification poll

**Constraints**: One API process (`Recreate`, one replica); credentials never in responses, errors or logs; fail closed on any key problem; no login-page leak of the install mode

**Scale/Scope**: Homelab-scale central panel managing a handful of remote clusters; 231 files changed in PR #630

## Constitution Check

*Evaluated against constitution 2.2.0 on the merged code.*

| Principle | Result | Evidence |
|---|---|---|
| I. E2E-tested delivery | **Not met** | No `test/e2e/` test runs a standalone panel and no bucket in `test/e2e/buckets.sh` covers it. Coverage is the Compose smoke test and render-only chart checks. Recorded as a known gap in [spec.md](spec.md#known-gaps); no task added. |
| II. Design-first | **Partly met** | 19 standalone frames exported to `assets/design-export/` (MANIFEST section "Standalone panel (PR #630, 2026-10-08)"), but design commit `6eb7ac3` came after the first UI commit `0182e16`. |
| III. Language best practice | Met | No `//nolint`, `eslint-disable`, `@ts-ignore` or `#nosec` added; no lint config loosened; errors wrapped with `%w`. |
| IV. Spec-driven development | **Partly met** | Only the contributor's design note came with the first code commit (`0182e16`); spec, plan and tasks were written after the merge, on 2026-10-14. `api/specs.md` and `web/specs.md` were updated in PR #630. |
| V. Delegation | Not applicable | Contributor PR. |
| VI. CI bears the load | Met | All 97 checks green on head `117caf35` before merge. |

**Gate decision**: The feature is merged, so the gate cannot block it. Every task is ticked, so rule 16 calls for the `done_025-` rename; valgul decided to keep the `025-` name until a standalone E2E test exists (OD-5) (see [OPEN-DECISIONS.md](OPEN-DECISIONS.md)).

## Project Structure

### Documentation (this feature)

```text
specs/025-standalone-panel/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── api-cli.md
│   ├── http-api.md
│   └── deployment.md
├── OPEN-DECISIONS.md
├── checklists/requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
api/
├── cmd/                      main.go (flags, rotate-panel-key), management.go, panel_key.go
├── internal/controlplane/    SQL management store, key handling, rotation, atomic registration
├── internal/db/migrations/common/016_*.sql, 017_*.sql
├── internal/kube/            management interface, registry sequencing, remote policy, bounded responses, SPDY guard, standalone health poller
├── internal/notify/          per-cluster notification snapshots
├── internal/ws/              gateway client destination guard
├── internal/handlers/        clusters (register, kubeconfig, gateway), installation, modules routing, fleet, managed secrets
└── internal/auth, rbac, telemetry, registry
web/src/
├── lib/useInstallation.ts, cluster.ts, endpoints.ts, api.ts
├── components/               ClusterSelector, RegisterClusterDialog, ModuleTarget, modules/*, NotificationsPanel, AppLayout
└── routes/                   Clusters, Cluster, Modules, AdminSettings, AdminLogs, Users, tabs/Settings
charts/gameplane/             values.yaml, templates/{api,panel-key,validate,...}.yaml, examples/standalone-panel-values.yaml
charts/tests/check_standalone.py
deploy/standalone/            compose.yaml, compose.provisioned-key.yaml, smoke.py, README.md
docs/standalone-panel.md      plus install, security, architecture, gateway and multicluster docs
```

**Structure Decision**: Existing modules only. Standalone logic lives in the API (`api/internal/controlplane/` is new); the operator and agent are unchanged. This respects Core rule 10: the remote operator stays authoritative for workloads, and the API only stores registrations and credentials.

## Design (as merged)

1. **Mode switch** (`api/cmd/management.go`): `managementClients` builds either a Kubernetes-backed client registered as `local`, or the SQL-backed `controlplane` client with no default cluster. Standalone with cluster-ops is a startup error.
2. **Management interface** (`api/internal/kube/management.go`): `SecretStore`, `ClusterStore` and `ClusterRegistrar`. Handlers call `Client.Secrets(ns)` and `Client.Clusters()` and never know which backend they have.
3. **SQL store** (`api/internal/controlplane/`): one `management_objects` table keyed by kind, namespace and name, with UID and version for compare-and-swap; label selectors and continue tokens on list; dry-run refused.
4. **Panel key**: 32 bytes, validated (regular file, one link, safe owner and mode, no extended ACL, read through `os.OpenRoot`). Generated keys are published by temp file, fsync, hard link and directory fsync under a lock file. Provisioned keys are read only.
5. **Ciphertext**: `gpk1.<keyID>.` plus AES-256-GCM, with kind, namespace, name, UID and version as associated data, so ciphertext cannot be moved between records. Legacy headerless ciphertext still decrypts.
6. **Key state and rotation**: the singleton `management_key_state` row is locked by every credential write and checked against the process's key ID. `api rotate-panel-key` re-encrypts all rows in one transaction and records `previous_key_id` so a retry after a lost commit is safe.
7. **Registration**: standalone registration writes credential and registration in one transaction; kubeconfig and gateway rotation publish a new immutable Secret, swap the reference under UID and version checks, then delete the old one.
8. **Remote access policy** (`api/internal/kube/remote_access.go`, `remote_response.go`, `remote_spdy.go`): kubeconfig validation, redirect refusal, dial-time IP checks with optional CIDR allowlist, size-bounded responses and watch frames, guarded SPDY upgrades. The gateway client uses the same dial guard.
9. **Health and notifications**: `watch_standalone.go` polls `/version` and writes a fixed status projection; `notify/poll_standalone.go` takes bounded per-cluster snapshots for alerts.
10. **Dashboard**: `GET /admin/installation` drives every mode decision; module, source, builder and backup-destination screens take an explicit cluster; empty and selection states replace the implicit `local`.
11. **Deployment**: Helm `operator.enabled`, `api.standalone`, `api.panelKey.*`, `api.remoteAllowedCIDRs`, validation guards and a panel-only example; Compose with `gameplane-api` and `web` only.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| Principle I: no `test/e2e/` coverage | Not justified by PR #630; it shipped with Compose smoke and chart render checks only | Recorded as a known gap. Adding an E2E bucket is new work and out of scope for this retroactive folder. |
| Principle IV: plan and tasks written after the code | Not justified; the contributor shipped a design note only | This folder now documents the merged design; no corrective work is planned. |
| Principle II: UI code before design commit | Not justified; historical ordering in the contributor branch | The final tree carries the design export, so no corrective work is planned. |
