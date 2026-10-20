# Contract: Helm values and Docker Compose

As merged in PR #630. Operator guide: `docs/standalone-panel.md`.

## Helm values (`charts/gameplane/values.yaml`)

| Value | Default | Meaning |
|---|---|---|
| `operator.enabled` | `true` | `false` removes every operator resource (Deployment, RBAC, namespaces, network policies, monitors, rules, module source, cosign key, Grafana dashboard; agent mTLS certificates in `mtls.yaml` unless the combined API needs them) |
| `api.standalone` | `false` | Passes `--standalone`; drops API RBAC, agent CA and client mounts, cluster-ops, CRD apply hook and MCP server |
| `api.replicas` | `1` | Must stay 1 in standalone (validated) |
| `api.panelKey.fileName` | `panel.key` | Key file under `/keys`; a simple name of at most 128 characters |
| `api.panelKey.existingSecret` | empty | Mount this Secret read-only at `/keys` (mode `0440`) and pass `--panel-key-provisioned` |
| `api.panelKey.secretKey` | `panel.key` | Entry in `existingSecret` |
| `api.panelKey.storage.existingClaim` | empty | Use an existing PVC for `/keys` |
| `api.panelKey.storage.size` | `1Mi` | Size of the generated `gameplane-api-key` PVC (kept on uninstall) |
| `api.panelKey.storage.storageClassName` | empty | Storage class for the generated PVC |
| `api.remoteAllowedCIDRs` | `[]` | Rendered as `--remote-allowed-cidrs` in standalone only |

### Render-time guards (`templates/validate.yaml`)

When `api.standalone=true`, rendering fails unless: the API is enabled; `operator.enabled=false`; `api.replicas=1`; `gateway.enabled` is not set; `panelKey.fileName` is a simple name; `existingSecret` and `storage.existingClaim` are not both set.

### Standalone pod settings (`templates/api.yaml`)

`Recreate` strategy, `automountServiceAccountToken: false`, `fsGroupChangePolicy: OnRootMismatch`; no data PVC when `db.driver=postgres`.

### Panel-only example

`charts/gameplane/examples/standalone-panel-values.yaml`. Install with `--skip-crds`: values cannot stop Helm from installing `crds/` (documented only; follow-up issue owed).

## Docker Compose (`deploy/standalone/`)

- `compose.yaml`: project `gameplane-panel`, services `gameplane-api` and `web` only. Volumes `panel-data` (`/data`) and `panel-keys` (`/keys`). API runs read-only, drops all capabilities, `no-new-privileges`, publishes no port. Web binds `127.0.0.1:${GAMEPLANE_PORT:-8080}`.
- `compose.provisioned-key.yaml`: overlay that mounts `GAMEPLANE_PANEL_KEY_SOURCE_FILE` as secret `panel-key` at `/run/secrets/panel-key` and sets `GAMEPLANE_PANEL_KEY_PROVISIONED=true`.
- Run exactly one `gameplane-api` container (documented; not enforced by Compose).
