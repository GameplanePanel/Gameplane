# Install

For a central dashboard/API on a host without Kubernetes, use the
[standalone panel guide](standalone-panel.md), including Docker Compose and a
panel-only Helm profile. The instructions below describe the default combined
installation, with an operator and a local game cluster.

## Prerequisites

- Kubernetes 1.28+
- Helm 3.13+
- A default StorageClass (any RWO CSI driver works)
- Optional: an ingress controller (nginx-ingress by default) + cert-manager for TLS

## One-shot install

The chart and its images are published to the GitHub Container Registry (GHCR)
as OCI artifacts — no `helm repo add` needed. Install a tagged release straight
from the registry (replace `<version>` with a release, e.g. `0.3.0`):

```sh
helm upgrade --install gameplane oci://ghcr.io/gameplanepanel/charts/gameplane \
  --version <version> \
  --namespace gameplane-system --create-namespace \
  --set ingress.host=gameplane.your-domain.test
```

The chart's `appVersion` pins matching component images under
`ghcr.io/gameplanepanel/gameplane/<name>:<version>`, so no image overrides are
needed for a released version. A default install pulls four of them:
`operator`, `api`, `web`, and `agent` (the last is pulled per-GameServer, on
demand). The remaining eight — `audit-syslog-bridge`, `telemetry-receiver`,
`sentinel`, `tunnel-frp`, `tunnel-tailscale`, `tunnel-playit`, `mcp-server`,
and `capture-sidecar` — are only pulled when the optional component they
belong to is enabled (and, for `sentinel` and `capture-sidecar`, only for
GameServers that opt in). See each component's values block for its enable
flag.

### Edge channel (latest edge build)

Every push to `master` publishes rolling `:edge` images. To track them, install
the chart and point images at the edge tag:

```sh
helm upgrade --install gameplane oci://ghcr.io/gameplanepanel/charts/gameplane \
  --version <version> --set image.tag=edge \
  --namespace gameplane-system --create-namespace \
  --set ingress.host=gameplane.your-domain.test
```

`:edge` moves with `main`; pin a specific commit with `image.tag=sha-<short>`
when you need reproducibility.

### Verifying image signatures

Every published image (tagged releases and `:edge`), the Helm chart, and the
official module bundles are signed with the project's cosign key,
[`cosign.pub`](../cosign.pub) at the repo root, and recorded in the public
Sigstore Rekor transparency log:

```sh
cosign verify --key cosign.pub \
  ghcr.io/gameplanepanel/gameplane/operator:<version>
```

Pre-rotation releases (v0.2.0-beta.7 and earlier) used the retired Ed25519 key <!-- doc-versions: historical -->
and lack transparency log entries — verify those with `cosign-legacy.pub` and
`--insecure-ignore-tlog=true`. See [`key-rotation.md`](key-rotation.md) for the
trust continuity proof. Module bundles are verified the same way; the chart
carries the key, so bundle verification is just a values flip — see
[`module-authoring.md`](module-authoring.md#signing-official-bundles).

From source (during development), the chart in this repo always renders against
the local default image path:

```sh
helm upgrade --install gameplane ./charts/gameplane \
  --namespace gameplane-system --create-namespace
```

> **Note:** GHCR packages are private on first publish. The maintainer makes the
> `gameplane/operator`, `gameplane/api`, `gameplane/agent`, and `charts/gameplane`
> packages public once (GHCR → package → *Package settings* → *Change visibility*)
> so anonymous `helm install` / `docker pull` works. For a private install,
> create a `kubernetes.io/dockerconfigjson` pull secret and set
> `image.pullSecrets`.

## First-time setup

Seed an initial admin user. Passwords must be at least 12 characters.

```sh
kubectl -n gameplane-system exec deploy/gameplane-api -- \
  /api bootstrap-admin --username admin --password "<choose>"
```

To avoid the password landing in your shell history, pipe it on stdin:

```sh
printf '%s' "$ADMIN_PASSWORD" | kubectl -n gameplane-system exec -i deploy/gameplane-api -- \
  /api bootstrap-admin --username admin --password-stdin
```

If a user with that name already exists, pass `--force` to rotate the
password, promote them to `admin`, and end their existing sessions.

Open `https://<ingress.host>` and log in.

## Values reference

Top-level knobs (see `values.yaml` for the full list):

- `image.registry` / `image.tag` — container image pinning
- `operator.enabled` — enable the workload operator (default `true`). Set to `false` for a standalone central panel.
- `api.enabled` — enable the central API and dashboard (default `true`). Set to `false` on a remote operator/gateway installation.
- `api.standalone` — store management records and encrypted credentials in SQL without a local cluster (default `false`). Requires `operator.enabled=false`, one API replica, and `--skip-crds` when installing the panel. See the [standalone guide](standalone-panel.md).
- `operator.replicas` — leader-elected, safe at 2+
- `operator.configInitImage` / `operator.resticImage` — the two images the operator
  injects into workloads it creates: the config-init container on game pods
  (`busybox`) and the backup/restore Jobs (`restic/restic`). Both default to the
  upstream pins; retag them to a private registry mirror for air-gapped clusters
  where Docker Hub is unreachable. They map to the operator's
  `--config-init-image` / `--restic-image` flags, mirroring `operator.agentImage`
- `operator.backupJobBackoffLimit` / `operator.backupJobActiveDeadlineSeconds` — retry limit (default `2`, must be >= 0) and wall-clock deadline in seconds (default `86400`, must be > 0) for the restic backup/restore Jobs. They map to the operator's `--backup-job-backoff-limit` / `--backup-job-active-deadline-seconds` flags. Negative or non-integer values fail the render; an unset key (e.g. `helm upgrade --reuse-values` from an older release) or a `0` deadline uses the default, while an explicit `0` backoff is honoured.
- `operator.gameDataStorage.storageClassName` — install-time default storage class
  for game server data volumes (unreleased; ships in the next release) (default `""`). Empty string uses the cluster's
  default StorageClass. Applies to all GameServers where neither the GameTemplate
  nor GameServer-level override specifies a class. **Precedence**: GameServer
  override > GameTemplate default > install-time default > cluster default.
  **Immutability**: Changing this value affects only new PVCs — existing volumes
  persist unchanged (PVCs are immutable by Kubernetes design). **Error handling**:
  If the named StorageClass doesn't exist, PVC provisioning fails and the
  GameServer enters Pending with a `PVCProvisioningFailed` condition (visible in
  the dashboard); no pod starts until resolved. Example:
  `--set operator.gameDataStorage.storageClassName=fast-nvme`
- `api.db.driver` — `sqlite` (default, production-tested) or `postgres` [experimental] (requires an api image built with `-tags postgres`; not yet covered by e2e or upgrade tests)
- `api.db.dsn` — connection string; SQLite default persists to a PVC
- `api.storage.existingClaim` — pre-existing PVC for the API's SQLite database and, in standalone mode, its encryption key (default `""`). Standalone PostgreSQL deployments also need persistent storage for the key. The chart annotates `gameplane-api-data` with `helm.sh/resource-policy: keep` so switching to an existing claim preserves the previous PVC.
- `api.oidc.enabled` + the following settings — wire OIDC login from Helm (shows
  up as the read-only `helm` provider). Providers can also be added at runtime
  under **Admin Settings → Authentication** — no Helm values or restart needed;
  see [security](security.md#dashboard-managed-providers). Settings:
  - Core connection: `issuer` / `clientID` / `clientSecretRef` / `redirectURL` /
    `displayName` — OIDC provider credentials and endpoints. Per-IdP walkthroughs
    (Keycloak, Authentik, Google) live in [oidc.md](oidc.md)
  - Role mapping (new, seeded at install time) (unreleased; ships in the next release):
    - `groupsClaim` — OIDC claim name containing group memberships (default `""`).
      Typically `"groups"` or `"roles"` depending on your IdP. Empty/omitted =
      read the `"groups"` claim. This only chooses which claim is read; it does
      not turn group-based role mapping on or off (`roleMappings` below does).
      Example: `--set api.oidc.groupsClaim=roles`
    - `roleMappings.admin`, `roleMappings.operator`, `roleMappings.viewer` — arrays
      of IdP group names mapping to each dashboard role (default `[]`). Example:
      `roleMappings.admin: ["gameplane-admins", "ops-team"]` means users in either
      group receive the `admin` role. **These Helm values seed the mappings at
      install/upgrade time.** After install, admins can override any role's groups
      from the dashboard (**Admin Settings → Authentication**) without restarting;
      a dashboard override survives future `helm upgrade`s that change the Helm
      value. Per-role precedence: dashboard override (if present) > Helm seed. No
      admin account or `bootstrap-admin` run needed for OIDC-only installs.
    - `defaultRole` — Helm-only (no dashboard override in v1). Default role when a
      user's IdP groups don't match any `roleMappings` entry (default `""`).
      Accepted values: `""` (treat as `"viewer"`), `"viewer"`, `"operator"`,
      `"admin"`, or `"deny"` (reject login). Meaningful only when role mappings
      are configured. Example: `--set api.oidc.defaultRole=viewer`
  **Backward compatibility**: Group-based mapping is active whenever at least
  one `roleMappings` list is non-empty or an admin has set a mapping override
  in the dashboard, whatever `groupsClaim` says. With neither, mapping is off:
  new OIDC users get `viewer`, existing users' roles are never re-evaluated,
  and existing OIDC setups continue unchanged
- `ingress.host` — dashboard hostname
- `ingress.annotations.nginx\.ingress\.kubernetes\.io/proxy-body-size` —
  defaults to `512m`, matching the dashboard nginx and API mod-upload
  request limit. The complete multipart request, including framing, must
  fit; the agent defaults to 256 MiB per mod file. When upgrading with
  reused values or custom annotations, replace an existing `64m` limit
  with `512m` to permit those uploads. Other ingress controllers need
  their equivalent body-size setting.
- `gamesNamespace` — namespace where GameServers are created (default `gameplane-games`)
- `networkPolicies.enabled` — default-deny in games namespace (recommended on)
  - `networkPolicies.kubeletCIDRs` — CIDRs for kubelet liveness/readiness probes (defaults to RFC1918 + link-local)
  - `networkPolicies.probePorts` — specific ports to allow on game pods for kubelet probes (empty = any port)
  - `networkPolicies.gameIngress` — control ingress to game pods from external sources (players)
    - `gameIngress.enabled` — toggle player-ingress allowance (default `true`)
    - `gameIngress.fromCIDRs` — CIDRs from which player traffic is allowed (default `[0.0.0.0/0]`)
  - `networkPolicies.apiServerCIDRs` — CIDRs allowed for agent heartbeat to kube-apiserver (defaults to all RFC1918 and link-local addresses on TCP 443/6443; customize to narrow to specific API endpoint(s))
  - `networkPolicies.gameEgress` — control public-internet egress for game pods (downloads, mod registries)
    - `gameEgress.enabled` — toggle public-egress allowance (default `true`)
    - `gameEgress.ports` — TCP ports for downloads (default 80, 443)
    - `gameEgress.privateCIDRs` — exclude private ranges from public-egress (anti-SSRF)
  - `networkPolicies.backupEgress` — control egress from Backup/Restore restic Job pods to their configured repository
    - `backupEgress.enabled` — toggle backup/restore-egress allowance (default `true`)
    - `backupEgress.ports` — TCP ports for repository connections (default 443, 22); unlike `gameEgress` there is no private-range exclusion, since the destination is an admin-configured repository Secret rather than an attacker-influenced URL, and is often itself private
- `clusterOps.enabled` — credential-minting cluster operations (Add node, Download kubeconfig) in the dashboard's Cluster page (default off; grants powerful kube-system + CSR-approval RBAC)
  - `clusterOps.externalAddress` — external (node-routable) API server address, e.g. `1.2.3.4:6443` or `https://k8s.example.com:6443`, used in the join command and downloaded kubeconfig instead of the in-cluster ClusterIP; leave empty only when the in-cluster address is itself reachable from outside the cluster
- `mcpServer.enabled` — optional strictly read-only MCP (Model Context Protocol) server [optional] for AI assistants to read cluster state and propose fixes (default off); see [mcp-server/README.md](../mcp-server/README.md)
  - `mcpServer.replicas` — MCP server replicas (default 1)
- `updates.channel` — informational release-channel label (e.g., `stable`, `edge`) shown read-only in the dashboard's Admin Settings → Updates section; purely informational (Gameplane upgrades via Helm, not auto-update)
- `podSecurity.enforceRestricted` — label games namespace for Pod Security Standards
- `defaultModuleSource.*` — the official game catalog shipped with the chart (enabled by default)
  - `defaultModuleSource.enabled` — whether to create the default `ModuleSource` (default `true`; disable when managing sources via GitOps)
  - `defaultModuleSource.name` — name of the `ModuleSource` resource (default `default`)
  - `defaultModuleSource.refreshInterval` — how often the catalog is re-indexed (default `1h`)
  - `defaultModuleSource.type` — source type: `oci` (default, pulls pre-built bundles from a registry) or `git` (index the public `GameplanePanel/module` repo)
  - `defaultModuleSource.git.*` — git configuration (when `type: git`)
    - `git.url` — repository URL (default `https://github.com/GameplanePanel/module.git`)
    - `git.ref` — git branch/tag (default `main`)
    - `git.subPath` — module subdirectory within the repository (default `""`, empty means root)
  - `defaultModuleSource.oci.*` — OCI registry configuration (when `type: oci`)
    - `oci.url` — OCI registry URL (e.g., `ghcr.io/gameplanepanel/gameplane-modules`)
    - `oci.insecure` — use plain HTTP (no TLS) for local registries such as kind/k3d; TLS verification is never skipped
    - `oci.modules` — which modules to pull from the registry
    - `oci.pullSecretName` — optional kubernetes.io/dockerconfigjson Secret for private registries
    - `oci.verify.enabled` — enable cosign signature verification for official bundles (default off)
    - `oci.verify.cosignPublicKey` — cosign public key for verification (official key shipped in values.yaml)
- `uploadModuleSource.enabled` — the `uploads` source backing dashboard bundle uploads (default on)
  - `uploadModuleSource.name` — name of the upload `ModuleSource` (default `uploads`)
  - `uploadModuleSource.refreshInterval` — re-index interval for uploaded modules (default `1h`)
- `operator.localModules.{enabled,hostPath,existingClaim,mountPath}` — mount a
  directory of module bundles into the operator for `local`-type sources
- `serviceMonitors.enabled` / `prometheusRules.enabled` / `grafanaDashboards.enabled`
  — opt-in Prometheus Operator integration (see [Observability](#observability))
- `operator.sentinelImage` — the optional sentinel [optional] component for wake-on-connect (default
  `ghcr.io/gameplanepanel/gameplane/sentinel:<version>`). The sentinel holds
  advertised ports while a GameServer is asleep and wakes it on a genuine
  connection attempt; opt-in per server via `spec.idle.wakeOnConnect` (default
  false). Runs as a small 1-replica Deployment per armed server; costs one pod
  per sleeping server, so disabled by default. See `docs/roadmap.md` for design
  details, caveats (Hostport asymmetry), and why only Minecraft and Terraria get
  real handshake parsing (other games use a generic packets-in-window heuristic).
- `operator.addressManager` — which load-balancer address manager runs in this
  cluster, so the operator knows how to express a GameServer's
  `spec.networking.addressPool` / `spec.networking.address` preference. One of:
  - `metallb` — Service annotations `metallb.io/address-pool` and
    `metallb.io/loadBalancerIPs`.
  - `cilium` — Service label `gameplane.local/lb-pool` plus annotation
    `lbipam.cilium.io/ips`. The label is a Gameplane convention, not something
    Cilium recognises on its own: mirror it in your
    `CiliumLoadBalancerIPPool`'s `spec.serviceSelector` or the pool preference
    selects nothing.
  - `none` (default) — no Service is mutated. A pool/address preference is
    reported on the GameServer's `AddressAssignment` condition as unhonored
    rather than silently falling back to the cluster's default pool.

  Any other value fails the operator at startup. The operator never writes the
  deprecated `service.spec.loadBalancerIP`.
- `capture.enabled` — the optional network packet capture sidecar [optional] for GameServers
  (default `false`). When enabled cluster-wide, admins can opt individual GameServers
  into live AF_PACKET capture with BPF filtering and download PCAPNG files. Captures
  are always opt-in per server via `spec.capture.enabled` and admin-only (`captures:manage`
  permission). Key sub-values:
  - `capture.defaultRetentionSeconds` — how long a completed capture is kept before
    automatic deletion (default `86400` = 24 hours).
  - `capture.maxRetentionSeconds` — cluster-wide maximum retention, clamping per-server
    overrides (default `604800` = 7 days). Reflects a GDPR Art. 5(1)(e) storage-limitation
    engineering default, not a legal requirement.
  - `capture.defaultMaxDurationSeconds` — default maximum runtime per capture in seconds
    (default `300` = 5 minutes); captures stop automatically when the duration is reached.
  - `capture.defaultMaxSizeBytes` — default maximum file size per capture in bytes
    (default `943718400` = 900 MiB, kept under the 1 GiB `emptyDir` limit backing
    the capture volume); captures stop automatically when the size limit is reached.
  - `capture.image` — sidecar container image (defaults to `{image.registry}/capture-sidecar:{image.tag}`).

## Observability

The operator, API, and in-pod agent sidecars expose Prometheus metrics on
`/metrics` (operator `:8080`, API `:9090`). The API serves metrics on a
dedicated listener (`api.metricsPort`, default `9090`), not on its public
port (`:8000`), so only in-cluster scrapers reach them. The agent's control
port (`:8090`) requires an mTLS client cert for every route it serves, so its
`/metrics` lives on a separate, unauthenticated listener instead
(`:9090`, `agent/cmd/main.go`'s `--metrics-addr`) — a Prometheus scraper
never needs, and never gets, the client cert that unlocks console/files/RCON
on `:8090`. Three **off-by-default** chart toggles wire these into a
Prometheus-Operator stack (e.g. kube-prometheus-stack):

- `serviceMonitors.enabled` — `ServiceMonitor`s so Prometheus scrapes the
  operator, API, and telemetry-receiver (when deployed with a dashboard
  token; its `/metrics` needs that token), plus a `PodMonitor`
  that scrapes per-GameServer agent metrics from game pods in
  `gamesNamespace` on their plain, named `metrics` containerPort (`9090`,
  declared by the operator's `buildAgentContainer`; no TLS, no client cert —
  the mTLS control port `8090` is never scraped).
- `serviceMonitors.scrapeNamespaceSelector` — set this to your Prometheus's
  namespace (e.g. `{matchLabels: {kubernetes.io/metadata.name: monitoring}}`)
  whenever `networkPolicies.enabled` is also `true`. Without it, both the
  games-namespace default-deny policy (agent metrics port `9090` is not
  admitted from any namespace by default) and the telemetry-receiver's
  `NetworkPolicy` (its metrics port `8081` admits only
  `api.telemetry.receiver.dashboard.ingressFrom` peers) leave the
  `PodMonitor` and `ServiceMonitor` targets unreachable even though they
  render.
- `prometheusRules.enabled` — a `PrometheusRule` of operator alerts.
- `grafanaDashboards.enabled` — a Grafana dashboard `ConfigMap` the Grafana
  sidecar auto-imports (relabel via `grafanaDashboards.labels` if your sidecar
  watches a different label).

All three add `labels:` you can set so a Prometheus/Grafana selector picks the
objects up.

### Metrics

**Operator fleet gauges** (computed at scrape time from the operator's cache):

| Metric | Labels | Meaning |
|---|---|---|
| `gameplane_gameservers` | `phase` | GameServers per lifecycle phase (Pending/Starting/Running/Stopping/Stopped/Suspended/Failed) |
| `gameplane_backups` | `phase` | Backups per phase (Pending/Running/Succeeded/Failed) |

Every phase is always present (0 when empty). With 2+ operator replicas each
replica reports the same cache-derived counts, so aggregate with
`max by (phase) (...)` (the bundled dashboard and alerts already do).

**Agent per-server metrics** (scraped over plain HTTP from the named `metrics` port, 9090, in each game pod when
`serviceMonitors.enabled: true`):

| Metric | Labels | Meaning |
|---|---|---|
| `gameplane_agent_cpu_millicores` | `server`, `namespace`, `template`, `game` | Game process CPU usage (millicores, read from `/proc`). Emitted only when readable. |
| `gameplane_agent_cpu_limit_millicores` | `server`, `namespace`, `template`, `game` | CPU limit for the game container (millicores). Emitted only when readable. |
| `gameplane_agent_memory_bytes` | `server`, `namespace`, `template`, `game` | Game process memory usage (bytes, read from `/proc`). Emitted only when readable. |
| `gameplane_agent_memory_limit_bytes` | `server`, `namespace`, `template`, `game` | Memory limit for the game container (bytes). Emitted only when readable. |
| `gameplane_agent_disk_used_bytes` | `server`, `namespace`, `template`, `game` | Disk used in the server's data directory (bytes). Emitted only when readable. |
| `gameplane_agent_disk_total_bytes` | `server`, `namespace`, `template`, `game` | Total disk capacity of the server's data directory (bytes). Emitted only when readable. |
| `gameplane_agent_players_online` | `server`, `namespace`, `template`, `game` | Number of players currently online. Emitted only when the game RCON succeeded. |
| `gameplane_agent_players_max` | `server`, `namespace`, `template`, `game` | Maximum player capacity: -1 for unlimited, positive value for a known limit. Emitted only when the capacity is known; absent when unknown or RCON fails. |

**Example PromQL queries for right-sizing servers:**

```
# Peak CPU over the past week per server
max_over_time(gameplane_agent_cpu_millicores[7d])

# Average memory utilization (as a percentage of limit) over 7 days
avg_over_time(gameplane_agent_memory_bytes[7d]) / gameplane_agent_memory_limit_bytes * 100

# 95th percentile disk usage per template
quantile_over_time(0.95, gameplane_agent_disk_used_bytes[7d])
```

### Alerts

`prometheusRules.enabled` ships (group `gameplane.operator` unless noted):

- `GameplaneOperatorReconcileErrors` — a controller failing reconciles for 10m.
- `GameplaneOperatorWorkqueueBacklog` — a workqueue over 50 items for 15m.
- `GameplaneOperatorReconcileStuck` — a single reconcile running over 5m.
- `GameplaneGameServerFailed` *(group `gameplane.fleet`)* — any GameServer in
  the Failed phase for 10m.
- `GameplaneBackupFailed` *(group `gameplane.fleet`)* — any Backup in the Failed
  phase for 15m (a failed backup is a data-loss risk until superseded or pruned).

### Notifications

Prometheus alerts cover operators watching a dashboard; for pushing events to
where a game-server admin actually lives — Discord, Slack, email, or any
webhook receiver — configure notification sinks under **Admin Settings →
Notifications**. No Helm values are involved: sinks are runtime config, with
credentials in labelled Secrets. Event types, Secret shapes, and the
test-send endpoint are documented in [notifications.md](notifications.md);
delivery health is visible at `/metrics` as
`gameplane_notify_deliveries_total`.

### Audit log

Every mutating API request is recorded to the `audit_events` table and served at
`GET /admin/audit` (and `GET /admin/audit/export` for a full CSV/JSON dump).
Beyond the database, the trail can be fanned out to external systems — each sink
**mirrors**, it never gates: events always land in the database regardless, and
a slow or down sink never blocks or fails a request.

- `api.audit.retentionDays` — prune events older than N days (`0` = keep
  forever, the default).
- `api.audit.stdout` — also emit each event as a structured JSON log line, for a
  cluster log aggregator (Loki/ELK/CloudWatch) scraping the pod's stdout.
- `api.audit.webhook.url` — POST each event as JSON to an HTTP receiver (a log
  aggregator's push endpoint, a SIEM, or your own collector). Delivery is
  best-effort from a bounded in-memory buffer; if the endpoint stalls, events
  are dropped rather than queued unboundedly. Watch
  `gameplane_audit_webhook_events_total{result="sent|failed|dropped"}` on
  `/metrics` to confirm the mirror is healthy.
- `api.audit.webhook.authSecretRef` — optional `Authorization` header for the
  webhook, sourced from a Secret (never a flag — see [security](security.md)).
- `api.audit.webhook.syslogBridge.enabled` — deploy the bundled
  [audit-syslog-bridge [optional]](../audit-syslog-bridge/README.md) and point the webhook
  at it automatically, so events are forwarded to a **syslog** collector. Set
  `syslogBridge.syslog.addr` to your collector `host:port` (required when
  enabled), and optionally `network` (`tcp`/`udp`), `tls`, `facility`, and
  `severity`. Setting `webhook.url` explicitly overrides the auto-wiring.

  ```sh
  helm upgrade ... \
    --set api.audit.webhook.syslogBridge.enabled=true \
    --set api.audit.webhook.syslogBridge.syslog.addr=syslog.example:514
  ```

- `api.audit.s3.*` — native S3-compatible sink for batching audit events as
  NDJSON objects. Events are buffered in memory and flushed when ANY of three
  thresholds are hit: 100 events, 1 MiB, or 5 seconds. Upload uses S3 `PutObject`
  with retries (immediate/+2s/+8s); watch `gameplane_audit_s3_events_total`
  for delivery health. Works with AWS S3, MinIO, Backblaze, Wasabi, or any
  S3-compatible endpoint.
  - `api.audit.s3.endpoint` — S3 endpoint `host:port` (e.g.,
    `minio:9000` for a local MinIO, `s3.amazonaws.com` for AWS).
  - `api.audit.s3.bucket` — bucket name (required when endpoint is set).
  - `api.audit.s3.prefix` — optional object key prefix (e.g.,
    `gameplane-audit`; empty = root).
  - `api.audit.s3.region` — S3 region (e.g., `us-east-1`; empty defaults to
    `us-east-1`).
  - `api.audit.s3.insecure` — `true` to use plain HTTP instead of HTTPS (no TLS at all; for local S3-compatible endpoints on dev/homelab clusters).
  - `api.audit.s3.credentialsSecretRef` — reference to a Secret holding S3
    credentials (see [security](security.md)); leave `name` empty to disable S3.

  **MinIO homelab example**:

  ```sh
  # Create a Secret with MinIO credentials (user must have read/write on the bucket).
  kubectl create secret generic gameplane-s3-creds \
    -n gameplane-system \
    --from-literal=access-key=minioadmin \
    --from-literal=secret-key=minioadmin

  # Enable S3 sink pointing at local MinIO.
  helm upgrade ... \
    --set api.audit.s3.endpoint="minio.gameplane-system:9000" \
    --set api.audit.s3.bucket="gameplane-audit" \
    --set api.audit.s3.prefix="events" \
    --set api.audit.s3.insecure=true \
    --set api.audit.s3.credentialsSecretRef.name=gameplane-s3-creds \
    --set api.audit.s3.credentialsSecretRef.accessKeyKey=access-key \
    --set api.audit.s3.credentialsSecretRef.secretKeyKey=secret-key
  ```

### Telemetry

Gameplane can send two tiers of usage reports, about once a day. Both are
described below, so you can decide before you install.

**What is sent**

- **Basic** — exactly three fields: `version` (the Gameplane version),
  `servers` (how many GameServers exist) and `templates` (how many
  GameTemplates exist). No names, namespaces, hostnames, or addresses.
- **Extended** — the basic fields plus the following, and nothing else:
  - a random **install ID** (a UUID generated on your install; it is not
    derived from your cluster, host, network, or users)
  - environment: Kubernetes minor version (for example `1.31`), a
    distribution category (`k3s`, `rke2`, `k0s`, `eks`, `gke`, `aks`,
    `openshift`, `microk8s`, `minikube`, `kind`, `doks`, `talos` or
    `other`), node CPU architectures, and a node-count band (`1`, `2-3`,
    `4-10`, `11-50`, `51+`)
  - game usage: a server count for each module from the official catalog,
    plus one combined `custom` count for every other module (custom
    module names are never sent)
  - feature adoption: wake-on-connect (yes/no), relay tunnel types in use
    (`frp`, `tailscale`, `playit`), packet capture enabled (yes/no),
    backups configured on any server (yes/no), single sign-on (yes/no),
    audit forwarding (yes/no), a registered-cluster band (`1`, `2-3`,
    `4-10`, `11+`), the database kind (`sqlite` or `postgres`) and the UI
    language
  - the install's public signing key and the time the report was sent

Every extended value comes from a fixed set of categories or bands, or is
a number, the install ID, the public key or the send time. A value that
doesn't fit is sent as `other`. Extended reports are signed with a key
derived from a random secret that never leaves your install combined with
the install ID, so nobody can report under your ID with a different key.
Signing proves which key sent a report; it does not prove the install is
real. The install ID is pseudonymous: it lets the receiver tell that two
reports came from the same install, and it is deleted when you turn
extended off. See [Security](security.md#telemetry) for the threat model.

**Defaults**

| Install | Basic | Extended |
|---|---|---|
| New install with a destination in effect | on | on |
| Existing install that saved a basic choice | keeps that choice | off |
| Existing install that never saved a choice | off | off |

On a new install, the first admin to sign in sees a notice that lists both
tiers and names the destination. No report is sent until an admin has seen
the notice. After that, **Admin Settings → Telemetry** shows the
destination, a preview of the exact next report (including the install
ID), the last attempt, separate basic and extended toggles, and an
install-ID reset. Turning basic off turns extended off with it. The
operator's install-time `api.telemetry.enabled=false` always wins over the
toggles.

The project's data-handling statement, including how long the project
keeps data (daily aggregates for 24 months; per-install activity records
expire after 90 days without a report) is at
<https://gameplane.net/telemetry/>.

> **Upgrading?** Installs that already had **Send anonymous usage
> metrics** switched on, while no destination was configured, saved that
> choice but it had no effect. After upgrading to the release that adds
> the project's default receiver (`telemetry.gameplane.net`), those installs
> start sending **basic** reports to it. Extended stays off until an admin turns it on. If you do
> not want that, set `api.telemetry.enabled=false` before you upgrade, or
> turn the toggle off in Admin Settings. Installs that already pointed at
> a custom `api.telemetry.endpoint` or the bundled receiver keep sending
> only there, never to the project's default receiver. Installs that
> never saved a choice stay off.

**Where reports go, and how to change it**

- `api.telemetry.enabled` — set to `false` for a hard off: the API never
  sends telemetry, whatever the admin toggles say, and no first-login
  notice is shown. Use this for air-gapped and privacy-sensitive
  clusters.

  ```sh
  helm upgrade ... --set api.telemetry.enabled=false
  ```

- `api.telemetry.endpoint` — when empty (the default), reports go to the
  project's default receiver, `https://telemetry.gameplane.net/ingest`. Set a
  URL to send them to your own receiver instead (e.g.
  `https://telemetry.example.com/ingest`); it replaces the default and never
  also sends to it. An install whose endpoint was already
  set keeps working unchanged.
- `api.telemetry.receiver.enabled` — deploy the bundled
  [telemetry-receiver](../telemetry-receiver/README.md) [optional] next to
  the API and point the API at it automatically (when `endpoint` is
  empty). The bundled receiver stores daily aggregates in a PVC
  (`api.telemetry.receiver.persistence.enabled`, default `true`, 1Gi; the
  chart runs a single replica while persistence is on). It serves
  aggregate figures on a private dashboard and, optionally, a public
  summary; see the next two options. To run a receiver for other
  installs, see the [provider runbook](telemetry-provider.md).
- `api.telemetry.receiver.dashboard.tokenSecretRef` — name a Secret (key
  `token` by default) holding the dashboard token. With a token set, the
  receiver starts a dashboard listener on port `8081` that you reach by
  `kubectl port-forward` (or from peers listed in
  `api.telemetry.receiver.dashboard.ingressFrom`). Without a token there
  is no dashboard. The same token (as a Bearer token) protects the
  receiver's `/metrics`, which moved to this port.
- `api.telemetry.receiver.publicSummary.enabled` — set `true` to enable
  `GET /v1/summary`, a public, unauthenticated endpoint that returns only
  five headline counts. Off by default.
- `api.telemetry.receiver.retentionDays` (default `730`, minimum `365`)
  and `api.telemetry.receiver.activityExpiryDays` (default `90`, minimum
  `31`) — how long the receiver keeps daily aggregates and per-install
  activity records. Lower values fail rendering.
- `api.telemetry.authSecretRef` — optional shared ingest token, sourced
  from a Secret. The API sends it verbatim as the `Authorization` header
  and the bundled receiver requires it — recommended when the receiver is
  enabled, since its Service is reachable by other in-cluster pods.

  ```sh
  kubectl -n gameplane-system create secret generic telemetry-ingest \
    --from-literal=token='Bearer some-long-random-string'
  kubectl -n gameplane-system create secret generic telemetry-dashboard \
    --from-literal=token="$(openssl rand -base64 32)"
  helm upgrade ... \
    --set api.telemetry.receiver.enabled=true \
    --set api.telemetry.authSecretRef.name=telemetry-ingest \
    --set api.telemetry.receiver.dashboard.tokenSecretRef.name=telemetry-dashboard
  kubectl -n gameplane-system port-forward svc/gameplane-telemetry-receiver 8081:8081
  ```

  Then open `http://localhost:8081` and sign in with the dashboard token.
  The receiver's session cookie is `Secure`, so use a browser that treats
  `localhost` as a secure origin or put TLS in front.

## Installing a module

The chart ships two `ModuleSource`s: `default` (pulls pre-built bundles from the
official registry) and `uploads` (dashboard bundle uploads). The default uses
`type: oci` for zero-configuration access to versioned, optionally signed bundles;
`type: git` is available to track an unreleased branch directly from the
`GameplanePanel/module` repository. Install games from the dashboard's **Modules** page,
or add more sources — git repositories, http archives, a local directory — under
**Modules → Manage sources** (requires `modules:manage` on that cluster) or by applying `ModuleSource` CRs. Standalone panels require a selected remote cluster for these operations. See
`docs/module-authoring.md` for the source types and the bundle format.

## Registering an additional cluster

Gameplane can manage game servers across multiple Kubernetes clusters
through a federation model. Each target cluster runs its own operator
instance; the central API dispatches requests to the
target cluster via a `?cluster=<name>` parameter. See
[architecture.md](architecture.md#multi-cluster-federation) for the
design details.

### Prerequisites

Before registering a target cluster, ensure it has:

- Kubernetes 1.28+
- Gameplane operator and agent images accessible (same registry as the control-plane)
- A valid kubeconfig scoped to the intended Gameplane operations and namespaces

### Pod logs and PTY console permissions

The central API must reach the target Kubernetes API (including streaming/SPDY
upgrades). Remote Pod logs and PTY attach need no cross-cluster Pod networking or
agent mTLS. In each allowed game namespace the registered kubeconfig needs:

| API group | Resources | Verbs | Purpose |
|---|---|---|---|
| `gameplane.local` | `gameservers` | `get` | Resolve the selected server |
| `apps` | `statefulsets` | `get` | Verify its controller ownership |
| core | `pods` | `get` | Verify ownership and startup status |
| core | `pods/log` | `get` | Stream init and game container logs |
| core | `pods/attach` | `create` | Interactive PTY input/output over SPDY |

These are the minimum **streaming** permissions, in addition to any CRD management
permissions used by other dashboard operations. Attach is write-capable and
requires Gameplane's `servers:console` permission; logs use `servers:read`. Bind
Kubernetes permissions only in the intended game namespaces. The chart adds the
StatefulSet read permission for the local API; existing custom remote credentials
must be updated too.

### Optional remote agent access

Add a [private gateway in the target cluster](gateway-install.md) and
[register its endpoint and credentials](multicluster-agent-gateway.md) to enable
supported RCON, game-file logs, file/player operations, module actions, live
status and agent-based mods. This requires the updated operator and UID-aware
agents; old agents fail closed on the versioned protocol. The gateway has no
separate user database, and the central API still needs direct access to the
target Kubernetes API. Existing local installations retain direct agent access.

Remote capture downloads and cleanup require an upgraded gateway and capture
sidecar with persisted GameServer and NetworkCapture identity. Historical files
without those bindings are unavailable remotely. Modpack and ID-list configuration
use the selected Kubernetes client and template; provider credentials stay central.
The existing `Cluster` health status reports Kubernetes connectivity, not gateway
readiness or complete interactive feature coverage.

### Path 1: kubectl apply

This path applies to a combined installation with a central Kubernetes cluster.
A standalone panel uses the dashboard or API in Path 2 and stores registrations
in its database.

1. Create a `kubeconfig` Secret in the control-plane's `gameplane-system` namespace.
   The Secret **must** be labelled `gameplane.local/cluster-kubeconfig=true`:

   ```yaml
   apiVersion: v1
   kind: Secret
   metadata:
     name: my-cluster-kubeconfig
     namespace: gameplane-system
     labels:
       gameplane.local/cluster-kubeconfig: "true"
   type: Opaque
   data:
     kubeconfig: <base64-encoded kubeconfig for the target cluster>
   ```

   To base64-encode your kubeconfig:

   ```sh
   cat /path/to/target-cluster-kubeconfig.yaml | base64 -w0
   ```

2. Create the `Cluster` CRD referencing the Secret:

   ```yaml
   apiVersion: gameplane.local/v1alpha1
   kind: Cluster
   metadata:
     name: my-cluster
   spec:
     displayName: My Cluster
     kubeconfigSecret:
       name: my-cluster-kubeconfig
       # key is optional; defaults to "kubeconfig" if omitted
       key: kubeconfig
   ```

   **Cluster spec fields:**
   - `displayName` (optional): Human-readable name shown in the dashboard
   - `kubeconfigSecret.name` (required): Name of the Secret containing the kubeconfig
   - `kubeconfigSecret.key` (optional): Data key within the Secret; defaults to `"kubeconfig"`
   - `agentGateway` (optional): HTTPS gateway and labeled TLS Secret reference for [remote agent access](multicluster-agent-gateway.md)

3. Apply both to the control-plane cluster:

   ```sh
   kubectl apply -f secret.yaml -f cluster.yaml
   ```

4. Verify the cluster status:

   ```sh
   kubectl get clusters
   kubectl describe cluster my-cluster
   ```

The operator on the control-plane will reconcile the `Cluster` and
update `status.phase` (Unknown → Healthy/Unhealthy). When `Healthy`,
the API can dispatch requests to that cluster.

Removing a cluster registered this way (from the dashboard or with
`DELETE /clusters/{name}`) deletes the `Cluster` but leaves your Secret
in place. Delete the Secret with kubectl when you no longer need it.

### Path 2: Dashboard API

In either installation mode, use **Clusters → Register cluster**, or POST to
`/clusters` with `cluster:manage`. Supply a self-contained kubeconfig as a raw
YAML string in JSON. The following example uses `jq` to encode it correctly.
`PANEL_URL` is the HTTPS dashboard origin, `cookies.txt` holds your authenticated
`gameplane_session` and `gameplane_csrf` cookies, and `CSRF_TOKEN` is the latter
cookie's value:

```sh
jq -n --rawfile kubeconfig remote-kubeconfig.yaml \
  '{name:"my-cluster",kubeconfig:$kubeconfig}' |
  curl --fail-with-body --cookie cookies.txt \
    --header "Content-Type: application/json" \
    --header "X-Gameplane-CSRF: $CSRF_TOKEN" \
    --data-binary @- "$PANEL_URL/clusters"
```

Combined installations store the kubeconfig in a labelled Secret and create a
`Cluster` resource. Standalone panels store the registration and encrypted
credential in SQL. The API never returns or logs the kubeconfig. Removing an
API-created registration also removes its managed credentials; it leaves the
remote workloads running. Keep cookie jars and kubeconfig files private.

### Helm CRD caveat

This section applies to combined and remote workload installations. A standalone
central Helm release skips CRDs and their apply hook; follow its
[panel profile](standalone-panel.md#central-panel-on-kubernetes).

**`helm upgrade` never updates CRDs.** That is a documented Helm limitation,
not a Gameplane one: files under a chart's `crds/` directory are installed on
first install and ignored on every upgrade thereafter.

The chart works around this for you. `crds.autoApply` (enabled by default)
ships a **pre-install/pre-upgrade hook** that runs `kubectl apply
--server-side` over the current CRDs on every `helm upgrade`, and also on a
`helm install` that lands on top of CRDs an earlier, uninstalled release left
behind, so the `Cluster` CRD — and every other Gameplane CRD — stays in step
with the chart automatically. **No manual `kubectl apply` step is needed.**

You only need to apply CRDs by hand if you have deliberately disabled the
hook:

```sh
# only when crds.autoApply.enabled=false
kubectl apply --server-side -f charts/gameplane/crds/
```

The hook fires on pre-upgrade always. On `helm install` it fires only when
the cluster already holds Gameplane CRDs from a different chart version, i.e.
ones an earlier, uninstalled release left behind (Helm's `crds/` install
silently skips existing CRDs). It tells those apart by content: `make
manifests` stamps every chart CRD with a `gameplane.local/crd-bundle-sha256`
annotation, a hash over the whole CRD set, and the hook compares the live
`gameservers.gameplane.local` CRD's stamp with the chart's. On a genuinely
fresh cluster, Helm's `crds/` step creates the CRDs, carrying this chart's
stamp, before the hook is evaluated, so the stamps match and the hook does
not run. A fresh install therefore never depends on pulling the hook's
`kubectl` image; an install over leftover CRDs does, so mirror
`crds.autoApply.image` if you reinstall on an air-gapped cluster. CRDs are
never owned or deleted by Helm here, so `helm uninstall` leaves your
GameServers intact.

**Helm 4** installs `crds/` with a server-side apply under the field manager
`helm` instead of skipping existing CRDs, so a `helm install` over leftover
CRDs updates them itself (stamp included) and the hook stays pre-upgrade
only. The hook applies under the same `helm` field manager so that apply
never conflicts with it. Releases up to and including `0.2.0-beta.8` applied under <!-- doc-versions: historical -->
kubectl's default manager (`kubectl`); if CRDs such a release upgraded were
left behind, a Helm 4 `helm install` stops with `conflict with "kubectl" …
.spec.versions`. Re-run it with `--force-conflicts` to take them over:

```sh
helm install gameplane charts/gameplane -n gameplane-system --create-namespace --force-conflicts
```

### RBAC and permissions

Registering a cluster grants no dashboard user access to its workloads. The
central API stores user roles and cluster/namespace grants in its database.
In **Users & RBAC**, create a custom workload role, then add a grant for the
intended user and remote cluster. Use **All namespaces** for inventory, module,
and template permissions, or a specific game namespace for namespaced access.
Changing a user's grants ends their sessions; they must sign in again.

Remote roles cannot contain central administration permissions or the built-in
admin wildcard. Keep the user's primary panel role when adding remote grants.
The [standalone guide](standalone-panel.md#grant-workload-access) lists the
permissions and dashboard steps.

Separately, the registered kubeconfig needs Kubernetes RBAC for the operations
the API performs on that target. Kubernetes RoleBindings grant access to that
credential's identity; they do not create Gameplane user grants.

## Upgrading

These Helm commands upgrade a combined installation. For Compose and panel-only
Helm releases, use [standalone upgrades](standalone-panel.md#upgrading).

```sh
helm upgrade gameplane oci://ghcr.io/gameplanepanel/charts/gameplane \
  --version <new-version> \
  --namespace gameplane-system \
  --reuse-values
```

CRDs are brought up to date automatically by the chart's pre-upgrade hook —
see [Helm CRD caveat](#helm-crd-caveat).

**A caution on `--reuse-values`:** it replays your previous values *and skips
the new chart's defaults*, so any value key introduced since your last upgrade
arrives unset rather than defaulted. If an upgrade fails with a nil/missing
value, re-run it with `--reset-then-reuse-values`, or pass your values file
explicitly with `-f`. Keeping a values file under version control and using
`-f` is the sturdier habit.

### What upgrades are tested

CI runs an end-to-end upgrade on every PR (`e2e upgrade`, on both amd64 and
arm64): it installs the **previous release's published chart and published
GHCR images** into a fresh cluster, seeds a running GameServer with data on
its PVC and an admin user in the database, then upgrades to the chart under
test and asserts that

- the CRD schema was actually updated by the pre-upgrade hook,
- the GameServer survives and the bytes on its volume are unchanged,
- the pre-upgrade admin can still log in — i.e. the new binary's database
  migrations ran against the populated SQLite volume without data loss.

See `test/e2e/upgrade_e2e_test.go`. What this does **not** yet cover: upgrades
that skip several releases at once, and Postgres (still an experimental
driver — see [`roadmap.md`](roadmap.md)). Take a backup before upgrading
production either way.

### SQLite database adoption (Kestrel → Gameplane)

Installations that predate the Kestrel → Gameplane rename (v0.2.0-beta.2, June 2026) <!-- doc-versions: historical -->
and use the SQLite database driver will have their legacy `kestrel.db` file
automatically adopted on the first start of the new API. The adoption is
one-time and atomic: the file is renamed to `gameplane.db` in place, and a
WARN-level log entry records the event. If a `gameplane.db` already exists
(e.g., if this is not a fresh upgrade), the legacy file is left untouched
and the existing database is used instead — no data loss.

Nothing else needs to happen; the upgrade proceeds normally.

### SQLite upgrades (brief downtime)

When using the SQLite database driver, the API Deployment uses a `Recreate`
upgrade strategy: the old pod is fully terminated before the new one starts.
This ensures no two API processes try to write the same SQLite database file
(which is a single-writer store on a ReadWriteOnce PVC). As a result,
SQLite-backed installs experience a few seconds of dashboard downtime during
an upgrade. Combined PostgreSQL installs use rolling updates, but the driver
remains experimental and the API should still run as a single replica (see
`api.replicas` in `values.yaml`). Standalone installs use `Recreate` with either
database driver because the API also mounts its persistent encryption-key volume.
