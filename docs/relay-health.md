# Relay readiness and registration identity

The frp/Playit wrapper's process health and its provider connection are separate.
On Pod TCP port 8091, `/healthz` reports that the wrapper's HTTP server is alive,
`/readyz` returns 200 only when the provider connection and configured forwards
are confirmed, and `/status` returns a small JSON state without credentials or
provider response text. The operator installs a startup probe on `/healthz` and
a readiness probe on `/readyz`. An external relay outage does not trigger a
liveness restart of the wrapper.

Provider checks run every two seconds with a two-second query deadline. Local
observations expire after ten seconds, and child exit immediately clears them.
The operator also requires the current Deployment generation, updated replicas
and the expected readiness probe; an old Pod without the probe cannot satisfy
the new readiness contract. Configured addresses remain visible while the
connection is unconfirmed, but `TunnelReady` is not true.

## frp

The generated frpc TOML sets `user` to the immutable GameServer UID. The wire
registration is `<UID>.<advertised-port-name>`; the GameTemplate's port names and
`remotePorts[].name` do not change. Pod and container restarts reuse the UID.
Deleting and recreating a GameServer creates a new identity, including when its
namespace and name are reused. Distinct clusters' UIDs also separate their
registrations on a shared frps.

frpc's authenticated status API listens only on `127.0.0.1:7400`. It uses the
mounted token as its local admin password; it is never exposed by a Service or
forwarded from the public relay. The wrapper does not use an environment HTTP
proxy or follow redirects for this request. Every configured TCP/UDP proxy must
appear with the matching name and protocol, a `running` status and no error.
Authentication failure, an occupied registration, absent proxy or unavailable
local API cannot produce readiness. This confirms the relay registrations, not
the health of a sleeping game backend.

Application heartbeat settings are five seconds between requests and fifteen
seconds before a silent control connection is closed; TCP multiplex keepalive
also uses five seconds. Allow the provider detection interval, local check and
Kubernetes probe/reconcile intervals for an outage to reach the dashboard.

### Upgrade and replacement

Upgrade the operator and tunnel image together. The new image requires
`GAMESERVER_UID`, so a mixed old-operator/new-image installation fails closed.
Existing unprefixed proxy names change to UID-prefixed names. Stop the old
client, wait for frps to unregister its old proxies and release their public
ports, or remove those registrations using the frps administrator's normal
procedure. Readiness stays false while a port is still occupied. Do not remove
an unrelated GameServer's registration to make an upgrade succeed.

The operator uses one replica and the Recreate strategy for relay Deployments.
It never deliberately rolls out two clients with the same provider identity.
Replacement has a short forwarding interruption while the old connection
closes and the new one registers. When a deleted old Pod's node is unreachable,
wait for frps to expire its old session before expecting the replacement ready.
Do not manually scale a relay Deployment above one replica.

## Playit connection evidence

The pinned Playit v1.0.10 IPC v2 contract exposes lifecycle and address
assignment, but no authenticated relay heartbeat or connection timestamp.
Its daemon caches `Running` after a successful API refresh and leaves that
cache unchanged when later refreshes fail. Receiving another `get_state`
response therefore cannot prove that a relay is connected or refresh a health
lease. Process age and the presence of an assigned address do not prove it
either.

Gameplane keeps reporting assigned addresses, but Playit's `/readyz` returns
503 and `TunnelReady` reports `RelayConnectionUnknown`; before assignment it
reports `AwaitingAddress`. `/healthz` remains successful, the child keeps running
and forwarding when it can, and Kubernetes does not restart it solely for this
unknown connection state. The JSON status distinguishes known assignment from
unknown connection. This is a deliberate limitation of the pinned provider,
not a claim that every Playit tunnel is disconnected.

Positive Playit readiness requires a future provider interface that exposes
fresh, authenticated relay connection evidence, or a separately designed
end-to-end probe. The relevant upstream definitions are
[the IPC model](https://github.com/playit-cloud/playit-agent/blob/v1.0.10/packages/playit-ipc/src/model.rs)
and [the daemon's cached lifecycle publisher](https://github.com/playit-cloud/playit-agent/blob/v1.0.10/packages/playitd/src/daemon.rs).

## Retry and resource policy

Unexpected child exits, including clean exits, wait 1, 2, 4, 8 seconds and so on,
up to 300 seconds, before another attempt. Cancellation interrupts the wait.
There is no jitter, as requested by the maintainer. Retry escalation resets only
after one uninterrupted minute of confirmed provider readiness. A failed or
stale observation breaks that interval. A long-lived disconnected process does
not reset it. Playit's unsupported connection evidence cannot reset it either.
The embedded Tailscale loop uses the same interval, fed by confirmed online
tailnet status and active forwarding listeners rather than process lifetime.

Resources use the existing `operator.tunnelResources` Helm value and
`--tunnel-resources` operator flag from the workload-resource policy. Omitted or
null configuration defaults to requests of 25m CPU and 64Mi memory, with no
limits. A non-null empty object explicitly clears defaults; supplied requests
and limits replace them. These are scheduling reservations, not a fleet
capacity guarantee. Readiness and replacement changes preserve this contract.

## Regression coverage

The CI optional-components lifecycle test runs two GameServers from the same
TCP/UDP echo template against one real frps, with different remote ports. An
independent client Pod checks replies identify the intended backend through
both protocols. It also covers registration rejection, replacement isolation,
invalid credentials, a shared relay outage and recovery, and credential-free
logs. Unit tests cover UID validation/stability, namespace/recreation identity,
status decoding, stale expiry, bounded retries/cancellation, healthy-interval
reset, old-deployment readiness, Playit's cached-running limitation, and
resource override removal. These regression tests are executed in CI.
