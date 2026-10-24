# Tailscale forwarding integration

This procedure needs a disposable Kubernetes installation and a disposable
tailnet with two client devices. It creates real Tailscale devices and sends real
TCP and UDP traffic. It is an integration environment for #722, not evidence
that a live tailnet run has passed. Ordinary CI runs the local socket and
controller regressions without provider credentials. Follow repository policy:
execute this in an authorized integration environment, not a local test run.

Use the operator and tunnel image built from the same revision. Record their
digests, the revision, client versions, tailnet device IDs, conditions and traffic
results. Do not record auth keys, login URLs or private node state.

## Provision the disposable environment

Enable MagicDNS. Create a reusable auth key permitted to request
`tag:gameplane-test`. Configure a tailnet policy with no broad pre-existing allow
rules: permit the approved client to reach that tag on TCP 25565 and UDP 19132.
The second client must have no grant to the tag. For example, replacing the
example email with the approved test user's identity:

```json
{
  "tagOwners": {"tag:gameplane-test": ["autogroup:admin"]},
  "grants": [{
    "src": ["approved-test-user@example.com"],
    "dst": ["tag:gameplane-test"],
    "ip": ["tcp:25565", "udp:19132"]
  }]
}
```

Install this fixture in the disposable Gameplane cluster. Its third TCP port is
deliberately unadvertised. The backend prefixes replies so replacing it can be
distinguished from continuing to reach an old process.

```yaml
apiVersion: gameplane.local/v1alpha1
kind: GameTemplate
metadata:
  name: tailnet-echo
spec:
  displayName: Tailnet echo integration
  game: tailnet-echo
  version: "1"
  image: python:3.14-alpine
  consoleMode: none
  command: [python, -u, -c]
  args:
    - |
      import socketserver, threading
      class TCP(socketserver.BaseRequestHandler):
          def handle(self):
              while True:
                  data = self.request.recv(65536)
                  if not data: return
                  self.request.sendall(b'backend-v1:' + data)
      class UDP(socketserver.BaseRequestHandler):
          def handle(self):
              data, sock = self.request
              sock.sendto(b'backend-v1:' + data, self.client_address)
      for port in (25565, 25575):
          server = socketserver.ThreadingTCPServer(('0.0.0.0', port), TCP)
          threading.Thread(target=server.serve_forever, daemon=True).start()
      socketserver.ThreadingUDPServer(('0.0.0.0', 19132), UDP).serve_forever()
  ports:
    - {name: game, containerPort: 25565, protocol: TCP, advertise: true}
    - {name: query, containerPort: 19132, protocol: UDP, advertise: true}
    - {name: private, containerPort: 25575, protocol: TCP, advertise: false}
---
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: tailnet-echo
  namespace: gameplane-games
spec:
  templateRef: {name: tailnet-echo}
  networking:
    expose: ClusterIP
    tunnel:
      enabled: true
      provider: tailscale
      credentialsSecretRef: {name: tailnet-echo-tunnel-auth}
      tailscale:
        hostname: gameplane-forward-test
        tags: ["tag:gameplane-test"]
```

Provide the reusable auth key through the dashboard's tunnel credential form or
the credential API; these create the required GameServer ownership reference.
Wait for the tunnel Deployment's ready replica and `TunnelReady=True`. Confirm
the admin console shows the requested tag and exact hostname, with no advertised
subnet routes. Record the node ID and tailnet IPs. Remove conflicting stale test
devices if the hostname has acquired a suffix; that state must remain not ready.

## Exercise the datapath

On the approved tailnet client, run this with the fixture's MagicDNS name:

```python
import socket
host = 'gameplane-forward-test'
for kind, port in [(socket.SOCK_STREAM, 25565), (socket.SOCK_DGRAM, 19132)]:
    with socket.socket(socket.AF_INET, kind) as client:
        client.settimeout(5)
        client.connect((socket.gethostbyname(host), port))
        client.sendall(b'game-payload')
        assert client.recv(65536) == b'backend-v1:game-payload'
```

Repeat with the node's IPv6 tailnet address and `AF_INET6` when the environment
supports IPv6. Run two UDP clients concurrently with distinct payloads; each
must receive only its own reply. TCP 25575 and any other unadvertised port must
fail even from the approved client. Repeat the advertised-port probes from the
denied client; both must time out/fail while the tunnel remains ready. Removing
the approved grant must block new flows too; restore it before continuing.

Create an unrelated echo Service with a distinguishable prefix in the cluster.
It must not be reachable through this tailnet device on any port, and no route
to its ClusterIP or the cluster's Service/Pod CIDRs may appear on either client.
The local regression also rejects unrelated backing DNS names before listeners
are opened; no incoming packet contains a selectable destination.

Change the fixture backend prefix to `backend-v2:` and wait for its game Pod
rollout. New TCP connections must receive v2. Stop UDP traffic for over 30 seconds
to expire the old session, then send again and require v2. In the disposable
cluster, delete only the fixture's game Service and wait for the operator to
recreate it; repeat both probes. Existing connections may fail during replacement;
new sessions must resolve the Service again and recover without replacing the
tunnel Pod.

## Identity, authentication and shutdown outcomes

| Change | Required outcome |
|---|---|
| Restart the tunnel **container** while retaining the Pod and EmptyDir (use the integration runner's container runtime controls) | Same node ID, hostname and tags reconnect; TCP/UDP recover. |
| Delete the tunnel **Pod**, remove its stale device from the admin console, allow replacement | New identity registers using the reusable key; exact hostname/tags recover and both protocols work. |
| Replace Pod with an expired/revoked key in its credential Secret | Registration never makes the Pod ready and no Tailscale endpoint is advertised. Store a valid reusable key, replace the Pod and require recovery. |
| Request a tag not permitted by tagOwners | Not ready; there must be no untagged fallback device serving traffic. Restore permitted tags and repair credentials/device state, then verify recovery. |
| Rename an existing device to collide with the configured hostname | A suffix assigned to the replacement device cannot make Gameplane advertise the wrong hostname as ready. Resolve the collision and re-register. |
| Disconnect the node from the tailnet/control plane | Readiness turns false after the status/probe intervals; restore connectivity and require recovery. Do not use client ACL denial as a simulation of a disconnected server. |
| Delete/terminate tunnel Pod during active TCP and UDP traffic | Listeners and sessions close, worker shutdown does not hang, and clients reconnect only after the replacement is ready. |

Remove the GameServer and fixture template, revoke the disposable auth key, and
delete all test devices from the tailnet. Retain only the sanitized result record.

The implementation uses the official [tsnet APIs](https://tailscale.com/docs/reference/tsnet-server-api).
Its Go 1.26.10 minimum and Go 1.27.3 image builders include the `crypto/x509`
security fix described in the [Go release history](https://go.dev/doc/devel/release)
and [Tailscale security bulletin TS-2026-013](https://tailscale.com/security-bulletins#ts-2026-013).
