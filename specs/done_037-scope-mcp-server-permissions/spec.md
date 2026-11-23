# Feature Specification: Scope MCP server permissions

**Feature Branch**: `spec-037/scope-mcp-server-permissions`

**Created**: 2026-11-17

**Status**: Draft (all open decisions settled, see `OPEN-DECISIONS.md`)

**Input**: User description: "Scope the MCP server's permissions down from every namespace (`*`) to the Gameplane main namespace and all Gameplane game namespaces. Also include checks on multicluster user permissions, so an MCP client only sees what the calling Gameplane user is allowed to see on each cluster."

## Background

The optional MCP server (`mcpServer.enabled`, code in the `mcp-server/` submodule, chart in `charts/gameplane/templates/mcp-server.yaml`) is read-only, but its Kubernetes role is cluster-wide. It can read Pods, Events and pod logs in every namespace, including `kube-system` and unrelated workloads. Its README and `docs/security.md` call this an accepted tradeoff. This spec removes that tradeoff.

The server also has no notion of a Gameplane user today. Anyone who can open a `serve` session (by `kubectl exec` into the pod, or with a kubeconfig) reads with the server's own Kubernetes identity. Gameplane roles, namespace grants and per-cluster grants (`docs/multicluster-ui.md`, "User access") play no part, and only the cluster the pod runs in is reachable.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Admin installs the MCP server without exposing unrelated workloads (Priority: P1)

A cluster admin enables the MCP server on a cluster that also runs other workloads. The server can read Gameplane's own namespace and the Gameplane game namespaces, and nothing else. Pods, Events and logs in `kube-system` or another team's namespace are out of reach, even for someone who opens a session directly in the pod.

**Why this priority**: This closes the blast radius the README warns about, and it is what the request asks for first. It is useful even before any per-user check exists.

**Independent Test**: Install the chart with `mcpServer.enabled=true` and one extra game namespace. Ask the MCP server to list pods, events and logs in `kube-system`, in an unrelated namespace, in the main namespace, in `gameplane-games` and in the extra game namespace. Only the last three succeed.

**Acceptance Scenarios**:

1. **Given** the MCP server is installed with default values, **When** a session lists pods with no namespace, **Then** the result holds only pods from the main namespace and the configured game namespaces, and no error is shown for the namespaces left out.
2. **Given** the MCP server is installed, **When** a session asks for pods, events or logs in `kube-system`, **Then** it gets a clear "namespace not in scope" answer and no data.
3. **Given** an admin adds a namespace to the managed game namespaces and upgrades the release, **When** a session lists pods in that namespace, **Then** the pods are returned.
4. **Given** the MCP server is installed, **When** the Kubernetes permissions of its identity are listed, **Then** no grant covers Pods, Events or pod logs outside the main and game namespaces, and no grant has a write verb.

---

### User Story 2 - An MCP session only sees what its Gameplane user may see (Priority: P1)

A Gameplane user with a namespace-scoped role (for example `servers:read` in `gameplane-games` only) connects an AI assistant to the MCP server. The assistant can read that user's servers, their pods, events and logs, and nothing that the dashboard would hide from the same user.

**Why this priority**: The request asks for it explicitly. Without it, every MCP user effectively has admin-level read access to all Gameplane namespaces.

**Independent Test**: Create two users with different roles and namespaces. Open one MCP session for each. Compare what each session returns against what the dashboard shows the same user.

**Acceptance Scenarios**:

1. **Given** a user whose role grants `servers:read` only in namespace A, **When** their session lists GameServers, pods or events across all namespaces, **Then** only objects in namespace A are returned.
2. **Given** a user without `backups:read`, **When** their session lists Backups, **Then** the answer is "permission denied" with no data.
3. **Given** a user without `templates:read` or `modules:read`, **When** their session lists the cluster-wide GameTemplates, Modules or ModuleSources, **Then** the answer is "permission denied".
4. **Given** a session for a user who is not a full admin, **When** it asks for pods or logs of Gameplane's own components (API, operator) in the main namespace, **Then** it is denied, in the same way the dashboard's system logs are admin-only.
5. **Given** a session with no valid Gameplane identity (missing, expired, revoked, or for a deleted or disabled user), **When** any tool is called, **Then** it is refused with a generic "not authenticated" answer that does not reveal whether the account exists.
6. **Given** an admin changes or removes a user's role while the user has an MCP session open, **When** the session makes its next call, **Then** the new permissions apply.

---

### User Story 3 - The same checks apply on every registered cluster (Priority: P2)

A user works on a Gameplane install with several registered clusters. Their MCP session can read each cluster they have a grant for, with the role and namespaces of that cluster's grant, and cannot read clusters they have no grant for.

**Why this priority**: The request asks for multicluster permission checks. It builds on Story 2 and only matters on installs with more than one cluster.

**Independent Test**: Register a remote cluster. Give a user a supplemental grant there for one namespace only, and no grant on a third cluster. Their session lists servers on each cluster.

**Acceptance Scenarios**:

1. **Given** a user with a supplemental grant on remote cluster R for namespace B, **When** their session lists GameServers on R, **Then** only objects in namespace B on R are returned.
2. **Given** a user with no grant on cluster S, **When** their session names S, **Then** the answer is the same "cluster not found or not permitted" answer used for an unknown cluster ID.
3. **Given** a session that does not name a cluster, **When** it lists objects, **Then** results cover every cluster the user may see, each item labelled with its cluster, and unreachable clusters are reported as unavailable instead of failing the whole call.
4. **Given** a user whose node inventory access on R comes only from namespace-scoped server grants, **When** their session asks for cluster-level data on R, **Then** it is denied, matching the dashboard rule that namespace-only server grants do not grant inventory.

---

### User Story 4 - Admins keep a break-glass path when the panel is down (Priority: P3)

When the Gameplane API itself is broken, a cluster admin can still use the MCP server to read Gameplane's namespaces and diagnose the problem, within the namespace scope of Story 1. The in-cluster `kubectl exec` mode stays as this break-glass mode (OD-3).

**Why this priority**: The MCP server exists for cluster debugging, and the case where the panel is down is when it is needed most. It is lower priority because Story 1 already covers the admin case in today's mode.

**Independent Test**: Stop the API. Open a session the break-glass way and list pods in the main namespace.

**Acceptance Scenarios**:

1. **Given** the API is unavailable, **When** an admin opens a break-glass session, **Then** they can read pods, events and logs in the main and game namespaces only.
2. **Given** a break-glass session, **When** it is used, **Then** the documentation states that it bypasses Gameplane user checks and is gated by Kubernetes access to the pod.

### Edge Cases

- A game namespace listed in the chart does not exist yet: the server reports it as empty or unavailable, it does not fail every call.
- A namespace is removed from the managed game namespaces: after upgrade, the server can no longer read it.
- A user's grant names "All namespaces" on a cluster: they see every managed game namespace on that cluster, never `kube-system` or other namespaces outside Gameplane's scope.
- A list call with a label or field selector that matches objects in out-of-scope namespaces: those objects are never returned, and the count or truncation notice counts only returned objects.
- `propose_fix` reads the current status of a resource: that read follows the same permission checks as the get tools, so the advice text never leaks a resource the user cannot read.
- Pod logs and events for a pod in an allowed namespace that belongs to a server the user cannot read (owner-only rules): the same server-level checks as the dashboard apply.
- Standalone panel installs (`api.standalone`), where no native in-cluster API exists: per-user reads still work for each registered cluster.
- The old cluster-wide ClusterRole and ClusterRoleBinding from an earlier release: an upgrade removes them, so no stale cluster-wide grant remains.

## Requirements *(mandatory)*

### Functional Requirements

**Namespace scope (Kubernetes permissions)**

- **FR-001**: The chart MUST NOT grant the MCP server read access to Pods, Events or pod logs in any namespace other than the release (main) namespace and the managed game namespaces (`gamesNamespace` plus `operator.gameNamespaces`).
- **FR-002**: The chart MUST grant namespaced Gameplane resources (GameServer, Backup, BackupSchedule, Restore) only in the managed game namespaces.
- **FR-003**: Read access to cluster-scoped Gameplane resources (GameTemplate, Module, ModuleSource) MAY stay cluster-wide, limited to those three kinds. Nothing else cluster-wide is granted.
- **FR-004**: Every grant MUST keep today's read-only verbs (get, list, watch, and get on pod logs). No write verb appears.
- **FR-005**: Adding or removing a managed game namespace in the chart values MUST add or remove the MCP server's access to it on the next upgrade, with no other setting to change.
- **FR-006**: An upgrade from a release with the cluster-wide role MUST remove the old cluster-wide role and binding.
- **FR-007**: Tools that accept "all namespaces" MUST mean "all namespaces in scope". A call naming an out-of-scope namespace MUST return a clear "namespace not in scope" answer and no data, instead of a raw Kubernetes 403.
- **FR-008**: The in-cluster `kubectl exec ... serve` mode MUST stay as an admin-only break-glass mode (OD-3) that works when the API is down. It reads Kubernetes directly with the namespace-scoped grants of FR-001 to FR-007, has no Gameplane user check (Kubernetes access to the pod gates it), and its documentation says so. The per-user mode (FR-010 onward) is the documented normal way to use the MCP server.

**Gameplane user permissions**

- **FR-010**: Every MCP session MUST act for exactly one Gameplane user, identified by a personal MCP token (OD-1), and refuse every tool call without a valid token.
- **FR-016**: Users MUST be able to create, list and revoke their own personal MCP tokens in the dashboard. A token is read-only, has an expiry, is shown in full only once at creation, and never grants more than its user's current permissions. Admins MUST be able to revoke any user's tokens, and removing a user revokes all of theirs.
- **FR-011**: Each tool MUST return only data the same user can see in the dashboard, using the same permission keys: GameServers, their pods, events and logs need `servers:read` in the namespace (and pass the server-level ownership checks the dashboard applies); Backups and Restores need `backups:read`; BackupSchedules need `schedules:read`; GameTemplates need `templates:read`; Modules and ModuleSources need `modules:read`.
- **FR-012**: Pods, events and logs in the main namespace (Gameplane's own components) MUST be readable only by users who can read system logs in the dashboard (full admins).
- **FR-013**: Permission changes, user removal and session revocation MUST apply to the next tool call of an open MCP session.
- **FR-014**: Refusals MUST NOT reveal whether a user, cluster, namespace or object exists beyond what the dashboard reveals to the same user.
- **FR-015**: Each MCP session start and each refused tool call MUST be recorded in the Gameplane audit log with the user, tool, cluster, namespace and outcome, so MCP access is attributable to a user.

**Multicluster**

- **FR-020**: Per-user MCP reads MUST go through the Gameplane API as the token's user (OD-2), so the API applies roles, namespace grants, server ownership checks and per-cluster grants exactly as for the dashboard. A session MUST be able to read every registered cluster the user has a grant for, through the existing cluster registration, with that grant's role and namespaces.
- **FR-024**: The API MUST offer the read-only data every MCP tool needs that it does not offer today (at least: pods of a namespace or server, raw events, and a bounded tail of a pod's logs), behind the same permission keys as FR-011. These reads follow the same size bounds as today's tools (100 newest events, log tail capped at 5000 lines and 256 KiB).
- **FR-025**: In per-user mode the MCP server MUST need no Kubernetes permissions of its own and MUST be able to run outside the cluster (for example on the user's own machine), reaching only the Gameplane API.
- **FR-021**: A session MUST NOT read a cluster the user has no grant for, and the answer MUST match the one for an unknown cluster.
- **FR-022**: Tools MUST accept an optional cluster argument; without it they cover all permitted clusters and label each item with its cluster. A cluster that cannot be reached is reported as unavailable without failing the call.
- **FR-023**: The remote-cluster rules in `docs/multicluster-ui.md` MUST hold for MCP reads as well (namespace-only server grants do not grant cluster-level data).

**Unchanged guarantees**

- **FR-030**: The MCP server MUST stay read-only: no tool creates, updates, patches, deletes or applies anything, and the existing read-only tripwire tests keep passing unchanged.
- **FR-031**: The MCP server MUST keep its stdio-only transport and open no listening port of its own.
- **FR-032**: The MCP server README, `mcp-server/specs.md` and the `mcp-server` section of `docs/security.md` MUST describe the new scope and the per-user model, and drop the "cluster-wide is an accepted tradeoff" text.

### Key Entities

- **MCP session**: one connection from an MCP client. Acts for one Gameplane user and ends when the client disconnects.
- **Personal MCP token** (OD-1): what a session presents to prove its Gameplane user. Belongs to one user, has a name, creation time, expiry and last-used time, can be revoked, and never grants more than that user's current permissions.
- **Scope**: per cluster, the set of namespaces the server's own identity can read (main namespace plus managed game namespaces).
- **Effective user permissions**: applied by the Gameplane API (OD-2), per cluster, the user's primary role (local cluster) or supplemental grants (remote clusters), with their namespaces. Same source of truth as the dashboard.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a fresh install with the MCP server enabled, 0 namespaces outside the main and managed game namespaces are readable by the MCP server's identity for Pods, Events or logs.
- **SC-002**: For a test matrix of at least 4 user profiles (full admin, single-namespace operator, read-only viewer, user with a remote-cluster grant only) across at least 2 clusters, every MCP tool returns exactly the objects the dashboard shows the same user: 0 extra, 0 missing.
- **SC-003**: After an admin removes a user's role, 100% of that user's next MCP tool calls are refused.
- **SC-004**: An admin can enable the MCP server and connect a client by following the README alone in under 10 minutes.
- **SC-005**: The README, `mcp-server/specs.md` and `docs/security.md` no longer describe a cluster-wide grant.

## Assumptions

- "Gameplane main namespace" means the Helm release namespace (`gameplane-system` by default), where the API and operator run. "All gameplane-games namespaces" means `gamesNamespace` (default `gameplane-games`) plus every entry in `operator.gameNamespaces`.
- Cluster-scoped Gameplane kinds (GameTemplate, Module, ModuleSource) have no namespace, so they stay readable cluster-wide by kind. They hold no pod logs or Secrets.
- The Cluster and NetworkCapture kinds stay out of the MCP server's tools, as today.
- The dashboard's existing permission keys and per-cluster grants are the only source of user permissions. This spec adds no new permission key for managing personal MCP tokens beyond what is needed for users to manage their own (decided in planning).
- Code changes span the `GameplanePanel/mcp` repo (server) and `GameplanePanel/Gameplane` (chart, API, docs, and the `mcp-server` submodule pointer after mcp PRs merge). Gameplane PR #993, which makes `mcp-server/` a submodule, merges first.
- Any dashboard screen this needs (the personal MCP token screen) is designed in `design.fig` before code, per the design-first rule.
