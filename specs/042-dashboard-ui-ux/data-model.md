# Data model: 042 dashboard UI and UX polish

Only one data change; the rest is presentation.

## Backup.status.dataSize (new, optional)

| Field | Type | Meaning |
|---|---|---|
| `dataSize` | `resource.Quantity`, optional | Bytes of server data when the backup was captured. Empty = unknown. |

Existing fields, unchanged:

| Field | Meaning (unchanged) |
|---|---|
| `size` | Logical size reported by the driver: Restic bytes processed, CSI restore size (= volume capacity). |
| `bytesAdded` | Restic packed data added to the repository by this run. Absent for CSI. |

### Rules

- Written once, when the backup reaches `Succeeded`; never rewritten.
- Restic: `dataSize = total_bytes_processed` (only when > 0).
- CSI snapshot: `dataSize = GameServer.status.agent.diskUsedBytes` read when the snapshot
  completes (game still quiesced) if the last heartbeat is no older than 10 minutes; otherwise empty.
- Never set from the volume capacity.
- Backups created before the operator upgrade have no `dataSize` (see OD-8 for display).

### Codegen chain (CLAUDE.md rule 7)

`operator/api/v1alpha1/backup_types.go` → `make generate && make manifests` → commit
deepcopy, `operator/config/crd`, `charts/gameplane/charts/gameplane-crds/crds/`,
`charts/gameplane/crd-manifests/` → `web/src/types.ts` (`dataSize?: string`).

## Display values (web)

| Where | Shows |
|---|---|
| Backup row Size | `dataSize`; second line "X added" when `bytesAdded` is set; OD-8 fallback when `dataSize` is empty |
| Summary card | "Total data size": sum of `dataSize` over succeeded backups, plus how many backups it covers when some lack it |
