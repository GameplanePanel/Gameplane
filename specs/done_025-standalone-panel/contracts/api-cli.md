# Contract: API flags, environment and commands

As merged in PR #630 (`api/cmd/main.go`, `api/cmd/panel_key.go`, `api/cmd/management.go`).

## `api serve` flags

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--standalone` | `GAMEPLANE_STANDALONE=true` | off | Store management records in SQL; never read a kubeconfig or in-cluster credentials; register no `local` cluster |
| `--panel-key-file` | `GAMEPLANE_PANEL_KEY_FILE` | `/data/panel.key` (legacy) | Credential encryption key. Compose and Helm set `/keys/panel.key` |
| `--panel-key-provisioned` | `GAMEPLANE_PANEL_KEY_PROVISIONED=true` | off | Key must already exist; never generated or written |
| `--remote-allowed-cidrs` | `GAMEPLANE_REMOTE_ALLOWED_CIDRS` | empty | Comma-separated CIDRs that narrow standalone Kubernetes and gateway destinations. Invalid entries fail startup |

Rules:

- `--standalone` with cluster-ops enabled fails at startup ("cluster-ops requires a local Kubernetes cluster").
- When credentials or a key-state row exist, a missing, unsafe or wrong key fails startup.

## `api rotate-panel-key`

Offline master-key rotation. Stop the API first.

| Flag | Environment | Required | Meaning |
|---|---|---|---|
| `--old-key-file` | `GAMEPLANE_PANEL_KEY_FILE` | yes | Current key; never removed or overwritten |
| `--new-key-file` | | yes | Separately named replacement; generated unless provisioned |
| `--new-key-provisioned` | | no | Replacement must already exist |
| `--db-driver` | `GAMEPLANE_DB_DRIVER` | no (`sqlite`) | `sqlite` or `postgres` |
| | `GAMEPLANE_DB_DSN` | no | Database DSN. Environment only: `--db-dsn` is rejected so credentials never reach argv or help text |

Behaviour: validates the old key against every stored row before creating the new key; re-encrypts all rows and updates the key-state row in one transaction; refuses identical paths or identical key material; a retry after a lost commit is recognised through `previous_key_id`. Afterwards, point `--panel-key-file` (or Helm `api.panelKey.fileName`) at the new file.
