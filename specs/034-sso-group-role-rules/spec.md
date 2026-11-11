# Feature Specification: Map SSO groups to any role, and keep roles set by hand

**Feature Branch**: `spec-034/sso-group-role-rules`

**Created**: 2026-11-11

**Status**: Draft (open decisions OD-1..OD-7 in `OPEN-DECISIONS.md`)

**Input**: valgul, 2026-11-11, after a homelab Authentik setup hit two limits: "Gameplane
can only map Authentik groups to its three built-in roles (admin, operator, viewer). It
can't map a group to a custom role like login-only. And once any mapping is on, Gameplane
resets the role on every login, so if you gave raven_ login-only by hand, it would be
overwritten the next time they log in."

## Context

Gameplane can turn the groups an identity provider (IdP) sends at sign-in into a
dashboard role. Today that mapping has two limits, both checked on master `7413822b`:

1. **Only the three built-in roles can be targets.** The mapping has exactly three
   lists, admin, operator and viewer (`api/internal/auth/registry.go:53-57`). The role
   is picked by fixed privilege order admin > operator > viewer
   (`api/internal/auth/oidc.go:208-240`), and the fallback role only accepts
   viewer, operator, admin or deny (`api/internal/handlers/config.go:560`). Custom
   roles already exist (`roles` table, `api/internal/handlers/roles.go`), but no
   mapping can point at one. The Helm chart has the same three keys
   (`charts/gameplane/values.yaml:449-463`, `charts/gameplane/templates/api.yaml:308-315`).
2. **Every login overwrites the role.** As soon as a provider has any mapping, each
   login of an existing user re-computes the role and writes it over whatever is stored
   (`oidc.go:478`, `oidc.go:525-535`, `syncUserRole` at `oidc.go:625`). A role an admin
   set by hand on the Users page lasts only until that person's next sign-in. The one
   exception is the guard that never demotes the last user who can manage users.

This feature replaces the three fixed lists with an **ordered list of rules**
("members of group X get role Y"), where Y can be any role, and records **who set each
user's role**: a rule (SSO) or an admin (by hand). Logins keep SSO-managed roles in
sync and leave hand-set roles alone.

## Clarifications

### Session 2026-11-11

- Q: Which roles should an SSO group be able to map to? → A: Any role, built-in or
  custom, through an ordered list of rules that the admin sets; the first matching rule
  wins. The old admin, operator and viewer keys keep working.
- Q: What should SSO login do to a role an admin set by hand? → A: Keep it. Gameplane
  stores who set each user's role. Logins never change a role set by hand; roles set by
  a rule keep syncing, including demotion when someone leaves a group. The Users page
  gets a "Return to SSO control" action.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Map an IdP group to a custom role (Priority: P1)

An admin has made a custom role (for example "login-only", with no permissions beyond
signing in) and wants everyone in the IdP group "gameplane login only" to get it at
sign-in, while "sys-admin" members get admin.

**Why this priority**: This is the limit that blocked the Authentik setup. Without it
the admin has to over-grant (viewer) or manage people by hand.

**Independent Test**: Create a custom role, add a rule mapping a group to it, sign in
as a member of that group, and confirm the user's role is the custom role.

**Acceptance Scenarios**:

1. **Given** rules `sys-admin → admin`, `gameplane login only → login-only`, **When** a
   member of only "gameplane login only" signs in for the first time, **Then** the
   account is created with role login-only.
2. **Given** the same rules, **When** a member of both groups signs in, **Then** they get
   admin, because that rule is listed first.
3. **Given** the admin moves the login-only rule above the admin rule, **When** that
   same person signs in again, **Then** they get login-only (list order decides, not
   privilege).
4. **Given** a user in no listed group, **When** they sign in, **Then** the provider's
   fallback role applies (any role, or "refuse sign-in").

---

### User Story 2 - A role set by hand survives sign-in (Priority: P1)

An admin changes an SSO user's role on the Users page. The user signs in again the next
day. The role the admin chose is still in place.

**Why this priority**: Today this silently undoes admin decisions, which is surprising
and was the second limit that was reported.

**Independent Test**: With rules on, change an SSO user's role by hand, sign in as that
user, and confirm the role did not change and the audit log records the rule result as
not applied because the role was set by hand.

**Acceptance Scenarios**:

1. **Given** an SSO user whose rules give viewer, **When** an admin sets them to
   operator by hand and the user signs in, **Then** they stay operator.
2. **Given** the same user, **When** they are added to the IdP's admin group and sign
   in, **Then** they still stay operator (a hand-set role is never changed by sign-in).
3. **Given** a user whose role was set by a rule, **When** they leave every mapped group
   and sign in, **Then** their role changes to the fallback role (demotion still works
   for SSO-managed users).
4. **Given** a hand-set user in no mapped group and a fallback of "refuse sign-in",
   **When** they sign in, **Then** the outcome follows OD-5.

---

### User Story 3 - Hand a user back to SSO control (Priority: P2)

An admin sees on the Users page which SSO users have a role set by hand and can return
any of them to SSO control, so their rules apply again.

**Why this priority**: Without a way back, a single manual edit would pin a user forever.

**Independent Test**: For a hand-set SSO user, use "Return to SSO control", sign in as
them, and confirm the role is now the one the rules give.

**Acceptance Scenarios**:

1. **Given** the Users page, **When** an admin looks at SSO users, **Then** each one shows
   whether its role is "From SSO" or "Set by hand".
2. **Given** a hand-set SSO user, **When** an admin chooses "Return to SSO control",
   **Then** the user is marked SSO-managed and the role is recomputed as OD-6 decides.
3. **Given** a local (password) user, **When** an admin views them, **Then** no SSO
   control indicator or action is shown.

---

### User Story 4 - Existing configurations keep working (Priority: P1)

An install already uses the admin/operator/viewer mapping (Helm values or dashboard) and
upgrades.

**Why this priority**: valgul chose to keep the old keys working; an upgrade must not
change anyone's access.

**Independent Test**: Upgrade an install that has old-style mappings and OIDC users;
confirm every user keeps the same role and the next sign-in gives the same result as
before the upgrade.

**Acceptance Scenarios**:

1. **Given** Helm values with `roleMappings.admin/operator/viewer` and no new rules,
   **When** the chart is upgraded, **Then** sign-in picks roles exactly as before
   (admin > operator > viewer).
2. **Given** a dashboard provider saved with the old keys, **When** the API upgrades,
   **Then** it still loads and behaves the same, and the dashboard shows it as the
   equivalent ordered rule list.
3. **Given** existing OIDC users, **When** the database migrates, **Then** their role
   source follows OD-4 and no role changes during the upgrade.

---

### Edge Cases

- A rule names a role that does not exist (deleted, or a typo in Helm values): see OD-2.
- An admin tries to delete a custom role a rule or fallback uses: see OD-2.
- A rule would demote the last user who can manage users: the existing guard still
  blocks it and records it in the audit log (unchanged).
- The same group appears in two rules: the first one wins; the dashboard warns that the
  later rule can never match for that group.
- Group names with spaces, commas or other punctuation (Authentik uses "gameplane login
  only"): matched exactly, case-sensitive, as today.
- A provider with rules but an empty list after edits: mapping is off again, as today
  when no mapping is set (new users get viewer, roles never touched).
- An admin sets a hand-set user's role to the same value it already has: it is still
  recorded as set by hand (the admin's choice is what is protected, not the value).
- The Helm-configured provider overridden from the dashboard: see OD-3.
- A user linked to an SSO account is later given a password (if possible) or the
  provider is disabled: the role source is kept; it only matters at SSO sign-in.

## Requirements *(mandatory)*

### Functional Requirements

**Rules**

- **FR-001**: Each SSO provider (dashboard-managed or Helm-configured) MUST accept an
  ordered list of role rules. Each rule names one IdP group and one role.
- **FR-002**: A rule's role MUST be able to name any role that exists in Gameplane,
  built-in or custom.
- **FR-003**: At sign-in, Gameplane MUST walk the rules in order and give the role of the
  first rule whose group the user belongs to.
- **FR-004**: When no rule matches, the provider's fallback role MUST apply. The fallback
  MUST accept any existing role or "refuse sign-in" (OD-7).
- **FR-005**: The old `roleMappings` keys (admin, operator, viewer) MUST keep working in
  the dashboard config and in Helm values. Old keys alone MUST behave exactly as today,
  as if they were rules in the order admin, operator, viewer. Mixing old keys and rules
  on one provider follows OD-1.
- **FR-006**: The dashboard MUST show and edit rules as an ordered list (add, remove,
  reorder, pick the role from the existing roles), for dashboard providers and for the
  Helm provider's override.
- **FR-007**: Rules that name a missing role MUST be handled as OD-2 decides, and the
  dashboard MUST refuse to save a rule or fallback that names a role that does not exist.

**Role source**

- **FR-008**: Gameplane MUST store, for every user, whether their current role was set by
  SSO rules or by hand.
- **FR-009**: Creating an SSO user at first sign-in MUST mark the role as from SSO.
- **FR-010**: Any role change made by an admin through the Users page or the users API
  MUST mark the role as set by hand.
- **FR-011**: At sign-in, a user whose role is set by hand MUST keep that role. The rule
  result MUST still be computed and written to the audit log as "not applied: set by
  hand".
- **FR-012**: At sign-in, a user whose role is from SSO MUST get the rule result, with
  the existing last-user-manager guard unchanged.
- **FR-013**: An admin with permission to change user roles MUST be able to return a
  hand-set SSO user to SSO control. The role then follows OD-6.
- **FR-014**: The Users page MUST show "From SSO" or "Set by hand" for SSO-linked users,
  and offer "Return to SSO control" on hand-set ones. Local users show neither.
- **FR-015**: Fallback "refuse sign-in" for a hand-set user follows OD-5.

**Audit and docs**

- **FR-016**: Audit events MUST record the matched rule (group and role), whether the
  role was applied, and why not (set by hand, last-manager guard, missing role).
- **FR-017**: Returning a user to SSO control and setting a role by hand MUST each write
  an audit event.
- **FR-018**: `docs/oidc.md`, `docs/security.md`, the Helm values reference and the
  website docs MUST describe rules, order, role source and the upgrade behavior.

**Compatibility**

- **FR-019**: Upgrading MUST NOT change any user's role, and the first sign-in after the
  upgrade MUST give the same result as before for configs that use only old keys.
- **FR-020**: Turning mapping off (no rules, no old keys) MUST keep today's behavior: new
  users get viewer and roles are never touched at sign-in.

### Key Entities

- **Role rule**: one entry in a provider's ordered list: IdP group (exact string) and
  role name.
- **Provider role policy**: the ordered rules, the fallback role (any role or refuse),
  the groups claim and extra scopes of one provider.
- **Role source**: per user, "sso" or "manual"; who decides that user's role.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An admin can map an IdP group to a custom role and see a member signed in
  with that role, with no Helm re-run or restart for dashboard providers.
- **SC-002**: A role set by hand is unchanged after 100% of later sign-ins of that user,
  whatever their IdP groups.
- **SC-003**: An SSO-managed user removed from every mapped group gets the fallback role
  (or is refused) on their next sign-in.
- **SC-004**: After upgrading an install that uses only the old keys, 0 users change role
  at upgrade or at their next sign-in.
- **SC-005**: The audit log explains every sign-in role decision: matched rule, applied
  or not, and why.

## Assumptions

- Group matching stays exact and case-sensitive, using the configured groups claim, as
  today.
- Rules are per provider; there is no cross-provider rule list.
- Role source is per user, not per cluster. Only the cluster-wide role binding that the
  primary role mirrors is affected; per-namespace bindings are untouched, as today.
- The dashboard parts (rule list editor, role source badge, return action) are designed
  in `design.pen` first (core rule 1), on valgul's machine through a local-agent prompt.
- No change to who may change roles: the existing users-management permission and
  role-grant checks apply to "Return to SSO control" too.

## Decisions

See `OPEN-DECISIONS.md`. The two clarifications above were answered by valgul on
2026-11-11 on decision cards in the project thread.
