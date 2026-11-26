# Data model: Downloadable backup archives

CRD edits go in `operator/api/v1alpha1/`, followed by `make generate && make manifests` with every generated file in the same commit (core rule 7).

## New CRD: `BackupExport` (namespaced, short name `bkx`)

One download of one Backup. Created by the API, owned by the Backup (deleted with it), deleted by the API when the transfer ends.

```go
type BackupExportSpec struct {
    // BackupRef is the source Backup in the same namespace. Immutable.
    BackupRef LocalObjectRef `json:"backupRef"`
    // BackupUID pins the Backup identity; a recreated Backup under the same name is refused.
    BackupUID string `json:"backupUID"`
    // Format of the archive.
    // +kubebuilder:validation:Enum=tar.gz;zip
    Format string `json:"format"`
    // TTLSeconds bounds the export's life, default 3600, max 86400.
    // +optional
    TTLSeconds *int32 `json:"ttlSeconds,omitempty"`
}

type BackupExportStatus struct {
    // Pending → Ready (worker serving) → Completed | Failed | Expired
    Phase          string       `json:"phase,omitempty"`
    // ServiceName and ServerName (TLS SAN) the API/gateway dial.
    ServiceName    string       `json:"serviceName,omitempty"`
    SnapshotID     string       `json:"snapshotID,omitempty"`
    StartTime      *metav1.Time `json:"startTime,omitempty"`
    CompletionTime *metav1.Time `json:"completionTime,omitempty"`
    BytesSent      *resource.Quantity `json:"bytesSent,omitempty"`
    Message        string       `json:"message,omitempty"`
    Conditions     []metav1.Condition `json:"conditions,omitempty"`
}
```

Validation (reconciler, before any Pod): Backup exists, UID matches, `phase=Succeeded`, strategy `restic-snapshot`, `snapshotID` non-empty, repo Secret has the keys `missingRepoSecretKeys` checks (`backup_controller.go:900`). Otherwise `Failed` with the reason. Volume-snapshot Backups are refused (FR-002).

## `Restore` changes

`spec.backupRef` becomes optional; CEL: exactly one of `backupRef` and `upload`. Both immutable after creation except `upload.confirmed`.

```go
type RestoreUpload struct {
    // FileName as uploaded, for audit and display only. MaxLength 255.
    FileName string `json:"fileName"`
    // +kubebuilder:validation:Enum=tar.gz;zip
    Format string `json:"format"`
    // DeclaredBytes is the Content-Length the API accepted.
    DeclaredBytes int64 `json:"declaredBytes"`
    // ConfirmedTemplate is required for a plain archive (OD-4): the game the user said it is for.
    // +optional
    ConfirmedTemplate string `json:"confirmedTemplate,omitempty"`
    // Confirmed is set by the user after reviewing status.upload; only false → true.
    // +optional
    Confirmed bool `json:"confirmed,omitempty"`
}
```

New status fields:

```go
type RestoreUploadStatus struct {
    // Kind is "gameplane" (manifest found) or "plain".
    Kind             string `json:"kind,omitempty"`
    Manifest         *ArchiveManifestSummary `json:"manifest,omitempty"`
    UnpackedBytes    int64  `json:"unpackedBytes,omitempty"`
    StagingClaimName string `json:"stagingClaimName,omitempty"`
    ReceiverService  string `json:"receiverService,omitempty"`
    // Warnings shown before confirmation, e.g. older game version, zip permission reset.
    Warnings         []string `json:"warnings,omitempty"`
}
```

New phases appended to `RestorePhase`: `Receiving`, `AwaitingConfirmation`. Existing values keep their meaning; backupRef restores never enter the new phases.

## Archive manifest (`gameplane-backup.json`)

```json
{
  "formatVersion": 1,
  "writer": "gameplane v0.5.0",
  "cluster": "local",
  "namespace": "games",
  "server": {"name": "mc", "uid": "…"},
  "template": {"name": "minecraft-java", "revision": "…"},
  "gameVersion": "1.21.4",
  "capturedAt": "2026-11-26T10:00:00Z",
  "resticSnapshotID": "…",
  "captureFormatVersion": 2,
  "volumes": [{"logicalName": "data", "folder": "volumes/data", "classification": "data"}]
}
```

Unknown `formatVersion` > 1 → upload refused with "made by a newer Gameplane".

## Helm values (`charts/gameplane/values.yaml`)

| Value | Default | Use |
|---|---|---|
| `backups.maxUploadSize` | `20Gi` | FR-017 upload cap |
| `backups.uploadStagingStorageClass` | `""` | OD-7 staging class; empty = cluster default |
| `backups.maxConcurrentExports` | `3` | per-namespace download limit |
| `backups.exportTTL` | `1h` | BackupExport TTL default |

## RBAC (operator ClusterRole)

`backupexports` (all verbs on the CRD), plus create/delete on `persistentvolumeclaims`, `services`, `pods` it already has for restore workers (verify in `operator/config/rbac/role.yaml` during T010).
