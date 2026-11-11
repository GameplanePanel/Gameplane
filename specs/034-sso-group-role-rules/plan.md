# Implementation Plan: Map SSO groups to any role, and keep roles set by hand

**Branch**: `spec-034/sso-group-role-rules` | **Date**: 2026-11-11 | **Spec**: [spec.md](spec.md)

## Summary

Replace the three fixed group lists with an ordered `roleRules` list whose roles can be any
role, keep the old keys as an equivalent read-only form, and add a per-user `role_source`
so sign-in only manages users whose role came from SSO. Everything stays in the API
(`api/internal/auth`, `api/internal/handlers`, one DB migration), the Helm chart, the
dashboard (Admin Settings and Users) and the docs. No CRD or operator change.

## Technical Context

- **Language**: Go 1.x (api), TypeScript strict + React 19 (web), Helm templates.
- **Storage**: SQLite and PostgreSQL through the portable `migrations/common/` set; next
  file is `020_user_role_source.sql`.
- **Testing**: Go unit tests in `api/internal/auth`, envtest handler suites in
  `api/internal/handlers`, Vitest for `web/`, Kind E2E bucket `api-auth` (it already runs
  the fake OIDC provider, `test/e2e/Dockerfile.fakeoidc`). CI only (core rule 8).
- **Constraints**: login privacy (core rule 3): a refused sign-in still returns the generic
  "login not permitted"; the rule, group and missing role only reach the audit log and the
  server log.

## Constitution / core rules check

| Rule | How this plan meets it |
|---|---|
| 1 Design-first UI | Rule editor, role-source badge and "Return to SSO control" are designed in `design.pen` first, through a local-agent design prompt (kept in the project notes, outside the repo). No React work starts before the export lands. |
| 3 Login privacy | Refusals keep the generic 403 text. |
| 4 Fix, don't silence | No suppressions. |
| 6 Go errors `%w` | All new errors wrap. |
| 8 Verification | Locally `go build ./...` and `npx tsc --noEmit` only. |
| 10 Operator authority | Not affected: auth is API-owned today. |
| Never weaken tests | Existing tests that assert today's always-overwrite behaviour for SSO users stay valid (SSO users still sync). Any existing test that asserts a hand-set role is overwritten, or that the fallback only accepts the three roles, needs valgul's written sign-off naming the file before it changes. T002 lists them first. |

## Design

### 1. Policy model (`api/internal/auth`)

- `registry.go`: add `RoleRule{Group, Role string}` and `Provider.RoleRules []RoleRule`
  (pointer-or-nil to tell "absent" from "empty"). Keep `RoleMappings`.
- `ProviderPolicy` gains `Rules []RoleRule`; a helper `rulesFromMappings(*RoleMappings)`
  produces admin, then operator, then viewer rules. Policy construction (registry and
  `main.go`) always fills `Rules`; `RoleMappings` stays only for the read-only display.
- `effectiveHelmPolicy`: when the override has `roleRules`, it replaces the list; when it
  has `defaultRole`, it replaces the fallback; old per-role override keeps today's merge,
  then converts to rules (OD-3).
- `computeRole` becomes `decideRole(groups, pol) Decision{RuleIndex int, Group, Role
  string, Deny bool}` (RuleIndex -1 = fallback). It no longer knows role names; existence is
  checked by the caller against the store.

### 2. Sign-in flow (`oidc.go` callback, `resolveOrLinkUser`, `syncUserRole`)

1. Compute the decision.
2. Look up the linked user and its `role_source` before acting on `Deny` (today the deny
   check runs first, `oidc.go:463`).
3. Apply the table in `data-model.md` → "Sign-in decision". Role existence uses
   `db.RoleExists` (`api/internal/db/rbac.go:51`).
4. First sign-in inserts `role_source = 'sso'`. `syncUserRole` writes `role_source = 'sso'`
   with the role in the same transaction and keeps the last-manager guard.
5. Replace `emitRoleAssignmentAudit` with one `emitRoleDecisionAudit` that writes the reason
   format in `contracts/audit-events.md`, including refusals and guard skips.

### 3. Config validation (`api/internal/handlers/config.go`)

- `authProvider` / `authHelmOverride` gain `RoleRules *[]authRoleRule` and (override only)
  `DefaultRole *string`.
- `validDefaultRoles` map is replaced by "empty, deny, or `RoleExists`".
- `validateAuth` takes the store (closure over `*db.Store`) to check role existence;
  `newValidators` passes it.
- Both-forms check (OD-1) for providers and the override.
- `buildInstallTimeSettings` adds `roleRules` to `oidcHelmProvider`.

### 4. Role deletion (`api/internal/handlers/roles.go`)

Before the binding count, load the `auth` config row and the Helm policy (pass the policy
into `MountRoles` like `MountConfig` gets it) and refuse with
`server.roles.in_use_by_sso` (409) when the role is referenced.

### 5. Users API (`api/internal/handlers/users.go`)

- Select `role_source` in every user query; add `RoleSource` to `userDTO`.
- `PATCH`: on `role`, also `UPDATE users SET role_source = 'manual'` in the same tx.
  New `roleSource` field per `contracts/users-api.md`; SSO link check is
  `SELECT 1 FROM oidc_links WHERE user_id = ?`.
- `POST /users` (invite/local create) inserts `'manual'` (column default already does).

### 6. Startup flags (`api/cmd/main.go`)

New `--oidc-role-rules` / `GAMEPLANE_OIDC_ROLE_RULES` (JSON). Startup error if combined with
any `--oidc-role-mapping-*`. `--oidc-default-role` no longer limited to the three names.

### 7. Helm chart

`values.yaml` documents `api.oidc.roleRules` and the any-role `defaultRole`;
`templates/api.yaml` passes `toJson` rules and `fail`s when both forms are set.

### 8. Dashboard (after the Pencil export)

- `web/src/lib/config.ts`: `AuthRoleRule`, `roleRules`, `AuthDefaultRole = "deny" | string`.
- `AdminSettings.tsx`: ordered rule editor (group text field, role select from
  `RolesAPI.list()`, move up/down, remove, add), used in the provider form and the Helm
  override card; read-only Helm card lists rules in order; duplicate-group warning; saving
  an old-form provider writes `roleRules`.
- `Users.tsx`: "From SSO" / "Set by hand" / "From SSO (applies at next sign-in)" badge on
  SSO users; "Return to SSO control" action on hand-set SSO users with a confirm.
- `web/src/lib/api.ts` user type gains `roleSource`; `Users.patch` accepts `roleSource`.
- New strings go through `intl.formatMessage` with descriptions; `en` pack updated, `fr`
  flagged for Weblate as usual.

### 9. Docs

`docs/oidc.md` (rules, order, worked Authentik example with a login-only custom role,
role source), `docs/security.md` ("Re-evaluation on Every Login" rewritten for role source
and refusals), `api/specs.md` and `web/specs.md` where they describe mapping,
`CHANGELOG.md`; website `users-auth-rbac.mdx`, `helm-values-reference.mdx`,
`user-administration.mdx` in a `GameplanePanel/website` PR, then a submodule bump.

## Delivery

One branch and PR per the house rule: this spec PR first (docs only), then one
implementation PR on `feat/034-sso-group-role-rules` stacked on it, then the website PR.
The design step happens on valgul's machine before the dashboard tasks start; API, chart
and docs tasks do not wait for it.
