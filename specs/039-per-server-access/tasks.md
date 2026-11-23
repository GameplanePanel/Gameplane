# Implementation tasks for spec 039

The [spec](spec.md) records the approved role design. The [plan](plan.md) defines implementation order, preview behavior and the later collaborator conversion.

## Permissions and storage

- [ ] T001 Register the granular actions, selectors and read projections. Seed the approved base roles, validate template-qualified role definitions and verify their exact effective actions across game families. [Permission catalog](contracts/permission-catalog.md)
- [x] T002 Approve the three shipped base roles: Viewer, Power User and Administrator. Administrator combines the former Player moderator and Backup operator action sets. Invitations start without a selection. [Base roles and template roles](plan.md#1--define-actions-and-ship-the-three-base-roles)
- [ ] T003 Preserve primary OIDC/Helm assignments and existing supplemental grants. Require exactly one base server role on new cluster/namespace roles, and an explicit previewed choice before converting historical grants. Keep the local workload’s whole-cluster grant distinct from the primary `local/*` binding during OIDC sync and manual editing. Implement automatic primary panel-admin coverage, section-level panel permissions and scoped provisioning. [Panel and cluster authorization](plan.md#2--resolve-panel-access-and-cluster-or-namespace-grants)
- [ ] T004 Store direct memberships, scope-role base and GameTemplate references, reviewed live roles and admin Blocks in panel SQL. For every current and future server in scope, resolve its inherited base and matching template roles alongside direct grants and ownership. [Storage and effective permissions](plan.md#3--store-live-roles-and-resolve-effective-rights)

## Access surfaces

- [ ] T005 Enforce each action and its selectors on list, detail, write, transfer and stream paths. Verify isolation between siblings, redaction, revocation and the protected admin recovery path. [Enforcement plan](plan.md#4--enforce-granular-actions-across-interfaces)
- [ ] T006 Design the Cluster tab and the person’s three-layer view around the same scoped bindings. On scoped assignments, removals and role edits, preview existing coverage, counts per template, future server coverage and any unknown or last-known counts. Verify both views show the same committed changes and surviving sources.
- [ ] T007 Deliver the role library, impact preview, explicit invitations, per-server delegation controls and admin-only Block management. [Access views and delegation](plan.md#5--build-the-access-views-invitations-and-delegation)

## Verification and conversion

- [ ] T008 In CI, verify release criteria across interfaces, databases and games. Include existing and future servers, matching and unmatched templates, overlapping scopes, Blocks, stale counts, revocation and a legacy inventory-only binding. Record verified reference-panel workflows and gaps. [Verification matrix](plan.md#6--verify-the-new-model-before-conversion)
- [ ] T009 Measure actual legacy collaborator behavior, including overlapping sources of access, and freeze a compatibility mapping against that evidence. Prove parity before conversion.
- [ ] T010 Fence legacy writers, import each eligible UID once and report offline or ambiguous targets. Exercise restore, revocation and replacement-server cases before completing the rollout. [Collaborator migration](plan.md#7--convert-existing-collaborators)
