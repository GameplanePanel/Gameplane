# Feature Specification: Module security defaults with a per-server override

**Feature Branch**: `spec-030/security-context-overrides`

**Created**: 2026-11-06

**Status**: Draft

**Input**: User description: "make the different security group controlable with default inside the module and a way in a "advanced" view to force it to something else on the frontend"

## Context

A game template can already declare the user id, group id and filesystem group its
game container runs with (the template's `security` block, used today by about 14
modules, for example ARK at uid 25000 and Minecraft at 1000 in module PR 56). Those
values are fixed: a server owner who hits a host-specific problem (like the gosu
spin on ARM64 that module PR 56 works around), or who mounts a volume already owned
by another uid, has no way to change them without forking the module.

This feature keeps the module's values as the default and adds a per-server
override that the dashboard exposes in an "Advanced" section.

The "security settings" in this spec are the three values the template already
supports: **run-as user**, **run-as group** and **filesystem group**.

## Clarifications

### Session 2026-11-07

- Q: Can a module lock its security values so servers can't override them? → A: No lock. A module can mark any of its security values as risky to override; overriding a risky value stays allowed but needs an extra confirmation.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Server uses the module's security defaults (Priority: P1)

A server owner creates a server from a module that declares security settings and
does nothing special. The server runs with exactly the module's values, and the
owner can see which values are in effect.

**Why this priority**: This is today's behaviour and must not regress. Every other
story builds on the module default being the baseline.

**Independent Test**: Create a server from a module with security settings and one
without; confirm each runs with the module's values (or the image's own default user
when the module sets none), and that the server's settings show those values marked
as "module default".

**Acceptance Scenarios**:

1. **Given** a module that sets run-as user 1000, run-as group 1000 and filesystem
   group 1000, **When** a server is created without overrides, **Then** the game
   runs as 1000/1000 and its data volume is group-owned by 1000.
2. **Given** a module that sets no security settings, **When** a server is created
   without overrides, **Then** the game runs exactly as it does today (the image's
   own default user), with no extra security settings applied.
3. **Given** a server with no overrides, **When** the module is upgraded to a
   version with different security defaults, **Then** the server picks up the new
   defaults on its next restart.

---

### User Story 2 - Override the security settings when creating a server (Priority: P1)

While creating a server, the owner opens an "Advanced" section, sees the module's
default for each of the three settings, and forces one or more of them to a
different value (or explicitly clears one so the image's own default applies).

**Why this priority**: This is the core ask: a way to force a different value
without editing the module.

**Independent Test**: In the create-server wizard, open Advanced, set run-as user to
a value different from the module default, create the server, and confirm the game
runs as that user while the other two settings keep the module default.

**Acceptance Scenarios**:

1. **Given** the create-server wizard, **When** the owner has not opened Advanced,
   **Then** no security values are sent and the module defaults apply.
2. **Given** the Advanced section, **When** the owner views it, **Then** each of the
   three settings shows the module default (or "image default" when the module sets
   none) and an empty override field.
3. **Given** the owner sets an override for one setting, **When** the server is
   created, **Then** only that setting differs from the module default.
4. **Given** the owner explicitly chooses "use image default" for a setting the
   module sets, **When** the server is created, **Then** that setting is not applied
   to the game container.
5. **Given** an invalid value (negative, non-numeric, above the allowed maximum),
   **When** the owner submits, **Then** the wizard rejects it with a clear message
   before anything is created.

---

### User Story 3 - Change or remove an override on an existing server (Priority: P2)

On an existing server's settings page, the owner opens the same Advanced section,
changes or removes an override, and the change takes effect on the next restart.
Removing an override returns that setting to the module default.

**Why this priority**: Fixes like the gosu one are usually discovered after the
server exists. It is P2 because changing the run-as user on a server with existing
data can lock the game out of its own files, so it needs a warning.

**Independent Test**: On a running server, set a run-as user override, save, restart,
and confirm the new user; then remove it and confirm the module default returns.

**Acceptance Scenarios**:

1. **Given** an existing server, **When** the owner changes an override and saves,
   **Then** the dashboard warns that the server must restart and that existing files
   may not be readable by the new user, and the change applies on restart.
2. **Given** a server with an override, **When** the owner clears it, **Then** the
   setting shows "module default" again and the module's value applies on restart.
3. **Given** a server with an override, **When** the module is upgraded with new
   defaults, **Then** the overridden setting keeps the override and the other
   settings follow the new defaults.

---

### User Story 4 - Restrict who can force security settings (Priority: P2)

An administrator controls who may set security overrides, because running a game as
root (user 0) or as an arbitrary uid changes what the container can do on the node.

**Why this priority**: Without it, any user who can create servers could run a game
container as root, which weakens the threat model in `docs/security.md`.

**Independent Test**: As a user without the permission, confirm the Advanced
security fields are read-only (module defaults shown) and that an API request
carrying overrides is refused; as a user with it, confirm both work.

**Acceptance Scenarios**:

1. **Given** a user without permission to override security settings, **When** they
   open Advanced, **Then** they see the applied values but cannot edit them.
2. **Given** such a user sends an override directly to the API, **When** the request
   is processed, **Then** it is refused with a permission error and nothing changes.
3. **Given** a request that sets run-as user 0, **When** it is processed, **Then** it is
   refused unless an admin has turned on the cluster-wide "allow root overrides"
   setting (OD-2).

---

### Edge Cases

- A module sets only the filesystem group (Palworld, whose image must start as root
  to drop privileges): an override of run-as user on such a server would break the
  image's own privilege drop. Such a module marks run-as user as risky (FR-006a), so
  overriding it needs the extra confirmation with the module's reason.
- An override sets the filesystem group but not the run-as user: allowed; it only
  changes which group the data volume is shared with.
- A server is restored from a backup made under different security settings: the
  restore uses the server's current applied settings.
- The module is uninstalled or its template removed while a server has overrides:
  the override stays on the server and is reapplied if the template returns.
- Helper containers (the management agent, config and mod init steps, backups) keep
  their own fixed users; only the game container's user/group and the pod's
  filesystem group change. Backup and restore jobs use the applied filesystem
  group so they keep access to the data.
- The values are numeric ids only; user and group names are not accepted.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Modules MUST remain the source of the default security settings
  (run-as user, run-as group, filesystem group); a server without overrides MUST
  behave exactly as it does today.
- **FR-002**: Each server MUST be able to store an override for each of the three
  settings independently. Each override is one of: not set (use module default),
  a specific numeric id, or "use image default" (do not apply this setting even if
  the module sets it).
- **FR-003**: The desired value of each setting MUST be the server's override when
  present, otherwise the module default, otherwise nothing (image default). The
  applied value is the one the running game container actually uses; a desired value
  becomes the applied value only when the server next (re)starts. Until then the
  change is pending.
- **FR-004**: The reconciler MUST be the only place that computes and applies the
  desired and applied settings; the API only stores and validates the override.
- **FR-005**: Override values MUST be validated as whole numbers from 0 to
  4294967295, matching the module's own limits, with the root rule from OD-2: an
  override of 0 for run-as user or run-as group MUST be refused unless an admin has
  turned on a cluster-wide "allow root overrides" setting, which is off by default.
  The setting lives in global settings (`config:manage`) and turning it on or off is
  audited.
- **FR-006**: The create-server wizard MUST offer a collapsed "Advanced" section that
  shows, per setting, the module default and an optional override.
- **FR-006a**: A module MUST be able to mark each of its security values as risky to
  override, with an optional short reason. Overriding a risky value (in the wizard or
  on the settings page) MUST stay allowed for `servers:security` holders but MUST need
  an extra confirmation that shows the module's reason. Modules cannot lock a value.
- **FR-007**: The server settings page MUST offer the same Advanced section for an
  existing server, with a warning before saving a change to the run-as user or group
  of a server that already has data.
- **FR-008**: Changing an override on an existing server MUST take effect on the next
  restart and MUST say so in the dashboard; it MUST NOT restart the server by itself
  (OD-3).
- **FR-009**: The server's details MUST show, for each setting, the applied value and
  its source, one of: module default, override, "use image default" override, or image
  default (neither the module nor the server sets it). When a saved change is not yet
  applied, the details MUST also show the pending value and that it applies on the
  next restart.
- **FR-010**: Setting, changing or removing an override MUST require the permission
  decided in OD-1, enforced by the API, and MUST be recorded in the audit log with
  before and after values. Per OD-1 this is a new namespaced `servers:security`
  permission, held by the built-in admin role only; admins can add it to other roles.
- **FR-011**: Backup and restore jobs MUST use the server's applied filesystem
  group (the one the data volume currently uses), not only the module's, so a pending
  change never affects a job before the restart.
- **FR-012**: Helper containers (agent, init steps, sentinel, tunnel) MUST keep their
  existing fixed security settings regardless of overrides.
- **FR-013**: The module authoring docs MUST explain that the template's security
  block is now a default that server owners can override, and when a module author
  should mark a value as risky to override and give a reason (for example "image
  must start as root").
- **FR-014**: All new dashboard text MUST go through the shared language packs.

### Key Entities

- **Module security defaults**: the three optional numeric values a game template
  declares today, now described as defaults, plus an optional per-value "risky to
  override" flag with a short reason.
- **Server security override**: per server, for each of the three settings, either
  absent, a numeric id, or "use image default". Owned by the server, survives module
  upgrades and reinstalls.
- **Desired and applied security settings**: the desired settings combine the module
  defaults and the server override; the applied settings are what the running game
  container uses. The reconciler computes both and reports them, with each value's
  source, on the server's status so the dashboard can show where each value came from
  and whether a change is pending.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of existing servers keep the same running user, group and
  filesystem group after the upgrade that ships this feature (no override means no
  change).
- **SC-002**: A server owner with the permission can force a different run-as user
  on a new server in under 1 minute, without editing or forking the module.
- **SC-003**: A user without the permission cannot change any of the three settings,
  through the dashboard or the API, in 100% of attempts.
- **SC-004**: For every server, the dashboard shows each of the three applied values
  and its source, matching what the game container actually runs with, plus any
  pending value not yet applied.
- **SC-005**: Every override change appears in the audit log with who, when, before
  and after.

## Assumptions

- "Security group" in the request means the three values the template's security
  block already supports (run-as user, run-as group, filesystem group). Other
  container hardening (capabilities, read-only root, privilege escalation, supplemental
  groups, the filesystem group change policy) is out of scope for this spec.
- The override applies to the game container and the pod's filesystem group only,
  matching how the module's values are applied today.
- The "Advanced" view is a collapsed section inside the existing create-server wizard
  and server settings screens, not a new page. It is designed in `design.pen` first
  (on valgul's local machine) before any dashboard code.
- The REST API and the MCP server expose the same override; the read-only MCP server
  only reads it.
- Existing data ownership is not changed automatically when the run-as user changes;
  the filesystem group is the supported way to share a volume. A one-off
  "fix ownership" action is out of scope.
- The end-to-end proof is a Kind E2E test that creates a server with an override and
  checks the running uid, per the constitution's E2E principle.

## Decisions

All open decisions are settled in [`OPEN-DECISIONS.md`](OPEN-DECISIONS.md)
(valgul, 2026-11-07): OD-1 new `servers:security` permission (admin only by
default), OD-2 root overrides only when a cluster-wide setting allows them (off by
default), OD-3 changes apply on the next restart.
