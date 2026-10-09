# Standalone panel

Run the dashboard and API on a host that has no Kubernetes installation. Remote
game clusters run their own operator and game server agents. The central host is
never registered as a `local` cluster and cannot host games itself.

The default Helm install remains a combined panel and operator installation.
Standalone mode is an explicit choice for a fresh central panel; this guide
does not migrate an existing installation's Kubernetes Secrets or Cluster CRs
into SQL storage.

## Docker Compose

Install Git and Docker with the Compose plugin. Use a release or commit that
contains standalone support; replace `<ref>` below with that tag or commit.
The Compose file builds the API and web images from the same checkout. Run the
commands from the repository root:

```sh
git clone https://github.com/GameplanePanel/Gameplane.git
cd Gameplane
git checkout <ref>
docker compose -f deploy/standalone/compose.yaml up -d --build
docker compose -f deploy/standalone/compose.yaml logs gameplane-api
```

Only two services run: `gameplane-api` and `web`. They need no host Kubernetes
installation or Docker socket mount. The API is reachable only through
the Compose network; nginx forwards requests to `http://gameplane-api:8000`.
The dashboard is bound to `127.0.0.1:8080` on the host. Set `GAMEPLANE_PORT` to
change that port. Use `http://localhost:8080` locally; for remote access, put an
HTTPS reverse proxy in front of the loopback port and forward WebSocket upgrades
and streaming responses. Login cookies require a secure browser context; serving
the dashboard on an ordinary remote HTTP origin will not work.

Create the first administrator using the same database volume. This Bash example
reads the password without including it in shell history or process arguments:

```sh
read -r -s -p 'Admin password: ' PANEL_PASSWORD
printf '\n'
printf '%s\n' "$PANEL_PASSWORD" | docker compose -f deploy/standalone/compose.yaml \
  exec -T gameplane-api /api bootstrap-admin --username admin --password-stdin
unset PANEL_PASSWORD
```

Use a password of at least 12 characters. Sign in at the dashboard. A new panel
has no clusters; register a remote cluster before using game-server operations.
Authentication, users, roles, settings, and registration remain available before
any remote cluster exists. Administrators can use **Register cluster** on the
Clusters page or the registration API below. The cluster picker selects already
registered clusters. Gateway credentials are configured through the API.

## Storage and recovery

The Compose volume `panel-data` contains `/data/gameplane.db`; a separate
`panel-keys` volume contains `/keys/panel.key`. Standalone settings and remote
credentials are stored in SQL; credential values are authenticated-encrypted
using the persistent key. Back up the database and its WAL files after stopping
the services. Back up the key separately, with restricted access and encrypted
backup storage. Both are needed for recovery, but a database backup should not
automatically include the decryption key. Restore generated volumes with
ownership `65532:65532`. Normal `docker compose down` retains both volumes;
`down --volumes` deletes them.

With the supplied Compose project name, Docker names these volumes
`gameplane-panel_panel-data` and `gameplane-panel_panel-keys`. Inspect both before
backing them up, especially if you changed the Compose project name.

The API image seeds a new named volume with a directory writable by its non-root
runtime user. If you replace it with a host bind mount, create that directory with
ownership `65532:65532` yourself. Generated key files use mode `0600`. Restored
keys must be regular files containing exactly 32 raw bytes, owned by the API user
or root, with no access for other users and no group write/execute permissions.
Group read is accepted only for a group the API belongs to, which permits
read-only Kubernetes Secret projections. Extended file access ACLs are rejected
because they can grant extra readers despite safe-looking mode bits. On Linux,
inspect restored keys with `getfacl` and remove extra file grants with
`setfacl -b` before applying the required ownership/mode; key directories should
not have inherited default ACL grants. Key permissions are validated at
startup; creating a replacement key cannot decrypt existing credentials.

For custom deployments, pass `--standalone` or `GAMEPLANE_STANDALONE=true` to the
API. `--panel-key-file` / `GAMEPLANE_PANEL_KEY_FILE` selects the key path (default
`/data/panel.key` for compatibility with earlier custom deployments; the supplied
Compose/Helm profiles use `/keys/panel.key`). A persistent key file is required
even when using the experimental PostgreSQL build. Run one API replica. Run
standalone key storage in Linux containers on a filesystem supporting POSIX ACL
inspection. Native non-Linux key access and uninspectable ACLs fail closed.

### Provision a key outside the database storage

Set `--panel-key-provisioned` / `GAMEPLANE_PANEL_KEY_PROVISIONED=true` to require
an existing key at startup. This mode never generates a missing key. Provision
32 cryptographically random bytes through your secret manager or a protected
file, then mount it read-only. For a Linux Docker host:

```sh
sudo install -d -m 0700 /etc/gameplane/keys
sudo sh -c 'set -C; umask 077; openssl rand 32 > /etc/gameplane/keys/panel.key'
sudo chown 65532:65532 /etc/gameplane/keys/panel.key
export GAMEPLANE_PANEL_KEY_SOURCE_FILE=/etc/gameplane/keys/panel.key
docker compose -f deploy/standalone/compose.yaml \
  -f deploy/standalone/compose.provisioned-key.yaml up -d --build
```

Use the same two Compose files for subsequent commands. File-backed Compose
secrets preserve the host file's ownership and permissions; configure those
before starting the API. Keep this file outside database backup jobs. The API
does not upload keys to a service or put their values in environment variables.
A separately protected key helps with database/backup disclosure; compromise of
the running API or a host that can read both files still exposes credentials.
See [OWASP's key-storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cryptographic_Storage_Cheat_Sheet.html#key-storage).

### Move an existing key to separate storage

For installations created before separate key volumes, stop the API and retain
a protected backup of the original `/data/panel.key`. Copy that exact file into
the new key volume as `/keys/panel.key`, preserving ownership and mode `0600`, or
provision it through the read-only file option above. Start using the new path
and verify existing registrations load before removing the old copy from the
database volume and excluding it from future database backups. Do not generate
a new key as a migration step; it cannot decrypt existing records.

### Rotate the master encryption key

Stop every API process that uses this database, then take a database backup and
a separately protected backup of its current key. The offline command accepts
file paths and never prints key material. For generated keys in the Compose
volume, use a new filename:

```sh
docker compose -f deploy/standalone/compose.yaml stop gameplane-api
docker compose -f deploy/standalone/compose.yaml run --rm --no-deps \
  --entrypoint /api gameplane-api rotate-panel-key \
  --old-key-file /keys/panel.key --new-key-file /keys/panel-2026-10.key
export GAMEPLANE_PANEL_KEY_FILE=/keys/panel-2026-10.key
docker compose -f deploy/standalone/compose.yaml up -d --no-build gameplane-api
```

Persist the selected path in your Compose environment configuration before
future restarts. Check login, cluster health and credential-backed operations.
Back up the new key separately. The command retains the old file because older
database backups still require it; retire that key only after its matching
backups have expired under your retention policy.

The new generated key is durably published before one database transaction
re-encrypts all management credentials and activates its identity. A failure
before commit leaves the old database state usable. Keep both files if the
command is interrupted or its result is uncertain, and retry the same old/new
pair; a completed immediately preceding rotation is authenticated and accepted.
Do not create another new key or restore only one half of a database/key backup.
Stale API processes cannot write credentials under the retired key, but stopping
them first is still required for a clean maintenance window.

For a secret manager or read-only Kubernetes Secret, durably provision a new
32-byte key first and mount the old and new keys at separate paths in the offline
maintenance container. Add `--new-key-provisioned` to the command; a missing new
key fails without creating a replacement. Use the same database configuration
as the API (`GAMEPLANE_DB_DRIVER` and `GAMEPLANE_DB_DSN`). After success, update the
API's mounted key/path and restart it. For Helm Secret projections, switch
`api.panelKey.existingSecret` or its `secretKey` entry to the new key while keeping
the old Secret protected for retained backups. Changing the mounted key without
running the rotation command cannot decrypt existing records.

For a generated Helm key PVC, scale the API Deployment to zero before running a
maintenance Job with the same database and key volumes. Rotate into a new file
under `/keys`, then set `api.panelKey.fileName` to that filename in your Helm
values and upgrade the release to restart its single API replica. The chart
keeps the selected filename across future upgrades. Preserve the old key file
for retained backups.

## Central panel on Kubernetes

The panel can also run in a Kubernetes cluster without using that cluster for
games or granting the API Kubernetes access. From this checkout:

```sh
helm upgrade --install gameplane charts/gameplane \
  --namespace gameplane-system --create-namespace --skip-crds \
  --values charts/gameplane/examples/standalone-panel-values.yaml
```

Use images built from the matching version (set `image.registry` and `image.tag`
for your build). The example enables `api.standalone`, disables `operator.enabled`
and the CRD upgrade hook, and disables ingress until you configure your hostname
and TLS. Expose the web Service through your HTTPS ingress or use a local port
forward. Bootstrap the admin with `/api bootstrap-admin` in the API pod as in the
[combined installation guide](install.md#first-time-setup).

Keep `--skip-crds` on installation commands. Helm installs files in `crds/`
before evaluating templates; setting values alone cannot suppress those CRDs.
Standalone mode skips the CRD apply hook automatically. It creates no operator,
agent certificates, game namespace, module source, game network policies,
operator monitors, or API Kubernetes RBAC. Its API pod disables service account
token mounting. Separate PVCs hold the database and generated panel key. Do not switch an
existing combined release to these values as a migration procedure.

For an externally managed key, set `api.panelKey.existingSecret` to a Secret in
the release namespace and `api.panelKey.secretKey` to its 32-byte data entry
(default `panel.key`). The chart mounts it read-only at `/keys/panel.key` with
mode `0440` and enables provisioned-key mode. No generated key PVC is created.
For generated keys, `api.panelKey.storage.existingClaim` selects an existing
key-only PVC; `size` and `storageClassName` configure a newly created one.
Apply separate RBAC, encryption-at-rest, and backup policies to the key Secret
or PVC. Do not put key bytes in Helm values, command arguments, or source control.
The standalone pod uses `fsGroupChangePolicy: OnRootMismatch` so normal generated
key PVC remounts do not widen mode `0600` to group-writable `0660`. Start generated
key storage with an empty PVC. When restoring or migrating an existing PVC, first
prepare its mount root with group `65532`, mode `2770` (including setgid), and restore the key as `65532:65532`,
mode `0600`. Storage drivers that independently rewrite file permissions must
preserve this protection; otherwise use the read-only Secret option. The API
rejects a group-writable key instead of silently changing its permissions.

## Install and register remote game clusters

Each target still needs Kubernetes, Gameplane CRDs, an operator, and storage for
games. From the same checkout, install the chart using credentials for the remote
cluster. Use matching component images accessible to that cluster; replace
`<image-registry>` and `<matching-tag>` with the registry and tag for your build:

```sh
helm --kubeconfig remote-admin.yaml upgrade --install gameplane charts/gameplane \
  --namespace gameplane-system --create-namespace \
  --set api.enabled=false --set api.standalone=false --set operator.enabled=true \
  --set image.registry=<image-registry> --set image.tag=<matching-tag>
```

The remote install needs its CRDs, so keep `--skip-crds` off this command. The
operator creates agents alongside game servers. Enable the optional
[private gateway](gateway-install.md) for agent operations such as console,
files, and player management. Without a gateway, Kubernetes resource operations
remain available, but agent-backed operations are unavailable.

The central API must reach each target's Kubernetes API directly. The gateway
does not tunnel Kubernetes traffic. Use a kubeconfig containing credentials and
CA data that are usable from the central host. Grant the credential's identity
Kubernetes access to the required Gameplane resources, plus the
[streaming](install.md#pod-logs-and-pty-console-permissions) and
[inventory](gateway-install.md#remote-inventory-permissions) permissions for
features you use. Module management needs access to the cluster-scoped
`modules`, `modulesources`, and `gametemplates` resources; bundle uploads also
need ConfigMap access in the operator namespace. Do not use kubeconfigs
whose credentials require local files or interactive credential plugins. Use
the same operator namespace on the targets as the API's `--namespace` setting
(default `gameplane-system`).

Standalone kubeconfigs must use HTTPS with certificate verification. HTTP,
`insecure-skip-tls-verify`, custom kubeconfig proxies, and redirects are rejected;
environment forward proxies are not used for workload connections. Use embedded
CA data for a private CA. Kubernetes API and gateway connections validate every
resolved address at dial time and reject loopback, link-local, cloud metadata,
unspecified, multicast, and address-translation bypasses. Private workload
addresses remain supported.

For tighter network isolation, set `GAMEPLANE_REMOTE_ALLOWED_CIDRS` (or
`--remote-allowed-cidrs`) to comma-separated destination networks, such as
`10.20.0.0/16,10.30.0.0/16`. The Helm equivalent is
`api.remoteAllowedCIDRs: ["10.20.0.0/16", "10.30.0.0/16"]`. This applies to
both Kubernetes API and gateway destinations and cannot override blocked address
classes. DNS names must resolve entirely within the allowed ranges. Restart the
API after changing the policy. Add host or network firewall rules where port-level
or additional service isolation is needed.

Register the target with an authenticated administrator session. In the examples
below, `cookies.txt` is a private cookie jar for that session and `CSRF_TOKEN`
is its `gameplane_csrf` cookie value. `PANEL_URL` is the HTTPS dashboard origin.
The API expects the kubeconfig's YAML text as the JSON string:

```sh
jq -n --rawfile kubeconfig remote-kubeconfig.yaml \
  '{name:"remote-1",displayName:"Remote games",kubeconfig:$kubeconfig}' |
  curl --fail-with-body --cookie cookies.txt \
    --header "X-Gameplane-CSRF: $CSRF_TOKEN" \
    --header 'Content-Type: application/json' \
    --data-binary @- "$PANEL_URL/clusters"
```

The standalone panel saves the registration and credentials in its database;
there is no central `Cluster` CR or Kubernetes Secret to create. It monitors
remote Kubernetes connectivity itself. Selecting that registration routes game
operations to the target, and never falls back to the central host.

Automatic remote observations have bounded memory: version responses are capped
at 64 KiB, other finite Kubernetes responses at 8 MiB after decompression, and
individual JSON watch frames at 1 MiB. User log and interactive streams remain
streaming. Interactive stdin/PTY uses the same destination policy, bounds its
TLS/upgrade handshake to 1 MiB, and caps its error-control stream at 64 KiB.
Standalone notifications use snapshots every five seconds with at
most 1,024 objects per resource type and only run when a notification sink is
enabled. Oversized or incomplete snapshots are ignored; transitions shorter than
the polling interval may be missed.

### Rotate a registered kubeconfig

Create a replacement credential on the remote cluster, then update its existing
registration. This requires central `cluster:manage` permission:

```sh
jq -n --rawfile kubeconfig replacement-kubeconfig.yaml '{kubeconfig:$kubeconfig}' |
  curl --fail-with-body --request PUT --cookie cookies.txt \
    --header "X-Gameplane-CSRF: $CSRF_TOKEN" \
    --header 'Content-Type: application/json' \
    --data-binary @- "$PANEL_URL/clusters/remote-1/kubeconfig"
```

A successful request returns 204, preserves the registration identity, gateway
settings and user grants, and reloads the workload client. The API checks the
configuration before storing it; success does not confirm connectivity or remote
RBAC. Verify the cluster is healthy and required operations work before revoking
the old remote credential. Requests already in flight can finish with the old
client. Overlapping rotations return a conflict; reload before retrying. Avoid
deleting and re-registering a cluster just to replace its kubeconfig.

## Grant workload access

Registration does not grant workload access, including to the bootstrap admin.
Its initial role administers the central panel. In **Users**, use the **Roles**
tab to create a custom role containing the target permissions you need:

- `cluster:read` for node inventory.
- `modules:read` and `modules:manage` for the catalog, sources, and module installation.
- `templates:read` and `templates:write` for game templates.
- The server, backup, and other namespace permissions needed for your workloads.

Open your user in the Users tab, then under **Cluster &
namespace grants**, select the remote cluster and that role. Choose a game
namespace for a namespace grant, or **All namespaces** for an allowed role that
should apply across that remote cluster, and click **Add**. Inventory, module,
and template permissions need an **All namespaces** grant on that target; a grant
limited to one game namespace does not grant those cluster-wide operations.
Adding a grant ends that user's sessions; sign in again to use it. Grant other
users access the same way. Do not remove the bootstrap user's primary role to
add remote access.

Remote grants cannot include central administration permissions or the built-in
admin wildcard. Use a custom workload role rather than assigning the built-in
admin role to all namespaces. Wait for the registration to connect before adding
a target grant; registration metadata can appear before its client is loaded.

On **Modules**, select the remote cluster before installing a module or opening
**Manage sources**. Module managers can edit sources there without central
`config:manage` permission. Under **Admin settings → Backup destinations**, select
the workload cluster before adding a repository. Those credentials remain in
that cluster's game namespace. Creating a destination also requires
`destinations:manage` on the target namespace; the panel's primary admin role
alone does not grant it. Switching clusters discards an open destination form.

## Configure remote agent access

For a configured remote gateway, store the central client's dedicated mTLS
credentials through the standalone registration API:

```sh
jq -n --rawfile caCert gateway-ca.crt \
  --rawfile clientCert central-client.crt --rawfile clientKey central-client.key \
  '{url:"https://remote-1-gateway.internal:8443",caCert:$caCert,clientCert:$clientCert,clientKey:$clientKey}' |
  curl --fail-with-body --request PUT --cookie cookies.txt \
    --header "X-Gameplane-CSRF: $CSRF_TOKEN" \
    --header 'Content-Type: application/json' \
    --data-binary @- "$PANEL_URL/clusters/remote-1/gateway"
```

The gateway's `clusterID` must be `remote-1`, and its `peerURI` must match the
client certificate URI SAN. Its server certificate must match the endpoint
hostname. Use a separate trust root from the target's local agent CA. Repeat PUT
to rotate credentials; DELETE on the same endpoint removes gateway access while
retaining the cluster registration. Credential responses do not return private
keys. See [gateway routing and trust](multicluster-agent-gateway.md) for the
network and certificate requirements; that document's central Kubernetes Secret
instructions apply to combined mode, while standalone uses the API above.

## Upgrading

Stop the Compose services and back up the database and key separately before upgrading.
From the repository root, check out the new release or commit, then rebuild both
panel images:

```sh
docker compose -f deploy/standalone/compose.yaml stop
# Back up gameplane-panel_panel-data and protect a separate panel-key backup.
git checkout <new-ref>
docker compose -f deploy/standalone/compose.yaml up -d --build
docker compose -f deploy/standalone/compose.yaml logs gameplane-api
```

Keep the same Compose project name, data volume, and selected key. The API applies database
migrations at startup; retain the pre-upgrade backup if you need to restore an
older version. Upgrade remote operators, gateways, and agents to matching
versions using their Helm values. Existing games continue running during a panel
restart; a remote operator upgrade may roll game pods.

For a panel-only Helm release, repeat the installation command with the updated
chart, matching images, `--skip-crds`, and your standalone values file. Keep
`api.replicas=1` and the existing key storage, including when using PostgreSQL.

## Troubleshooting

| Symptom | Check |
|---|---|
| Login succeeds but returns to the login page | Use HTTPS for remote access, or `localhost` locally. The session cookie is `Secure`. |
| API cannot write `/data` | Restore volume ownership to `65532:65532`. A host bind mount needs those permissions too. |
| API reports a missing key or credential authentication failure | Restore the original `panel.key` with its database. A new key cannot decrypt existing credentials. |
| Registered cluster is unhealthy | Check the Kubernetes API address from the panel's network, embedded credentials, CA data, and target RBAC. Registration polling runs every 30 seconds. |
| Cluster appears, but workload requests return 403 | Add the user's remote cluster/namespace grant, then sign in again. Also check the registered kubeconfig's Kubernetes permissions. |
| Inventory works, but files or RCON do not | Check gateway routing, its cluster ID, certificate hostname, and central client URI SAN. Kubernetes health does not test the gateway. |
| Module uploads fail | Check ConfigMap permissions and the matching operator namespace on the panel and target. |

Standalone system logs come from the container runtime. Use
`docker compose -f deploy/standalone/compose.yaml logs gameplane-api web` for
panel logs and the remote cluster's Kubernetes tools for operator and game logs.
