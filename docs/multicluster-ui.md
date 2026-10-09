# Cluster administration

One central login manages independent registered clusters. A cluster contains
nodes and game servers; registering another cluster is separate from adding a
node. Dashboard, Servers, Backups and Search combine authorized resources across
locations. See [Unified dashboard and server management](unified-dashboard.md)
for filtering and server navigation.

## Registered clusters

The **Clusters** page (`/clusters`) lists authorized registrations with their
display name, stable ID, Kubernetes API health, version and last check when
available. **Local** identifies the central cluster in combined installations;
standalone panels have no Local entry and can start with no registrations. The
administration navigation is available to users with cluster-management or
inventory access.

- **View servers** opens the server list filtered to that location.
- **View nodes** opens the selected cluster's inventory when the user has its
  inventory permission.
- Connection health reports Kubernetes API connectivity. It does not establish
  gateway readiness or game health.

Users with `cluster:manage` can register a target through **Register cluster**.
Registration grants no workload access by itself. See
[standalone setup](standalone-panel.md) or [combined installation](install.md) and
[remote agent access](multicluster-agent-gateway.md) for gateway credentials.

## Node inventory

The **Cluster** page (`/cluster`) shows the selected cluster's nodes and capacity.
Its queries retain that cluster ID, and changing the inventory selection clears
pending queries and cached results. A failed remote read displays an error;
it never substitutes local inventory. Loading, denied access and empty inventory
have separate states.

Node-join tokens, kubeconfig issuance and installation storage configuration are
available only for the local cluster. Inventory selection does not change an
open server's target or the location filters on ordinary resource pages.

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
