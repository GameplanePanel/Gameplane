# Port pools (#1001)

## Intent and constraints

Some installs reach every game server through one public address: a relay,
or an edge that forwards a range of ports to the cluster's load balancer.
Minecraft Java clients send the hostname they connect to, so one port can
serve many servers behind a name-based router, but most games send no
hostname. Behind one address, each of their servers needs ports of its own.
Today an admin picks them by hand with `portOverrides` for every new server.
A port pool hands them out, so servers that players create are reachable with
no per-server work.

Constraints:

- A server keeps its ports for its life, since players save addresses.
- Two servers on one shared endpoint never publish the same protocol and port.
- A pool that has run out says so; it never falls back to ports another server
  in the pool may hold.
- A server that publishes pool ports keeps them, reserved, while a change it
  asked for can't be made yet.
- Only the Service players reach uses pool ports. Everything that dials the
  game inside the cluster keeps working.
- No CRD change. Configuration is operator flags and Helm values, like the
  address manager.

## The pool

A pool is a range of Service ports on one shared public endpoint. Its name
says which endpoint (for example `edge`), and it is the unit of competition:
every server that draws from the pool competes for its ports, whatever its
template. A port is reserved as a (protocol, port) pair, so TCP 2456 and UDP
2456 are different ports.

Templates select a pool. Each template draws from at most one pool. Template
names say which servers draw from the pool; they don't decide who competes,
which only the pool does.

```yaml
operator:
  portPools:
    - name: edge
      ports: "2456-2499"
      templates: [valheim, factorio]
```

renders `--port-pool=edge=2456-2499`, `--port-pool-template=valheim=edge` and
`--port-pool-template=factorio=edge`. The operator refuses to start with a
malformed range, a pool defined twice, a template that names an unknown
pool, or a template in two pools. Pools may overlap: they are different
endpoints. Gateway provisioning (forwarding the range at the edge) is outside
this feature.

## The records

A server's ports are recorded in annotations, in the format
`<pool>:<name>=<port>/<protocol>,...`, with one entry per advertised port in
template order, overridden ports included:

- The **active record**, `gameplane.local/pool-ports`, is the server's
  effective allocation. Its main Service and relays publish exactly these
  ports.
- The **pending record**, `gameplane.local/pool-ports-pending`, holds the
  ports reserved for a template rollout whose revision can't publish through
  the active record (see Rollouts).

Both records are reservations. Other servers read them, never the holder's
template, so a holder pinned to an older revision reserves exactly what its
Service publishes. They are annotations, not status, so a `kubectl replace`
of status can't lose them, and they survive operator restarts.

## Allocation

`reconcilePoolPorts` runs each pass before the tunnel and the Service, which
both use its result. It lists GameServers through the uncached reader and
holds a reconciler mutex while it decides and writes, so two servers are
never given the same block. The write carries the server's resourceVersion.

For a server whose template draws from pool P:

1. **Demand.** The server's advertised ports from its effective template
   revision. A port with an explicit `portOverrides[].servicePort` is
   **fixed** at that port; the others are **pooled**.
2. **Reservations.** Every other server's active and pending records for P.
   A terminating server still holds its ports, since its Service can outlive
   it until garbage collection removes it.
3. **Keep.** The active record is kept when it publishes exactly the
   demand's ports (the same names and protocols, each fixed port at its
   servicePort) and isn't **contested**: no older server's record holds one
   of its ports. Servers are ordered by creation time, then namespace, then
   name. The range isn't checked, so a pool that shrank leaves existing
   servers their ports (see Changes).
4. **Overrides.** A fixed port another server's record holds is refused (see
   Overrides); no new allocation is made.
5. **Allocate.** Otherwise the pooled ports get one contiguous block, in the
   range and in template order, that no other server's record and none of the
   server's own fixed ports use: at the active record's first pooled port
   when the block fits there, else the lowest such block. The server's own
   records don't block it, since its Service moves to the new block in one
   update. A server whose ports are all fixed records them and takes no
   block.
6. **No allocation.** When step 4 or 5 finds nothing, an active record that
   still names each of the effective template's advertised ports, with the
   same protocols, and isn't contested **stays in effect**: its ports stay
   reserved and published, and the change is reported as pending. Otherwise
   the active record is removed and the server is **unpublished**: its main
   Service is deleted and it is never given its template's ports instead.
   Retention never keeps a contested or unusable record.

Contiguous blocks matter for games that derive one port from another, such
as Valheim and other Steam games whose query port is the game port plus one.

A server with an effective record keeps its whole Service reconciled while a
change waits: selectors (so sleep and wake-on-connect keep working), ports
and metadata. Its relays and `status.endpoints` follow the same record.

### Overrides

`portOverrides[].servicePort` must respect existing reservations. An override
naming a (protocol, port) that another server's record holds is refused,
whatever the two servers' ages, and the holder keeps its record. The
requester reports `ServicePortConflict` and keeps its previous allocation
(rule 6), or reports `ServicePortConflictUnpublished` when it has none. An
override applied before another server's record names the same port is the
server's own; the age rule settles a duplicate only between records.

### Exhaustion

A new server with no block reports `PortAllocation=False`, reason
`PoolExhausted`, naming the pool and how many ports it needs, and has no
Service. A server with an effective record whose change can't be made
reports `ChangePending` and keeps its ports. Either way the game itself
keeps running, Stop and other lifecycle work continue, and the server checks
again every 30 seconds, since a deleted server frees its ports without
notifying the others.

### Rollouts

A template revision that changes the advertised ports can't use the active
record. The existing rollout machinery (`reconcileTemplateRevision`)
reserves the replacement before it disrupts anything:

1. **Admission.** Before a rollout is admitted (before its lifecycle owner is
   claimed and the running workload drained), `reservePoolPortsForRollout`
   plans the target revision's demand. A target the active record already
   fits needs nothing. Otherwise the plan is written as the pending record,
   under the mutex, so the capacity is durably reserved rather than merely
   checked. When no plan exists (a full pool, or an override another server
   holds), the rollout is not admitted: the server keeps its current
   revision, workload, Service and relays, and reports `RolloutPending` or
   `RolloutPortConflict` until capacity appears.
2. **Switch.** Before the rollout switches the effective revision, it
   confirms the reservation. The active record stays reserved and published
   until then.
3. **Promotion.** Once the effective revision is the target, the pending
   record fits the demand and becomes the active record; the Service, relays
   and endpoints move to it in the same pass.

A pending record is dropped when no rollout owns the server's lifecycle, for
example after a canceled rollout. A server with no active record has no
exposure to keep, so its rollout needs no reservation. If a pending record
is lost by hand after admission, the switch waits until it can be reserved
again, with the workload drained; this is not a rollback framework.

### Status

`PortAllocation` reasons:

| Reason | Status | Meaning |
| --- | --- | --- |
| `Allocated` | True | Lists the ports (`2456/UDP, 2457/UDP`) and the pool. |
| `AllocatedOutsideRange` | True | Also lists the ports that are outside a shrunk range. |
| `PoolExhausted` | False | No block; no Service. |
| `ServicePortConflict` | False | An override is held by another server; the server keeps its ports. |
| `ServicePortConflictUnpublished` | False | The same, for a server with no ports yet; no Service. |
| `ChangePending` | False | A change waits for capacity; the server keeps its ports. |
| `RolloutPending` | False | A template update waits for capacity, on the current revision. |
| `RolloutPortConflict` | False | A template update waits for an override's port another server holds. |

The condition is absent for a server outside every pool. Each reason has a
`condition.portallocation.*` translation and `status.conditionArgs`.
`status.endpoints` and the dashboard show the Service's ports, so players see
the pool ports.

## Where pool ports apply

- **Main Service** (`svcPortsFromTemplate`): each advertised port publishes
  its port in the active record, which already includes overrides, so an
  override waiting for a held port doesn't move the Service. A server with no
  record publishes its override `servicePort`, else its container port.
  `targetPort` is always the container port.
- **game-direct Service** (the sentinel's): publishes container ports. The
  sentinel listens on and dials container ports, so pool ports and override
  `servicePort`s apply only to the main Service.
- **Relays** (frp, Tailscale, Playit): dial the main Service, so their backing
  ports are its ports (`relayBackingPorts`). The Tailscale endpoint reports
  the same port, since the relay listens on it. Tunnel NetworkPolicies keep
  container ports, since traffic reaches the pod after the Service
  translates it.
- **Game NetworkPolicy and pod**: unchanged, container ports.

## Changes and recovery

- **Template revision changes the port set:** reserved before the rollout
  (see Rollouts), at the old first pooled port when the new block fits
  there, so players keep the address when it does.
- **Range shrinks:** existing servers keep their ports, including ports
  outside the reduced range, across operator restarts. They report
  `AllocatedOutsideRange`, listing those ports. The reduced range governs only
  new and replacement blocks. Retiring the old ports is a deliberate
  migration (for example, removing a server's record so it is placed again);
  keep forwarding them at the edge until it is done.
- **Range grows:** nothing moves; new blocks can use the new ports.
- **Template removed from a pool, or pool removed:** both records and the
  condition are removed, and the Service returns to the template's ports.
- **Template moved to another pool, or pool renamed:** the records are for the
  wrong pool, so the server is placed in the new one.
- **Record lost** (annotation deleted): the server is placed again, at the
  lowest free block, which may differ from its old ports.
- **Duplicate records** (a GameServer restored from a backup of the cluster):
  the older server keeps the ports. The younger is placed again, or
  unpublished when no block is free; it never keeps the shared ports.
- **Operator restart:** records are durable, so nothing moves.

## Non-goals

- Provisioning the shared endpoint's forwarding (gateway, edge or relay
  configuration).
- Checking overrides against servers outside the pool, or Services Gameplane
  doesn't manage.
- A CRD or dashboard surface for pools; the dashboard shows the result.

## Verification

Unit tests (`gameserver_portpool_test.go`) cover parsing, records, every
allocation rule above (age order, keeping, never taking held ports,
duplicates, a holder on another revision, a changed port set, reassignment,
overrides and refused overrides, per-protocol reservation, exhaustion, a
shrunk pool kept across a restart), retention of a serving record while a
change waits, contested records, rollout reservation and promotion, the
unpublished Service, Service, game-direct and relay ports, reconciliation
and API errors, and every condition reason and its args. E2E:

- `TestGameServer_PortPoolAllocation` (pool `e2e-pool`, 31000-31005): two
  templates' servers share the pool, an override is reserved, a full pool
  reports `PoolExhausted` with no Service, a deleted server's block goes to
  the waiting server, a client reaches a server through its pool port, and
  the game-direct Service keeps container ports.
- `TestGameServer_PortPoolServingAllocation` (pool `e2e-pool-serving`,
  31010-31012): in a full pool, an override naming another server's port is
  refused and both keep theirs; a template update that needs more ports waits
  on the current revision, with the game running, the Service kept, and
  sleep and wake routing still switching; when capacity frees up, the server
  it fits is rolled out onto its new block, the waiting server keeps its
  reservation, and a new server isn't given it.

Runtime tests run only in CI; local verification is compilation.
