# mcp-server

The **mcp-server** [optional] is a strictly **read-only** [Model Context Protocol](https://modelcontextprotocol.io/)
(MCP) server for Gameplane clusters. It lets an AI assistant read cluster
state — 7 of Gameplane's 9 CRDs (not Cluster or NetworkCapture), Pods, Events, and pod logs — and get a
suggested fix as plain text (YAML and/or `kubectl` commands) for a human
operator to review and run.

It **never** creates, updates, patches, deletes, or applies anything. That
is a hard invariant, held by two layers:

1. **The handler boundary**: every MCP tool handler (`tools.go`,
   `fixadvice.go`) lives in `package main` and takes a `*kube.Client`
   (`internal/kube/client.go`). That type's only exported methods are
   List/Get-shaped (`ListCRD`, `GetCRD`, `ListPods`, `GetPod`,
   `ListEvents`, `PodLogs`); the typed and dynamic Kubernetes clientsets
   behind it are **unexported fields** on `Client`, defined in
   `internal/kube`. A handler that holds only the `*kube.Client` therefore
   cannot call `Create`/`Update`/`Delete`/`Patch`/`Apply` through it. The
   boundary is on the `*kube.Client`, not on `package main`: `runServe`
   loads the `*rest.Config` itself, so keeping handlers on the
   `*kube.Client` (and having no mutating call site anywhere in the
   module) is a code convention, not a compile-time guarantee.
   `main_test.go`'s `TestClientHasNoMutatingMethods` is a tripwire on top
   of that (it reflects over `kube.Client`'s method set so a future
   mutating method added to `internal/kube` fails a test immediately).
2. **By RBAC**: the Kubernetes ClusterRole the chart's `mcpServer.enabled`
   toggle installs grants only `get`/`list`/`watch` (plus `get` on
   `pods/log`) — there is no `create`/`update`/`patch`/`delete` verb in it.
   This is the **authoritative** layer and the only one that stops
   mutation by any code in the process: the API server rejects a write
   from the ServiceAccount this pod runs as, whatever client makes it. A
   standalone run against a kubeconfig (see below) has that kubeconfig
   user's permissions instead, so point it at a read-only identity.

A third, cosmetic layer: every tool this server installs (`tools.go`)
carries the MCP spec's `readOnlyHint: true` annotation, and `main_test.go`'s
`TestRegisteredToolsAreReadOnly` asserts the registered tool set never
contains a mutating verb — this makes the guarantee visible to MCP clients,
it doesn't enforce it.

### RBAC blast radius

The `get`/`list`/`watch` ClusterRole above is **cluster-wide**: it is not
scoped to `gameplane-games` or any other single namespace, so it can read
Pods, Events, and pod logs in every namespace the cluster has, including
`kube-system` and any other workload's namespace. Pod logs in particular
can surface application secrets that a container logs at startup (API keys,
connection strings, etc.) — Kubernetes doesn't distinguish "log line" from
"log line containing a secret". This is an accepted tradeoff (the server is
opt-in, admin-installed via `mcpServer.enabled`, and write-free even so),
but install it with that blast radius in mind: anyone who can
`kubectl exec` into the `gameplane-mcp-server` pod (or otherwise reach an
MCP client already wired to it) can read logs and object state for
*any* pod in the cluster, not just Gameplane-managed ones. If that's wider
than you want, don't enable `mcpServer` on a cluster that also runs
unrelated, more sensitive workloads. See `docs/security.md` for the rest of
the threat model.

## Tools

| Tool | Reads |
|---|---|
| `list_gameplane_resources` | One Gameplane CRD kind (GameServer, GameTemplate, Backup, BackupSchedule, Restore, Module, ModuleSource), optionally scoped to a namespace and/or label selector |
| `get_gameplane_resource` | A single Gameplane CRD object by kind/namespace/name |
| `list_pods` | Core Pods in a namespace (or all namespaces), optionally by label selector |
| `get_pod` | A single Pod's spec and status |
| `list_events` | Core Events in a namespace (or all namespaces), optionally by field/label selector. Bounded to the most-recent 100 events (sorted newest-first); responses include a truncation notice if more exist. |
| `get_pod_logs` | A bounded tail (default 200 lines, capped at 5000) of a container's logs; retains the newest 256 KiB from the complete response, with a notice when truncated |
| `propose_fix` | Given a resource reference + a free-text symptom, returns suggested YAML/kubectl text, grounded in a best-effort read of the resource's current status. Never applies anything itself. |

## Transport: stdio only, no network port

This server speaks MCP (JSON-RPC 2.0) over stdio — it never opens an HTTP
or TCP port. It has two subcommands:

- `idle` (default, no args) — block until terminated. This is what the
  bundled Helm Deployment runs as its long-lived container process, purely
  so there is always a live container to `kubectl exec` into.
- `serve` — run one MCP stdio session against the real cluster. Point an
  MCP host's launcher (e.g. Claude Desktop / Claude Code's MCP config, or
  any other MCP-compatible client) at:

  ```sh
  kubectl exec -i deploy/gameplane-mcp-server -n gameplane-system -- /mcp-server serve
  ```

  Each `kubectl exec -i` spawns an independent, isolated `serve` process
  sharing the pod's ServiceAccount credentials and network access —
  concurrent sessions from different users/hosts don't interfere with each
  other or with the idle placeholder process.

## Run via the Helm chart

Set `mcpServer.enabled=true`. The chart deploys a Deployment (no Service —
there is no network port to expose), a dedicated ServiceAccount, and a
cluster-scoped, read-only ClusterRole/ClusterRoleBinding (`get`/`list`/
`watch` only, on 7 of Gameplane's 9 CRDs (not Cluster or NetworkCapture) plus core Pods/Events, and `get` on
the `pods/log` subresource).

```yaml
mcpServer:
  enabled: true
  replicas: 1
  resources:
    requests: { cpu: 50m, memory: 32Mi }
    limits:   { cpu: 200m, memory: 128Mi }
```

## Run standalone (against a kubeconfig)

```sh
KUBECONFIG=~/.kube/config docker run --rm -i \
  --user "$(id -u):$(id -g)" --network host \
  -v ~/.kube/config:/kubeconfig:ro -e KUBECONFIG=/kubeconfig \
  ghcr.io/gameplanepanel/gameplane/mcp-server:edge serve
```

`serve` builds its Kubernetes client via `ctrl.GetConfig()`, so it works
in-cluster (via the pod's ServiceAccount) or locally against `KUBECONFIG`/
`~/.kube/config`, same as any other controller-runtime-based Gameplane
component. Two flags matter for the standalone case above:

- `--user "$(id -u):$(id -g)"` — the image runs as a fixed non-root UID
  (65532), which normally can't read a kubeconfig with the usual `0600`
  permissions owned by your own user. Running the container as your own
  UID/GID lets it read the mounted file without loosening its permissions
  or copying it elsewhere.
- `--network host` — only needed when the kubeconfig's server address is a
  loopback address (e.g. a local kind/k3d cluster at `https://127.0.0.1:<port>`).
  Without it, `127.0.0.1` inside the container resolves to the container
  itself, not the host, and every tool call fails with a connection error.
  It's not needed against a real (non-loopback) API server address.

## Response size optimization

Every tool that returns Kubernetes objects strips `metadata.managedFields`
from the response to reduce payload size (~33% of unstripped responses). This
field carries an audit trail of every manager that has written to the object;
it is rarely needed for debugging and wastes LLM context tokens.

`list_events` additionally bounds responses to the most-recent 100 events
(sorted by `lastTimestamp`, falling back to `eventTime`, newest first). When more events exist in the
cluster than can fit in 100, the response's `metadata.resourceVersion` field
is set to a truncation notice (e.g. `"truncated (showing newest 100 of 342
events)"`). This prevents large event logs from exhausting the LLM context.
