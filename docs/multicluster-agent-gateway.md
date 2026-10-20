# Remote agent access

The optional agent gateway extends registered clusters with console, game-file
logs, file management, player operations, live status, agent-based mods and capture
files. Registry browsing, modpacks and ID-list mods use the selected Kubernetes
client and template; provider credentials remain in the central installation. The
central API still connects directly to each Kubernetes API for resource changes,
pod logs, PTY attach, and stdin module actions. The gateway does not provide a
Kubernetes tunnel, replicate storage, or run a second user database.

## Register the gateway with the central API

In either installation mode, register the workload cluster through
**Clusters → Register cluster** or `POST /clusters`. Set its gateway URL and mTLS
credentials with `PUT /clusters/{name}/gateway`; the
[standalone guide](standalone-panel.md#configure-remote-agent-access) shows the
request. Standalone panels encrypt those credentials in SQL and have no central
Kubernetes Secret or `Cluster` resource to apply.

The Kubernetes manifests below are an alternative for combined installations.

Install the updated operator, UID-aware agents and [optional gateway](gateway-install.md) in each game
cluster. The gateway must have an HTTPS certificate valid for its configured URL
and trust the central API's dedicated client certificate identity. Use separate
trust material from the local agent CA. Restrict network access to the central API
through private networking or an explicit firewall rule.

Create a Secret in the central API namespace (normally `gameplane-system`):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: remote-1-gateway-client
  namespace: gameplane-system
  labels:
    gameplane.local/agent-gateway-credentials: "true"
type: Opaque
stringData:
  ca.crt: <PEM CA bundle validating the gateway server>
  tls.crt: <PEM central API client certificate with the gateway's expected URI SAN>
  tls.key: <PEM central API client private key>
```

These placeholders describe the Secret keys; provide real credentials through
your normal Secret-management workflow. Do not commit private keys to Git.

Add the optional gateway reference to the existing `Cluster` resource:

```yaml
apiVersion: gameplane.local/v1alpha1
kind: Cluster
metadata:
  name: remote-1
spec:
  kubeconfigSecret:
    name: remote-1-kubeconfig
  agentGateway:
    url: https://remote-1-gateway.internal:8443
    tlsSecretRef:
      name: remote-1-gateway-client
```

The registration name must match the gateway's configured cluster ID. The URL is
an HTTPS origin with no userinfo, path, query or fragment. Credentials are read
only from the central API namespace and must carry the label above. Register the
cluster through the dashboard, central registration API, or its `Cluster`
resource in combined mode. Configure gateway credentials through the API above
or these manifests. The dashboard's registration form accepts the kubeconfig;
gateway credentials use the API or the combined installation's Kubernetes tools.

## Routing and identity

Each request keeps the selected Kubernetes client and gateway transport separate
from other requests. The central API authorizes the user, reads that cluster's
GameServer UID, and sends an allowlisted operation to:

```text
/v1/clusters/{cluster}/namespaces/{namespace}/servers/{name}/uids/{uid}/{operation}
```

The gateway authenticates the central API, verifies its own cluster ID and the
live GameServer/agent Service ownership, and connects to the local agent's UID
route. The agent rejects a different UID. Older agents do not implement that
versioned route and fail closed; they must be upgraded before gateway access is
available. User cookies, bearer tokens and CSRF headers are never forwarded.

The central API is the user-authorization authority. A trusted gateway client
certificate therefore grants delegated access to the gateway's allowlisted agent
operations; it is not a user credential. The gateway does not independently
reconstruct central role bindings. Keep that client private key restricted to the
central API workload.

Internal mod-update reads use the same selected cluster for agent data and game
template lookup. RCON module actions use the gateway. Stdin module actions use the
selected Kubernetes client and verify workload ownership before attach; Kubernetes
attach does not support atomic UID preconditions, so this is a preflight check.

## Failure and rotation behavior

The central API reads the registration and credential from its management store
for every new gateway operation. Missing, deleted, unlabeled or malformed credentials fail closed;
there is no fallback to a local namesake. Removing the optional gateway reference
removes new interactive access without removing Kubernetes management. Redirects
are not followed and writes are not automatically retried after an ambiguous
failure. Healthy streams and transfers have no fixed total-duration cap by
default. Peer certificate expiration, caller cancellation and gateway shutdown
still end them; operators can set `gateway.maxRequestDuration` to a positive
duration to impose an additional limit. Ordinary operations and Kubernetes
identity lookups retain 30-second timeouts. Existing streams can survive trust
or permission removal until one of those termination conditions; reconnect
performs fresh authorization and lookup.

Gateway health and Kubernetes reachability are separate. The existing Cluster
health field describes Kubernetes connectivity; it does not claim that the
gateway is healthy. The authenticated gateway capability endpoint is
`/v1/capabilities`. A gateway outage does not stop games or their local operator.

Capture files use a separate allowlisted gateway route:

```text
/v1/clusters/{cluster}/namespaces/{namespace}/servers/{name}/uids/{serverUID}/captures/{captureName}/uids/{captureUID}/file
```

Only GET and DELETE are supported. The gateway checks the live server, capture
ownership, completion state and retention before reaching the fixed local capture
sidecar port (9091). The sidecar verifies the server and capture UIDs against
persisted file identity, including after a restart. Remote deletion removes the
file before deleting its NetworkCapture resource; an unavailable gateway leaves
the resource available for retry. Old files without this identity binding cannot
be fetched remotely. No arbitrary sidecar path, address or legacy fallback is
accepted.

`GET /servers/{name}/capabilities` reports the selected server identity and its
cluster's capture support, enabled state, retention limits and start defaults.
The central installation's capture flag does not enable or disable a remote
cluster. Missing or incompatible gateway capabilities disable capture controls;
permissions still come from normal server and capture authorization. Historical
downloads remain available when a cluster disables new captures, provided its
gateway and bound files remain accessible.

See [remote parity design and acceptance](remote-parity.md) for the test matrix
and the boundary between server operations and central administration.
