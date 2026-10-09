# Per-cluster gateway installation

Run one central Gameplane API/dashboard and an operator plus optional gateway in
each remote cluster. The gateway proxies approved agent operations over mTLS. The
central API still connects directly to each registered Kubernetes API for CRD
management, Pod logs and PTY attach; a gateway does not replace those credentials.
See [central registration and request routing](multicluster-agent-gateway.md) for
the matching central API configuration.

The central panel can run in Kubernetes or on a separate Docker host. Use the
[standalone setup guide](standalone-panel.md) when the panel has no local game
cluster. The remote operator/gateway installation below is the same in both cases.

## Prepare the remote cluster

Use matching API, operator and agent versions. The gateway uses the API image's
`gateway` subcommand, a dedicated ServiceAccount, and read-only `get` access to
GameServers, NetworkCaptures and Services in its configured namespaces. It has no database and no
user login. Users authenticate to the central API, which retains authorization.

Create these Secrets in the chart release namespace before enabling the gateway:

| Secret value | Required keys | Purpose |
|---|---|---|
| `gateway.serverTLSSecret` | `tls.crt`, `tls.key` | Gateway server identity, with DNS SAN matching the configured endpoint |
| `gateway.centralClientCASecret` | `ca.crt` | CA trusted to issue central API client certificates |
| `gateway.agentClientSecret` | `tls.crt`, `tls.key` | Existing local agent client credentials; defaults to `gameplane-agent-client` |
| `gateway.agentCASecret` | `ca.crt` | Existing local agent trust; defaults to `gameplane-agent-ca` |

The gateway additionally requires an exact `gateway.peerURI` URI SAN in the central
client certificate. Use a separate management trust root from the local agent CA.
The chart mounts only `ca.crt` from the agent CA Secret, never its signing key.
The existing chart continues provisioning the local agent CA/client Secrets when
the central API is disabled. The gateway reloads mounted server/trust material on
new TLS handshakes, rechecks central-peer trust on requests, and reloads agent
credentials for new upstream requests. Allow time for Kubernetes Secret volume
projection to update and coordinate client/server trust overlap before retiring
old certificates. Streams and transfers have no fixed total-duration cap by
default (`gateway.maxRequestDuration: 0s`); they still end at the central client
certificate's expiry or when the caller disconnects or the gateway shuts down.
A positive `maxRequestDuration` adds an optional total lifetime (for example
`1h`), capped by certificate expiry. Ordinary operations and Kubernetes identity
lookups remain bounded to 30 seconds; connection, TLS handshake and response
header timeouts also remain in place.

Trust or permission removal prevents new operations but does not immediately
revoke an existing stream. Use a gateway restart to terminate active sessions
sooner, or configure a maximum lifetime to bound this window. Reconnection checks
authorization and trust again. When upgrading with reused Helm values, change an
existing `gateway.maxRequestDuration: 5m` to `0s` to remove the old cutoff.

## Helm configuration for a fresh remote installation

Adapt the [example values](../charts/gameplane/examples/gateway-values.yaml):

```yaml
api:
  enabled: false
  standalone: false
operator:
  enabled: true
gateway:
  enabled: true
  clusterID: remote-1
  peerURI: spiffe://gameplane.example/central-api
  namespaces: [gameplane-games]
  serverTLSSecret: gameplane-gateway-server
  centralClientCASecret: gameplane-central-client-ca
  networkPolicy:
    peerCIDRs: [10.20.30.40/32]
    apiServerCIDRs: [10.96.0.1/32, 10.0.0.10/32]
```

`clusterID` must equal the registered cluster name in the central installation.
An empty namespace list selects only `gamesNamespace`; additional namespaces must
already exist. The chart creates a Role/RoleBinding in each configured namespace. When
`networkPolicies.enabled` is true, it also creates matching agent ingress rules.
When game network policies are disabled, no new game Pod ingress isolation is
introduced; any externally managed default-deny policies must allow gateway
traffic on TCP 8090 and TCP 9091 for capture files. It does not grant cluster-wide
gateway permissions. Enable `capture.enabled` in this remote chart when captures
are wanted; the gateway advertises this cluster's capture settings to the central
API. Upgrade the operator and capture-sidecar together so new capture files carry
their server and capture UID bindings.

With both `capture.enabled` and `networkPolicies.enabled`, the chart separately
allows TCP 9091 from this release's operator and enabled API pods in the system
namespace. The operator uses this port to start, stop and poll captures; the local
API uses it for files. Narrowing kubelet probe ports does not remove this allowance.
Externally managed policies must permit the same control traffic.

```sh
helm upgrade --install gameplane ./charts/gameplane \
  --namespace gameplane-system --create-namespace \
  --values gateway-values.yaml
```

`api.enabled: false` omits the API Deployment, Service, ServiceAccount/RBAC and
SQLite PVC, plus the dashboard, dashboard ingress, cluster-operations grants,
API ServiceMonitor and API-only audit/telemetry receivers. The operator, CRDs,
agent mTLS and operator/agent monitoring remain available. Default installations
retain `api.enabled: true` and `gateway.enabled: false`.

Use this profile for a fresh remote installation. For an existing API/database,
preserve its PVC and data before disabling the API. Current chart-created SQLite
PVCs carry `helm.sh/resource-policy: keep`; Helm retains those during removal.
A live Helm render also refuses to disable an older unannotated PVC. That lookup
cannot inspect the cluster during offline `helm template`/GitOps rendering, and
non-Helm pruning controllers may not honor Helm's retention annotation. Configure
the actual deployment controller's retention mechanism before changing an
existing installation. The profile does not migrate the database or its users.

## Remote inventory permissions

The central API reads node inventory and storage totals directly from the selected
cluster's Kubernetes API. Bind these additional read permissions to the identity
in that cluster's **registered kubeconfig**, alongside its existing game-resource
permissions. Remote capture management additionally requires `get`, `list`,
`create`, `patch` and `delete` on `networkcaptures.gameplane.local`, plus `update`
on its `status` subresource, in each managed game namespace. Add these separate
rules to the registered identity's namespace-scoped Role:

```yaml
- apiGroups: [gameplane.local]
  resources: [networkcaptures]
  verbs: [get, list, create, patch, delete]
- apiGroups: [gameplane.local]
  resources: [networkcaptures/status]
  verbs: [update]
```

The API initializes Pending through the status subresource after creating the CR.
Without its status permission, start can return 403 after the capture was created;
inspect the capture list before submitting another start request.
The gateway ServiceAccount keeps its namespace-scoped GameServer,
NetworkCapture and Service reads; it does not need the inventory grants or write
access to capture resources.

| API group | Resource or path | Verbs | Purpose |
|---|---|---|---|
| core | `nodes` | `list` | Node identities, readiness and capacity |
| core | `persistentvolumes` | `list` | Capacity of bound volumes for provisioned-storage totals |
| `metrics.k8s.io` | `nodes` | `list` | Optional current node CPU/memory usage |
| non-resource | `/version`, `/version/` | `get` | Target Kubernetes version; already needed for registration health |

For example, create this supplemental ClusterRole in the **remote cluster** and
bind it only to the ServiceAccount used by the registered kubeconfig:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: gameplane-central-inventory
rules:
  - apiGroups: [""]
    resources: [nodes, persistentvolumes]
    verbs: [list]
  # Optional: omit this rule when node usage metrics are not required.
  - apiGroups: [metrics.k8s.io]
    resources: [nodes]
    verbs: [list]
  - nonResourceURLs: [/version, /version/]
    verbs: [get]
```

Inventory does not require `watch`, namespace or StorageClass enumeration,
Secret access, node proxy access, or cluster-admin. `/namespaces` uses the central
API's configured game-namespace allowlist and the user's cluster/namespace grants;
it does not discover every namespace in the target cluster. Provisioned storage
is the sum of bound PV capacity, not measured disk usage.

Without metrics-server or its optional permission, node capacity remains visible
and current CPU/memory usage is unknown. A denied node/PV read is an inventory
error; it must not be interpreted as an empty healthy cluster or retried against
the central cluster. Remote inventory reports the selected cluster ID and its
Kubernetes version. The Gameplane version identifies the central API build.
Node-join token creation and kubeconfig issuance remain local-only operations and
are unavailable while viewing a remote cluster.

Kubernetes credentials and dashboard-user grants are separate. Once the remote
registration's client has loaded, create a custom role containing `cluster:read`
in **Users & RBAC → Roles**. Edit the intended user, choose that remote cluster,
select the reader role and **All namespaces**, then add the grant. This grants
inventory access only to that cluster; the user's local primary role and existing
game-namespace grants remain separate. Changing a user's grants revokes their
sessions, so that user must sign in again.

Remote all-namespace roles may contain `cluster:read`, `modules:read`,
`modules:manage`, `templates:read`, `templates:write`, and namespaced permissions.
Wildcard or central-administration permissions are rejected, and a
role cannot gain them later while it has remote all-namespace bindings.

## Private networking

The gateway Service is **ClusterIP on TCP 8443**. Provide private routing or a
private TLS-passthrough relay to reach it; this chart creates no public ingress,
NodePort or LoadBalancer. TLS must reach the gateway unchanged, because the
gateway verifies the central client's certificate itself.

A dedicated NetworkPolicy is installed whenever the gateway is enabled, including
when the chart's game network policies are disabled. It requires explicit peer
and API endpoint allowlists. It permits only:

- Incoming TCP 8443 from configured `peerCIDRs` or `peerSelectors`.
- DNS on TCP/UDP 53 to configured DNS pods (or exact `dnsCIDRs` for NodeLocal DNS).
- TCP 443/6443 to `apiServerCIDRs` in the local cluster.
- TCP 8090 to game agents in the explicitly allowed namespaces.
- TCP 9091 to capture sidecars in those same namespaces.

Use the source IPs the target cluster actually observes, accounting for private
relay/SNAT behavior. `peerSelectors` identify pods **inside this cluster**, such
as a relay; they cannot select pods by labels across clusters. Supply both a
namespace selector and pod selector to narrow a relay allowance. Include the
Kubernetes Service and post-DNAT API endpoint addresses as required by the CNI.
NetworkPolicy enforcement requires a supporting CNI; other overlapping policies
can broaden access because Kubernetes policies are additive.

The chart uses TCP liveness/readiness probes so no unauthenticated HTTP endpoint
is exposed. These indicate an open listener, not an end-to-end authenticated
agent health check. Confirm the central API can authenticate and reach a test
server before relying on interactive management.

## Upgrade behavior

The operator now injects `GAMEPLANE_SERVER_UID` into agents. Updating the operator
can change existing StatefulSet templates and roll game Pods during reconciliation;
schedule this rollout for a suitable maintenance window. Remote requests use only
`/v1/targets/{uid}/...`. Old agents or an operator that has not populated this UID
fail closed with 404; there is no fallback to legacy agent paths.

Capture sidecars also receive the server UID. New capture start requests bind the
output to both the server UID and NetworkCapture UID on disk. Files created by an
older sidecar without those bindings remain available through the existing local
path, but remote file access fails closed. Upgrade all components before expecting
remote capture downloads or cleanup. See the [parity design](remote-parity.md).

## Capture cleanup recovery

For an unreconciled (empty-phase), Pending or Running capture, request
`:capture-stop`, wait for the operator to report a terminal phase, then retry
deletion. The stop flow also handles
Pending captures whose sidecar started before a status write failed. Repair
operator permissions on `networkcaptures/status` if completion cannot persist.

An upgraded sidecar returns 204 after deleting a matching bound file, or 410
after confirming that neither its identity nor PCAP exists and no writer is
active. A retry after a lost response uses the retained identity tombstone.
The API retains the record on ambiguous 404/409/5xx responses, transport errors
and unavailable sidecars. Repair gateway connectivity, credentials or sidecar
availability and retry; none of these failures proves the file is gone.

If the site cannot be restored, an administrator must inspect the capture's
`gameplane.local/capture-pod-uid` annotation, the actual Pod UID, and capture
storage. A container restart preserves emptyDir data; replacement of the Pod
removes the old emptyDir. Confirm that capture activity has stopped before
manual cleanup. Historical unbound PCAPs must be handled through the site's local
maintenance path; remote routes cannot adopt them under a new UID.

Only after confirming no capture remains active and accepting or removing any
retained file, an administrator may delete the record on the selected cluster:

```sh
kubectl --context <remote-context> -n <namespace> delete networkcapture <capture-id>
```

Record-only deletion does not free retained file storage. The API automatically
skips file cleanup only for an operator-confirmed never-started capture with no
recorded pod UID, as described in the [parity design](remote-parity.md).
