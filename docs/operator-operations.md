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

`status.captureManifest` is recorded once before the first copy worker is
created, after lifecycle ownership and quiesce preparation. It lists the primary
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
ownership. A pending CSI capture retains the current capture-drain behavior; a
separate CSI timeout change is tracked in issue #719.

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
shared owner. No new CRD fields or Kubernetes permissions are required.

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

Stopped-source preparation (#716), Restic preparation and quiesce deadlines (#717),
CSI capture deadlines (#719), and staged template adoption (#731) are separate
changes building on this ownership boundary.
