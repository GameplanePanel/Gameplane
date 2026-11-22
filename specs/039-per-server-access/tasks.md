# Implementation tasks for spec 039

Track implementation here. The [spec](spec.md) defines the behavior and the [plan](plan.md) contains the delivery sequence and verification cases. The four starter server roles still need maintainer approval before the legacy mapping is designed.

## Permissions and storage

- [ ] T001 Register the granular actions, selectors and read projections. Verify their behavior against the supported multi-game operations in the [permission catalog](contracts/permission-catalog.md).
- [ ] T002 Review and approve the exact shipped presets, including their exclusions and invitation behavior. [Starter roles and review gate](plan.md#1--define-actions-and-approve-the-shipped-roles)
- [ ] T003 Preserve primary OIDC/Helm and supplemental scope semantics. Implement automatic coverage for the built-in primary admin, section-level panel permissions and scoped provisioning. [Panel and cluster authorization](plan.md#2--resolve-panel-access-and-cluster-or-namespace-grants)
- [ ] T004 Store live server roles, UID-bound memberships and admin Blocks in panel SQL. Implement consistent evaluation for direct roles, ownership and existing scoped grants. [Storage and effective permissions](plan.md#3--store-live-roles-and-resolve-effective-rights)

## Access surfaces

- [ ] T005 Enforce each action and its selectors on list, detail, write, transfer and stream paths. Verify isolation between siblings, redaction, revocation and the protected admin recovery path. [Enforcement plan](plan.md#4--enforce-granular-actions-across-interfaces)
- [ ] T006 Design the Cluster tab and the person’s three-layer view. Connect both to the same scoped bindings. Verify whole-cluster and single-namespace eligibility, revocation from either view and the limits of the viewer’s authority.
- [ ] T007 Deliver the role library, impact preview, explicit invitations, per-server delegation controls and admin-only Block management. [Access views and delegation](plan.md#5--build-the-access-views-invitations-and-delegation)

## Verification and conversion

- [ ] T008 In CI, verify the release criteria across the dashboard, API, streams, databases and supported game families. Record verified reference-panel workflows and gaps. [Verification matrix](plan.md#6--verify-the-new-model-before-conversion)
- [ ] T009 After T002, map actual legacy collaborator behavior, including overlapping sources of access. Prove parity before conversion. Do not select a shipped preset by approximate similarity.
- [ ] T010 Fence legacy writers, import each eligible UID once and report offline or ambiguous targets. Exercise restore, revocation and replacement-server cases before completing the rollout. [Collaborator migration](plan.md#7--convert-existing-collaborators)
