# Decisions for per-server access

D1 through D6 are decided. The remaining design review covers the four server roles Gameplane will ship. Approve their action sets before mapping legacy collaborators. [Spec](spec.md) · [Implementation plan](plan.md)

## D1 · Editing a base role

**Decided.** A server role has one stable ID and one current permission set. Saving an edit changes what every assignee receives on their next request, across all assigned servers. An unreachable cluster does not delay the edit. Existing streams recheck as specified in FR-09. The editor previews who gains or loses access before the single save. Count committed assignments from panel SQL even when a cluster is offline. Label last-known workload details separately. A partial workload preview never blocks a live role edit. There is no role revision, assignment upgrade or per-user opt-out from the edit. Audit the before/after action delta. An ordinary one-off permission selection is a distinct grant, and any independent source that still grants an action appears in effective access.

## D2 · Who can delegate

**Decided.** Delegation has its own permissions. Holding a gameplay, file or settings permission alone never lets someone give it to another person. Assign the following independently on a target server.

| Permission | What the holder may do |
| --- | --- |
| `members.view` | See that server's member roster and the grant details they are authorized to inspect. |
| `members.invite` | Add a new direct assignment for an existing account on that server. This does not provision a panel account or overwrite an existing assignment. |
| `members.edit` | Change an existing direct assignment within the actor's transferable rights. Editing a reusable role definition remains a panel-admin operation. |
| `members.remove` | Revoke an existing direct assignment within the actor's authority. Account deletion and removal of inherited fleet bindings have their own administrative paths. |

A delegated mutation requires the applicable membership permission and authority over the actions and selectors being assigned or affected. Checking one does not imply granting the other. Compare the full stored grant, including dormant actions. For example, a shift lead holding Invite and Kick on Match 1 can add a kick-only referee there. The lead cannot assign Ban, remove an existing referee, grant themselves a new permission or appoint another membership manager. Invite alone permits the minimal exact-account workflow without revealing the entire member roster or account directory.

The server owner and an admin authorized for the target may appoint membership managers. Owners can delegate only actions they hold with equal or narrower selectors. Delegates cannot edit or remove a superior assignment, grant membership-management powers onward, change inherited grants or manage Blocks. A panel admin editing a shared role later authorizes that change for every assignee. The edit preview includes people originally assigned by delegates. Losing a delegation permission prevents subsequent membership changes and leaves independently authorized assignments in place.

## D3 · Invitations and migration order

**Decided.** The inviter must choose the access. Start manual general-account and server invitations with nothing selected. Creating an account requires selecting its panel access explicitly, including **Assigned servers only** if desired. For server access, select one or more live roles or choose **Custom permissions** and pick at least one action. Editing an existing person's server access leaves their account role alone. Show **Assigned servers only** as an available account-level choice, never as a preselection. A blank form or omitted grant in an invitation request issues no grant and returns a validation error before creating an account. Keep the configured SSO first-sign-in flow and its independently configured fallback.

**Order.** Design and review the base server roles Gameplane ships first. The candidate action sets and example jobs are in the [permission catalog](contracts/permission-catalog.md). Only after approval, map existing collaborators against the old route behavior and implement their migration. The compatibility grant preserves the old contribution even if no shipped role matches it. It is not an invitation option. The four candidate starter roles still need approval.

## D4 · Where assignments live

**Decided.** Panel SQL is the source of truth for new and converted server memberships, live server roles and admin Blocks. Store the account ID and the target’s registered cluster, namespace, name and immutable GameServer UID. Verify that UID before workload access. Kubernetes continues to identify the live workload and its owner. A replacement with the same name receives no previous membership.

A panel admin can revoke a known, converted assignment and record the audit entry in one transaction while the cluster is offline. Creating a grant or acting as an owner or delegate requires verified target and authority. During legacy conversion, keep a durable record for each imported UID even after its last member is removed. After a database restore, reconcile subsequent revocations, role edits and Blocks against independent records before reopening affected scopes. [Migration and recovery](plan.md#7--convert-existing-collaborators)

## D5 · Panel, cluster and server access

**Decided.** Show three independent layers on a panel account. The primary panel role and the cluster/namespace bindings use Gameplane’s existing fleet permission catalog. Individual server roles have their own granular catalog. A user can operate one assigned server with zero effective panel permissions and no supplemental cluster or namespace grant.

| Layer | Where an admin works | Example |
| --- | --- | --- |
| Panel access | **Users & RBAC** sets the primary role or shows its OIDC/Helm source. Only the built-in primary panel admin maintains the reusable server-role library. | Change the default language, manage panel accounts or explicitly choose **Assigned servers only**. This choice gives zero permissions from the primary role. |
| Cluster / namespace access | **Cluster → Roles & permissions → Assignments** opens with the cluster selected. Choose an eligible whole-cluster or named-namespace grant. **Users → Person → Access** shows the same binding from the person’s side. | Grant **Cluster observer** on EU with cluster-wide `cluster:read`, or **Provisioner** with `servers:create` only in `eu/tournaments`. |
| Individual server access | On **Server → Access**, an authorized inviter chooses one or more named roles or a custom permission set for one UID. | Assign **Referee** on Match A and **Power operator** on Match B. Memberships are independent of the caller’s cluster or namespace grants. |

The Cluster tab shows assignments, role eligibility by scope, and a link to the shared role definition for authorized role editors. Binding edits in either view immediately appear in the other. Editing a role definition previews all its affected bindings. An assignment editor’s authority is checked for the target person, role, cluster and namespace. Cluster discovery, inventory access and server delegation alone do not authorize binding changes.

Keep existing scope rules. The local cluster-wide primary grant is managed through the primary role. An eligible supplemental remote whole-cluster grant uses **All namespaces**. The current validator excludes wildcard and central-administration permissions there. A single-namespace grant cannot reveal node inventory. Existing `servers:write` holders retain their authorized operations. Placement discovery still requires template read. [Existing panel bindings](../../docs/multicluster-ui.md#user-access) · [Panel permission catalog](../../api/internal/rbac/catalog.go)

A future Match C shares the namespace provisioning policy and receives no moderator membership from A or B. Bulk assignment grants the role to each explicitly selected UID. Templates declare available actions and settings. Gameplane supplies candidate presets and panel admins maintain the definitions. Template-published role presets and automatic namespace-wide inheritance of granular server roles would require separate decisions.

## D6 · Explicit exceptions to inherited access

**Decided.** Implement a scoped **Block** managed exclusively by the built-in primary panel admin. An owner, delegated membership manager, fleet-role editor or target-only admin cannot create, change or remove one. Applicable subjects are a user or role. The target is one server UID or an exact cluster/namespace. A rule names the ordinary workload action and applicable selectors, such as a networking field or file subtree. No existing grant is converted into a Block.

A user Block matches that account. A role Block matches an account only where it holds that role on the target. Holding the role on a sibling server is insufficient. Once the subject, target, action and selectors match, the Block wins over every direct or inherited allow for that action. Removing the Block reveals whatever access remains. It never manufactures a grant. Keep platform/credential limits and the built-in panel admin recovery path. Explain the affected action, scope and source to authorized viewers, and audit who imposed or removed it. Validate the same decision on fleet reads, detail, individual/compound writes, live streams and MCP reads. A namespace Block restricts matching actions throughout that cluster/namespace and grants no server membership.

For example, Alex can read logs on Final through an existing namespace-scoped panel Viewer binding and a direct server role. An admin Block on Alex reading logs on Final suppresses both allows there. The Viewer binding continues to permit the same logs on Match A. Removing the Block restores log access on Final only if a grant still allows it. [Reference comparison](reference-panels.md)
