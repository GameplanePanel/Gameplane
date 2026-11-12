# Research: spec 034 SSO group role rules

Phase 0 for [plan.md](plan.md). The Technical Context had no open unknowns once OD-1..OD-7
were settled (2026-11-11); the items below record the technical choices the plan makes and
why. Code references are to master after PR #929.

## R-1: Shape of the rule list

- **Decision**: an ordered JSON array `roleRules: [{group, role}]` per provider; array order
  is priority.
- **Rationale**: valgul chose "any role, ordered list, first match wins". An array keeps
  order without extra fields, reorders by moving entries, and serializes the same way in
  the config row, the Helm values and the flag.
- **Alternatives considered**: a `priority` number on each rule (ties need a rule of their
  own, and renumbering is noisy in the UI); a map of role to groups (the current shape,
  which cannot express order between custom roles).

## R-2: Reading the old `roleMappings` keys

- **Decision**: convert old keys into rules in the order admin, then operator, then viewer
  (`rulesFromMappings`), and run everything through the one rule engine. A provider that has
  both forms is refused (OD-1). The dashboard saves edited providers in the new form.
- **Rationale**: today's precedence is fixed admin > operator > viewer
  (`api/internal/auth/oidc.go:224-230`). Emitting every admin group first, then operator,
  then viewer reproduces it exactly, so FR-019 (no behavior change for old configs) holds
  by construction and there is a single code path to test.
- **Alternatives considered**: keeping `computeRole` for the old form beside a new engine
  (two paths that can drift); migrating stored configs at upgrade (rewrites Helm-owned
  input the API does not own).

## R-3: Passing rules from Helm to the API

- **Decision**: one flag `--oidc-role-rules` (env `GAMEPLANE_OIDC_ROLE_RULES`) carrying a
  JSON array, rendered with `toJson` in `charts/gameplane/templates/api.yaml`. The chart
  calls `fail` when both `roleRules` and any `roleMappings` list are set; the API also
  refuses to start in that case.
- **Rationale**: today's flags join groups with commas (`api.yaml:308-315`, parsed by
  `parseRoleMapping` in `api/cmd/main.go`), so a group name containing a comma cannot be
  expressed. Authentik group names contain spaces ("gameplane login only") and may contain
  other punctuation. JSON carries any string, and the chart already quotes the flag value.
- **Alternatives considered**: repeated `--oidc-role-rule=group=role` flags (needs escaping
  for `=` and gets awkward through Helm); mounting a ConfigMap (more moving parts for a
  small value).

## R-4: Where the role source lives

- **Decision**: one column `users.role_source TEXT NOT NULL DEFAULT 'manual'` with values
  `sso`, `sso_pending` and `manual`, added by `020_user_role_source.sql`, which also sets
  `sso` for every user with a row in `oidc_links` (OD-4).
- **Rationale**: the source belongs to the user's primary role, which is already a column
  on `users` (`001_init.sql`) mirrored into the cluster-wide binding by
  `SetClusterRoleBinding` (`api/internal/db/rbac.go:21`). `ALTER TABLE … ADD COLUMN … NOT
  NULL DEFAULT` and `UPDATE … WHERE id IN (SELECT …)` are portable across SQLite and
  PostgreSQL, matching `019_session_epoch.sql`. `sso_pending` lets the Users page show
  "applies at next sign-in" (OD-6) without a second column or a timestamp.
- **Alternatives considered**: a boolean `role_locked` (cannot express the pending state);
  a column on `oidc_links` (a user can in principle have several links, but has one role);
  storing last-seen groups to recompute at once (rejected by OD-6).

## R-5: Checking that a role exists when the auth config is saved

- **Decision**: `newValidators` (`api/internal/handlers/config.go:458`) receives the store,
  and the auth validator calls `db.RoleExists` for every rule role, each `defaultRole`, and
  the override's rules and fallback.
- **Rationale**: validators are pure `func([]byte)` today; role names are now open-ended,
  so the only reliable check is against the `roles` table at save time (FR-007, OD-2).
- **Alternatives considered**: checking only at sign-in (lets a broken config be saved and
  only fail later for users); a foreign key from the config row (it is a JSON blob).

## R-6: Missing role at sign-in (Helm values)

- **Decision**: the decided rule (or the fallback) is checked with `db.RoleExists`. If the
  role is missing, a new or SSO-managed user is refused with the existing generic 403 and an
  audit `refused_missing_role`. There is no fall-through to later rules (OD-2). A hand-set
  user is admitted with their stored role (OD-5).
- **Rationale**: Helm values are not validated against the database at install, so a typo
  can only be caught at sign-in. Failing closed avoids handing out a later, possibly
  stronger, rule. Without this check, `SetClusterRoleBinding` would insert a binding to a
  role that does not exist (SQLite runs with foreign keys off,
  `api/internal/db/rbac.go:36-38`).
- **Alternatives considered**: skipping the broken rule (rejected by OD-2); refusing API
  startup (the role may be created later from the dashboard, so startup cannot know).

## R-7: Order of checks in the callback

- **Decision**: decide the role, then load the linked user and its role source, then apply
  the "Sign-in decision" table in [data-model.md](data-model.md). The deny check moves after
  the user lookup.
- **Rationale**: today `deny` returns before the user is looked up
  (`api/internal/auth/oidc.go:462-468`), which would refuse hand-set users, contrary to
  OD-5. Loading the user first costs one indexed query that `resolveOrLinkUser` already
  runs.
- **Alternatives considered**: a separate pre-check query just for the role source (a
  duplicate of the existing lookup).

## R-8: Audit format

- **Decision**: one `emitRoleDecisionAudit` writing a structured reason string
  ([contracts/audit-events.md](contracts/audit-events.md)) for every SSO sign-in decision
  with mapping on, including refusals and last-manager guard skips.
- **Rationale**: today the guard skip only logs a warning and refusals write no event
  (`oidc.go:301-305`, `oidc.go:463-468`), so SC-005 ("the audit log explains every
  decision") cannot hold without it. The reason field already exists and is part of the
  hash chain (`api/internal/audit/audit.go:319-345`), so no schema change is needed.
- **Alternatives considered**: new audit columns (a schema and hash-chain change for no
  gain over a parseable reason).

## R-9: E2E placement and login budget

- **Decision**: one new test file `test/e2e/api_sso_role_rules_e2e_test.go` with a single
  top-level test that logs in as `e2e-admin` once and runs sequential subtests. It creates
  a custom role, creates a labelled client Secret, and adds its own **dashboard-managed**
  OIDC provider pointing at the in-cluster fake IdP (every bucket's cluster runs it,
  `deploy/kind/e2e.sh:267-276`). It then signs in fake-IdP users with chosen groups
  (`oidcHelmLogin`-style helper, groups passed comma-joined, so test group names avoid
  commas). It goes in an API bucket that has at least one admin login of headroom and no
  other test writing `/admin/config/auth`. The bucket is chosen at T021 from the tallies in
  `test/e2e/buckets.sh`, and the tally comment is updated.
- **Rationale**: `api-auth` and `api-roles` are at their ~7 admin-login ceiling
  (`buckets.sh:156-185`). Using its own dashboard provider avoids writing `helmOverride`,
  which the bucket notes warn would flake `TestAPI_OIDCHelmSeeded_*`, and avoids racing the
  other `/admin/config/auth` writers (`TestAPI_AuthConfig_RoleMappings` and the api-auth
  SSO tests). The Helm-typo missing-role path cannot be created through the dashboard
  (saves are validated), so it is covered by unit tests on the sign-in table rather than
  E2E.
- **Alternatives considered**: a new CI bucket (needs a workflow matrix change for one
  test); reusing the Helm provider through `helmOverride` (flake risk documented in
  `buckets.sh`).

## R-10: Existing tests that encode replaced behavior

- **Decision**: before any edit, T002 lists every existing test asserting (a) that a role
  set by hand is overwritten at sign-in, (b) that `defaultRole` only accepts
  viewer/operator/admin/deny, (c) the `computeRole` or `emitRoleAssignmentAudit`
  signatures, or (d) that a guard skip writes no audit event. Each needs valgul's written
  sign-off naming the file and the change (CLAUDE.md override 1).
- **Rationale**: the SSO re-evaluation tests (for example T049 scenario (b) in
  `test/e2e/api_auth_e2e_test.go:772`) stay valid because SSO-managed users still sync;
  only tests that encode the replaced behavior change, and not without sign-off.
- **Alternatives considered**: none (house rule).
