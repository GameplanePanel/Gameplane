# Backup, restore and wipe ownership

## Backup consistency and volume coverage

A successful copy is not necessarily a complete recovery point. Defaults remain
`powerPolicy: preserve` and `volumePolicy: primary-only`: only `<server>-data`
is captured, using the existing quiesce behavior. Explicit opt-in is available on
Backup, BackupSchedule and GameServer's inline backupPolicy through the CRD/API:

```yaml
spec:
  serverRef: {name: my-game}
  repoRef: {name: restic-credentials, key: repo}
  strategy: restic-snapshot
  powerPolicy: stop-copy-restart
  volumePolicy: all-data
  quiesce: true
```

The two policies are independent. CSI (`volume-snapshot`) rejects stopped or
all-data policies; it remains primary-only and offers no cross-volume atomicity.
A preserve/all-data Restic copy captures one snapshot but reports
`Consistency=False/LiveMultiVolumeCopy`, even when primary-game quiesce succeeds.
Only a stopped, fully drained source supports consistency across included roots.
Quiesce still depends on the module's commands and cannot prove application state.
`Completed`/phase and snapshot ID report copy success separately from consistency.

`stop-copy-restart` acquires shared lifecycle ownership, persists original intent
and process identity, requests the existing graceful stop, and waits for zero
replicas, observed workload generation and all selected-claim users to drain.
It never changes `spec.suspend`. The source's planned stop precedes worker binding;
replacements after that binding invalidate the copy. Existing persisted preparation
and copy deadlines remain in force across controller restart. Failed stop/copy or
cancellation revokes authorization and drains workers before release. A grace
backstop may force Kubernetes shutdown; timeout alone is never proof of drainage.
After cleanup, only current allowed power intent resumes: manual suspension,
idle markers and pending restart/wake requests are preserved. A force-deleted
Backup leaves its stop debt on the GameServer; autonomous reconciliation drains
orphan workers and continues the original stop before releasing ownership.
`RecoveryIntervention=True` retains the fence when provenance is missing or a
workload was replaced. Inspect those identities and resolve the conflicting
workload/worker before recovery; deleting an owner marker does not prove safety.

`all-data` includes primary data, explicitly classified `data` extra volumes and
active separate loader/mod volumes. Every observed or declared required claim
must be classified and have a stable identity. Unknown or retained unmounted
claims reject admission rather than silently reporting a complete backup.
Declare genuinely reconstructible extra volumes `backupRole: rebuildable`;
these are excluded and reported as recovery prerequisites. Optional excluded
claims may be absent on the target; recovery never mounts or overwrites them.
Reconstruction remains an administrator/module responsibility and is not verified
by copy success. Suspend a recovery target until its prerequisites are prepared
if starting the game would otherwise be unsafe.

A v2 `status.captureManifest` pins template digest/name, selected game version,
claim names/UIDs, logical identities, original paths and exact disjoint
`/volumes/<logical-name>` archive roots. A single Restic invocation receives an
explicit argument per included root, mounted read-only; nested game mounts never
alias one another in the archive. The API and existing coverage rows expose
included/excluded volumes, roots and prerequisites. Captures keep that metadata
across later template changes.

V2 restore preflights the saved manifest against an existing target before
stopping it or creating a writer. Same-server recovery requires original claim
UIDs; another existing server must match the exact logical layout, template digest
and selected version, and its target claim UIDs are pinned. Missing/replaced
included claims, changed paths, duplicate/traversing roots or missing/downgraded
all-data metadata fail visibly without suspension or destructive work. A prepared
restore Pod mounts only validated included roots; after readiness the operator
revalidates the mapping, claim UIDs and PVC protection before issuing a Pod-bound,
expiring authorization. Retries need their own authorization. Only those explicit
roots receive `--delete`/`--include`; excluded roots are untouched. V2 restore
uses the lifecycle guard to stop without changing user suspend intent. Success
releases the guard and preserves current suspend/idle/restart intent; failed or
cancelled writes leave the target suspended for inspection.

Restic backup and restore workers run as UID 65532, with the template's
`security.fsGroup` (65532 by default). Source files must be readable by that
identity; restore requires writable existing files and writable/searchable
parent directories on every included claim. A successful backup does not prove
that an in-place restore can overwrite existing files. Configure ownership,
group permissions or ACLs for the worker before recovery. `fsGroup` relies on the
storage driver applying ownership; hostPath-based provisioners may not do so.
For example, a root-owned mode-0644 file remains unwritable to this worker even
when its parent directory is writable. Game containers may use other UIDs when
their storage permissions provide the required access. Permission failures are
reported as failed restores and leave the target suspended; recovery does not
elevate worker privileges or bypass filesystem permissions.

Legacy primary-only snapshots retain `/data` restore semantics. Missing historical
metadata or `LegacyUnknown` never receives fabricated coverage. All-data archives
cannot downgrade into that legacy path. Inspect original module/server settings
and recover uncovered volumes separately before using an old primary-only copy.

## Shared operation ownership

Gameplane serializes these operations per GameServer. All backups participate,
including backups with `quiesce: false` and CSI VolumeSnapshots. A held operation
also defers idle sleep/wake, restart, and pod-template rollout. Requests remain
pending and reconciliation resumes after the operation's cleanup completes.

Initial source admission waits within the prepared worker's persisted preparation
budget. CSI captures and older Jobs use a separate two-minute startup deadline,
recorded only after lifecycle ownership when the workload or its live source Pod
is unavailable. Queue time does not spend either budget, and operator restarts do
not extend them. Expiry fails the Backup and runs normal cleanup to release its
owner and workload fence. Prepared source identity and outstanding worker or
save-on obligations retain their existing safety checks.

The GameServer annotation `operations.gameplane.local/owner` identifies the
operation kind, name, UID, admitted generation and target UID. A wipe is identified
by the GameServer UID and its request token. The existing backup, restore and wipe
owner annotations remain compatibility markers. A `recovery` array identifies
older owners whose workers or quiesce obligations must be cleaned up first.

Deleting an operation requests cleanup. Restic Jobs are canceled with foreground
deletion; live and terminating worker pods must drain before the owner is released.
Quiesced backups also owe a successful save-on call. The existing bounded Backup
deletion retry may release its finalizer, but an unconfirmed save-on leaves the
durable owner behind for recovery by the next operation. Time alone never releases
ownership. CSI cancellation or expiry first invalidates the capture, then allows
save-on independently of driver reclamation. A late-ready aborted capture remains
unrestorable.

If an operation waits unexpectedly, inspect the referenced Backup/Restore, its
conditions, Jobs and worker pods. Resolve terminating workers, repository failures
or agent connectivity before retrying. Removing owner annotations or force-removing
finalizers cannot establish that a worker has stopped. A failed or canceled restore
leaves its target suspended for inspection.

Upgrades recognize existing owner annotations and import backup recovery lineage.
If that legacy record lacks the original Pod UID, cleanup cannot safely address
save-on to a running Pod. Its owner remains held until an uncached read proves
the game Pod absent or terminal and not terminating. An available agent or a
replacement Pod does not establish this proof; no unbound save-on is sent.
Already-running legacy quiesced copies continue being observed until their workers
finish and cleanup succeeds. Legacy raw workers also block new admission. Conflicting
legacy guards fail closed until their existing operations finish cleanup. Keep a
single active operator version during upgrade; older binaries do not honor the
shared owner. CSI abort reclamation adds cluster-scoped delete permission for
VolumeSnapshotContents; runtime checks restrict it to recorded, UID-verified
contents with `Delete` policy.

CSI restores create a new target and reserve it for the Restore. Initial startup
is allowed under that reservation so storage using WaitForFirstConsumer can bind.
Other operations wait until the restore completes. Existing data-PVC restores
continue to require an observed stopped workload and drained writers.

If initial startup fails or the Restore is deleted, cleanup first retires its
startup permission and suspends the target. The operation remains held until the
StatefulSet has observed zero replicas and every live or terminating game/PVC
writer has drained. Recovery of a force-deleted Restore applies the same check
before a successor can proceed, preserving the existing pod template while
requesting zero replicas. A successful Restore leaves its initial workload running.

Stopped-source proof, bounded copy preparation and CSI invalidation build on this
ownership boundary. Staged template adoption is tracked separately in #731.

## Backup preparation and capture budgets

Restic workers now schedule, pull their image, mount the source and validate the
repository before the agent is asked to disable saving. A Ready worker waits for
an expiring authorization bound to its pod UID, Backup UID and observed source.
The preferred node is the game's current node; `operator.backupHardSameNode: true`
makes that placement mandatory. Source tolerations and `operator.backupResources`
apply to preparation and copy containers. Stopped sources leave node selection
to the scheduler and volume affinity.

When the source StatefulSet requests a running workload, initial preparation
waits for its owned pod to be created and assigned a node before binding the
worker to that source. This wait uses the preparation budget and does not disable
saving. A pod or node replacement after binding still fails the copy. Raw backups
of intentionally stopped workloads remain eligible without a source pod.

Temporary Kubernetes source-read failures allow retries within the backup's
existing deadline. An unsuccessful source/readiness check revokes the start
token until the operator can verify the source again. Revocation can take time
to reach a worker and does not stop an already running copy, so lifecycle
ownership and save-on obligations remain until worker drain is confirmed.

The install settings are `operator.backupPreparationTimeout: 10m`,
`operator.backupQuiesceTimeout: 5m`, and `operator.backupCSICaptureTimeout: 10m`.
Equivalent operator flags use kebab-case names. The recorded deadlines remain
fixed across restarts or later configuration edits. Restic's existing Job
backoff/deadline settings remain separate; raw and already-stopped copies use
the full Job deadline. A live quiesce attempt retains the short budget even if
the agent reports the command unsupported. Increase the live quiesce budget
deliberately for worlds that cannot finish within five minutes, or use a
supported CSI capture strategy.

Preparation failure leaves saving enabled. Quiesce expiry marks the copy failed,
revokes authorization and cancels its Job. Saving is restored only after all
live or terminating workers have stopped. The worker itself also stops Restic
at the token deadline (TERM, then KILL after five seconds), including when the
operator is unavailable. This bounds copying; save-on still requires the operator
and agent to recover. `CaptureReady` describes preparation/authorization or a
pending drain. Resolve stuck mounts, node failures or terminating workers before
retrying; removing owner annotations is not proof that copying stopped.

Custom Restic images must provide the restic CLI (including `snapshots --retry-lock`),
POSIX `/bin/sh`, `date +%s`, `cat`, `touch`, `sleep`, and `timeout -s TERM -k 5`.
The pinned `restic/restic:0.17.1` release uses Alpine/BusyBox and supports this
contract. The gate runs under the existing non-root, read-only-root-filesystem
security context. Node/operator clocks must be synchronized for absolute token
expiry. No worker receives Kubernetes API credentials.

CSI expiry and Backup deletion invalidate an incomplete capture before save-on.
The `snapshot-aborted` marker is terminal even if a delayed driver later reports
readiness. Normal restore and new raw GameServer PVC references reject such
captures. `SnapshotCleaned` reports reclamation progress and records snapshot
and content names/UIDs; `Retain` content and unverified replacements require
administrator action. A slow driver can keep Backup deletion pending for storage
cleanup after saving and game ownership have already been restored. Inspect the
condition/event identities before deleting retained storage manually. Existing
PVCs remain usable after source Backup retention; administrator-managed external
snapshots keep their direct-CRD behavior.
