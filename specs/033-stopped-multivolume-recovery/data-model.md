# Data model

BackupSpec and BackupScheduleSpec: powerPolicy enum preserve|stop-copy-restart; volumePolicy enum primary-only|all-data. Omitted fields retain existing behavior; CSI only permits preserve/primary-only.

BackupCaptureManifest: formatVersion, volumePolicy, templateName, templateRevision, gameVersion. Each BackupVolumeCoverage adds logicalName, snapshotRoot, recoveryPrerequisite. all-data logical roots are primary, extra-<declared name>, mods-<version identity>; one root per included claim.

GameServer lifecycle identity: optional stoppedBackup record with initial running intent, GameServer generation, suspend/idle/restart/wake baselines, original Pod UID and durable stage. Record survives Backup force deletion through recovery lineage. Status Recovery condition exposes stop/copy/release/intervention.

RestoreStatus: recoveryPlan containing immutable source Backup UID/snapshot/manifest digest, target GameServer UID/template/version and mapped volume records with pinned target claim UID and names. Reconciliation compares the live layout and PVCs before destructive worker creation.
