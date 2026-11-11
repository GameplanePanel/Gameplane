# Relay readiness and registration identity

The frp/Playit wrapper's process health and its provider connection are separate.
On Pod TCP port 8091, `/healthz` reports that the wrapper's HTTP server is alive,
`/readyz` returns 200 only when the provider connection and configured forwards
are confirmed, and `/status` returns a small JSON state without credentials or
provider response text. The operator installs a startup probe on `/healthz` and
a readiness probe on `/readyz` for frp. Playit uses `/healthz` for Pod readiness
because its pinned provider cannot supply live connection evidence. An external relay outage does not trigger a
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
The upgraded operator reconciles existing tunnel Deployments in place, replaces
their Pod templates with the UID environment variable and current probes, and
uses Recreate to retire the old client before starting its replacement. No
manual Deployment environment edit is required; upgrade the operator first or
together with its configured tunnel image.
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

Gameplane keeps reporting assigned addresses, but Playit's relay `/readyz` returns
503 and `TunnelReady` reports `RelayConnectionUnknown`; before assignment it
reports `AwaitingAddress`. Its Kubernetes readiness probe uses `/healthz`, so a
running wrapper can become Pod Ready while its connection evidence remains
unknown. The child keeps running and forwarding when it can, and Kubernetes does
not restart it solely for this unknown connection state. The JSON status distinguishes known assignment from
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

## Forwarding capacity

The embedded Tailscale relay exports the following series on port 8091 at
`/metrics`. A Ready relay can have full queues. Readiness measures the
connection and listeners, while these series measure the local forwarding path.

| Series | Meaning |
| --- | --- |
| `gameplane_tunnel_forward_metrics_supported` | 1 for Tailscale, 0 for frp and Playit. Those child processes own their data paths, so their forwarding series are absent. |
| `gameplane_tunnel_active_flows{protocol}` | Admitted TCP connections or UDP peer sessions, including upstream dial time. Protocol is `tcp` or `udp`. |
| `gameplane_tunnel_flow_rejections_total{protocol}` | TCP accepts closed at the shared limit, or incoming UDP datagrams from a peer for which no slot was available. A retry from the same UDP peer counts again. |
| `gameplane_tunnel_udp_queue_drops_total` | Datagrams discarded on arrival at an existing full peer queue. |
| `gameplane_tunnel_udp_forwarded_packets_total{direction}` | Successful datagram writes. Use `rate(...[1m])` for packets per second. Directions are `to_backend` and `to_client`. |
| `gameplane_tunnel_udp_forward_duration_seconds{direction}` | Histogram from local receipt through successful write, including the queue wait toward the backend. The echo client measures round-trip time. |

Counters span listener retries and reset on process restart. A successful UDP
write records handoff to the local socket. A datagram can still be lost
downstream, during dialing, or when a session ends.

The Helm values `operator.tailscaleMaxSessions` (default 128, range 1–256)
and `operator.tailscaleUDPQueue` (default 4, range 1–16) apply to every
Tailscale relay. Sessions × (queue + 1) must be at most 640. This keeps
queued datagrams and per-session receive buffers within the default worst-case
payload budget of about 40 MiB. The relay also needs memory for listeners,
socket buffers, workers, tsnet and the Go runtime. Changing the values replaces
the affected relay Pods. frp and Playit retain their provider-managed limits.

## Comparing direct and relayed UDP

Build `./cmd/relay-load` in the `tunnel` module and run its echo endpoint on
an isolated test backend. Expose the same UDP port through both the direct and
provider routes. Run the driver on one client host with access to both paths.

```sh
relay-load -echo 0.0.0.0:9000
# On the client, substituting the addresses reachable from that host:
relay-load -clients 32 -hz 128 -bytes 256 -seconds 30 direct.example:9000 relay.example:9000
```

Each target gets the same number of sockets, send schedule and payload size.
The driver emits a JSON object per target with scheduled sends, actual sends,
unique echoes within a one-second drain, late generator ticks, observed send
rate, loss percentage, and p95/p99 RTT and jitter in milliseconds. Jitter is
the absolute RTT difference for consecutive received sequence numbers on one
socket. Absent percentiles are null. Compare actual sends with the offered
rate before attributing a shortfall to the relay.

For a saturation trial, set the session limit below the number of clients.
Capture counter deltas between scrapes and compare UDP rejections and queue
drops with the client's missing echoes. Repeat both target orders at increasing
load on the same hardware. Record image digest, provider/route (including
direct tailnet or DERP), CPU and memory limits, relay CPU seconds during each
trial, offered and achieved packet rates, loss, jitter and RTT percentiles.
The sustainable rate is the highest repeated load meeting the game's declared
loss and latency limits with the generator keeping pace.

The amd64 tunnel CI job runs the driver against direct loopback echo and the
local Tailscale forwarding code. Its profile step prints CPU and allocation
hot spots under 32 clients at 128 Hz. It bypasses tsnet transport. Hosted
Tailscale direct/DERP and Playit rates require separate provider runs.

### Isolated FRP capacity qualification

On November 11, 2026, the actual frpc and frps processes forwarded UDP over a
loopback TCP connection on a Core i9-13900K Linux/WSL2 runner (kernel
6.18.54.1). The generator and echo process each had one logical CPU. Both FRP
processes shared a third. This topology measures software forwarding with a
finite relay CPU budget. WAN latency, cross-node CNI and hosted FRP capacity
remain unqualified.

Build the driver in `tunnel` with `go build -o /tmp/relay-load ./cmd/relay-load`.
Build frpc using the pinned Go module in `tunnel/frpc` and take frps from the
upstream FRP 0.71.0 Linux/amd64 archive (SHA-256
`84f27e39f11169f7adcef8e8b70c9329de17747b1f14dad9fb95eef5682ea716`).
On a Linux host with Python 3.11+, `taskset` and three available CPUs:

```sh
(cd tunnel/frpc && CGO_ENABLED=0 GOWORK=off go build -trimpath -mod=readonly -tags frpc,noweb -o /tmp/frpc github.com/fatedier/frp/cmd/frpc)
python3 tunnel/bench/frp_udp.py --relay-load /tmp/relay-load --frpc /tmp/frpc --frps /tmp/frps --clients 8 32 64 128 --seconds 10
```

Each client sent a 256-byte datagram at 128 Hz. Two series each ran both
route orders. The second series also sampled the FRP dashboard's per-proxy
payload byte counters. Columns give the worst measurement across four trials
per route and rate. CPU is the largest combined frpc + frps CPU-seconds delta
per ten-second trial, including the driver's final one-second receive window.

| Offered packets/s | Route | Minimum sent/s | Loss % | RTT p95/p99 ms | Jitter p95/p99 ms | Relay CPU s |
|---:|---|---:|---:|---:|---:|---:|
| 1,024 | Direct | 1,024 | 0 | 0.14 / 0.19 | 0.07 / 0.13 | 0.00 |
| 1,024 | FRP | 1,021.7 | 0 | 0.61 / 1.13 | 0.32 / 1.20 | 2.71 |
| 4,096 | Direct | 4,050.8 | 0 | 0.13 / 0.18 | 0.10 / 0.17 | 0.00 |
| 4,096 | FRP | 4,093.4 | 0 | 0.96 / 4.79 | 1.24 / 6.14 | 5.70 |
| 8,192 | Direct | 8,045.5 | 0 | 0.18 / 3.00 | 0.21 / 3.62 | 0.01 |
| 8,192 | FRP | 8,189 | 0.22 | 1.06 / 8.98 | 1.41 / 7.88 | 9.16 |
| 16,384 | Direct | 16,384 | 1.32 | 0.13 / 0.28 | 0.13 / 0.30 | 0.01 |
| 16,384 | FRP | 16,364.5 | 0.57 | 32.39 / 98.87 | 7.48 / 14.12 | 10.01 |

For a budget of at least 99% of offered sends, at most 0.1% loss and at most
5 ms each for p99 RTT and jitter, 1,024 offered packets/s passed all four FRP
trials. At 8,192 packets/s one instrumented trial sent 81,920 datagrams. The
server counted 81,738 datagrams' worth of payload in each direction and the
client received 81,738. The direct route left the FRP counters unchanged. One
high-load direct trial also lost packets, so the host contributes variability.
Report the individual trials when repeating this procedure on a deployment.

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
