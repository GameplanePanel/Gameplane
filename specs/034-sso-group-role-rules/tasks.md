# Tasks: Map SSO groups to any role, and keep roles set by hand

**Input**: Design documents from `/specs/034-sso-group-role-rules/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md
(all merged: spec in #929, plan/research/quickstart in #950).

**Tests**: Required. Constitution Principle I needs E2E coverage, and the repo coverage
gates (api 80, web 92/76/82/92) need unit, envtest and Vitest tests. Every test task below
adds **new** test functions or files. Changing an **existing** test needs valgul's written
sign-off from T002 first (CLAUDE.md override 1). Tests run in CI only (Principle VI); the
only local checks are `go build ./...` and `npx tsc --noEmit`.

**Organization**: by user story, in spec priority order: US1, US2 and US4 (all P1), then
US3 (P2). US4 (compatibility) comes after US2 because its upgrade check needs the role
source.

**Delivery**: one PR on `feat/034-sso-group-role-rules`, implemented through `Workflow`
scripts (Principle V): haiku agents with an explicit `model:`, then one sonnet diff review
per batch.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task).
- **[Story]**: US1 to US4 from spec.md. Setup, Foundational and Polish tasks carry no story.

---

## Phase 1: Setup

**Purpose**: the branch, the test sign-off gate and the design gate.

- [ ] T001 Create branch `feat/034-sso-group-role-rules` from `master`. If valgul's local agent already pushed the design commit (T003) there, base on that branch instead.
- [ ] T002 List every existing test that encodes behavior this spec replaces, with file, test name, the assertion and the needed change, and get valgul's written sign-off on each before editing. Candidates found by grep on master `4c96b1ff`: `api/internal/auth/oidc_test.go`, `oidc_rolemap_test.go`, `oidc_db_test.go`, `oidc_profile_test.go` and `oidc_management_lock_test.go` (these use `computeRole`, `getMatchedGroup`, `resolveOrLinkUser`, `RoleAssignmentOutcome`, `emitRoleAssignmentAudit`); `api/internal/handlers/config_validators_test.go` and `config_test.go` (fallback limited to viewer/operator/admin/deny); `roles_*_test.go` and `users_*_test.go` (delete guard, `userDTO` shape); `web/src/routes/AdminSettings*.test.tsx` and `Users*.test.tsx`; `test/e2e/api_auth_e2e_test.go` (role-mapping e2e). The mapping-off test at `oidc_rolemap_test.go:260-292` ("manual promotion must survive re-login") stays valid unchanged. Write the list as a sign-off card, with before and after shown for each test.
- [ ] T003 Design step on valgul's machine: run the spec 034 local-agent design prompt (rule editor in the provider form, the Helm override card and the read-only Helm card; the role-source badge and "Return to SSO control" on Users), export to `assets/design-export/{json,screenshots}/`, and get valgul's sign-off. Blocks T024, T025, T033 and T042.

**Checkpoint**: sign-off list approved. Back-end phases may start; dashboard tasks wait for T003.

---

## Phase 2: Foundational (blocks every story)

**Purpose**: the rule model, the role-source column and the decision function every story uses.

- [ ] T004 [P] Add migration `api/internal/db/migrations/common/020_user_role_source.sql` exactly as in data-model.md "User role source" (`ALTER TABLE users ADD COLUMN role_source TEXT NOT NULL DEFAULT 'manual'`; `UPDATE users SET role_source = 'sso' WHERE id IN (SELECT user_id FROM oidc_links)`). Use portable SQL only (see `api/internal/db/migrations/README.md`).
- [ ] T005 [P] In `api/internal/db/rbac.go`, add `RoleSource(ctx, userID) (string, error)` and `SetRoleSource(ctx, ex Execer, userID, source string) error`, accepting only `sso`, `sso_pending` and `manual` and wrapping errors with `%w`.
- [ ] T006 [P] In `api/internal/auth/registry.go`, add `RoleRule{Group, Role string}` (JSON `group`, `role`) and `Provider.RoleRules *[]RoleRule` (`json:"roleRules,omitempty"`), and keep `RoleMappings`. Add `rulesFromMappings(*RoleMappings) []RoleRule`, which emits every admin group, then operator, then viewer (research R-2).
- [ ] T007 In `api/internal/auth/oidc.go`, add `ProviderPolicy.Rules []RoleRule` and `ProviderPolicy.MappingOn bool`. Every place that builds a policy (registry build and `api/cmd/main.go` around lines 206-219) fills `Rules` from `RoleRules`, or from `rulesFromMappings` for the old form. `MappingOn` is true when either form is set.
- [ ] T008 In `api/internal/auth/oidc.go`, replace `computeRole` and `getMatchedGroup` with `decideRole(groups []string, pol *ProviderPolicy) Decision`, where `Decision` is `{RuleIndex int, Group, Role string, Deny bool}`: first matching rule wins, `RuleIndex` is -1 for the fallback, `DefaultRole` "" means viewer and "deny" sets Deny. It does no role-existence check.
- [ ] T009 In `api/internal/auth/oidc.go`, replace `emitRoleAssignmentAudit` with `emitRoleDecisionAudit`, which writes the reason format and result values from `contracts/audit-events.md`, including refusals (403) and `guard_last_manager`. It still writes nothing when `auditWriteSync` is nil.
- [ ] T010 [P] Add the new message keys to `i18n/locales/en/app.json` with descriptions: `server.roles.in_use_by_sso` (`{provider}`), `server.users.role_source_invalid`, `server.users.role_source_with_role`, `server.users.role_source_not_sso`, `server.config.auth_role_unknown` (`{role}`), and `server.config.auth_both_mapping_forms`.
- [ ] T011 [P] New unit tests in `api/internal/auth/oidc_rolerules_test.go`: `rulesFromMappings` order, `decideRole` (first match, order over privilege, fallback, deny, empty rules), and the old form giving the same role as today's precedence for every combination of the three lists.

**Checkpoint**: `go build ./...` passes in `api/`, and the foundation compiles with the old call sites switched over.

---

## Phase 3: User Story 1, map an IdP group to a custom role (P1) 🎯 MVP

**Goal**: a rule can grant any existing role, in list order, from the dashboard and from Helm.

**Independent Test**: create role `login-only`, add rule `gameplane login only → login-only`, sign in as a member, and confirm the new account has role `login-only` (quickstart.md §1).

### Tests for User Story 1

- [ ] T012 [P] [US1] New envtest tests in `api/internal/handlers/config_rolerules_envtest_test.go`: save accepts `roleRules` naming a custom role; refuses an unknown role (`server.config.auth_role_unknown`); refuses old keys plus rules (OD-1); accepts any existing role or `deny` as `defaultRole` (OD-7); `helmOverride.roleRules` and `helmOverride.defaultRole` round-trip (OD-3); `installTimeSettings.oidcHelmProvider.roleRules` is present.
- [ ] T013 [P] [US1] New envtest test in `api/internal/handlers/roles_sso_guard_envtest_test.go`: deleting a role used by a provider rule, a provider `defaultRole`, `helmOverride` or the Helm policy returns 409 `server.roles.in_use_by_sso` naming the provider.
- [ ] T014 [P] [US1] New unit tests in `api/internal/auth/oidc_signin_table_test.go` for the new-user rows of the data-model.md "Sign-in decision" table: create with the rule's role and source `sso`; refuse on deny; refuse with `refused_missing_role` when the matched rule's role is missing, without trying later rules (OD-2).

### Implementation for User Story 1

- [ ] T015 [US1] In `api/internal/handlers/config.go`, add `authRoleRule`, `authProvider.RoleRules *[]authRoleRule`, `authHelmOverride.RoleRules *[]authRoleRule` and `authHelmOverride.DefaultRole *string`. Trim groups, refuse blank group or role, refuse old keys plus rules on providers and on the override (OD-1), and replace `validDefaultRoles` with "empty, `deny`, or a role that `RoleExists`".
- [ ] T016 [US1] Give the auth validator store access: change `newValidators` in `api/internal/handlers/config.go:458` to take `*db.Store` and pass it into `validateAuth`, then update `MountConfig`, `MountConfigWithTelemetry` and the caller at `api/cmd/main.go:393` (research R-5).
- [ ] T017 [US1] In `api/internal/handlers/config.go` `buildInstallTimeSettings`, add `roleRules` (the effective Helm seed as an ordered list) to `oidcHelmProvider`, and keep `roleMappings` for one release.
- [ ] T018 [US1] In `api/internal/auth/oidc.go` `effectiveHelmPolicy`, make override `roleRules` replace the whole Helm list and override `defaultRole` replace the fallback (OD-3). Keep the old per-role override merge, then convert it with `rulesFromMappings`.
- [ ] T019 [US1] In the `api/internal/auth/oidc.go` callback, for a **new** user: run `decideRole`, check the role with `db.RoleExists`, and either refuse with the existing generic 403 and audit (`refused_deny` or `refused_missing_role`), or create the user with the role and `role_source = 'sso'` in `resolveOrLinkUser`'s insert transaction.
- [ ] T020 [US1] In `api/internal/handlers/roles.go`, change `MountRoles` to take the Helm policy (update `api/cmd/main.go:390`). In `del`, before the binding count, read the `auth` config row and refuse with 409 `server.roles.in_use_by_sso` when the role is referenced (data-model.md "Role deletion guard").
- [ ] T021 [P] [US1] In `api/cmd/main.go`, add `--oidc-role-rules` and env `GAMEPLANE_OIDC_ROLE_RULES`, a JSON array of `{group, role}` (research R-3). Exit at startup on bad JSON, a blank group or role, or the new flag combined with any `--oidc-role-mapping-*` flag. `--oidc-default-role` accepts any non-blank name or `deny`.
- [ ] T022 [P] [US1] In `charts/gameplane/values.yaml`, document `api.oidc.roleRules` (list of `{group, role}`) and the any-role `defaultRole`. In `charts/gameplane/templates/api.yaml` next to lines 296-316, render `--oidc-role-rules` with `toJson`, and `fail` with a message naming both keys when `roleRules` and any `roleMappings` list are both set.
- [ ] T023 [P] [US1] Add `AuthRoleRule`, `roleRules` on `AuthProvider` and `HelmRoleOverride`, `defaultRole` on `HelmRoleOverride`, and `AuthDefaultRole = "deny" | string` in `web/src/lib/config.ts`.
- [ ] T024 [US1] Following the T003 export, build the ordered rule editor in `web/src/routes/AdminSettings.tsx`: group field, role select from `RolesAPI.list()`, move up and down, remove, add, a warning on a duplicate group, and a fallback select with "Refuse sign-in". Use it in the provider form and the Helm override card (with "Reset to Helm default"). The read-only Helm card lists rules in order. Saving an old-form provider writes `roleRules`.
- [ ] T025 [US1] New Vitest file `web/src/routes/AdminSettings_rolerules.test.tsx` and MSW additions in `web/src/test/handlers.ts`: add, reorder and remove rules; duplicate warning; custom roles listed; old-form provider saved as `roleRules`; override reset.
- [ ] T026 [US1] New E2E file `test/e2e/api_sso_role_rules_e2e_test.go` (`//go:build e2e`, `t.Parallel()`, unique names), set up per research R-9: one `e2e-admin` login, a custom role, a labelled client Secret, and the test's own dashboard OIDC provider against the in-cluster fake IdP. Subtests: custom-role rule, rule order, deny fallback refused, unknown-role save refused, delete of a used role refused.

**Checkpoint**: US1 works alone. SSO users get custom roles, and roles set by hand are still overwritten (fixed in US2).

---

## Phase 4: User Story 2, a role set by hand survives sign-in (P1)

**Goal**: an admin's role change is never undone by sign-in, and SSO-managed users still sync and get demoted.

**Independent Test**: with rules on, set an SSO user's role by hand, sign in as them, and confirm the role is unchanged and the audit says `kept_manual` (quickstart.md §2).

### Tests for User Story 2

- [ ] T027 [P] [US2] In `api/internal/auth/oidc_signin_table_test.go`, add the existing-user rows: `sso` user synced (`applied`, `unchanged`); `manual` user kept (`kept_manual`); `manual` user admitted on deny or a missing role (`admitted_manual`, OD-5); `sso` user refused on deny or a missing role, with role and source unchanged; last-manager guard (`guard_last_manager`).
- [ ] T028 [P] [US2] New envtest test in `api/internal/handlers/users_rolesource_envtest_test.go`: `PATCH /users/{id}` with `role` sets `roleSource: "manual"`, also when the value is unchanged; every user response carries `roleSource`.

### Implementation for User Story 2

- [ ] T029 [US2] In `api/internal/handlers/users.go`, select `role_source` in every user query, add `RoleSource string \`json:"roleSource"\`` to `userDTO` (around line 59), and in `PATCH` set `role_source = 'manual'` with `db.SetRoleSource` in the same transaction as the role update (around lines 672-691).
- [ ] T030 [US2] In the `api/internal/auth/oidc.go` callback, move the deny handling after the linked-user lookup (today it is at lines 462-468, research R-7), then apply the existing-user rows of the data-model.md table: a `manual` user keeps the stored role and is always admitted; an `sso` or `sso_pending` user gets the rule result, or is refused on deny or a missing role.
- [ ] T031 [US2] In `api/internal/auth/oidc.go` `syncUserRole` (around line 625), write `role_source = 'sso'` in the same transaction as the role and cluster binding update, keep the last-manager guard, and report `guard_last_manager` to `emitRoleDecisionAudit`.
- [ ] T032 [P] [US2] Add `roleSource: "sso" | "sso_pending" | "manual"` to the user type in `web/src/lib/api.ts`.
- [ ] T033 [US2] Following the T003 export, in `web/src/routes/Users.tsx`, show the "From SSO", "Set by hand" or "From SSO (applies at next sign-in)" badge next to the role for users whose `provider` is not `local`, and add the helper under the role select in the edit dialog for SSO users.
- [ ] T034 [US2] New Vitest file `web/src/routes/Users_rolesource.test.tsx`: the badge for each source, no badge for local users, and the edit-dialog helper.
- [ ] T035 [US2] Add subtests to `test/e2e/api_sso_role_rules_e2e_test.go`: set a user's role by hand, then sign in again and see the role kept; the same user in no group with a deny fallback is still admitted; an SSO-managed user removed from every group is refused.

**Checkpoint**: US1 and US2 work together, and roles set by hand are stable.

---

## Phase 5: User Story 4, existing configurations keep working (P1)

**Goal**: upgrading changes nobody's role, and old-form configs sign in exactly as before.

**Independent Test**: migrate a database that has SSO-linked and local users, and confirm the role sources match OD-4 and no roles changed; an old-form policy gives the same roles as today (quickstart.md §4).

- [ ] T036 [P] [US4] New test in `api/internal/db/migrations_rolesource_test.go`: on SQLite (and on PostgreSQL where `db_postgres_test.go` runs), users with an `oidc_links` row become `sso`, other users `manual`, and `role` is unchanged.
- [ ] T037 [P] [US4] New unit test in `api/internal/auth/oidc_rolerules_test.go`: a Helm policy built from only `--oidc-role-mapping-*` flags and a dashboard provider with only `roleMappings` decide the same role as the pre-change precedence table for a fixed set of group combinations (FR-019).
- [ ] T038 [P] [US4] New envtest test in `api/internal/handlers/config_rolerules_envtest_test.go`: a stored old-form provider still loads, and `GET /config/auth` returns it unchanged until it is saved again.
- [ ] T039 [US4] Add `helm template` checks to the chart job in `.github/workflows/ci.yaml`, next to the existing ones around lines 1291-1400. One check confirms that setting both `roleRules` and a `roleMappings` list fails to render, using the same pattern as the negative monitoring check at line 1391. Another confirms that old-form-only values still render the same `--oidc-role-mapping-*` flags as today, and that `roleRules` renders one `--oidc-role-rules` JSON flag.

**Checkpoint**: US1, US2 and US4 are done, and the feature can ship without US3.

---

## Phase 6: User Story 3, hand a user back to SSO control (P2)

**Goal**: an admin can return a hand-set SSO user to SSO control, and it applies at their next sign-in.

**Independent Test**: on a hand-set SSO user, use "Return to SSO control", check `roleSource: "sso_pending"`, sign in as them, and confirm the rule result applies and the source is `sso` (quickstart.md §3).

### Tests for User Story 3

- [ ] T040 [P] [US3] Extend `api/internal/handlers/users_rolesource_envtest_test.go` with new test functions: `roleSource: "sso"` sets `sso_pending` and leaves `role` alone; refusals `role_source_invalid`, `role_source_with_role` and `role_source_not_sso`; the same permission and target checks as a role change; sessions are not ended.
- [ ] T041 [P] [US3] In `api/internal/auth/oidc_signin_table_test.go`, add a test that an `sso_pending` user gets the rule result and the source moves to `sso` (`applied` or `unchanged` with an event).

### Implementation for User Story 3

- [ ] T042 [US3] In `api/internal/handlers/users.go` `PATCH`, accept `roleSource` per `contracts/users-api.md`: only `"sso"`, not combined with `role`, user must have an `oidc_links` row, then `db.SetRoleSource(..., "sso_pending")` under `LockUserManagement`, behind `allowTargetChange` and the users-management permission.
- [ ] T043 [US3] In the `api/internal/auth/oidc.go` sign-in flow, treat `sso_pending` like `sso` and write `sso` after a successful decision (OD-6).
- [ ] T044 [P] [US3] Accept `roleSource: "sso"` in the users patch client in `web/src/lib/api.ts`.
- [ ] T045 [US3] Following the T003 export, in `web/src/routes/Users.tsx`, add "Return to SSO control" (row menu and edit dialog) for SSO users with source `manual`, with the confirm dialog text from the design, and refresh the users query on success.
- [ ] T046 [US3] Add tests for the action, the confirm dialog, the pending badge and no action for local users to `web/src/routes/Users_rolesource.test.tsx`.
- [ ] T047 [US3] Add a subtest to `test/e2e/api_sso_role_rules_e2e_test.go`: return the hand-set user to SSO control, check `sso_pending`, sign in, and check the rule result and `sso`.

**Checkpoint**: all four stories work on their own and together.

---

## Phase 7: Polish and cross-cutting

- [ ] T048 Register `TestAPI_SSORoleRules` in `test/e2e/buckets.sh`, in an API bucket with admin-login headroom and no other `/admin/config/auth` writer (research R-9), and update that bucket's tally comment.
- [ ] T049 [P] Update `docs/oidc.md` (rules, order, an Authentik example with `login-only`, role source, the return action) and `docs/security.md` (rewrite "Re-evaluation on Every Login" for role source, refusals and audit reasons).
- [ ] T050 [P] Update `api/specs.md` (auth: rules, role source, sign-in table, new refusals) and `web/specs.md` (rule editor, role-source badge) in the same PR (Principle IV).
- [ ] T051 [P] Add a `CHANGELOG.md` entry: custom-role mapping, hand-set roles kept, new Helm `api.oidc.roleRules`, and that old keys still work.
- [ ] T052 Browser smoke test of Admin Settings and Users against the T003 screenshots (CLAUDE.md core rule 13, sonnet), in parallel with the diff review.
- [ ] T053 Open a `GameplanePanel/website` PR updating `src/content/docs/users-auth-rbac.mdx`, `helm-values-reference.mdx` and `user-administration.mdx`; after it merges, bump the `website` submodule pointer in the main repo.
- [ ] T054 Push, open the PR with `type: feature` and `area: api`, `area: web`, `area: chart`, `area: e2e` labels, and drive CI green with every review comment fixed. Run the quickstart.md scenarios through the E2E and envtest results, not locally.
- [ ] T055 After merge, archive with `git mv specs/034-sso-group-role-rules specs/done_034-sso-group-role-rules` and update references (the `spec-archive` skill).

---

## Dependencies and execution order

### Phase dependencies

- **Setup (Phase 1)**: T001 first. T002 gates any edit to an existing test file. T003 runs on valgul's machine and gates only the dashboard tasks (T024, T025, T033, T034, T045, T046, T052).
- **Foundational (Phase 2)**: after T001; blocks every story. T004, T005, T006, T010 and T011 run in parallel; T007 needs T006; T008 needs T007; T009 needs T008.
- **US1 (Phase 3)**: after Phase 2.
- **US2 (Phase 4)**: after Phase 2. It touches the same callback as T019, so T030 runs after T019.
- **US4 (Phase 5)**: after T004 (migration) and T007 (policy build); best run after US2, so the role source is in place.
- **US3 (Phase 6)**: after US2 (it needs `roleSource` in the DTO, T029, and the sign-in source handling, T030 and T031).
- **Polish (Phase 7)**: after the stories it documents. T048 runs together with T026, T035 and T047.

### User story dependencies

- **US1 (P1)**: needs only the foundation.
- **US2 (P1)**: needs the foundation; shares `oidc.go` with US1 (sequential edits, independent behavior).
- **US4 (P1)**: needs the foundation and the migration; checks US1 and US2 together.
- **US3 (P2)**: needs US2.

### Within each story

New tests are written with or before the code they cover and must fail without it. Order: server model and helpers, then handlers and sign-in flow, then client types, then UI, then E2E.

---

## Parallel examples

### Phase 2

```text
T004 migration 020 · T005 rbac.go helpers · T006 registry.go RoleRule · T010 i18n keys · T011 unit tests file
```

### User Story 1

```text
Tests:  T012 config envtest · T013 roles guard envtest · T014 sign-in table (new user)
Code:   T021 main.go flag · T022 Helm chart · T023 web config types   (alongside T015–T020)
```

### User Story 2

```text
T027 sign-in table (existing users) · T028 users envtest · T032 web user type
```

### User Story 3

```text
T040 users envtest · T041 sign-in table (pending) · T044 web patch client
```

---

## Implementation strategy

### MVP first (User Story 1)

1. Phase 1 (T001, T002; T003 runs in parallel on valgul's machine).
2. Phase 2 foundation.
3. Phase 3 US1 back end (T012–T022), then the dashboard once T003 lands.
4. Stop and check: CI green on the US1 tests.

### Incremental delivery

1. Foundation + US1: custom-role mapping (fixes the first reported limit).
2. US2: hand-set roles kept (fixes the second reported limit).
3. US4: compatibility proven; this is the minimum shippable set (all P1).
4. US3: the return action (P2).
5. Polish: docs, website, E2E registration, archive.

### Workflow batches (Principle V)

One scout brief per batch (exact `file:line`, before and after), haiku implementers per task
group, one sonnet reviewer per batch checking `git diff` against the brief. Suggested
batches: [Phase 2], [US1 back end], [US2 + US4 back end], [US3 back end], [all dashboard
tasks after T003], [E2E + docs].
