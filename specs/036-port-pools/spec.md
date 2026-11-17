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

## The record

A server's ports are recorded in its `gameplane.local/pool-ports`
annotation, `<pool>:<name>=<port>/<protocol>,...`, with one entry per
advertised port of its effective template revision, in template order,
overridden ports included. The record is the reservation: other servers read
it, never the holder's template, so a holder pinned to an older revision with
a different number of ports reserves exactly what its Service publishes. It is
an annotation, not status, so a `kubectl replace` of status can't lose it.

## Allocation

`reconcilePoolPorts` runs each pass before the tunnel and the Service, which
both use the result. It lists GameServers through the uncached reader and
holds a reconciler mutex while it decides and writes, so two new servers are
never given the same block. The write carries the server's resourceVersion.

For a server whose template draws from pool P:

1. **Demand.** The server's advertised ports from its effective template
   revision. A port with an explicit `portOverrides[].servicePort` is
   **fixed** at that port; the others are **pooled**.
2. **Reservations.** Every other non-terminating server's record for P.
   Terminating servers hold nothing.
3. **Keep.** The pooled ports keep their recorded ports when the record is
   for P, names every pooled port with the same protocol, each port is in the
   range, none equals one of the server's own fixed ports, and none is held by
   an **older** server's record. Servers are ordered by creation time, then
   namespace, then name.
4. **Allocate.** Otherwise the pooled ports get one contiguous block, in
   template order, that collides with no other server's record and none of the
   server's fixed ports: at its previously recorded first pooled port when the
   block fits there, else the lowest such block. A younger server holding a
   contested port is not displaced by an allocation; only rule 3's older-wins
   resolves a true duplicate (for example after a restore).
5. **Record.** The server's record becomes its fixed and pooled ports. A
   server whose ports are all fixed records them, reserving them, and takes
   no block.

Contiguous blocks matter for games that derive one port from another, such
as Valheim and other Steam games whose query port is the game port plus one.

### Overrides

`portOverrides[].servicePort` still wins. The fixed port is recorded, so the
pool never hands it to another server. The operator doesn't refuse an
override that collides with another server's record; it is the admin's
explicit choice, and the other server keeps its record.

### Exhaustion

When no block is free, the server's record is removed and it reports
`PortAllocation=False`, reason `PoolExhausted`, naming the pool and how many
ports it needs. It gets no Service: it is not created, and one left from
before is not updated. The game itself still runs. The server checks again
every 30 seconds, since a deleted server frees its block without notifying
the others, and takes a block as soon as one is free.

### Status

`PortAllocation=True`, reason `Allocated`, lists the ports (`2456/UDP,
2457/UDP`) and the pool. The condition is absent for a server outside every
pool. Both reasons have `condition.portallocation.*` translations and
`status.conditionArgs`. `status.endpoints` and the dashboard show the
Service's ports, so players see the pool ports.

## Where pool ports apply

- **Main Service** (`svcPortsFromTemplate`): each advertised port publishes
  its override `servicePort`, else its recorded pool port, else its container
  port, with `targetPort` the container port.
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

- **Template revision changes the port set:** the record no longer names the
  same pooled ports, so the server is placed again, at its old first pooled
  port when the new block fits there. Players keep the address when it does.
- **Range changes:** a server with a recorded port outside the new range is
  placed again, at its old first pooled port when the block still fits;
  servers whose ports are all in range keep them. Shrinking a pool can move servers or leave them
  exhausted, so grow pools rather than shrink them.
- **Template removed from a pool, or pool removed:** the record and the
  condition are removed, and the Service returns to the template's ports.
- **Template moved to another pool, or pool renamed:** the record is for the
  wrong pool, so the server is placed in the new one.
- **Record lost** (annotation deleted): the server is placed again, at the
  lowest free block, which may differ from its old ports.
- **Duplicate records** (a GameServer restored from a backup of the cluster):
  the older server keeps the ports, the younger is placed again.
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
overrides, per-protocol reservation, exhaustion), Service, game-direct and
relay ports, reconciliation and API errors, and the condition and its args.
E2E `TestGameServer_PortPoolAllocation` runs a kind install with pool
`e2e-pool` (31000-31005): two templates' servers share the pool, an override
is reserved, a full pool reports `PoolExhausted` with no Service, a deleted
server's block goes to the waiting server, a client reaches a server through
its pool port, and the game-direct Service keeps container ports. Runtime
tests run only in CI; local verification is compilation.
