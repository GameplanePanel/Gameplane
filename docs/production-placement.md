# Resource reservations and management placement

New installs and upgrades reserve resources for supporting containers. These
requests are scheduling floors, not measured capacity targets:

| Container | CPU request | Memory request |
| --- | --- | --- |
| Agent and config initializer | 25m | 64Mi |
| Wake sentinel | 25m | 32Mi |
| Tunnel, every provider | 25m | 64Mi |
| Restic backup, repository initializer and restore | 100m | 128Mi |

These defaults add no CPU or memory limits. CPU limits can throttle bursts;
memory limits can terminate the process when its working set exceeds them.
Repository forget/prune retains its existing 50m/128Mi requests and 512Mi memory
limit unless overridden. Init containers receive requests too; Kubernetes
accounts for their peak requirements when scheduling the Pod.

## Overrides and upgrades

The new chart keys `operator.agentResources`, `operator.sentinelResources`,
`operator.tunnelResources` and `operator.backupResources` each accept a complete
Kubernetes ResourceRequirements object. Backup resources also apply to restore
and forget/prune workers; agent resources also apply to the config initializer.
The matching operator flags are `--agent-resources`, `--sentinel-resources`,
`--tunnel-resources`, and `--backup-resources`, with JSON values.

Null or a missing chart key uses the binary defaults, including older
`helm upgrade --reuse-values` configurations. A supplied object replaces defaults
wholesale. Clear reservations explicitly:

```yaml
operator:
  agentResources: { requests: {}, limits: {} }
  sentinelResources: { requests: {}, limits: {} }
  tunnelResources: { requests: {}, limits: {} }
  backupResources: { requests: {}, limits: {} }
```

An empty object `{}` has the same effect. Setting a key back to `null` restores
the defaults. No chart-side default map is merged into the supplied object.
For example `agentResources: {requests: {memory: 96Mi}}` reserves only memory.

Helm still merges user-supplied maps. When replacing a previously populated
override with empty maps, export the release's values, edit that complete file,
and upgrade with `--reset-values -f <edited-values.yaml>`. An empty nested map
combined with `--reuse-values` can retain the release's old resource entries.
Keep all unrelated install values in the edited file; resetting to chart defaults
alone is not a resource-only change.

Existing `GameTemplate.spec.agent.resources` takes precedence when it declares
requests or limits, replacing the install agent object wholesale. An image-only
agent override keeps install defaults. Kubernetes may omit empty maps in a
template, so use explicit zero CPU/memory requests for a per-template zero
reservation, or the install-level empty object to clear all defaults. Game
container resources continue to use the existing template defaults and complete
`GameServer.spec.resources` override. Game placement fields are unchanged.

Changing resources changes generated Pod templates. Existing game StatefulSets,
sentinels and tunnels roll according to their normal reconciliation policy;
new Restic Jobs use the new configuration. Schedule this upgrade when game pod
restarts are acceptable. Insufficient capacity can leave replacement pods or
Jobs Pending. Check requested resources and concurrent backups before upgrading.

## Management workers

The chart now exposes `nodeSelector`, `tolerations`, `affinity`,
`topologySpreadConstraints`, and `priorityClassName` beneath:

- `operator`, `api`, `web`, `gateway`, and `mcpServer`.
- `api.audit.webhook.syslogBridge` and `api.telemetry.receiver`.
- `crds.autoApply` for the upgrade hook Job.

Each Deployment also supports `pdb.enabled` and either `pdb.minAvailable` or
`pdb.maxUnavailable`; enabled budgets default to `maxUnavailable: 1`. PDBs select
both the component name and Helm release. Disabled components get no PDB. Hook
Jobs have placement settings but no disruption budget.

The API remains one process for both SQLite and PostgreSQL. Its optional PDB
only permits `maxUnavailable: 1`, so a voluntary node drain can evict it and will
cause API downtime. A PDB is not an availability guarantee and does not prevent
node failures. The persistent telemetry receiver is also a singleton; avoid a
zero-unavailable budget unless intentionally accepting a blocked drain.

Keep operator leader election enabled with redundant replicas and rolling
updates. The chart rejects multiple replicas with leader election disabled;
with one replica and election disabled it uses Recreate to avoid overlapping
reconcilers during upgrades. API replica/process restrictions remain unchanged.

When switching an existing operator to Recreate, a pre-upgrade hook clears the
server-defaulted RollingUpdate fields before Helm applies the Deployment. The
hook uses `crds.autoApply.image` and its placement settings, including when CRDs
are managed externally. It can patch only `gameplane-operator` in the release
namespace and is skipped when that Deployment does not exist. Upgrades need
access to the configured kubectl image.

The [example profile](../charts/gameplane/values-production.example.yaml) places
management services on labeled, tainted workers and spreads redundant operator
and dashboard pods. First label and taint the selected **worker** nodes:

```sh
kubectl label node management-a management-b gameplane.local/pool=management
kubectl taint node management-a management-b gameplane.local/pool=management:NoSchedule
kubectl label node game-a game-b gameplane.local/pool=games
kubectl taint node game-a game-b gameplane.local/pool=games:NoSchedule
```

Use existing GameServer placement fields for game workers:

```yaml
spec:
  nodeSelector: { gameplane.local/pool: games }
  tolerations:
    - { key: gameplane.local/pool, operator: Equal, value: games, effect: NoSchedule }
```

Preserve control-plane taints. Avoid broad `operator: Exists` tolerations.
Optional management components need their corresponding placement values too.
Per-game sentinel and tunnel Deployments are operator-generated and are not
covered by these chart management placement keys. A priorityClassName must name
an existing PriorityClass. Unsatisfied hard selectors, taints or topology
constraints correctly leave pods Pending; do not silently relax them.

Local or node-bound volumes cannot move just because a pod has a new selector.
Check API/receiver PVC node affinity before selecting management workers, and
game PVC topology before backups or moving game pods. Restic scheduling and
source preparation are separate from management placement.

## Sizing and controlled-drain checks

Collect per-container CPU/memory, pod requests, Pending events, game tick/query
latency, storage throughput and backup duration during busy periods and actual
backup concurrency. Use `kubectl top pods --containers`, scheduler events and
the available Prometheus metrics. Include headroom for one failed management
node, game startup, repository maintenance and replacement pods. No fixed server
count follows from these request values.

In a disposable multi-worker environment, apply the example and verify every
management pod's node labels. Temporarily set a nonexistent selector and verify
Pending/FailedScheduling, then restore it. Cordon one management worker, evict
its redundant operator/web pods through the eviction API, and verify their PDB
allows only one unavailable replica while the remaining replica stays Ready.
Uncordon after checking replacements. A full `kubectl drain` also evicts the
singleton API and can interrupt access; account for local PVCs and validate
recovery before doing this in production. CI chart checks verify selectors,
PDB gates, resource clearing and the production profile; cluster behavior needs
the multi-node integration scenario, not just a successful render.
