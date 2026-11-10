# Cluster administration

One central login manages independent registered clusters. A cluster contains
nodes and game servers; registering another cluster is separate from adding a
node. Dashboard, Servers, Backups and Search combine authorized resources across
clusters. See [Unified dashboard and server management](unified-dashboard.md)
for filtering and server navigation.

## Registered clusters

The **Clusters** page (`/clusters`) lists authorized registrations with their
display name, stable ID, Kubernetes API health, version and last check when
available. **Local** identifies the central cluster in combined installations;
standalone panels have no Local entry and can start with no registrations. The
directory is available to cluster managers and users who can discover a
registration through their workload, inventory or configuration permissions.

- **Open cluster** opens its Overview, Nodes and Configuration tabs.
- **View servers** opens the server list filtered to that cluster.
- **View nodes** opens `/nodes?cluster=<id>` when the user has its inventory
  permission. It does not change the remembered infrastructure selection.
- Connection health reports Kubernetes API connectivity. It does not establish
  gateway readiness or game health.

Users with `cluster:manage` can register a target through **Register cluster**.
Registration grants no workload access by itself. See
[standalone setup](standalone-panel.md) or [combined installation](install.md) and
[remote agent access](multicluster-agent-gateway.md) for gateway credentials.

## Node inventory

The **Nodes** page (`/nodes`) combines all authorized clusters by default. Its
cluster filter issues a narrower backend request; search, status and sortable
columns help find nodes within the returned inventory. Rows keep the pair of
cluster ID and node name as their identity. Desktop uses a table; mobile uses
stacked rows with the same fields. Ready counts, reporting clusters and weighted
CPU/memory summaries show their coverage. Missing metrics remain unknown.

Unavailable or truncated clusters remain filterable and show incomplete coverage.
Changing the filter cannot substitute another cluster's data. The Nodes tab at
`/clusters/<id>?tab=nodes` reuses the inventory with a fixed cluster. Old `/cluster`
bookmarks redirect to Nodes with their remembered selection.

**Add node** requires an explicit target when viewing all clusters. Node-join
tokens remain local-only and require permission and installation support.
Changing targets discards pending join results and commands.

## Cluster configuration

`/clusters/<id>?tab=configuration` owns kubeconfig access, read-only installation
storage, backup destinations and remote registration removal. Kubeconfig issuance
and installation storage remain local-only. Remote access and storage are managed
in that cluster's installation. Backup destination requests and drafts bind to
the explicit cluster and `gameplane-games` namespace, with that scope's destination
permissions. Removal requires confirmation and never applies to Local.

Panel settings retain central authentication, telemetry, registries and
notifications. Old `/admin?section=backups` links lead users to the owning cluster
configuration without issuing an ambient destination request. Discovering a
registration for configuration does not grant node inventory access.

## User access

Kubernetes credentials and dashboard-user grants are separate. The registered
kubeconfig needs permission to read the target cluster's resources; users need
grants for the cluster and namespace they are accessing. See
[remote inventory permissions](gateway-install.md#remote-inventory-permissions)
for the Kubernetes roles.

To grant remote access in **Users & RBAC**:

1. Create a role in **Roles** with the required workload permissions.
2. Edit the user and select the remote cluster for a supplemental grant.
3. Select the role and namespace. For node inventory, use a role containing
   `cluster:read` with **All namespaces**.
4. Add the grant. The user's local primary role remains separate.

Remote all-namespace roles may contain `cluster:read`, `modules:read`,
`modules:manage`, `templates:read`, `templates:write`, and namespaced permissions.
Wildcard and central-administration permissions are rejected, including when an
existing role is edited. Namespace-only server grants do not grant node inventory
access. Changing grants revokes the user's sessions, so they must sign in again.

Accounts, roles, audit and installation settings remain centrally managed. Their
requests do not inherit the inventory selection. Combined panels manage the local
module catalog; standalone panels require an explicit remote target for modules
and sources.
