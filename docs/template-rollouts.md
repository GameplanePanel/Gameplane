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
a time or through the restored-world fleet procedure below. Once admitted, an update
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

To cancel a held rollout, including one blocked by an unreadable admitted
snapshot or failed revalidation, read its exact admitted token and annotate:

```sh
revision=$(kubectl get gameserver my-server -n gameplane-games -o json |
  jq -r '.metadata.annotations["operations.gameplane.local/owner"] | fromjson | select(.kind == "Rollout") | .token')
kubectl annotate gameserver my-server -n gameplane-games --overwrite \
  "gameplane.local/cancel-template-rollout=$revision"
```

Cancellation first finishes any inherited worker/quiesce recovery, then releases
the lifecycle owner and workload fence. It preserves the current effective pin
and power intent, whether the workload is running or already shutting down; an
already-issued stop may still finish and restart with the retained pin. It sets
`OnRequest` and clears `requestedRevision`. The canceled digest remains blocked
until an operator removes `gameplane.local/cancel-template-rollout`, so automatic
adoption and first-pin initialization cannot immediately reacquire the owner.
Other lifecycle operations can proceed after cleanup. A token for a different
rollout cannot cancel the current owner. Cancellation does not roll back a pin
that has already committed or repair an unreadable effective snapshot.

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
backup procedure. Legacy primary-only Restic restores use the target's current effective layout.
V2 all-data Restic restores pin the capture template digest, selected version and
logical volume paths; an incompatible target fails preflight before any stop or
write. They restore included data/mod roots only and report excluded rebuildable
content as reconstruction prerequisites. CSI
restores copy the source server's effective template at restore planning time,
not necessarily its template at backup time. Select a compatible Module version
and `OnRequest` policy before recovery if automatic adoption would be unsafe.

## Restored-world fleet procedure

Use a dedicated Module installation for one fleet. Put **every** server
referencing this cluster-scoped template under `OnRequest` before publishing
a new bundle. Keep both bundle and game image in the registry. The admin
runner needs Python 3, kubectl, cosign, a trusted *game image* public key and
a join client for the game. Build the Minecraft Java/Terraria client from
the platform checkout with `go build -o join-once ./test/e2e/cmd/join-once`.
For an authenticated or proprietary game, supply a trusted entitled client
that implements the same CLI and returns `VERDICT<TAB>PASS<TAB>JOINED<TAB>evidence`.
The bundled Minecraft client expects offline-mode test servers.
The plan's `port` and `protocol` must name a game-container port. TCP (the
default) uses a port-forward to that specific Pod. For a UDP game set
`"protocol": "UDP"` and run its qualified join client on a host or in a Pod
that can route to the game's Pod IP. Permit this runner's traffic to the
declared UDP port in the selected workload's ingress policy.

Create a stopped, all-data Restic backup of a representative server. Create
and start a **separate** GameServer with the same template and version, restore
that backup into it, and wait for the Restore to succeed. Give each included
data or extra volume a persistent game marker. For excluded rebuildable mod
files, verify the reconstructed content or its expected absence on the target.
The runner compares the PVC UIDs in the Restore plan with the live clone,
records all owned claims around every adoption and compares marker and game
save command output exactly. Use a trusted plan with argument arrays that
check the actual data in each listed claim. A server's checks must account for
every retained claim. Use the server name in each real claim name.

An example plan for three Minecraft servers, each with only its primary PVC:

```json
{
  "namespace": "games", "module": "survival-fleet", "source": "survival-a",
  "backup": "pre-upgrade", "restore": "clone-from-pre-upgrade",
  "rollback": "restore-required", "game": "minecraft-java", "port": 25565,
  "baseline": {"revision": "<64-hex-template-revision>", "bundle": "sha256:<64-hex>", "image": "registry.example/game@sha256:<64-hex>"},
  "candidate": {"revision": "<64-hex-template-revision>", "bundle": "sha256:<64-hex>", "image": "registry.example/game@sha256:<64-hex>"},
  "canary": {"name": "survival-clone", "checks": [{"claim": "survival-clone-data", "argv": ["cat", "/data/fleet-marker"], "expected": "world-a"}]},
  "targets": [
    {"name": "survival-a", "checks": [{"claim": "survival-a-data", "argv": ["cat", "/data/fleet-marker"], "expected": "world-a"}]},
    {"name": "survival-b", "checks": [{"claim": "survival-b-data", "argv": ["cat", "/data/fleet-marker"], "expected": "world-b"}]}
  ],
  "save": {"argv": ["rcon-cli", "save-all flush"], "expected": "Saving the game (this may take a moment!)Saved the game"}
}
```

Use the exact positive acknowledgment for your qualified game version and
locale. The runner follows the server order in the target list.

```sh
opts=(fleet.json --context games-production --image-key keys/game.pub --join ./join-once --timeout 900)
python3 hack/rollout/canary.py preflight "${opts[@]}"
# Install the signed, digest-pinned candidate Module through GitOps; await Ready.
python3 hack/rollout/canary.py canary "${opts[@]}"
# Reinstall the signed, digest-pinned baseline Module; await Ready.
python3 hack/rollout/canary.py rollback "${opts[@]}"
# Restore the exact signed candidate Module; await Ready.
python3 hack/rollout/canary.py promote "${opts[@]}"
```

Preflight verifies the installed baseline and full inventory across namespaces.
The canary must boot, join and save on its restored old data, adopt the exact
candidate revision, then join, validate markers and save again. A compatible
rollback must boot the baseline on the migrated save. For `restore-required`,
the runner requests a suspended baseline and waits for its pod to disappear
before restoring the original snapshot and starting it. The
production targets stay on the baseline throughout this rehearsal. Promotion
requalifies the candidate on the clone and admits one fleet member at a time.
Each member must retain its PVCs and pass the same join, marker and save checks.

Progress, active target, PVC UIDs, image, revision and individual receipts
live in a namespace ConfigMap named `gp-rollout-<module-name-hash>`.
Resource-version updates serialize runs independently of the GameServer owner.
After a failure, inspect the journal and target before retrying the same phase
with the same plan. After a runner crash, verify that process has exited before
supplying its exact recorded token with `--recover-owner`. Archive the completed
journal before deleting it to start a different plan. Restrict this runner to
trusted release operators: it reads cluster-wide GameServers and module state,
reads namespaced pods/workloads/PVCs, patches named GameServers, creates clone
Restores and journal ConfigMaps, and uses namespaced pod exec and port-forward.
