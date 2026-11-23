# Data Model: Standalone Panel and API

As merged in PR #630. Source of truth is the code named under each entity.

## Management object

Table `management_objects` (`api/internal/db/migrations/common/016_standalone_management.sql`). Used only in standalone mode; the combined install keeps Kubernetes Secrets and Cluster CRs.

| Field | Type | Notes |
|---|---|---|
| `kind` | text | `secrets` or `clusters` |
| `namespace` | text | Secret namespace; empty for cluster-scoped registrations |
| `name` | text | Object name |
| `uid` | text | Assigned on create; checked by delete and update preconditions |
| `version` | bigint | Starts at 1; compare-and-swap on every update (Kubernetes `resourceVersion` semantics) |
| `payload` | text | `secrets`: ciphertext (see below). `clusters`: plain JSON with references only, never credential values |

Primary key `(kind, namespace, name)`. List supports label selectors and a continue token (unpadded URL-safe base64 of the last name). Dry-run writes are refused.

## Credential ciphertext

`api/internal/controlplane/key_ciphertext.go`, `store.go`.

- Format: `gpk1.<keyID>.` followed by base64 of nonce and AES-256-GCM output.
- Associated data: kind, namespace, name, UID and version, so a ciphertext cannot be copied to another record.
- Legacy headerless ciphertext from early PR #630 builds still decrypts.

## Panel key

`api/internal/controlplane/store.go`, `key_file_unix.go`, `key_acl_linux.go`, `key_directory.go`, `key_publication_unix.go`, `key_options.go`.

- 32 random bytes in a file outside the database. Path from `--panel-key-file`.
- Accepted only when: regular file, exactly 32 bytes, one hard link, owned by root or the API user, no group or world write, no execute bits, group-readable only through the API process group, no extended POSIX ACL entries (checked on the opened inode).
- Key ID: SHA-256 over a fixed domain string and the key.
- Generated keys: written to a temp file, fsynced, hard-linked into place without overwrite, parent directory fsynced, re-read and compared, all under `.management-key.lock`. New directories are created `0700`.
- Provisioned keys (`--panel-key-provisioned`): read only; never created or written.

## Key state

Table `management_key_state` (`017_standalone_key_state.sql`, `api/internal/controlplane/key_state.go`).

| Field | Type | Notes |
|---|---|---|
| `singleton` | integer | Always 1 (CHECK constraint) |
| `key_id` | text | Active key identity |
| `previous_key_id` | text | Key identity before the last rotation; lets a retried rotation recognise a lost commit |

Every credential write and every registration locks this row with `UPDATE ... WHERE key_id = ?` and holds it until commit. Zero rows updated means the process holds a retired key, and the write is refused.

### State transitions

```text
empty store ──first start──▶ key generated or provisioned, key_state initialised
established ──start with missing or wrong key──▶ startup fails (fail closed)
established ──rotate-panel-key (offline)──▶ all rows re-encrypted, key_id=new, previous_key_id=old
```

## Cluster registration

Stored as a `clusters` management object (standalone) or a `Cluster` CR (combined). Fields written by the API:

- `spec.kubeconfigSecret.{name, key: "kubeconfig"}`, pointing at an immutable Secret `cluster-<name>-kubeconfig-<32 hex>`.
- `spec.agentGateway.{url, tlsSecretRef.name}`, pointing at an immutable Secret `cluster-<name>-gateway-<32 hex>` with keys `ca.crt`, `tls.crt`, `tls.key` and label `gameplane.local/agent-gateway-credentials`.
- `status.{phase, message, serverVersion, lastCheckTime}`, written only by the standalone health poller with fixed messages.

API-owned credentials carry `gameplane.local/managed-by=gameplane-api` and are deleted with UID and version preconditions.

## Installation capabilities

`GET /admin/installation` (`api/internal/handlers/installation.go`): `{ "standalone": bool, "localCluster": bool, "version": string }`. `localCluster` is `!standalone`.

## Notification event

`api/internal/notify/notify.go`: `Event` gains an optional `cluster` field (`json:"cluster,omitempty"`); titles show `cluster/namespace/name` when it is set.
