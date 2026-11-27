# Contract: Backup status `dataSize`

CRD `backups.gameplane.io/v1alpha1`, additive and optional (no breaking change).

```yaml
status:
  phase: Succeeded
  size: 200Gi          # unchanged: logical/driver size
  bytesAdded: 1.2Gi    # unchanged: Restic only
  dataSize: "15032385536"  # NEW: bytes of server data at capture; absent = unknown
```

- Producer: the operator's Backup reconciler only (Restic summary or GameServer agent
  disk sample, see `../data-model.md`).
- Consumers: web Backups tab and Backups page (via the existing Backup list/get routes,
  which return the object as-is: no API change).
- Older operators never set it; clients MUST treat absence as unknown, never as zero.
