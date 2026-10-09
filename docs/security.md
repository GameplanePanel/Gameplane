# Security

## Threat model

Gameplane's dashboard is deliberately internet-exposed — that's the whole
point. Assume:

- the login page is enumerable by any scanner,
- cluster-internal attackers may land a pod in `gameplane-games` via a
  compromised game image (Minecraft plugins, Valheim mods, etc.),
- the game pods themselves should be treated as low-trust,
- a dashboard user with only `servers:read` must not be able to read game passwords (RCON, admin or server passwords) out of a GameServer,
- a usage-telemetry receiver is reachable by anyone on the internet, so it must stay safe when fed hostile or invented reports, and must never reveal figures to an unauthenticated viewer (see [Telemetry](#telemetry)).

## Authentication

Operator-managed agent and capture-sidecar control traffic uses mTLS with TLS 1.2 or newer and a verified
client certificate. Both servers reload their serving certificate, key and
client CA on each new handshake, so operator Secret renewal takes effect
once Kubernetes updates the mounted projection. Each server reads one pinned
`..data` generation from the Secret rather than mixing files during a
projection swap. Invalid rotated material rejects new handshakes until
repaired; it never falls back to old trust. A decoded client-CA
`CERTIFICATE` block with invalid DER rejects the entire bundle, even
when it also contains valid CAs. Valid multi-CA bundles remain supported.
TLS session tickets are
disabled so new connections cannot resume authentication against a
removed CA. Existing connections retain their established TLS state.

Two modes, configurable independently:

- **Local accounts** — argon2id (64 MiB, t=3, p=2) password hashing.
  Session cookies are HttpOnly, Secure, SameSite=Lax. The database stores only a SHA-256 digest of the session cookie value, so a leaked database or backup does not yield usable session cookies; the digest itself is rejected if presented as a cookie. CSRF protection
  via a double-submit `X-Gameplane-CSRF` header on mutating requests.
- **OIDC** — Keycloak, Google, GitHub, any RFC-7519 compliant IdP.
  State validated through a short-lived cookie; `id_token` signature
  verified against the provider's JWKS.

On first OIDC login for a subject, Gameplane creates a user row with
role `viewer`. Admins must promote new OIDC users manually.

### Dashboard-managed providers

OIDC providers can be added at runtime under **Admin Settings →
Authentication**: issuer + client id live in the auth config row (they
are public OAuth identifiers), and the client secret is stored as an
API-managed Secret `gameplane-auth-<name>` in the control-plane
namespace. Two labels bound the API's reach: it only *reads* Secrets
labelled `gameplane.local/auth-provider=true`, and only *deletes* ones
additionally labelled `gameplane.local/managed-by=gameplane-api` — so a
`config:manage` user can neither exfiltrate arbitrary control-plane
Secrets through a provider's `configRef` nor delete kubectl-/GitOps-
created ones over HTTP. Provider changes apply on save, no restart: the
registry re-reads the config row per auth request and rebuilds OIDC
clients lazily (issuer discovery cached, failures back off).

A provider configured through Helm flags (`api.oidc.*`) appears as the
read-only `helm` provider; it is owned by values.yaml and cannot be
edited, disabled, or deleted from the dashboard.

### Lockout guard and break-glass

Saving an auth config with zero enabled providers is rejected (the Helm
provider counts as always-enabled). If you still lock yourself out —
local login disabled while the only OIDC provider is broken — run the
break-glass inside the API pod:

```sh
kubectl -n gameplane-system exec deploy/gameplane-api -- \
  /api bootstrap-admin --enable-local-login
```

It force-enables the local provider in the auth config row, keeping
everything else in that row as it was (the other providers and the
`helmOverride` role-mapping overlay), and takes effect on the next login
attempt.

`bootstrap-admin --username <name> --force` resets that account's password,
promotes it to `admin`, and ends every existing session of the account, the
same way a dashboard password reset does.

### Client IP extraction from forwarded headers

The API records a client IP for every request. Login rate limiting
(per-IP caps) and audit records key on it. The IP comes from the TCP peer
and, only when that peer is a trusted proxy, from the `X-Forwarded-For`
header. Trusted proxies are set by `api.trustedProxies` (default:
loopback and private ranges `127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`,
`192.168.0.0/16`, `169.254.0.0/16`, `::1/128`, `fc00::/7`, `fe80::/10`).

The API applies these rules in order (`api/internal/auth/clientip.go`):

1. A TCP peer outside `api.trustedProxies` is the client. Any
   `X-Forwarded-For` header on its request is ignored.
2. When the peer is a trusted proxy, the API reads `X-Forwarded-For` from
   right to left and takes the first address outside `api.trustedProxies`
   as the client.
3. When every address in the chain is inside `api.trustedProxies`, the
   leftmost address is the client.
4. An entry that isn't an IP address ends the walk at the last address
   already reached. With no `X-Forwarded-For` header that is the peer.
5. With `api.trustedProxies` empty, the peer is always the client.

**IPv4-mapped IPv6 prefixes.** Prefixes must be standard IPv4 (e.g., `10.42.0.0/16`)
or IPv6 (e.g., `fc00::/7`). IPv4-mapped IPv6 prefixes (e.g., `::ffff:10.42.0.0/112`)
are automatically normalized: they are converted to their unmapped IPv4 form and must
be at least `/96` bits to be valid (`::ffff:10.42.0.0/96` becomes `10.42.0.0/0`,
`::ffff:10.42.0.0/112` becomes `10.42.0.0/16`). Prefixes shorter than `/96` are
rejected at startup.

**In a normal Kubernetes install** (ingress controller, then the web
front end, then the API), the default works out of the box: the proxies
run in pod and node ranges the default covers, and a client on the public
internet is recorded by its own address.

**Clients on a private network.** The default treats every private-range
address as a possible proxy, so for clients that connect from a private
range the recorded IP depends on the forwarded chain those addresses
present (rule 3). If dashboard users reach Gameplane from a private
network and you rely on per-client limits for them, narrow
`api.trustedProxies` to the ranges your proxies actually run in, usually
the cluster's pod CIDR plus any load balancer in front of the ingress:

```yaml
api:
  trustedProxies: "10.42.0.0/16"   # k3s default pod CIDR; use your cluster's
```

**When the API is directly exposed** (no proxy), set
`api.trustedProxies` to `""`: the TCP peer is then always the client. If
you place a proxy in front of the API, list that proxy's addresses so the
API reads `X-Forwarded-For` from it. A proxy outside the list is recorded
as the client itself, so every user behind it shares one rate-limit
bucket.

Example for a single proxy at `203.0.113.1`:

```yaml
api:
  trustedProxies: "203.0.113.1/32"
```

The client IP is used for login rate limiting (per-IP caps) and audit
records, so misconfiguring this can either record a proxy instead of the
client in audit logs or group legitimate users behind one proxy address
in a single rate-limit bucket.

## Authorization

RBAC is **permission-based**. A *permission* is a fixed `resource:action`
string from the server-defined catalog (`api/internal/rbac/catalog.go`,
e.g. `servers:write`, `backups:restore`, `users:manage`). A *role* is a
named set of permissions, and a user is bound to roles **per namespace**.

- **Roles** live in the API database (`roles` / `role_permissions`). The
  built-in `admin`, `operator`, and `viewer` roles are seeded so their
  cluster-wide grants reproduce the historical role matrix exactly. `admin`
  holds the `*` wildcard and is immutable; `operator`/`viewer` are editable
  templates; custom roles can be created with any subset of the catalog (the
  `*` wildcard is never grantable through the API). Built-in roles and roles
  still assigned to a user cannot be deleted.
- **Bindings** (`user_role_bindings`) grant a role in a namespace; `*` means
  cluster-wide. A user's primary role (`PATCH /users/{id}`) is their
  cluster-wide binding; additional per-namespace grants are managed via
  `…/users/{id}/bindings`. Allowed namespaces are the `GAMEPLANE_EXTRA_NAMESPACES`
  allow-list plus the default `gameplane-games`.
- **Enforcement** (`api/internal/rbac/rbac.go`): each route maps to one
  required permission; the middleware resolves the request's target namespace
  and checks the caller's resolved permission set. A *namespaced* permission
  is granted by a cluster-wide binding **or** a binding in the target
  namespace; a *cluster-scoped* permission requires a cluster-wide binding —
  the same Role vs ClusterRole split Kubernetes uses. Unmatched routes fail
  closed.
- **Lockout guards.** The API refuses to demote or delete the last user who
  can manage users, and refuses self-demotion below `users:manage`. Role
  edits follow the same rules: a change that removes `users:manage` from the
  caller's own primary role, or from the role every user manager holds, is
  refused.
- **Event stream.** `GET /events` carries only the resource kinds the caller
  may read in the resolved cluster and namespace, using the same read
  permission as each kind's list route.
- **Account removal.** `DELETE /users/{id}` removes the account and every row
  tied to it in one transaction (SSO links, preferences, sessions, API
  tokens, role bindings) and revokes the share links the account created.
  The API does this itself rather than relying on foreign-key cascades,
  which the shipped SQLite DSN leaves off. An SSO user who is deleted and
  signs in again is provisioned as a new user.

### Per-GameServer access (owner + collaborators)

In addition to namespace-based RBAC, GameServers support ownership and
collaboration: the **owner** (who created the server) and any **collaborators**
(managed via `PUT /servers/{name}:collaborators`) gain operational control over
that specific server, regardless of their namespace role. This is purely additive
— it does not override namespace bindings. Collaborators retain: read, console,
WebSocket access, start/stop/restart/clone operations, and files/players/config
subroutes. Destructive operations are owner-only: delete, wipe-data, ownership
transfer, and collaborator list edits. Only the server's owner or an admin (a
role holding `*` in the server's cluster and namespace) can perform owner-only
operations. The namespace `servers:write` permission alone does not grant them,
and a server with no recorded owner (for example one created with kubectl or
GitOps) can be transferred, wiped or deleted only by an admin. The transfer,
collaborator-edit and wipe patches are conditional on the server's
`resourceVersion` as read by the ownership check: if the server changes in
between (for example its ownership is transferred), the API re-reads it and
repeats the check, so a caller who is no longer the owner is refused, and
after three conflicting attempts the request fails with 409. Backups,
restore jobs, schedules, and events remain namespace-gated in this release.

Generic GameServer create/update requests cannot set or remove annotations in
the `gameplane.local` domain or its subdomains, except the user-editable
`description` and unused legacy `grace-period-seconds` hints. The API strips supplied values
on creation before assigning the owner, and preserves live values on updates.
Wipe requests, controller acknowledgements and lifecycle guards therefore cannot
be injected or erased through ordinary settings writes. Use the authorized
lifecycle and ownership endpoints for those operations.

## GameServer config passwords

`GameServer.spec.config` holds wizard values as stored in Kubernetes, including
the values of `type: password` fields of the template's `configSchema`. The API
never returns those in clear: every response that carries a GameServer
(`/servers`, `/servers/{name}` including create/update replies, `:clone`,
`/users/me/servers`, `/fleet/servers` and the `/events` stream) replaces each
non-empty password value with the marker `__gameplane_redacted__`, through one
helper (`api/internal/handlers/config_redact.go`). If the GameTemplate cannot be
read, every config value is redacted (fail closed).

These responses also omit the `kubectl.kubernetes.io/last-applied-configuration`
annotation, which can contain passwords from an older manifest even after they
are removed from current config. This applies even to servers without config;
the annotation remains stored in Kubernetes.

On `PUT /servers/{name}` a password value equal to the marker keeps the stored
value, a different non-empty value replaces it, and an empty string clears it
only when the field is optional (a required field keeps its value); a required
password omitted from the body is restored. Share links, capture, mods, tunnel
credentials, notifications, WebSockets and the audit log (which never records
request bodies) do not carry `spec.config`.

Residual exposure: anyone who can read the GameServer object through the
Kubernetes API (kubectl, GitOps tooling, the optional read-only `mcp-server`)
still sees the stored values; restrict that with cluster RBAC.

## Share links

Share links (`api/internal/db/shares.go`, schema in `api/internal/db/migrations/sqlite/006_share_links.sql`, Postgres twin in `migrations/postgres/`) grant unauthenticated, token-bearing access to a single GameServer's status and connection address, optionally with permission to wake it. Because they are unauthenticated, their security rests entirely on the token being both hard to guess and hard to recover if the database or a backup leaks.

**Storage.** Only a SHA-256 hash of the token is persisted (`token_hash`, indexed for O(1) lookup); the raw 32-byte random token is generated at creation, returned exactly once in the create response, and never stored, logged, or recoverable afterwards.

**Why a fast hash is the right choice here, not a weakness.** SHA-256 is not a password-hashing function (no salt, no work factor), and that is deliberate:

- The input is a 32-byte cryptographically random value, not a low-entropy human-chosen secret. There is no dictionary or brute-force search over a keyspace of 2^256 that a fast hash makes newly feasible — the security margin comes entirely from the token's entropy, not from how expensive the hash is to compute.
- A database or backup leak exposes only hashes. Recovering a usable token from a hash would require reversing SHA-256 or brute-forcing a 256-bit random space; both are computationally infeasible regardless of the hash function's speed. A slow KDF (bcrypt/argon2/scrypt) would add no meaningful protection here, since the "weak secret" scenario those algorithms defend against does not apply.
- The token is looked up on every public request to `GET /shares/{token}` and `POST /shares/{token}/start`. A fast, indexed equality lookup keeps that path cheap; a deliberately slow KDF would turn every anonymous share-page load into an expensive hashing operation — the opposite of what a slow KDF is for (limiting an attacker's guesses per second), since here the attacker's limiting factor is token entropy, not hash speed.

**Indistinguishability.** An unknown token, an expired token, and a revoked token all produce the identical response (`ErrShareLinkInvalid` internally; a uniform 404 at the HTTP layer). This prevents an attacker from learning, by probing, whether a guessed token ever existed, expired, or was deliberately revoked.

**Token placement.** The token travels as a path segment (`/shares/{token}`), not a query parameter, keeping it out of query-string capture by proxies, access logs and analytics, and letting audit logging redact it from recorded request paths with a simple prefix rewrite (`redactShareToken` in `api/internal/audit/audit.go`). A path segment is still part of the URL, so it does not by itself keep the token out of `Referer` headers; API responses set `Referrer-Policy: no-referrer` (`secureHeaders` in `api/cmd/main.go`) for that.

**Expiry.** Every share link either has an expiry timestamp or is explicitly created with no expiry (owner's choice; see `specs/done_017-share-link-expiry/`). There is no platform-enforced maximum lifetime: a non-expiring or long-lived link is exactly as hard to guess on any given day as a short-lived one, because guessing difficulty comes from the token's entropy, not from its age. The tradeoff of a long-lived or non-expiring link is operational — a forgotten link stays live until the owner revokes it — not cryptographic, which is why the create-link UI warns the owner explicitly ("This link works until you revoke it." for no expiry; a long-lived-token warning for a custom date a year or more out) rather than the system silently capping the choice.

**Revocation.** Revocation sets `revoked_at` (never a delete, preserving the audit trail) and is checked independently of, and prior to, any expiry check, so it applies uniformly regardless of whether the link expires, expires far in the future, or never expires. `DELETE /servers/{name}/shares/{id}` revokes a link only when it belongs to that server (cluster, namespace and name); any other id answers 404. Deleting a user revokes every share link that user created.

**Rate limiting.** The public resolve/start endpoints are rate-limited (`auth.ShareLimiter`) specifically because tokens are guessable-by-brute-force in principle (just computationally infeasible in practice); the rate limit is defense in depth against automated probing, not a substitute for token entropy.

## API → Agent

mTLS. The Helm chart provisions a self-signed CA via a post-install
hook (or takes an existing `gameplane-agent-ca` Secret). The operator
uses the CA to sign per-pod server certs; the API uses a single client
cert. Agent refuses plain-HTTP traffic when TLS material is present.

Fallback: a shared-secret bearer token via `--api-token-file`. Only
intended for local `kind` development where mTLS is overkill.

**Console-injection guard.** Free text folded into an RCON/stdin command (module action string params and player kick/ban reasons) passes through `gameaction.CheckText`, which rejects ASCII control characters and the metacharacters ; & | $ ` \ " '. The agent applies it independently of the API.

## NetworkPolicies

When `networkPolicies.enabled=true` (default) the chart applies:

- `default-deny-ingress` — denies all ingress to all pods in the games
  namespace. A Kubernetes Service does not create any NetworkPolicy
  allowance; ingress traffic is fully isolated unless an allow-policy
  explicitly permits it.
- `default-deny-egress` — denies all egress from all pods in the games
  namespace except DNS (UDP/TCP port 53 to `kube-system`). This is the
  most restrictive policy; outbound downloads (binaries, assets, mods) are
  gated by the `allow-game-public-egress` policy below, and apiserver
  access by `allow-agent-to-apiserver`.
- `allow-agent-to-apiserver` — allows game pods to reach the kube-apiserver
  for status heartbeat (GameServer status patches). By default permits TCP 443
  and 6443 to all addresses in RFC1918 and link-local ranges (10.0.0.0/8,
  172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16), not limited to the apiserver
  endpoint. Customize via `networkPolicies.apiServerCIDRs` to narrow to specific
  API server addresses.
- `allow-api-to-agent` — allows the API and operator pods (in the
  control-plane namespace) to reach every game pod's agent on TCP port 8090.
  The API proxies console/files/logs/players; the operator calls `/quiesce`
  before backups.
- `allow-api-to-capture` — rendered only when `capture.enabled=true`; allows TCP
  9091 to game pods from this release's operator and enabled API pods in the
  control-plane namespace. Both the namespace and pod selectors must match.
  This admits operator capture control and local API file access without opening
  other ports or depending on the kubelet probe allowance. Remote file access has
  its separate gateway policy.
- `<gameserver-name>-game-ingress` — created by the **operator** for each
  GameServer, allows external traffic to reach only the advertised ports
  declared in the GameTemplate. Selects traffic from `networkPolicies.gameIngress.fromCIDRs`
  (default `0.0.0.0/0`, the internet) to only the container ports marked
  `Advertise: true` at their declared protocol (UDP preserved). This policy
  does not itself open RCON or the agent's port 8090; however, those ports
  remain reachable from `kubeletCIDRs` via the `allow-kubelet-probes` policy
  (see below), which has no port restrictions by default. To close that hole,
  narrow `networkPolicies.probePorts` — previously unsafe to do, but now
  safe since this policy protects advertised player traffic separately.
  Operators may also narrow `fromCIDRs` to a private range (e.g. a LAN, a VPN
  CIDR) to gate player access. If a template declares no advertised ports or
  `networkPolicies.gameIngress.enabled: false`, the operator ensures this
  policy does not exist. The policy is owned by the GameServer and cascade-deletes
  with it.
- `allow-kubelet-probes` — allows kubelet to reach game pods for
  liveness/readiness probes. By default targets RFC1918 + link-local ranges,
  or customizable via `networkPolicies.kubeletCIDRs`; probe ports via
  `networkPolicies.probePorts`.
- `allow-prometheus-to-agent` — opt-in (rendered only when
  `serviceMonitors.scrapeNamespaceSelector` is set) policy allowing Prometheus
  pods in the selected namespace to reach the agent's plain-HTTP metrics port
  (TCP 9090). That port is unauthenticated HTTP, not mTLS, and is otherwise
  already reachable from `kubeletCIDRs` via `allow-kubelet-probes` above
  unless `networkPolicies.probePorts` is narrowed to exclude it.
- `allow-game-public-egress` — (enabled by default, gated by
  `networkPolicies.gameEgress.enabled`) allows game pods to reach the public
  internet for binary/asset/mod downloads. Set `networkPolicies.gameEgress.enabled: false`
  to withhold public egress. Permits egress to 0.0.0.0/0 except private ranges
  defined in `networkPolicies.gameEgress.privateCIDRs` (by default: 10.0.0.0/8,
  172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 to block in-cluster and
  cloud-metadata access). Ports customizable via `networkPolicies.gameEgress.ports`.

**Note:** `default-deny-egress` applies to all pods in the games namespace
(`podSelector: {}`), while `allow-agent-to-apiserver` and
`allow-game-public-egress` only select game pods labelled
`app.kubernetes.io/name: gameplane-game`. Any pod in the games namespace
without that label receives DNS-only egress (connecting to kube-dns on port
53). Do not place helper or debug pods in the games namespace expecting them
to route traffic—place them in a different namespace instead.

## Pod security

Every Gameplane-managed pod (operator, api, agent, and the optional
audit-syslog-bridge) runs as:

- `runAsNonRoot: true` (uid 65532)
- `readOnlyRootFilesystem: true`
- `seccompProfile.type: RuntimeDefault`
- `capabilities.drop: [ALL]`
- `allowPrivilegeEscalation: false`

The agent sidecar in each game pod gets the same fixed hardening. The game
container itself does not: it runs as the template's
`spec.security.runAsUser`/`runAsGroup` when the template sets them (see
[module authoring](module-authoring.md#security-context)), and otherwise as
the image's own default user, which may be root. The operator sets no
`runAsNonRoot`, `allowPrivilegeEscalation`, `readOnlyRootFilesystem`,
`seccompProfile` or capability drop on the game container, so it keeps the
container runtime's defaults. This keeps arbitrary third-party game images
working unchanged.

Game pods are shaped per-template. For a hostile game module, enable
Pod Security Standards `restricted` on the games namespace via
`podSecurity.enforceRestricted=true`.

### Network Capture Security Exception

The optional network capture feature [optional] (see
[`docs/roadmap.md`](roadmap.md)) adds an ephemeral sidecar container to game pods when
capture is enabled. This sidecar requires **`allowPrivilegeEscalation: true`** in
its securityContext, which violates the Pod Security Standards `restricted`
profile.

**Why the exception is necessary**: The sidecar acquires `CAP_NET_RAW` capability
via file capabilities (`setcap cap_net_raw+ep`), applied at container image build
time. This mechanism is necessary because Kubernetes does not set ambient
capabilities on a container's process by default: declaring
`securityContext.capabilities.add: ["NET_RAW"]` alone on a non-root user grants
nothing — the kernel clears the effective capability set when the entrypoint
binary is executed (via `execve`), leaving the non-root process with no
capabilities. File capabilities survive this exec because they are consulted
independently by the kernel at exec time, independent of ambient state.

The container's `securityContext.capabilities` still lists
`Drop: ["ALL"], Add: ["NET_RAW"]`, but that `Add` is not what grants the
capability — it exists only because `Drop: ["ALL"]` on its own would also empty
the process's *bounding* capability set, and the kernel refuses to grant a file
capability at `execve` that isn't in the bounding set (EPERM). Re-adding NET_RAW
keeps it available in the bounding set so the file capability grant can proceed;
the process's own *effective* set is still empty at start, and the actual grant
comes from the setcap'd binary at exec, not from `capabilities.add`. Separately,
file capabilities are ignored by the kernel when `no_new_privs` is set, and
Kubernetes sets `no_new_privs` whenever `allowPrivilegeEscalation: false`.
Therefore, the container must also set `allowPrivilegeEscalation: true` for the
file capability to function.

The game container does not share this exception: the operator never sets
`allowPrivilegeEscalation: true` on it or adds `NET_RAW` to it, and the capture
sidecar is the only container Gameplane grants `CAP_NET_RAW` for capture. The
game container keeps the posture described in [Pod security](#pod-security):
the template's uid/gid or the image's default user, and the container
runtime's default capability set. Which capabilities the game container holds
therefore depends on that runtime default and on the user the image runs as,
not on the capture feature.

**Trade-off with PodSecurity `restricted`**: A cluster enforcing the `restricted`
Pod Security Standards profile on the games namespace will reject any pod with
`allowPrivilegeEscalation: true`. If you require `restricted` admission on your
games namespace, you have three options:

1. **Disable capture** — leave the cluster's capture feature disabled via Helm
   value `capture.enabled: false` (default is false). Captures are not required
   for normal operation; this is the safest option if you cannot or prefer not to
   relax the `restricted` profile.
2. **Exempt the games namespace** — remove or relax the Pod Security Standards
   `restricted` enforcement for the games namespace, using `baseline` or
   `privileged` instead. The games namespace remains an untrusted environment
   (game code can run arbitrary containers), but the admission level permits the
   capture sidecar to be injected when needed.
3. **Leave the games-namespace label off** — the chart only adds the
   `pod-security.kubernetes.io/enforce: restricted` label to the games namespace
   when `podSecurity.enforceRestricted=true`; that value defaults to `false`.
   A default install therefore already leaves the label off, and captures work
   without any change. If you previously set `podSecurity.enforceRestricted=true`
   and accept the operational trade-off, set it back to `false` in the Helm
   values to drop the label. This setting only affects the games `Namespace`
   object; it has no cluster-wide effect and there is no per-pod opt-in.

**Data sensitivity**: Captures contain binary game protocols, player IP addresses,
and may include sensitive data like in-game chat or credentials. An admin with
`captures:manage` permission can start a capture and read all traffic reaching
that pod, including other players' packets. This access is admin-only and fully
audited (every capture operation is logged). Default capture retention is 24 hours,
reducing the exposure window for captured data.

## Module supply chain

A `GameTemplate` materialized from a module chooses the container image,
command, and config a game pod runs — so a module source is a trust
boundary. Three controls protect it:

- **Fetch SSRF guard.** The operator's `git`/`http` source fetchers
  (`netguard.IsAllowed`) refuse link-local, cloud-metadata
  (`169.254.169.254`), unspecified, and multicast destinations, at dial time
  (so a DNS name rebinding to one is caught). This blocks a source from being
  aimed at the instance-metadata endpoint to steal the operator's IAM
  credentials. Private/loopback addresses stay reachable for self-hosted
  registries. `ModuleSource` mutation is admin-only, so this is
  defense-in-depth.
- **Signature verification.** `ModuleSource.spec.verify` (OCI sources) makes
  the operator refuse any bundle without a valid cosign signature — keyed (a
  public key) or keyless (a pinned Fulcio issuer + identity). Use it for any
  source you don't fully control. The official `modules/*` bundles are
  keyed-signed by the release pipeline (and now also recorded in the public
  Sigstore Rekor log), and verify **offline** (no Rekor/Fulcio reachability
  needed). The operator's verification is intentionally offline/keyed, keeping
  air-gapped and self-hosted clusters functional. Opt-in enforcement of
  transparency log inclusion is future work. Signing is an OCI concept, so
  switch the default source to `type: oci` and enable
  `defaultModuleSource.oci.verify.enabled`.
- **Digest pinning.** `Module.spec.digest` pins exact bundle content; a moved
  tag fails the install with `DigestMismatch`.

Verification and pinning are opt-in. A source with neither is trusted to
serve a `GameTemplate` whose image/command runs in your cluster — only point
Gameplane at module sources you trust, and prefer signed, pinned installs for
third-party games. Authoring details: [`module-authoring.md`](module-authoring.md).

## Agent file browser (path confinement)

The agent's `/files/*` routes (list, read, download, write, upload, mkdir,
delete) operate on the game data volume and are reachable by anyone who can
use the dashboard file browser, while the game container (a different uid)
can create symlinks on the same volume. The agent therefore treats the data
directory contents as untrusted and does not trust a path it validated
earlier:

- The requested path is validated lexically (no `..` escape, no dot-prefixed
  component) and every operation then runs relative to a directory descriptor
  opened on the data root, opening one component at a time with
  `openat(O_NOFOLLOW)`. A symlink at any component is refused (HTTP 400); there
  is no "follow it if it stays inside the root" case.
- Reads, writes, creates and deletes use the descriptors they obtained
  (`fstatat`, `mkdirat`, `unlinkat`, `renameat`, serving from the opened file)
  and never re-open an absolute path, so swapping a checked directory or file
  for a symlink between validation and use cannot redirect the operation out of
  the root. Writes are atomic: temp file in the parent descriptor, `fsync`,
  `renameat` in the same directory.
- Recursive delete walks descriptors and unlinks links as links; it refuses
  trees that contain dot-prefixed entries (agent state such as the mods
  manifest).
- The data-root path itself is operator-configured and trusted (it may be a
  symlink); only components below it are checked.

The mods package confines its own paths separately (`ConfinePath`/`ConfineRelPath`)
and does not use this descriptor walk.

## Runtime mod installs (agent)

Separately from the module supply chain above, a running server can install
mods/plugins at runtime if its template declares
`capabilities.mods.install` — a user-supplied URL the agent downloads into
the server's data volume (see
[`module-authoring.md`](module-authoring.md#mods)). This is a distinct trust
boundary: the target is whatever host the logged-in user types, not an
admin-configured `ModuleSource`, so it is guarded more strictly
(`netguard.IsPublic`): only globally routable addresses are allowed —
loopback, private/ULA, link-local, and CGNAT/reserved ranges are all
refused, not just the cloud-metadata range. An `allowedHosts` allow-list is
also required before installs are enabled at all, and redirects are
re-checked against both the host allow-list and the address guard. This
guard and the operator's fetch guard above share their dial-time enforcement
machinery in the `netguard/` module; its package doc explains why the two
policies (`IsAllowed` vs `IsPublic`) stay separately selectable rather than
being collapsed into one.

## Notifications

Notification sinks ([docs](notifications.md)) are a third outbound-dial
surface, between the two above in trust: the URLs are configured at runtime
through the dashboard (unlike the deploy-time audit webhook flag), but only
by users holding `config:manage` — admin-tier, the same trust class as the
operator's `ModuleSource`s. They get the same guard: every sink dial
(HTTP and SMTP) goes through `netguard.IsAllowed`, so LAN/in-cluster
receivers (ntfy, a syslog bridge, an SMTP smarthost) keep working while
link-local (cloud metadata), unspecified, multicast, and NAT64/6to4
destinations are refused at dial time — DNS rebinding can't slip past.
Two further containments: sink credentials resolve only from Secrets
labelled `gameplane.local/notification-sink=true` in the control-plane
namespace (so a sink `configRef` can't be aimed at an arbitrary Secret),
and delivery errors are sanitized to never echo the sink URL, whose path
often embeds a capability token.

## Audit log integrity

`audit_events` is a hash chain (migration `005_audit_chain.sql`): every row
inserted after that migration stores `hash = SHA-256(prev_hash ||
canonical(row))`, and `GET /admin/audit/verify` re-walks the chain to report
the first broken link. Two config-table entries bound the walk: a
`Prune`-written checkpoint anchors the oldest surviving row after a
retention sweep, and a per-insert head anchors the newest row, so a
`DELETE FROM audit_events WHERE id > N` — truncating only the tail, which
would otherwise leave every surviving link internally consistent — is
detected too.

**Be precise about what this catches.** The chain is unkeyed: it recomputes
hashes from row content and two config-table entries, and `config` is
writable by anyone with the same database access an attacker would need to
tamper with `audit_events` in the first place. This mechanism reliably
detects:

- naive in-DB tampering — `UPDATE`/`DELETE` (including tail truncation)
  against `audit_events` alone, without also touching `config`; and
- accidental corruption (a bad migration, a restore from an inconsistent
  backup, etc).

It does **not** detect a sophisticated attacker who has DB write access and
also recomputes and rewrites the checkpoint and head to match — that
attacker can forge an internally-consistent chain from any starting point.
Nothing server-side can close that gap while the verification data lives in
the same database the attacker can already write to.

**The real append-only record of last resort is the external sinks** —
stdout (cluster log aggregation), the audit webhook, and the S3 batch sink
(see [Secrets](#secrets) below for how their credentials are contained).
Because delivery is push-based and decoupled from the request path, an
attacker who compromises the database after the fact cannot retroactively
alter what was already shipped to those destinations. Treat the hash chain
as tamper-*evidence* for common-case tampering and corruption, and the
external sinks as the actual tamper-*proof* trail.

A documented future hardening is HMAC-keyed chaining (`hash =
HMAC-SHA256(key, prev_hash || canonical(row))` with the key held outside the
database — e.g. a K8s Secret the API process reads but never writes back),
which would raise the bar to compromising that external key as well. Not
implemented today; tracked in [`roadmap.md`](roadmap.md).

## GitHub Actions supply chain and CI security

Gameplane's release binaries and container images are built via GitHub Actions.
The CI pipeline is a trust boundary: compromised build steps can inject malicious
code into what users deploy. Several controls harden the pipeline:

### Action pinning and mutable-tag defense

External GitHub Actions in `.github/workflows/` and `.github/actions/` are pinned
to a 40-character commit SHA with an inline `# vX.Y.Z` semver comment, never to
a mutable tag like `@v4`. The threat this closes: a compromised or malicious
action maintainer can repoint the tag at new code, giving arbitrary code execution
inside CI with that workflow's `GITHUB_TOKEN` privileges — a supply-chain attack
on the entire user base.

Pinning to an immutable commit SHA is enforced mechanically by the `zizmor` linter
in the `workflow-lint` job (`.github/zizmor.yml` config). Dependabot's
`github-actions` ecosystem entry maintains pins through reviewable pull requests,
so the operational trade-off — pins grow stale and miss security updates — is
managed rather than ignored: every update lands as a visible, auditable PR before
it ships.

### Least-privilege token grants and scope confinement

The threat: a compromised or buggy build step can use the full scope of the
`GITHUB_TOKEN`. An over-broad top-level `permissions` grants those scopes to
every job and step, multiplying the blast radius.

The defense: `.github/workflows/ci.yaml` and `release.yaml` now grant
`permissions: {contents: read}` at the top level — the minimum viable scope —
and elevate scopes only on the specific job(s) that need them, in an explicit
per-job `permissions` block. Concrete reductions from earlier config:

- **ci.yaml**: `statuses: write` (needed only for the `web` job to mark PR
  checks) was inherited by every job; it now lives on `web` alone.
- **release.yaml**: top-level `permissions` was `{contents: write}`, granting
  write access to every release job; it is now `{contents: read}` at the top,
  with `contents: write` elevated only to the `github-release` job.

Every job in every workflow has an explicit `timeout-minutes` to bound the
duration a compromised job can run (see below).

### Expression injection and shell-escaping discipline

The threat: attacker-controlled PR text — `github.event.pull_request.title`,
`.body`, `github.head_ref` — can be interpolated directly into a `run:` shell
body without quoting, giving arbitrary shell execution. A malicious PR title
like `"; rm -rf /; #"` then becomes a command.

The safe pattern: pass attacker-controlled values through the `env:` block and
reference them as quoted shell variables (`"$VAR"`, not `$VAR`), so they are
treated as data, not code. Example:

```yaml
env:
  PR_TITLE: ${{ github.event.pull_request.title }}
run: echo "Title is: \"$PR_TITLE\""
```

The `actionlint` linter (run in the `workflow-lint` job) detects the unsafe form
— direct interpolation of `github.event.*` or `github.head_ref` into `run:` —
and rejects it. This repo does not use `pull_request_target` (the trigger that
runs workflows on untrusted fork code with the base repo's `GITHUB_TOKEN`),
closing the attack surface entirely: `pull_request_target` was designed for
workflows that *must* access repo secrets (e.g., automated releases), but it
introduces risk if any step trusts PR body content as code.

### Timeouts as denial-of-service and cost bounds

Every job carries an explicit `timeout-minutes` to bound job duration. The threat
is twofold: a compromised job could hang indefinitely (DoS on CI capacity and
cost), and a build failure could leave a job in a partially-modified state if the
termination is not clean.

Default timeout budget is ≤30 minutes. Documented exceptions with inline
justification comments:

- The five e2e jobs in ci.yaml run at 60 minutes (the `e2e-go` job uses a
  `job_timeout` matrix value; `e2e-multicluster`, `e2e-upgrade`, `e2e-web-live`,
  `e2e-game-bot` set it directly). E2E test suites on kind clusters are
  inherently slow; 60 minutes is measured from prior runs.
- **publish-edge.yaml**: the `images` job runs at 35 minutes (measured 31-minute
  historical max for full image build and push across all components).

### Diagnostics redaction and secret confinement

The threat: CI failures on a public repository produce world-readable artifacts
(downloaded test logs, pod state dumps, `$GITHUB_STEP_SUMMARY` markdown). Any
unredacted secret — a pod env var, a log line, a manifest dump — becomes public
and compromised immediately. Multi-cluster environments and complex setups make
this harder to spot: a cluster dump is hundreds of lines and secrets can hide in
labels, annotation values, or environment variable lists.

The defense: `.github/actions/dump-cluster-state/action.yml` applies a `redact()`
filter at **every** emit boundary — before any data reaches `$GITHUB_STEP_SUMMARY`,
before any artifact is uploaded, before logs are written. The filter is
**conservative**: it redacts *values* (preserving *keys* so dumps stay debuggable)
by matching known patterns:

- Labelled key/value pairs: any value for keys containing `password`, `passwd`,
  `token`, `secret`, `api`, `key`, `bearer`, or `authorization`
  (case-insensitive, with optional dashes/underscores).
- Bare tokens: JWT-shaped values (`eyJ...`), PEM private-key blocks.

An important limitation: **redaction is pattern-based and therefore best-effort.**
A credential in an unrecognised shape — a long hex string, a custom token format,
or a value buried in a JSON log without a recognisable key — may not be caught.
No Kubernetes Secret object is ever collected into dumps (`for obj in deployments
statefulsets daemonsets jobs configmaps`), so high-entropy database passwords and
OIDC secrets bound to the pod via Secrets are outside the dump scope entirely.

Operators should: treat CI artifacts as sensitive (not suitable for sharing with
untrusted parties without review), verify no live credentials appear in failures,
and rotate any that do immediately. This is not a substitute for not logging
credentials in the first place.

### Automated code review and trust model

Optional feature: the repository uses the **CodeRabbit GitHub App** for automated
code review (configured in `.coderabbit.yaml`). The app is structurally safer than
a self-hosted API-key reviewer:

- **No repository secret in untrusted job**: A self-hosted API-key reviewer would
  require placing a repository secret (e.g., `ANTHROPIC_API_KEY`) inside a job
  that checks out untrusted fork code. If the fork is malicious, it can
  exfiltrate the secret from `$GITHUB_TOKEN`, env vars, or the runner's
  filesystem. The CodeRabbit app is a GitHub App, not a personal API key — it
  integrates via GitHub's OAuth flow and never places a repository secret
  alongside untrusted code.
- **PR content treated as data, not instructions**: Review is advisory (cannot
  block a merge) and does not execute instructions from PR body, title, or branch
  name. However, the app's configuration (`.coderabbit.yaml`) is read from the
  PR's HEAD branch — meaning a pull request, including one from a fork, can modify
  `path_instructions` and `labeling_instructions` and thereby change how the review
  is conducted. This is a real limitation of the GitHub App model: the bot's advice
  is weaker or differently-oriented on that PR than intended. It does **not** grant
  the PR repository permission, exfiltrate a secret (no repo secret is in the
  review path at all), or block or force a merge — the review remains advisory.
  The mitigation is the same as for any PR that edits CI workflows: human review
  should scrutinize changes to `.coderabbit.yaml`.
- **No implicit privilege escalation**: The app cannot request scopes it was not
  granted at install time, and it cannot modify its own permissions.

The review is advisory — a human reviewer must still validate changes before merge
— and the tool can be disabled at any time via GitHub's app management UI.

## Telemetry

Usage telemetry has two ends, each with its own trust boundary. For what
is sent and how to turn it off, see [install.md](install.md#telemetry).
For running a receiver, see [telemetry-provider.md](telemetry-provider.md).
The project's data-handling statement is at
<https://gameplane.net/telemetry/>.

### Public unauthenticated ingest and summary

A receiver's `/ingest` is open to the internet by design: installs have no
project-issued credential, so it cannot require one. (A self-hosted
receiver can require `AUTH_TOKEN`; the project's default receiver cannot.)
Anyone can therefore post reports. Defences:

- strict decoding: one JSON object, no duplicate or unknown keys, a 16 KiB
  body cap, and every string matched against a pattern or a fixed list, so
  hostile text can't create unbounded categories or reach the dashboard
- a per-source daily limit on accepted reports (`INGEST_SOURCE_DAILY_LIMIT`,
  default `20`, tolerant of shared NAT) and a rate limit on the public
  summary, both held in memory only
- optional proof-of-work (`INGEST_POW`, off by default; see below), which
  raises the cost of bulk reports in proportion to the request rate
- only daily aggregates and expiring activity records are stored; raw
  reports and source addresses are never written to disk

`GET /v1/summary` is off unless `PUBLIC_SUMMARY=true`. It returns five
fixed counts and nothing else: no breakdown, no per-day series, no install
ID. It is cached and rate-limited so public polling can't degrade ingest.

What this does not stop: someone can invent installs and send fabricated
basic reports, within the per-source limits. Figures are approximate and
self-reported, and the dashboard labels them that way.

### Proof-of-work on ingest

When a receiver sets `INGEST_POW=true`, `/ingest` requires a solved
challenge from `GET /v1/challenge` in the `Gameplane-Telemetry-PoW` header.
Challenges are HMAC-signed with a key held only in the receiver's memory,
expire after 15 minutes and work once. The difficulty is `INGEST_POW_MIN_BITS` (default zero) while at most
than `INGEST_POW_TARGET_PER_MIN` challenges a minute are issued, then rises
with the rate (up to `INGEST_POW_MAX_BITS`, at most 26) and falls one bit per
five minutes. The check runs before the body is read, so a refused report
costs the receiver one HMAC and one hash, and it answers `428`
(`pow_required` or `pow_invalid`) or, when its used-challenge set (1,000,000
entries) is full, `503 pow_busy`. Challenge requests have their own
per-source limit.

What it stops: cheap bulk fabrication. The work a flood must do grows
faster than its rate, while an install sending one report a day pays
nothing under normal load and a few seconds during a flood. It also
needs no per-install secret, so it works against an attacker who holds many
source addresses, which the per-source limits do not.

What it does not stop:
- a determined or well-resourced sender: SHA-256 is cheap on GPUs and
  dedicated hardware, so it deters volume, not a patient attacker
- fabricated installs: solving a challenge proves work was done, not that an
  install exists, so invented installs remain possible and figures stay
  approximate
- many sources raising the difficulty for everyone: the difficulty is
  global and capped by `INGEST_POW_MAX_BITS`; the per-source challenge limit
  only stops a single source doing it alone
- anything on the dashboard login: it has no proof-of-work (the dashboard
  uses no JavaScript); its defence is the credential minimum below

Proof-of-work does not replace signing or the per-source daily limit; all
three apply. A restart drops the challenge key and the used set, so
outstanding challenges fail once and installs retry with a new one.

### The pseudonymous install ID

The extended tier carries a random UUID. It is pseudonymous, not anonymous:
it links one install's reports over time. It is never derived from the
cluster, host, network or users; it is kept only on the install; an admin
can reset it at any time (the install then looks new to the receiver, and
the old ID lapses); and it is deleted when extended is turned off. The
receiver stores only `HMAC-SHA256(pepper, installID)` and never stores an
extended attribute (environment, games, features) next to it; those exist
only as daily aggregates.

### Signing: what it does and doesn't stop

Every extended report is signed (Ed25519) with a key derived from a random
secret kept in the install's database combined with the install ID. The
secret and private key never leave the install. The receiver binds an
install ID to the first valid key that uses it (the claim) and then
refuses, changing no stored data:

- a bad or missing signature (`403 bad_signature`)
- a send time outside `[now - 36h, now + 1h]` (`403 stale`)
- a different key for a claimed ID (`409 id_claimed`)
- a send time not later than the last accepted one (`403 replay`)

Signing stops impersonation of an existing install and replayed reports.
It does not stop fabricated installs: anyone can generate a new ID and key
pair, so a flood of invented installs is limited only by the per-source
limits. A claim lasts as long as the activity record (90 days without a
report by default), then the ID can be claimed again. If the receiver says
an ID is claimed by another key, the install replaces its ID and resends
once; any other refusal never rotates the ID.

### Dashboard authentication and the refusal invariant

The receiver's dashboard runs on its own listener (`:8081`), started only
when `DASHBOARD_TOKEN` is set; with no token there is no dashboard. Keep
that port off the public internet (the chart's NetworkPolicy admits only
configured peers). It uses one operator token from a Secret, which must be
at least 32 characters (generate it from 32 random bytes); the receiver
refuses to start with a shorter one. Per-source login limits can be
outrun by anyone holding many IPv6 `/64`s, so the token's length, not the
limiter, is what makes guessing infeasible:

- browser login compares the token in constant time and sets an
  `HttpOnly; Secure; SameSite=Strict` session cookie valid 12 hours; login
  and logout also require a same-origin `Origin`; logins are limited to 5
  per minute per source
- scripts can send `Authorization: Bearer <token>`; `/metrics` accepts only
  the Bearer token
- the cookie key is derived from the token, so replacing the token
  invalidates every session and loses no data
- every dashboard response carries a strict `Content-Security-Policy`, with
  no inline script or style, and `Cache-Control: no-store`

**Refusal invariant:** an unauthenticated response never contains a figure,
a date other than page chrome, a version, category or module name, or
anything that varies with whether data exists. A wrong token gets the same
"Invalid credentials" login page whether or not the receiver is empty, and
an unauthenticated JSON request gets `401 {"error":"unauthorized"}`. CI
compares the unauthenticated response against the login page of an empty
receiver.

### Source-address handling

The receiver uses the TCP peer address as the source for limits. It trusts
`X-Forwarded-For` only when the peer is inside `TRUSTED_PROXY_CIDRS`
(empty by default), so a client can't pick its own source by setting the
header. IPv4 addresses and IPv6 `/64` prefixes are treated as one source
each. Sources live in memory only (capped at 100,000 per limiter,
least-recently-used evicted), are never logged to the database, and
counters reset at UTC midnight. If you run behind a proxy and leave
`TRUSTED_PROXY_CIDRS` empty, every client shares the proxy's address and
the per-source limits throttle everyone together.

### The pepper

`ID_PEPPER` keys the HMAC that turns an install ID into the stored
activity-record key, so a stored value can't be matched to an ID taken
from an install without the pepper. If unset, the receiver generates one
on first start and keeps it in its database, so it is as exposed as the
database file. Set it from a Secret to keep it out of backups of the
database. Changing the pepper makes every existing record unmatchable: all
installs look new for one day and their activity history is lost; no
install is locked out. See
[telemetry-provider.md](telemetry-provider.md#rotating-secrets).

## API cluster-wide pod list

The API's `<release>-api-read` ClusterRole grants `list` on Pods in **every**
namespace, so the Cluster page can show per-node pod usage
(`countNodePods` in `api/internal/handlers/cluster.go`). A pod list returns
full pod specs, including plain-text environment values, for every workload
sharing the cluster — not just `gameplane-games`. A compromised API could
read them. The maintainer accepted this tradeoff on 2026-10-05 for the pod
meter. Values held in Secrets are not exposed: the API has no cluster-wide
Secret read. Without the grant, the API omits "used" and the dashboard shows
"—".

## mcp-server (optional)

The optional MCP server [optional] (`mcpServer.enabled`, see [`mcp-server/README.md`](../mcp-server/README.md))
is strictly read-only — no tool it exposes can create, update, patch,
delete, or apply anything, enforced structurally (its tool handlers only
ever hold a client whose exported methods are List/Get-shaped) and by RBAC
(a ClusterRole granting only `get`/`list`/`watch`, plus `get` on
`pods/log`).

That RBAC grant is **cluster-wide**, not scoped to `gameplane-games` or any
other single namespace: the server can list/read Pods, Events, and pod logs
in every namespace, including `kube-system` and any other workload's
namespace sharing the cluster. Pod logs in particular can surface secrets
an application logs at startup or during errors (API keys, connection
strings, stack traces) — Kubernetes has no mechanism to redact those.
Combined with write-freedom and opt-in, admin-only installation
(`mcpServer.enabled` plus whatever gates `kubectl exec` access to the
`gameplane-mcp-server` pod), this is an accepted tradeoff, not an oversight
— but install it knowing that anyone who can reach a `serve` session gets
read access to cluster-wide pod state and logs, not just Gameplane-managed
namespaces. If that blast radius is wider than acceptable for a given
cluster, don't enable `mcpServer` there.

## Secrets

Combined installations store management credentials in Kubernetes Secrets.
Standalone panels store their labelled management credentials, including remote
kubeconfigs, gateway keys, OIDC client secrets, notification credentials, and mod
registry keys, as AES-GCM ciphertext in SQL. They use a persistent 32-byte key.
The supplied standalone profiles mount it separately at `/keys/panel.key`;
custom deployments retain the legacy `/data/panel.key` default. Keep protected
key backups separate from database backups. Provisioned-key mode requires an
existing read-only key file or Kubernetes Secret and never creates a replacement.
Startup validates file ownership, permissions, type and size, and rejects extended
access ACLs that could grant additional readers. Encryption does not protect credentials from someone who can
read both files or control the running API. A missing key with existing
credentials, a wrong key, or corrupted credential data prevents API startup.
See [standalone storage and recovery](standalone-panel.md#storage-and-recovery).

Master-key rotation is an offline transaction through `rotate-panel-key`.
Versioned authenticated ciphertext identifies its key; the database key-state
lock rejects writes by processes still holding a retired key. Old key files are
retained for historical backups. See the
[rotation procedure](standalone-panel.md#rotate-the-master-encryption-key) before
changing a mounted key.

The feature labels and managed-secret deletion guards apply to both stores.
Game credentials and backup repository Secrets remain on the workload cluster.
The Kubernetes Secret and Helm environment examples below apply to Kubernetes
deployments.

Secrets Gameplane reads or creates, by convention:

- `gameplane-<gameserver>-rcon` — per-game RCON password, created by operator
- `gameplane-agent-ca` — CA bundle the API trusts
- `gameplane-agent-client` — API's client cert/key
- `gameplane-oidc` — OIDC client secret (user-supplied)
- `gameplane-backup-repo` — restic repo URL + password (user-supplied)
- audit-webhook auth — any Secret you reference via
  `api.audit.webhook.authSecretRef` (user-supplied). The token is injected as an
  env var, never a flag, so it does not appear in the pod spec or `ps` output.
- audit S3 credentials — any Secret you reference via
  `api.audit.s3.credentialsSecretRef` (user-supplied). The access key and secret
  key are injected as env vars (`GAMEPLANE_AUDIT_S3_ACCESS_KEY`,
  `GAMEPLANE_AUDIT_S3_SECRET_KEY`), never flags, so they do not appear in the pod
  spec or `ps` output.
- notification sinks — any Secret labelled
  `gameplane.local/notification-sink=true` in the control-plane namespace
  (user-supplied; referenced by name from Admin Settings → Notifications, read
  by the API at delivery time — see [notifications](notifications.md)).
- telemetry receiver credentials — `api.telemetry.receiver.dashboard.tokenSecretRef`
  (dashboard and `/metrics` token, at least 32 characters) and `api.telemetry.receiver.pepperSecretRef`
  (install-ID pepper), both user-supplied and injected as env vars
  (`DASHBOARD_TOKEN`, `ID_PEPPER`), never flags. See
  [telemetry-provider.md](telemetry-provider.md#rotating-secrets).

Rotation: deleting the `-rcon` secret triggers a reconciliation and
generates a fresh password on the next pod restart.

## Kubeconfig Secret handling

Each registered cluster references a labelled kubeconfig credential, stored in
a Kubernetes Secret in combined mode or encrypted SQL in standalone mode.
Access to cluster credentials is protected by several layers:

- **Embedded credentials only.** The API and operator reject token files,
  client certificate/key files, CA files, `exec` authentication, and
  `auth-provider` plugins before creating a client. This applies to every
  entry, including unused contexts. Remote kubeconfigs must carry tokens
  or certificate/key/CA data directly; they cannot read control-plane files
  or run local authentication commands.
- **Standalone destination policy.** Standalone clients require verified HTTPS,
  reject redirects and forward proxies, and validate all DNS answers before
  dialing a pinned address. Loopback, metadata, link-local and other unsafe
  address classes are blocked; optional operator CIDRs narrow access to the
  intended workload networks. The same policy protects gateway connections.
  Remote response, watch-frame and notification-cache limits bound memory used
  by an untrusted workload endpoint. See the
  [standalone connection policy](standalone-panel.md#install-and-register-remote-game-clusters).
- **Label guard.** The API only reads Secrets labelled
  `gameplane.local/cluster-kubeconfig=true` when registering a cluster
  via the dashboard or API. This prevents a user from pointing at an
  arbitrary control-plane Secret (e.g., the OIDC client secret or
  backup credentials) and using it as a kubeconfig.
- **Delete guard.** `DELETE /clusters/{name}` drops the cluster's client at
  once and deletes the referenced Secret only when it uses the API's fixed
  registration name or a nonce-suffixed rotation name for that cluster, and carries
  `gameplane.local/cluster-kubeconfig=true`. Rotation names additionally require
  the API's managed-by label (legacy fixed-name Secrets predate that label).
  UID/resourceVersion preconditions prevent cleanup from deleting a replacement.
  Any other Secret, including
  one named for a different cluster or one without the kubeconfig label, is
  left in place. A kubeconfig Secret you create with kubectl or GitOps under
  another name is never deleted over HTTP.
- **In-place rotation.** `PUT /clusters/{name}/kubeconfig` requires central
  `cluster:manage`, publishes an immutable replacement, and switches the
  registration reference only if its UID and previous reference still match.
  It preserves gateway configuration and user grants and reloads the client.
  Revoke the old credential on the target after verifying the replacement;
  requests already in flight may still use it.
- **Never logged or returned.** The kubeconfig is never logged by the
  API, never echoed in responses, never visible in audit trails. It
  exists only to bootstrap the Kubernetes client for that cluster.
- **Permission gating.** Registering or deleting a cluster requires
  `cluster:manage`. Discovery returns only registrations the caller may see;
  workload grants and central cluster-management permission determine visibility.
- **No implicit RBAC.** Registering a cluster does not grant any user
  access to resources on that cluster. The central API stores dashboard user
  grants by target cluster and namespace. The registered kubeconfig's Kubernetes
  permissions are a separate requirement. See [install.md](install.md#rbac-and-permissions).

## Install-Time OIDC Role Mappings

When OIDC authentication is configured at install time via Helm values
(`api.oidc.groupsClaim`, `api.oidc.roleMappings`, `api.oidc.defaultRole`) (unreleased; ships in the next release),
Gameplane automatically assigns roles to users based on their OIDC provider's
group/role claims on every login. This eliminates the need for a bootstrap-admin
account in OIDC-only deployments — an operator can configure group mappings at
install time and the first user to log in receives the correct role immediately.
The security model consists of:

### Trust Chain: IdP Group Membership → Gameplane Roles

**Core risk**: Gameplane trusts the IdP's group claim unconditionally. Whoever
controls IdP group membership effectively controls Gameplane role assignments.
If an attacker compromises the IdP or its group directory (LDAP, Active Directory,
cloud identity service), they can add themselves to a mapped group and gain that
group's Gameplane role on their next login — up to and including admin access.

This is an accepted architectural boundary: the IdP is a trust root. If the IdP
is compromised, Gameplane cannot defend against that. Mitigation is at the IdP
level: strong authentication to the IdP, audit logging of group membership changes,
and monitoring for suspicious group additions.

### Helm-Seeded Values vs. Database Overrides (Hybrid Model)

Install-time values (`api.oidc.roleMappings.*`) seed the role-mapping policy in
the database when the API starts. An admin can then override one or more roles'
group lists through the dashboard (`PUT /admin/config/auth` with `helmOverride`)
at runtime, without restarting the API or re-running Helm. Each role's effective
mapping is determined independently:

- **Database override present** (even if an empty list `[]`): That list is the
  effective mapping for that role, used on every login. An empty list means
  "nobody maps to this role from any group" — a valid and meaningful override
  distinct from "no override set."
- **Database override absent**: The Helm-seeded value is the effective mapping for
  that role.

When an operator runs `helm upgrade` and changes a Helm value (`api.oidc.roleMappings.*`),
the new value updates the seed — but it does NOT overwrite a database override that
has already been set for that role. The override persists until explicitly reset via
the dashboard (`DELETE /admin/config/auth/role-mappings/{role}`). This is deliberate:
Helm upgrades should not silently undo admin customizations made through the UI.

Consequences for operators: the effective role mappings in the dashboard may not
match what is in `values.yaml` after one or more roles have been dashboard-overridden.
To audit what is actually configured, consult the dashboard's `/admin/config` view
(`installTimeSettings.oidcHelmProvider` shows the Helm seed, `auth.helmOverride`
shows any database overrides) rather than relying on Helm values alone.

### Most-Privileged-Match Rule Across Sources

When resolving a user's role, Gameplane matches the user's groups against every
role's effective mapping (seed + overrides merged) and assigns the **highest
privilege match**: `admin` > `operator` > `viewer`. This is applied *after* the
per-role merge of Helm seed and database override, so a user matching both an
overridden (database-managed) viewer group *and* a Helm-seeded admin group still
resolves to `admin`.

Overriding a lower role does **not** revoke a higher one. Example: if the database
overrides the `viewer` list to `[]` (nobody maps to viewer), but the Helm-seeded
`admin` list is `["admins"]`, a user in the "admins" group still resolves to `admin`
on the next login — the admin mapping was never overridden.

### Re-evaluation on Every Login

On each OIDC login, Gameplane:

1. Extracts the user's group membership from the OIDC token's group claim (configured
   via `api.oidc.groupsClaim`; defaults to `"groups"`).
2. Reads the effective role mappings: the Helm-seeded values, merged with any
   database overrides for each role independently.
3. Matches the user's groups against the effective mappings to compute their role.
4. If no role matches, assigns the default role (configured via `api.oidc.defaultRole`;
   defaults to `viewer`; can be set to `deny` to reject login).

This re-evaluation runs whenever the effective role mappings exist: Helm-seeded
`api.oidc.roleMappings` (at least one non-empty role array), a dashboard
`helmOverride.roleMappings` overlay (which counts even when the Helm values set
no mappings), or the mappings of a dashboard-managed provider. If none is
configured, new OIDC users receive the fixed `viewer` role and existing users'
roles are never re-evaluated.

Two guards prevent lockout during re-evaluation:

- **No lockout rule at login**: If re-evaluating a user's role would remove the
  last user able to manage users (hold the `users:manage` permission), the
  re-evaluation is **not applied** — that user retains their old role and can
  still manage other users. This prevents an operator from accidentally creating
  an unrecoverable lockout via an override change.
- **Break-glass mechanism**: If role mappings are misconfigured such that nobody
  can reach admin, an operator can run the `bootstrap-admin` break-glass command
  to create a local admin account and fix the mappings. Bootstrap-admin and
  OIDC-mapped admin accounts coexist peacefully.

### Admin-Mapping Warning (FR-015)

Gameplane **always warns** when an operator configures or changes a role mapping
that includes a group. The warning text states: "Be aware that an OIDC group may
include a large number of users, and assigning it to the admin role grants admin
access to all members of that group." This warning is unconditional — it appears
on every such configuration change, not just when the operator first enables role
mappings.

**Why unconditional**: Gameplane cannot enumerate OIDC group membership — it cannot
tell whether a group name refers to a 3-person team or a 3000-person organization.
An attacker with dashboard access who knows the group structure could configure a
mapping for an unexpectedly large group to gain access. Operators must be aware of
this risk at every configuration step. The warning does not prevent the change, but
it ensures operators cannot claim they were not aware of the risk.

### Audit Trail: Every Assignment, Override, and Reset

Every role assignment driven by OIDC group mappings (on initial login or re-evaluation)
is recorded in `audit_events` with the matched group name and role transition:

- **Action**: OIDC login with role assignment
- **Target**: the user (subject of the OIDC token)
- **Details recorded**:
  - Which OIDC provider performed the assignment (`"helm"` for the Helm-seeded provider, otherwise the dashboard-managed provider's name)
  - Which group matched a mapping rule (or `"none"` if no mapping matched)
  - The user's old role (`"new_user"` on first login, or the previous role)
  - The assigned role (`"viewer"`, `"operator"`, `"admin"`, or `"denied"` if rejected)

Examples of what gets logged:
- First login, matched admin group: user created with admin role from group membership
- Re-evaluation, no mapping match: role re-evaluated to default role on next login
- Subsequent login, role upgraded: user's role changed from viewer to operator based on new group membership

Every dashboard change to a role's override — writing a new group list via
`PUT /admin/config/auth` or resetting it via `DELETE /admin/config/auth/role-mappings/{role}`
— is also audited as a configuration change:

- **Action**: Role mapping override write or reset
- **Target**: the affected role (`"admin"`, `"operator"`, or `"viewer"`)
- **Details recorded**:
  - Which admin made the change (from session)
  - The new or reset value (the group list, if changed)

This allows operators to track who changed what mappings and when, and to
correlate unexpected role assignments with dashboard configuration changes.

### `groupsClaim` and `defaultRole` Are Helm-Only

`api.oidc.groupsClaim` (the claim name from the OIDC token that holds group
information) and `api.oidc.defaultRole` (the fallback role when no group matches)
are configured via Helm values only — there is no dashboard write path for either
in v1.

**Why this matters for security**: An attacker with dashboard admin access can
override individual role mappings but **cannot** repoint the group claim to a
different OIDC token field (e.g., changing from `"groups"` to a field they control),
nor can they change the default role fallback to `"deny"` to lock everyone out. These
two settings remain under the operator's full control, via Helm values only, and
require a `helm upgrade` to change — an out-of-band action that can be audited and
gated by access controls on the cluster itself (e.g., who can run Helm in production).

## Pre-auth screens

No internal infrastructure metrics are displayed on the login page or
any other unauthenticated surface. This is a hard requirement — see
`web/src/routes/Login.tsx` for the enforcement.

The API's Prometheus metrics follow the same rule. They are served on a
dedicated listener (`--metrics-addr`, chart value `api.metricsPort`,
default `9090`), never on the public API port that the Ingress and the web
front end route to, so `/metrics` on the dashboard host answers 404. The
chart's ServiceMonitor scrapes the metrics port from inside the cluster.
