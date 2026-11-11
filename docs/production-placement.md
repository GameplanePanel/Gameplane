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
container requests and limits merge per resource. For example, a server
overriding only `requests.cpu` inherits both memory values and any template
CPU limit. A tier with no CPU limit in either object keeps CPU bursting.

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

Redundant operator replicas use leader election and rolling updates. The chart
automatically enables election when replicas exceed one, even when an older
release retained `leaderElect: false`; upgrades continue rendering and install
notes explain the effective setting. With one replica and election disabled it uses Recreate to avoid overlapping
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

The production example enables `operator.gameWorkloadPolicy`. The operator
adds the chosen pool selector and its exact `Equal` / `NoSchedule` toleration
to each game Pod. A server can narrow placement with additional node labels
or affinity. A conflicting pool selector or an extra toleration produces a
failed Ready condition. A pool with no schedulable workers leaves games Pending.
Keep permission to change pool labels and taints with cluster administrators.
With the NodeRestriction admission plugin, a label key such as
`gameplane.node-restriction.kubernetes.io/pool` also prevents a kubelet from
labeling itself as a game worker. Set the configured key and worker taint to match.

The policy validates effective game resources before workload creation and
template adoption. The CPU request must fall between the configured bounds.
The memory request must meet its floor, and the memory limit must cover the
request and fit the configured ceiling. Set a CPU limit in the template or
server only for tiers that require one. Identify tiers by their measured
reservations and ceilings, since sidecar requests also influence Pod QoS.

The chart installs a Kubernetes v1 ValidatingAdmissionPolicy with the profile.
It checks newly created game Pods and in-place resizes in the managed game
namespaces, including replacements from pre-upgrade StatefulSets. Existing
running Pods keep their allocation until replaced. Before enabling the profile,
inventory live requests, limits, selectors, tolerations and PVC topology.
Correct incompatible templates or server overrides and roll games during a
maintenance window. A `null` policy keeps administrator enforcement disabled.

Without a policy, a server can request its own placement:

```yaml
spec:
  nodeSelector: { gameplane.local/pool: games }
  tolerations:
    - { key: gameplane.local/pool, operator: Equal, value: games, effect: NoSchedule }
```

Preserve control-plane taints. Avoid broad `operator: Exists` tolerations.
Optional management components need their corresponding placement values too.
The operator places Service-backed wakers and FRP, playit and Tailscale relays
on the game worker pool by default when `operator.gameWorkloadPolicy` is set.
To run those components on a separate pool, set
`operator.auxiliaryWorkerPool: gameplane.local/pool=relays`. Label the relay
workers with that pair and taint them `gameplane.local/pool=relays:NoSchedule`.
The operator adds the exact selector and toleration to each generated Deployment.
A Hostport waker always uses the game placement and the recorded endpoint node;
moving it to a relay worker would change the address players reconnect to.
Relays and Service wakers do not mount the game PVC. A node-bound game PVC
still determines where the game can resume after that relay receives a wake.

If the pool is full, drained or absent, the scheduler leaves the listener
Pending. Run `kubectl -n TENANT describe pod POD` and inspect its
`PodScheduled=False` condition and `FailedScheduling` events for conflicting
selectors, taints, affinity, or exhausted resources. Restore an eligible
worker for the same pool (and the original endpoint for Hostport).

The disposable kind management drill runs the operator on a tainted management
worker and provisions local-path and topology-bound CSI game volumes on a
separate game worker. It reads each world's saved marker after a wake through
ClusterIP, NodePort, the MetalLB address and Hostport. It drains each pool
in turn. The relay and games must report an unschedulable replacement while
their own worker is cordoned. After the game worker returns, the drill records
recovery time and verifies both PVC and PV identities and saved markers.
Each tunnel provider uses a local health responder. The CSI lane uses the
upstream distributed host-path reference driver. Qualify the site's external
network path and production CSI driver on its deployment topology.

A priorityClassName must name
an existing PriorityClass. Unsatisfied hard selectors, taints or topology
constraints correctly leave pods Pending; do not silently relax them.

Local or node-bound volumes cannot move just because a pod has a new selector.
Check API/receiver PVC node affinity before selecting management workers, and
game PVC topology before backups or moving game pods. Restic scheduling and
source preparation are separate from management placement.

## Tenant namespace quotas

Provision a namespace for each tenant and add it to the operator's managed
game namespaces. Configure API/RBAC grants for the same tenant boundary.
Set `operator.tenantQuotas.<namespace>.hard` for `requests.cpu`,
`requests.memory`, `limits.memory`, `pods`, `persistentvolumeclaims` and
`requests.storage`. The production values illustrate a budget for the default
games namespace. Replace that key if you change `gamesNamespace`. Tenants
sharing a namespace share its quota.

When using `limits.memory`, give supporting containers a memory limit.
`defaultContainerMemoryLimit` installs a LimitRange that supplies a default
for containers without an explicit memory limit. Size it for backup workers,
sidecars and initializers as well as games. A quota denial appears in
StatefulSet or Job events. Reserve quota for active backups, restore PVCs,
surge Pods and starts. Leave `limits.cpu` out of quotas for uncapped CPU tiers.

## Sizing and controlled-drain checks

Collect per-container CPU/memory, pod requests, Pending events, game tick/query
latency, storage throughput and backup duration during busy periods and actual
backup concurrency. Use `kubectl top pods --containers`, scheduler events and
the available Prometheus metrics. Include headroom for one failed management
node, game startup, repository maintenance and replacement pods. No fixed server
count follows from these request values.

Qualify each production tier on the intended node and storage classes. Record
the concurrent game count, tick/query latency p95 and p99, CPU throttling,
memory working sets and completed backup duration at steady state. Repeat
while a neighboring game allocates to its memory limit, a cold game starts and
the expected peak backup concurrency runs. Record the OOM termination and
verify the neighboring game stays within its latency target. Cordon and drain
one game worker and record replacement placement, PVC attachment, Pending
events and recovery time. For each eligible worker, compare remaining
allocatable CPU and memory after requests to the largest game start plus its
sidecars, and reserve capacity for the backup burst after one worker is lost.
Record each tier's accepted tenant quotas, node count and latency target from
that run before offering it to tenants.

The disposable `management-placement` CI bucket runs a Minecraft Java
1.21.4 flat world alongside the replacement tenant Pod and a Restic worker
on the sole remaining eligible node. It records Minecraft's own `tick query`
p95/p99, the worker's allocatable and requested resources, the tenant OOM
exit and the time to complete and verify the backup. It checks the neighbor's
Pod UID and container restart counts across the event. The small fixture's
100 ms p95 and 250 ms p99 budgets apply to this CI run. Qualify production
tiers with representative player counts, worlds and storage.

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
