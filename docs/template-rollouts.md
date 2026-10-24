# Adopting template updates

Installing a Module updates its shared GameTemplate. Each GameServer now retains
an immutable copy of the template content it has admitted. The default
`Automatic` policy adopts installed changes as before, with lifecycle exclusion
and a graceful stop before replacement. To choose the time for a server, set:

```yaml
spec:
  templateRollout:
    policy: OnRequest
```

Enable this before updating the Module. A new server always provisions from the
currently installed template. Existing servers without a recorded snapshot get
a baseline snapshot without a workload restart. The operator preserves their
existing Pod template, including API defaults and admission additions, until
workload inputs change or the workload has stopped and drained. Their `running`
revision stays unknown until a replacement Pod actually carries a revision.

This baseline exception does not defer actual security changes: installing
agent-only credential projection or enabling the Restricted profile still
updates the workload. New template revisions must satisfy the enabled security
profile before admission and before the old workload is drained.

The baseline records currently installed content; it cannot recover historical
template contents from an old Pod or prove that the old Pod used that content.
Pin `Module.spec.version` before upgrading and enable staging before a later
catalog change. A pre-upgrade backup, restore or wipe already in progress finishes
before baseline initialization. A new server gets its pin before first startup.

Inspect the three digests:

```sh
kubectl get gameserver my-server -n gameplane-games \
  -o jsonpath='{.status.templateRevision}'
```

- `desired` identifies the installed template contents. It is empty when the
  installed template is unavailable.
- `effective` identifies the immutable template used for images, versions,
  config files and init containers, storage, ports, probes, agent configuration
  and other derived resources. During baseline initialization, the existing
  unknown Pod template is retained until a real workload change or stopped drain.
- `running` is observed on an owned Pod with a running game container. It is
  empty while stopped, while the game container is not running, or when the
  pre-upgrade Pod has no revision annotation. It is not a health assertion.

`TemplateUpdatePending=True` explains whether a revision is available, waiting
for another operation, or being adopted. `Unknown/TemplateUnavailable` preserves
an existing effective snapshot if the installed template disappears. A missing,
mutable, corrupt or foreign effective snapshot fails closed and requires repair;
it never silently switches the server to the latest catalog content.

To admit an update, copy the exact 64-character `desired` digest into the request:

```sh
revision=$(kubectl get gameserver my-server -n gameplane-games \
  -o jsonpath='{.status.templateRevision.desired}')
kubectl patch gameserver my-server -n gameplane-games --type=merge \
  -p "{\"spec\":{\"templateRollout\":{\"policy\":\"OnRequest\",\"requestedRevision\":\"$revision\"}}}"
```

An old request never approves a newer digest. Requests can be made one server at
a time or by an administrator's batch process. There is no built-in canary,
maintenance-window or fleet concurrency scheduler. Once admitted, an update
finishes using that exact snapshot even if another catalog update arrives or
the policy changes during shutdown.

Admission uses the same durable owner as backups, restores and wipes. It waits
for their cleanup, runs the previous effective template's graceful stop contract,
waits for the old Pod to disappear, then switches the effective pin and creates
the replacement. Ownership is released when the new Pod is observed, or when a
suspended workload is confirmed stopped with the new template. A bad image may
still fail startup; check ordinary readiness and health conditions separately.
Suspend and idle intent remain in effect. User changes to per-server overrides,
including `spec.version`, `spec.image` and `spec.config`, remain immediate and are
not controlled by the shared-template adoption policy.

Snapshots are immutable, GameServer-owned ConfigMaps, bounded to 900 KiB each.
The current and admitted revisions are retained during adoption; obsolete copies
are removed afterwards. Do not edit or delete them or the operator-owned revision
and operation annotations. A snapshot pins template contents, including image
references; use immutable image digests to prevent a mutable image tag changing
its registry content independently.

## Pinning and rollback

Set `Module.spec.version` to a known version to prevent tracking the catalog's
latest entry, including on installations that do not yet support staged adoption.
Use `Module.spec.digest` as well when the source supports OCI digest verification;
version tags alone may move. See [module installation](module-authoring.md#installing-a-module).

Adoption retains existing primary, extra and mod PVC identities and their data;
it does not recreate them to apply new template storage defaults. Removed extra
or mod volumes remain retained. Deleting the GameServer is different: its owned
claims can be garbage-collected. Keep an independently verified backup before
changing a game's runtime or save format.

Reinstalling an older Module version produces a desired template that can be
admitted with the same process. Reverting a runtime does not reverse save-format
changes. Restoring data may be necessary, and compatibility is game-specific.
Backups remain data backups, not automatic runtime rollback bundles. Record the
effective digest, immutable image reference and server configuration with your
backup procedure. Restic restores use the target's current effective layout; CSI
restores copy the source server's effective template at restore planning time,
not necessarily its template at backup time. Select a compatible Module version
and `OnRequest` policy before recovery if automatic adoption would be unsafe.
