# Backup, restore and wipe ownership

## Backup consistency and volume coverage

A successful copy is not necessarily a complete server recovery point.
`status.conditions[type=Consistency]` reports the achieved result separately
from `Completed`: Quiesced means the agent confirmed the module's quiesce
commands before the completed copy; it does not prove those commands flush all
application state. UnsupportedRawCopy, RawCopy and AgentUnavailableRawCopy
describe restic copies without confirmed quiesce. CSI without confirmed
quiesce reports CrashConsistentSnapshot. StoppedSource means the source was
already stopped and its writers drained before capture; the backup did not stop
it. Failed or unfinished copies, including completed Jobs still awaiting a
snapshot ID, cannot report achieved quiesce or stopped-source consistency.

`status.captureManifest` is recorded once before copy authorization or CSI
capture, after lifecycle ownership and quiesce preparation. It lists the primary
`<server>-data` claim as included and every other observed persistent claim as
uncovered, including extra volumes, nested mod/loader mounts and retained
server-owned claims. It records claim UIDs, observed mount paths, source layout
and optional `data`/`rebuildable` declarations. Included describes copy scope;
check the Backup's phase and snapshot ID for copy success. A declaration that
an uncovered claim is rebuildable is not a recovery guarantee.

The API returns this status and the backup details drawer displays consistency
and coverage. Capture metadata is immutable through ordinary reconciliation:
editing a template later never changes the historical list. This release does
not add multi-volume restic, automatic game stopping or a stricter consistency
request policy.

Pre-upgrade captures have missing metadata or `state: LegacyUnknown`; their
Consistency condition is Unknown. The operator never reconstructs their history
from the current template. Before recovery, inspect the original restic snapshot
or CSI source claim, recover the original module/server configuration and arrange
separate recovery of extra/mod volumes. Use an isolated recovery server to
verify those prerequisites before replacing a live world.

## Shared operation ownership

Gameplane serializes these operations per GameServer. All backups participate,
including backups with `quiesce: false` and CSI VolumeSnapshots. A held operation
also defers idle sleep/wake, restart, and pod-template rollout. Requests remain
pending and reconciliation resumes after the operation's cleanup completes.

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
