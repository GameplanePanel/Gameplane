# Remote server feature parity

## Routing and identity

The same server pages and permissions apply to local and registered remote game
servers. Each operation stays bound to its cluster, namespace and resource
identity; an unavailable remote never falls back to a local namesake.

Registry browsing resolves the selected GameServer and GameTemplate before using
the central provider configuration. Modpack and mod-ID configuration updates go
to that selected cluster, with Kubernetes resource-version and UID protection.
Provider credentials remain centrally managed. The operator still applies the
desired game configuration.
Mod-ID and modpack writes retry bounded Kubernetes version conflicts only when
the selected server's UID, original spec and ownership grants are unchanged.
Concurrent configuration or ownership edits remain conflicts, and transport
failures are not retried.

Capture data uses a separate, explicit gateway operation rather than the agent
file protocol. The request identifies both the GameServer UID and NetworkCapture
UID. The gateway verifies live ownership, state and expiry, and derives the local
capture endpoint. The sidecar verifies a persisted identity binding before
serving or removing bytes. No client-supplied host, port or filesystem path is
accepted. Existing captures without the identity binding cannot use the remote
route. Downloads preserve Range handling, streaming and synchronous audit.
Remote deletion requires a 204 bound cleanup acknowledgement or a 410 confirming
both identity and PCAP are absent before deleting the CR with a UID precondition.
A failed cleanup retains the CR for retry. A retained
identity tombstone makes a repeated same-UID cleanup safe after a lost response;
it cannot authorize deletion of a replacement file. Local cleanup keeps its
existing behavior.

The API can skip sidecar cleanup only when the operator has marked the capture
Completed with current-generation `SidecarStopped=True`, reason `never_started`,
and no `gameplane.local/capture-pod-uid` annotation was ever recorded. Server
ownership and capture UID checks still apply. Unreconciled (empty-phase) and
Pending/Running captures must use `:capture-stop` and reach a terminal phase
first: a Pending record can already
have an active sidecar if its status update failed. Failed phase, missing
CompletionTime, a 404, and gateway errors do not establish file absence.

See [capture cleanup recovery](gateway-install.md#capture-cleanup-recovery) for
unreachable sites and historical captures without identity metadata.

Capture availability comes from the selected site's configuration. Authenticated
gateway capabilities advertise protocol, capture support, retention limits and
start defaults; the central installation's capture switch does not determine
a remote site's availability.
The UI keeps the existing capture screen, download button and error components.
Loading, unavailable and older-gateway states disable unsupported operations with
an explanation; a capability response never grants authorization.

The access/capability state board (`w1QYmf`), disabled capture screen (`Bbnga`),
Start Capture modal (`O08uaD`) and mobile Capture screen (`SUtGZ`) use the
selected site's capabilities and limits. Retained-download availability remains
distinct from permission to start new captures. The
[design manifest](../assets/design-export/MANIFEST.md) records the Pencil export scope.

Users, provider credentials, the module catalog, installation administration and
node enrollment remain centrally managed. Clusters retain independent game
workloads and storage; cross-cluster scheduling and migration are not supported.

## Acceptance

- Same-named local and remote servers and captures remain independent across all
  registry, modpack, mod-ID, capture download and cleanup operations.
- Wrong cluster, namespace, server/capture UID, ownership, permissions, peer
  identity or certificate cannot read or mutate another target.
- Capture files remain bound to identity after sidecar restart and in-memory
  history eviction; older unbound files fail closed on remote routes.
- Full and ranged downloads stream correctly; cleanup removes only the intended
  remote file; unavailable sites and ambiguous writes are not retried locally.
- Existing local operations and narrow custom-role permissions continue to work.
- CI deploys an actual gateway between two independent Kubernetes clusters and
  exercises real agent/game operations, capability compatibility, certificate
  rotation/revocation, endpoint changes and connection failures.

The integration scenarios are documented in [the e2e specification](../test/e2e/specs.md).
See [contributing](contributing.md#testing) for the test workflow.
