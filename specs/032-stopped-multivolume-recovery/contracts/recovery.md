# Recovery contract

1. Acquire shared lifecycle owner. Persist original intent before graceful stop. No spec.suspend mutation by backup.
2. Freeze workload template; graceful stop uses existing deadline. Confirm zero replicas, observed generation, original Pod gone, all selected PVC writers drained. Never interpret timeout as drainage.
3. Persist exact coverage before creating immutable worker mounts. Reject unavailable, unclassified or ambiguous all-data layouts. Recheck claim UIDs/layout before authorizing worker token.
4. Capture one Restic snapshot with explicit argument roots. No glob or shell expansion from manifest text. Copy readers are read-only.
5. Revoke, drain and resolve original-process debt before release on success, failure or deletion. Original running intent allows normal desired-state reconciliation; newer user power requests remain untouched. Unknown debt retains ownership and intervention condition.
6. Restore preflight precedes suspension. Persist mapping and digest; compare again immediately before Job creation and before authorization of the ready mounted worker. Writable mounts and --delete scopes are exactly validated included roots. Legacy captures explicitly retain historical primary-only restore.

CI acceptance includes running/suspended/idle backup, stop/copy failure, cancellation and owner loss, claim replacement, changed paths, missing required extras, rebuildable exclusion, malicious roots, legacy and CSI compatibility; Kind round-trip covers primary + extra + separate mod volume.

Restic v2 restore selects `<snapshot>:/volumes` with target `/volumes`; each validated root `/volumes/<logical-name>` receives the subtree-relative include `/<logical-name>`. This preserves the exact mounted PVC paths and scoped deletion while preventing restoration of archived ancestor metadata onto the container's read-only `/volumes` directory. The snapshot and persisted manifest retain their original absolute roots.

Recovery retains the existing non-root Restic worker identity (UID 65532 with the template fsGroup). Included data must grant it backup read access and restore write access to existing files and parent directories. Storage-driver ownership behavior is a deployment prerequisite; manifest identity validation does not establish filesystem permissions. Failed permission checks during copying keep the target suspended and never trigger privilege escalation.
