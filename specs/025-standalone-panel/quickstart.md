# Quickstart: validating the standalone panel

Validation runs in CI (CLAUDE.md rule 8, Constitution VI). Locally, only compile checks are allowed (`go build ./...` in `api/`, `npx tsc --noEmit` in `web/`). The operator walkthrough is `docs/standalone-panel.md`.

## What CI runs

| Scenario | Where | Proves |
|---|---|---|
| Compose panel starts with no Kubernetes, bootstraps an admin, logs in, lists empty clusters and fleet | `deploy/standalone/smoke.py`, job `standalone-compose` | US1, SC-001 |
| Register a cluster, inspect a stopped-container SQLite snapshot for ciphertext, restart, log in again | same | US2, SC-002 |
| Replace a kubeconfig, rotate the panel key once and re-run the same command as a retry, check key digests | same | US2, US4, SC-004 |
| Provisioned-key mode against a read-only key volume | same (`smoke.py` builds its own read-only override; `compose.provisioned-key.yaml` is not exercised) | US4 |
| Remove and re-register a cluster | same | US2 |
| Render combined, remote-only and standalone profiles; every invalid combination fails | `charts/tests/check_standalone.py`, chart-template job | US5, SC-005 |
| Store, key, rotation and registration behaviour on SQLite and PostgreSQL | `api/internal/controlplane/*_test.go`; `api-postgres` job adds `./internal/controlplane/...` | US2, US4, SC-003 |
| Remote access policy, bounded responses, SPDY guard, health and notification pollers | `api/internal/kube/*_test.go`, `api/internal/notify/*_test.go`, `api/internal/ws/*_test.go` | FR-012 to FR-015 |
| Handlers: registration, kubeconfig and gateway rotation, credential secrecy, module routing | `api/internal/handlers/*_test.go` | US2, US3, SC-006 |
| Dashboard with zero clusters, explicit targets, standalone admin screens | `web/src/routes/Standalone.test.tsx` and related tests | US3 |

## What is not covered

No `test/e2e/` test starts a standalone panel, and no CI job installs the Helm panel-only profile in a cluster. See [spec.md](spec.md#known-gaps).

## Manual check (optional, on a host with Docker)

1. Follow "Docker Compose" in `docs/standalone-panel.md`.
2. Expect: login works, Clusters says no cluster is registered, and the bell shows no stream.
3. Register a remote cluster with a self-contained kubeconfig; within about 30 s its status shows the server version.
