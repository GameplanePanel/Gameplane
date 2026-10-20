# Contract: profile catalogue (`.devcontainer/profiles.toml`)

Initial catalogue (OD-1, valgul 2026-10-20). Folder ids are relative to the repo root.

| Profile id | Size | Covers | Cluster | Submodules | Ports | Checks (CI + ready message) |
|---|---|---|---|---|---|---|
| `web` | component | `web` | no | none | 5173 | `cd web && npm ci && npx tsc --noEmit` |
| `api` | component | `api` + the libraries it imports (`netguard`, `gameaction`, `telemetryschema`, `i18n`, `svcutil`) | no | none | none | `go build ./...` in each covered module |
| `operator` | component | `operator` (+ `netguard`, `i18n`, `svcutil`, as imported) | no | none | none | `go build ./...` |
| `agent` | component | `agent` (+ imported libraries) | no | none | none | `go build ./...` |
| `go-services` | component | every other Go module: `audit-syslog-bridge`, `capture-sidecar`, `gameproto`, `gp-module`, `mcp-server`, `sentinel`, `telemetry-receiver`, `tunnel` + the libraries | no | none | none | `go build ./...` per module |
| `website` | component | `website` | no | `website` | 4321 | `cd website && npm ci && npm run check` |
| `modules` | component | `modules` | no | `modules` | none | `modules/build.sh` validate mode (scout confirms the name) |
| `dashboard-api` | part | `web`, `api`, `operator`, `agent` + `deploy/kind`, `charts/gameplane` | **yes** (OD-2) | `modules` (for `make dev-up` sample modules) | 5173, 8080, 8443, 5001 | union of the components' checks |
| `control-plane` | part | `operator`, `api`, `agent`, `charts/gameplane`, `deploy/kind`, `test/e2e` | yes | `modules` | 8080, 8443, 5001, 30565 | union + `go vet -tags e2e ./...` in `test/e2e` (compile only) |
| `modules-gp` | part | `modules`, `gp-module` | no | `modules` | none | union |
| `full` | full | everything + `[full].personal` | yes | all | today's 5 ports | union of all |

Notes:
- Library membership per component comes from each module's `go.mod` `require`/`replace` lines for in-repo modules. The generator derives it, so nobody hand-maintains it.
- `dashboard-api` includes the operator and agent because a kind dev cluster runs them; without them the dashboard has no servers to show (OD-2's reason).
- Ports and extensions per profile are derived: each component folder's catalogue entry lists its own, and parts take the union.
