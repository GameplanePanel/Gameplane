# Implementation Plan: Map SSO groups to any role, and keep roles set by hand

**Branch**: `feat/034-sso-group-role-rules` | **Date**: 2026-11-12 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/034-sso-group-role-rules/spec.md`

## Summary

Each SSO provider's three fixed group lists (admin, operator, viewer) become an ordered
`roleRules` list (`{group, role}`, first match wins) whose role can be any role, built-in or
custom. The old `roleMappings` keys stay valid and are read as the equivalent rules. A new
per-user `role_source` column (`sso`, `sso_pending`, `manual`) makes sign-in keep
SSO-managed roles in sync while never changing, or refusing, users whose role an admin set
by hand. The change is API-owned (auth package, config and users handlers, one portable
migration), plus Helm values and flags, the dashboard (Admin Settings and Users, designed in
Pencil first), docs and E2E. No CRD, operator or agent change. Decisions are in
[research.md](research.md); all spec decisions (OD-1..OD-7) are settled.

## Technical Context

**Language/Version**: Go 1.27 (`api/go.mod`), TypeScript strict with React 19 (`web/`), Helm 3 templates.

**Primary Dependencies**: `chi`, `coreos/go-oidc` and `golang.org/x/oauth2` (existing sign-in), `database/sql` with modernc SQLite and pgx; TanStack Query and HeroUI v3 on the dashboard. No new dependency.

**Storage**: the API database (SQLite default, PostgreSQL experimental) through the portable `api/internal/db/migrations/common/` set; next file `020_user_role_source.sql`. Provider config stays in the existing `auth` config row (JSON).

**Testing**: Go unit tests (`api/internal/auth`), envtest handler suites (`api/internal/handlers/*_envtest_test.go`), Vitest + MSW (`web/`), Kind E2E with the in-cluster fake IdP (`test/e2e/internal/fakeoidc`). All run in CI only (Principle VI).

**Target Platform**: Linux containers on Kubernetes (single-node k3s through multi-node).

**Project Type**: web service (API) + web dashboard + Helm chart.

**Performance Goals**: sign-in adds at most two indexed lookups (`users.role_source` with the existing link lookup, and `roles` by primary key for the decided role). Rule matching stays linear in the number of rules (tens).

**Constraints**: login privacy (core rule 3): every refusal returns the existing generic "login not permitted"; the rule, group and role reach only the audit log and the server log. Upgrades change no user's role (FR-019). E2E login budgets per bucket (CLAUDE.md "E2E conventions").

**Scale/Scope**: about 10 Go files in `api/`, 2 chart files, 4 web files, 1 migration, 1 E2E file, 4 repo docs plus the website docs.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design (below).*

| Principle | Gate | Status |
|---|---|---|
| I. E2E-tested delivery | A real E2E path through the fake IdP covers custom-role rules, hand-set roles kept, and return to SSO control. | PASS: T021, scoped in [research.md](research.md) R-9 with its login budget. |
| II. Design-first | Rule editor, role-source badge and "Return to SSO control" are designed in `design.pen` and exported before React work. | PASS: T003 gates Phase 5. The prompt for valgul's local agent is kept in the project notes. |
| III. Language best practice | `%w` wrapping, strict TS, no suppression directives, no CRD change. | PASS. |
| IV. Spec-driven + `specs.md` | Spec, plan and tasks exist; `api/specs.md` and `web/specs.md` change in the same PR as the behavior. | PASS: T022. |
| V. Delegate to workflows | Implementation runs as `Workflow` scripts, haiku first, with explicit `model:` and tier-up diff review. | PASS: Delivery section. |
| VI. CI bears the load | Local checks are `go build ./...` and `npx tsc --noEmit` only. | PASS. |
| CLAUDE.md "never weaken tests" | Existing tests that encode replaced behavior are changed only with valgul's written sign-off naming each file. | PASS: T002 gates every test edit. |

## Project Structure

### Documentation (this feature)

```text
specs/034-sso-group-role-rules/
├── spec.md              # what and why (settled)
├── OPEN-DECISIONS.md    # OD-1..OD-7, all settled 2026-11-11
├── plan.md              # this file
├── research.md          # Phase 0 decisions
├── data-model.md        # Phase 1: rules, policy, role source, sign-in table
├── quickstart.md        # Phase 1: validation scenarios
├── contracts/
│   ├── auth-config.md   # config section, Helm values and flags
│   ├── users-api.md     # userDTO, PATCH /users/{id}, DELETE /roles/{name}
│   └── audit-events.md  # sign-in decision audit reasons
├── checklists/requirements.md
└── tasks.md             # breakdown
```

### Source Code (repository root)

```text
api/
├── cmd/main.go                          # --oidc-role-rules flag, both-forms startup error
├── internal/auth/
│   ├── registry.go                      # RoleRule, Provider.RoleRules, policy build
│   └── oidc.go                          # decideRole, sign-in flow, role source, audit
├── internal/db/
│   ├── rbac.go                          # role-source helpers next to RoleExists
│   └── migrations/common/020_user_role_source.sql
├── internal/handlers/
│   ├── config.go                        # auth validator with store access, roleRules, override
│   ├── roles.go                         # in_use_by_sso delete guard
│   └── users.go                         # roleSource in DTO and PATCH
└── specs.md
charts/gameplane/
├── values.yaml                          # api.oidc.roleRules, any-role defaultRole
└── templates/api.yaml                   # toJson flag, fail on both forms
i18n/locales/en/app.json                 # new server.* and web.* message keys
web/src/
├── lib/config.ts, lib/api.ts            # types
├── routes/AdminSettings.tsx             # ordered rule editor, Helm override card
├── routes/Users.tsx                     # role-source badge, return action
└── test/handlers.ts                     # MSW
web/specs.md
test/e2e/api_sso_role_rules_e2e_test.go  # new, registered in buckets.sh
docs/oidc.md, docs/security.md, CHANGELOG.md
website/ (submodule): users-auth-rbac.mdx, helm-values-reference.mdx, user-administration.mdx
```

**Structure Decision**: existing monorepo layout; the feature lives in `api/`, `web/`,
`charts/gameplane/`, `test/e2e/` and docs. No new module or package.

## Design

### 1. Policy model (`api/internal/auth`)

- `registry.go`: add `RoleRule{Group, Role string}` and `Provider.RoleRules *[]RoleRule`
  (nil = absent, empty = mapping off). Keep `RoleMappings` for the old form.
- `ProviderPolicy` gains `Rules []RoleRule` and `MappingOn bool`. Policy construction
  (registry and `main.go`) always fills `Rules`, converting the old form with
  `rulesFromMappings` (admin, then operator, then viewer; research R-2).
- `effectiveHelmPolicy`: override `roleRules` replaces the list and override `defaultRole`
  replaces the fallback (OD-3); the old per-role override keeps today's merge and is then
  converted.
- `computeRole` becomes `decideRole(groups, pol) Decision{RuleIndex int, Group, Role
  string, Deny bool}` (RuleIndex -1 = fallback). Role existence is checked by the caller.

### 2. Sign-in flow (`oidc.go` callback, `resolveOrLinkUser`, `syncUserRole`)

1. Decide.
2. Look up the linked user and its `role_source` before acting on `Deny` (today the deny
   check runs first, `oidc.go:463`).
3. Apply the "Sign-in decision" table in [data-model.md](data-model.md). Role existence uses
   `db.RoleExists` (`api/internal/db/rbac.go:51`).
4. First sign-in inserts `role_source = 'sso'`. `syncUserRole` writes the role and
   `role_source = 'sso'` in one transaction and keeps the last-manager guard.
5. Replace `emitRoleAssignmentAudit` with `emitRoleDecisionAudit` writing the reason format
   in [contracts/audit-events.md](contracts/audit-events.md), refusals and guard skips
   included.

### 3. Config validation (`api/internal/handlers/config.go`)

- `authProvider` and `authHelmOverride` gain `RoleRules *[]authRoleRule`; the override also
  gains `DefaultRole *string`.
- `validDefaultRoles` is replaced by "empty, `deny`, or an existing role".
- `newValidators` takes the store so the auth validator can check role existence
  (research R-5).
- Both-forms refusal (OD-1) for providers and the override.
- `buildInstallTimeSettings` adds `roleRules` to `oidcHelmProvider`.

### 4. Role deletion (`api/internal/handlers/roles.go`)

Before the binding count, read the `auth` config row and the Helm policy (passed into the
roles mount the way `MountConfig` receives it) and refuse with
`server.roles.in_use_by_sso` (409) when the role is referenced.

### 5. Users API (`api/internal/handlers/users.go`)

- Select `role_source` in every user query; add `RoleSource` to `userDTO`.
- `PATCH`: a `role` change also sets `role_source = 'manual'` in the same transaction. The
  new `roleSource` field follows [contracts/users-api.md](contracts/users-api.md); the SSO
  link check is `SELECT 1 FROM oidc_links WHERE user_id = ?`.
- Local create keeps the column default `manual`.

### 6. Startup flags (`api/cmd/main.go`)

New `--oidc-role-rules` / `GAMEPLANE_OIDC_ROLE_RULES` (JSON array, research R-3). Startup
error when combined with any `--oidc-role-mapping-*`. `--oidc-default-role` accepts any
non-blank name or `deny`.

### 7. Helm chart

`values.yaml` documents `api.oidc.roleRules` and the any-role `defaultRole`;
`templates/api.yaml` passes the rules with `toJson` and `fail`s when both forms are set.

### 8. Dashboard (after the Pencil export)

- `web/src/lib/config.ts`: `AuthRoleRule`, `roleRules`, `AuthDefaultRole = "deny" | string`.
- `AdminSettings.tsx`: ordered rule editor (group text field, role select from
  `RolesAPI.list()`, move up and down, remove, add) in the provider form and the Helm
  override card; the read-only Helm card lists rules in order; duplicate-group warning;
  saving an old-form provider writes `roleRules`.
- `Users.tsx`: "From SSO", "Set by hand" and "From SSO (applies at next sign-in)" badges on
  SSO users; "Return to SSO control" with a confirm on hand-set SSO users.
- `web/src/lib/api.ts`: user type gains `roleSource`; the users patch accepts `roleSource`.
- New strings use `intl.formatMessage` with descriptions; the `en` pack is updated and `fr`
  goes through Weblate as usual.

### 9. Docs

`docs/oidc.md` (rules, order, an Authentik example with a login-only custom role, role
source), `docs/security.md` ("Re-evaluation on Every Login" rewritten for role source and
refusals), `api/specs.md`, `web/specs.md`, `CHANGELOG.md`; website pages in a
`GameplanePanel/website` PR, then a submodule bump.

## Constitution Check (post-design)

Re-checked against the Phase 1 artifacts: the data model adds one column through a portable
migration (no dialect-specific SQL), the contracts keep refusals generic on the login path,
the E2E plan fits existing bucket budgets without touching `helmOverride` in a shared
bucket (R-9), and no CRD or operator code is involved. All gates still PASS. No violations,
so Complexity Tracking is empty.

## Delivery

One implementation PR on `feat/034-sso-group-role-rules`, which valgul's local agent
creates with the design commit (T003). Implementation runs as `Workflow` scripts per
Principle V: one scout brief, then haiku agents per phase, with a sonnet diff review per
batch. API, chart and docs phases may start before the design lands; dashboard tasks wait
for it. The website change is a separate `GameplanePanel/website` PR followed by a
submodule bump.

## Complexity Tracking

No constitution violations to justify.
