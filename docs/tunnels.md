# Tunnels

A GameServer can optionally route players through an external relay — the server
dials outbound to the relay and players connect to the relay's address. This exists
for installs with no public IP and no ability to port-forward (homelab k3s behind
CGNAT, ISP-controlled routers, etc.). It layers over the backing Service; it is
not a fifth `expose` mode.

## When to use a tunnel

Use a tunnel [optional] if:

- You have no public IP (e.g., CGNAT, shared ISP).
- Port-forwarding is unavailable (router is locked down or managed by your ISP).
- `expose: LoadBalancer` is not an option (you don't have an external load balancer
  service, e.g., you're not on AWS/GCP/Azure).

Do **not** use a tunnel if:

- You already have a public IP and working port-forwarding.
- You have a working LoadBalancer and it reaches your cluster.
- You're only deploying to private networks (no public play access needed).

If you have any of the above, those approaches are simpler and cheaper — tunnels
add an extra pod and outbound dependency.

## Choosing a provider

| Feature | frp | Tailscale | playit.gg |
|---|---|---|---|
| **Public/Private** | Public | Private (tailnet only) | Public |
| **UDP support** | Yes* | Yes | Yes |
| **Address assignment** | Static (you set it) | MagicDNS (static name) | Dynamic (assigned at runtime) |
| **VPS required** | Yes (you run frps) | No | No |
| **Cost** | Your VPS cost | Free (open source) | Free (limits: 8 hours/day or paid unlimited) |
| **Setup time** | 30–60 min (deploy frps) | 5 min (get auth key) | 2 min (sign up, get secret) |
| **Best for** | Production, custom domains | Private homemade networks | Casual play, quick setup |

\* For frp: the protocol (TCP or UDP) for each forwarded port is determined by the port's `protocol` field in the GameTemplate, not configured separately in the frp tunnel spec. Each port you forward to frps must be declared with its matching protocol in the template.

### frp

**Use case:** production servers, custom public domain, full control.

Self-hosted. You run an `frps` (frp server) on a VPS you control, and Gameplane
runs the `frpc` (frp client) in a tunnel pod that dials your `frps` and holds
open reverse-proxy tunnels. The public address is static and derived from your
config: `<serverAddr>:<remotePort>`.

### Tailscale

**Use case:** private networks, invite-only play, no public internet needed.

The tunnel pod joins your Tailscale network and becomes a device reachable by
all users on your tailnet via MagicDNS. Players access it only if they're also
on your tailnet. This is **not** public — Tailscale Funnel does not help here,
as Funnel only exposes TCP on ports 443/8443/10000 with TLS, and cannot carry
arbitrary game ports or UDP.

### playit.gg

**Use case:** free, public, zero setup. Casual play and quick testing.

Free public relay service. Gameplane writes a config file with your secret key,
the tunnel pod connects and holds a tunnel, and playit assigns a public address
at runtime. The address appears in `status.endpoints` once the tunnel pod
reports it back. Free tier allows 8 hours/day per secret; paid plans unlock
24/7. No VPS needed.

## Setup

### frp

#### 1. Deploy frps on a VPS

You control the VPS (this is not part of Gameplane). Deploy `frps` and pick a
free port for client connections (e.g., 7000). Refer to [frp's
documentation](https://github.com/fatedier/frp) for detailed setup.

Example systemd unit (adjust paths and token to suit):

```ini
[Unit]
Description=frps server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/frps -c /etc/frp/frps.toml
Restart=on-failure
User=frp

[Install]
WantedBy=multi-user.target
```

Example `frps.toml`:

```toml
bindAddr = "0.0.0.0"
bindPort = 7000

# Require clients to authenticate with a token
auth.method = "token"
auth.additionalScopes = ["api"]
auth.token = "your-secure-token-here"
```

#### 2. Create a GameServer with frp tunnel

```yaml
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: minecraft-tunneled
  namespace: gameplane-games
spec:
  templateRef:
    name: minecraft-java
  networking:
    expose: ClusterIP
    tunnel:
      enabled: true
      provider: frp
      credentialsSecretRef:
        name: minecraft-tunneled-tunnel-auth
      frp:
        serverAddr: frp.example.com
        serverPort: 7000
        remotePorts:
          - name: game
            remotePort: 25565
```

The `credentialsSecretRef.name` is the name that the dashboard or API write after you provide the credential.

#### 3. Provide the credential

The operator only mounts a credentials Secret whose `ownerReference` matches this GameServer's name **and UID** — a plain `kubectl create secret generic` Secret has none, and a Secret left over from a deleted same-name GameServer has a stale UID, so the operator refuses it and the GameServer's `TunnelReady` status condition
reports `False` with reason `TunnelCredentialRefused`, naming the Secret (never
its contents). The tunnel pod starts without credentials until the credential is
provided (the Secret does not exist yet). Use one of these instead:

- **Dashboard:** open the server's Networking tab and enter the credential
  there — it creates the Secret with the correct ownerReference for you.
- **API:** `PUT /servers/{name}:tunnel-credentials` with body
  `{"provider": "frp", "values": {"token": "your-secure-token-here"}}`.
- **kubectl, with an explicit ownerReference:** if you must create the Secret
  directly, get the GameServer's UID with `kubectl -n gameplane-games get gameserver minecraft-tunneled -o jsonpath='{.metadata.uid}'`,
  then set `metadata.ownerReferences` to point at the GameServer
  (`apiVersion: gameplane.local/v1alpha1`, `kind: GameServer`, matching `name`
  and `uid`) so the operator accepts it.

To repair a Secret that is already refused: the dashboard and API always write
`<server>-tunnel-auth`, so they fix a refused Secret with any other name by
creating an owned one and pointing `credentialsSecretRef` at it. If the refused
Secret is itself named `<server>-tunnel-auth`, they answer `409 Conflict`
instead — add the ownerReference to it, or delete it and save the credential
again.

Players connect to `frp.example.com:25565`.

### Tailscale

#### 1. Generate an auth key

Log into [login.tailscale.com](https://login.tailscale.com), go to **Settings →
Keys**, and create a **Reusable key**. Copy it.

#### 2. Create a GameServer with Tailscale tunnel

```yaml
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: minecraft-tailnet
  namespace: gameplane-games
spec:
  templateRef:
    name: minecraft-java
  networking:
    expose: ClusterIP
    tunnel:
      enabled: true
      provider: tailscale
      credentialsSecretRef:
        name: minecraft-tailnet-tunnel-auth
      tailscale:
        hostname: my-minecraft-server
        tags:
          - tag:gameplane
          - tag:minecraft
```

The pod joins your tailnet and is reachable via MagicDNS under the hostname you
specify in `spec.networking.tunnel.tailscale.hostname` (or defaults to the
GameServer name if left empty). Port 25565 is available to devices on your
tailnet.

> **`tags` requires an ACL grant.** `spec.networking.tunnel.tailscale.tags`
> is requested when the device registers with your tailnet: the tunnel pod
> runs `tailscale up --advertise-tags=<tags>` once, over the local daemon's
> socket. Tailscale's headless daemon config has no field for tags, and
> changing the tags of a registered device needs a fresh login (see
> `tunnel/specs.md`). The tailnet's ACL must grant `tagOwners` for the
> requested tags to the auth key's owner. If it doesn't, Tailscale refuses
> the request and the tunnel pod logs the error. If the device is not
> running afterwards, the pod tries once more without tags. That untagged
> attempt can fail too (for example with a single-use auth key), in which
> case the error is logged and the device stays logged out. Configure `tagOwners` in your tailnet's ACL before setting
> `tags` here. Each tag is `tag:<name>` or a bare `<name>`, where the name
> starts with a letter and holds only letters, digits and `-`. If any tag is
> invalid, the whole list is logged and ignored and the device registers
> untagged.

The `credentialsSecretRef.name` is the name that the dashboard or API write after you provide the credential.

> **Private only.** The server is not exposed to the public internet — only to
> devices on your Tailscale network. This is suitable for playing with friends
> or family who are all on your tailnet.

#### 3. Provide the credential

The operator only mounts a credentials Secret whose `ownerReference` matches this GameServer's name **and UID** — a plain `kubectl create secret generic` Secret has none, and a Secret left over from a deleted same-name GameServer has a stale UID, so the operator refuses it and the GameServer's `TunnelReady` status condition
reports `False` with reason `TunnelCredentialRefused`, naming the Secret (never
its contents). The tunnel pod starts without credentials until the credential is
provided (the Secret does not exist yet). Use one of these instead:

- **Dashboard:** open the server's Networking tab and enter the credential
  there — it creates the Secret with the correct ownerReference for you.
- **API:** `PUT /servers/{name}:tunnel-credentials` with body
  `{"provider": "tailscale", "values": {"authKey": "tskey-client-xxxxx"}}`.
- **kubectl, with an explicit ownerReference:** if you must create the Secret
  directly, get the GameServer's UID with `kubectl -n gameplane-games get gameserver minecraft-tailnet -o jsonpath='{.metadata.uid}'`,
  then set `metadata.ownerReferences` to point at the GameServer
  (`apiVersion: gameplane.local/v1alpha1`, `kind: GameServer`, matching `name`
  and `uid`) so the operator accepts it.

To repair a Secret that is already refused: the dashboard and API always write
`<server>-tunnel-auth`, so they fix a refused Secret with any other name by
creating an owned one and pointing `credentialsSecretRef` at it. If the refused
Secret is itself named `<server>-tunnel-auth`, they answer `409 Conflict`
instead — add the ownerReference to it, or delete it and save the credential
again.

### playit.gg

#### 1. Sign up and get a secret key

Visit [playit.gg](https://playit.gg), sign up, and create an account. Your
**Secret Key** appears in **Settings**. Copy it.

#### 2. Create a GameServer with playit tunnel

```yaml
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: minecraft-playit
  namespace: gameplane-games
spec:
  templateRef:
    name: minecraft-java
  networking:
    expose: ClusterIP
    tunnel:
      enabled: true
      provider: playit
      credentialsSecretRef:
        name: minecraft-playit-tunnel-auth
```

The `credentialsSecretRef.name` is the name that the dashboard or API write after you provide the credential.

#### 3. Provide the credential

The operator only mounts a credentials Secret whose `ownerReference` matches this GameServer's name **and UID** — a plain `kubectl create secret generic` Secret has none, and a Secret left over from a deleted same-name GameServer has a stale UID, so the operator refuses it and the GameServer's `TunnelReady` status condition
reports `False` with reason `TunnelCredentialRefused`, naming the Secret (never
its contents). The tunnel pod starts without credentials until the credential is
provided (the Secret does not exist yet). Use one of these instead:

- **Dashboard:** open the server's Networking tab and enter the credential
  there — it creates the Secret with the correct ownerReference for you.
- **API:** `PUT /servers/{name}:tunnel-credentials` with body
  `{"provider": "playit", "values": {"secretKey": "<your-secret-key>"}}`.
- **kubectl, with an explicit ownerReference:** if you must create the Secret
  directly, get the GameServer's UID with `kubectl -n gameplane-games get gameserver minecraft-playit -o jsonpath='{.metadata.uid}'`,
  then set `metadata.ownerReferences` to point at the GameServer
  (`apiVersion: gameplane.local/v1alpha1`, `kind: GameServer`, matching `name`
  and `uid`) so the operator accepts it.

To repair a Secret that is already refused: the dashboard and API always write
`<server>-tunnel-auth`, so they fix a refused Secret with any other name by
creating an owned one and pointing `credentialsSecretRef` at it. If the refused
Secret is itself named `<server>-tunnel-auth`, they answer `409 Conflict`
instead — add the ownerReference to it, or delete it and save the credential
again.

Wait for the pod to reach `Running` state. Once the tunnel pod connects and
reports the assigned address, it appears in `status.endpoints`. The address
changes if the pod restarts; playit does not guarantee address stability on free
tier.

How the address gets there: the tunnel pod polls `playitd`'s local control
socket for the tunnels on your playit account and their assigned addresses. It
patches them into the GameServer's `status.tunnelEndpoints`, and the operator
validates them and copies them into `status.endpoints`. playit doesn't say which
tunnel belongs to which game port, so the pod matches each playit tunnel to a
game port by the **local port** the tunnel forwards to. Set each tunnel's local
port in the playit dashboard to the game's container port (for example `25565`
for Minecraft Java). If the template advertises a single port and the account
has a single enabled tunnel, the pod pairs them even when the ports differ. If a
playit address has no port (a `*.joinmc.link` name served through a DNS SRV
record), it's shown with the game's port. The pod re-checks every 30 seconds, so
a changed address shows up without a restart.

> **Free tier limits.** By default, playit assigns 8 hours/day per secret. Upgrade
> to a paid plan for 24/7 access.

## Cost and resource usage

**A tunnel-enabled GameServer runs an extra always-on pod for its entire
lifecycle, including while the server is idle and asleep.** This is deliberate
— the tunnel must hold the public address so players can reconnect after waking
the server. (See **Interaction with idle auto-sleep** below for how sleep and
tunnels compose.)

Implications:

- You pay for two pods instead of one while the server is running.
- You pay for one pod (the tunnel) even when the game is asleep.
- The tunnel pod is small (~50 MiB memory, negligible CPU), but it is not free.

If you have many servers and want to minimize idle costs, consider:

- Disabling idle auto-sleep (`spec.idle.enabled: false`) and stopping servers
  manually.
- Routing multiple servers through a single tunnel relay (if your relay supports
  it; this requires multiple `remotePorts` entries in your frp config, or a
  custom relay setup).
- Using a provider with free tier or time-based billing (playit's free tier and
  Tailscale's free tier).

## Interaction with other networking fields

Tunnels layer over your backing Service. Other networking fields apply to the
Service, not to traffic arriving through the tunnel:

- **`hostname` / external-dns**: applies to the Service, not the tunnel address.
  If your tunnel provider assigns a public address, set your DNS CNAME to that
  address, not to the `hostname` field.
- **`sourceRanges` (ingress allowlist)**: applies to the Service port. Traffic
  through the tunnel originates from the relay's address, not the player's,
  so `sourceRanges` does not gate tunnel traffic. The relay (frp, Tailscale, or
  playit) is responsible for authentication and access control.
- **`portOverrides` (NodePort specifics)**: applies to NodePort-mode exposures
  only. Tunnels ignore this entirely.

> **Planned:** Gameplane will report configuration mismatches as a status
> condition (informational) if you set conflicting options — for example, both a
> public `expose: LoadBalancer` and a tunnel. This will not be an error, but will
> indicate redundant configuration.

## Interaction with idle auto-sleep and wake-on-connect

Idle auto-sleep and tunnels compose; a few things to know:

- **The tunnel pod stays awake.** Only the game pod sleeps. When the game enters
  idle sleep, the tunnel pod continues to hold the relay connection and the
  public address remains advertised.
- **Wake-on-connect still works.** If a player connects to the public address
  while the game is asleep, the relay wakes the game pod (assuming
  `spec.idle.wakeOnConnect` is enabled). The player's connection is handed off
  to the newly-started game pod.
- **Potential reconnect.** Depending on the relay provider and your expose mode,
  a player may need to reconnect once after the game wakes — the relay may drop
  the dormant connection and require a new dial to the newly-running pod.
  (frp is stateless about game connections, so reconnects are expected. Tailscale
  handles this more gracefully. playit is in between.)

If you want to avoid reconnects on wake, consider:

- Using Tailscale (handles connection state better).
- Keeping `spec.idle.enabled: false` and stopping servers manually.
- Disabling `spec.idle.wakeOnConnect` so players only join running servers.

## Troubleshooting

### No address appears in `status.endpoints`

1. **Check the tunnel pod is running:**
   ```bash
   kubectl -n gameplane-games get pods -l app.kubernetes.io/name=gameplane-tunnel,app.kubernetes.io/instance=<server-name>
   ```
   Look for a pod in `Running` or `Ready 1/1`.

2. **Check tunnel pod logs:**
   ```bash
   kubectl -n gameplane-games logs -f <tunnel-pod-name>
   ```
   Look for connection errors, auth failures, or provider-specific errors.

3. **Verify the credential Secret:**
   ```bash
   kubectl -n gameplane-games get secret <secret-name> -o yaml
   ```
   Ensure the keys match the provider's requirements (`token` for frp, `authKey`
   for Tailscale, `secretKey` for playit). If the Secret lacks an ownerReference
   to this GameServer, the operator refuses it — check `kubectl get gameserver
   <name> -o jsonpath='{.status.conditions}'` for a `TunnelReady=False/TunnelCredentialRefused`
   entry naming the Secret. While the Secret is refused, the tunnel Deployment
   is scaled to zero with no credential mounted. A refused pass stops the
   reconcile early, so the condition's own `observedGeneration` is current
   while `status.observedGeneration` may still lag: automation should read
   this condition's `observedGeneration` rather than wait for the top-level
   one to catch up.

4. **Check NetworkPolicy.**
   By default, `networkPolicies.enabled=true` applies a default-deny-egress
   policy to the games namespace. The operator automatically creates a
   `<server-name>-tunnel-egress` NetworkPolicy that admits the tunnel pod's
   egress to DNS and the template's advertised container ports, plus
   provider-specific relay ports:
   - **frp:** the frp server's `ServerAddr:ServerPort` (default TCP 7000).
   - **tailscale:** the control plane on TCP 443 and DERP relays on UDP 41641.
   - **playit:** an unrestricted egress rule (all ports, all protocols, any
     destination), in addition to the DNS/advertised-ports rule every
     provider gets. playit does not publish a fixed set of relay endpoints
     or ports, so a per-port allow list can't be expressed the way it can
     for frp and tailscale.

   For frp, tailscale, and playit, no manual NetworkPolicy is needed for a
   standard setup.
   
   If the tunnel pod cannot reach the relay:
   ```bash
   kubectl -n gameplane-games exec <tunnel-pod-name> -- \
     nc -zv <relay-address> <relay-port>
   ```
   If this fails, the NetworkPolicy may be overly restrictive. Check the
   `NetworkPolicy` resources in the namespace and ensure `podSelector` includes
   the tunnel pod.

### Address keeps changing (playit.gg)

On playit's free tier, the assigned address is not persistent — it may change if
the tunnel pod restarts or if your daily 8-hour window resets. Upgrade to a paid
plan for stable addresses, or accept that players must rejoin after address
changes.

### Players can connect but get `Connection refused`

1. Verify the game pod is `Running` and `Ready 1/1`:
   ```bash
   kubectl -n gameplane-games get pods <game-server-name>-0
   ```

2. Check the game is listening on the expected port. Most games bind to
   `0.0.0.0`, so `netstat` inside the pod will show it:
   ```bash
   kubectl -n gameplane-games exec <game-server-name>-0 -- netstat -tlnp
   ```

3. Verify the relay is correctly configured to forward the port. For frp, check
   your `frps.toml` and the `remotePorts` in the GameServer spec match.

### Tailscale: server not appearing in `tailscale status`

1. Check the pod logs:
   ```bash
   kubectl -n gameplane-games logs -f <tailscale-tunnel-pod>
   ```

2. Verify the auth key is valid and not expired. Log into
   [login.tailscale.com](https://login.tailscale.com) and check **Settings →
   Keys** — expired keys will be marked.

3. If the pod joined but the name is wrong, check the `hostname` in the GameServer spec:
   ```bash
   kubectl -n gameplane-games get gameserver <server-name> -o jsonpath='{.spec.networking.tunnel.tailscale.hostname}'
   ```
   If empty, the device defaults to the GameServer name.

## Security

- **Credentials are Secrets.** The relay authentication credentials (frp token,
  Tailscale auth key, playit secret key) are stored as Kubernetes Secrets in the
  games namespace and are never surfaced through the API to users. Only
  cluster-admin can read them directly.
- **Tunnel images are signed.** The tunnel relay clients (frp, Tailscale,
  playit) run as container images published and cosign-signed alongside other
  Gameplane images. Verify them the same way: with `cosign verify --key
  signing/cosign.pub`.
- **Relay trust model.** When you route traffic through a relay, you are
  trusting the relay operator (whether it's your own frps VPS, Tailscale Inc.,
  or playit.gg) not to inspect or modify player connections. For production
  use, frp (self-hosted) gives you full control; Tailscale and playit are
  third-party services whose privacy policies should be reviewed before use.
- **Tailscale is invite-only.** Tailscale tunnels expose servers only to devices
  already on your tailnet, so they do not introduce new attack surface to the
  public internet.
- **playit and frp are public.** Both expose servers to the public internet; use
  the same security practices as you would for a public-facing LoadBalancer
  (e.g., disable admin consoles, run security updates, restrict in-game
  permissions).
