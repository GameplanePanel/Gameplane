# Tasks: spec 034 SSO group role rules

Legend: `[P]` can run in parallel with other `[P]` tasks in the same phase. Verification is
CI only (core rule 8); local checks are `go build ./...` and `npx tsc --noEmit`.

## Phase 0: Gate

- [ ] T001 Merge this spec PR (docs only).
- [ ] T002 List every existing test that asserts behaviour this spec changes (hand-set role
  overwritten at sign-in, fallback limited to admin/operator/viewer, `computeRole`
  signature, `emitRoleAssignmentAudit` silence on guard skip, role delete with no SSO
  check). Get valgul's written sign-off naming each file and change before touching it.

## Phase 1: Design (valgul's machine)

- [ ] T003 Run the spec 034 design prompt with the local agent: rule
  editor (provider form + Helm override card + read-only Helm card), role-source badge and
  "Return to SSO control" on Users; export to `assets/design-export/`; valgul signs off.

## Phase 2: API core (US1, US2, US4)

- [ ] T004 Migration `api/internal/db/migrations/common/020_user_role_source.sql` per
  data-model.md (OD-4).
- [ ] T005 `RoleRule`, `Provider.RoleRules`, `ProviderPolicy.Rules`, `rulesFromMappings`,
  `decideRole` in `api/internal/auth/registry.go` and `oidc.go`; `effectiveHelmPolicy` per
  OD-3.
- [ ] T006 Sign-in flow per data-model.md "Sign-in decision": role-source lookup before
  deny, missing-role refusal without fall-through (OD-2), hand-set users always admitted
  (OD-5), `sso_pending` → `sso` (OD-6), source written with the role.
- [ ] T007 `emitRoleDecisionAudit` per `contracts/audit-events.md`.
- [ ] T008 [P] Unit tests for `decideRole`, `rulesFromMappings`, `effectiveHelmPolicy` and
  the sign-in table (every row of data-model.md), in new test functions.

## Phase 3: Config, roles, users, flags (US1, US3, US4)

- [ ] T009 Auth validator with store access: `roleRules`, any-role `defaultRole`, both-forms
  refusal (OD-1), override `roleRules`/`defaultRole` (OD-3); `installTimeSettings` gains
  `roleRules`.
- [ ] T010 [P] Role delete refusal `server.roles.in_use_by_sso` (OD-2).
- [ ] T011 [P] Users API: `roleSource` in DTO, `manual` on role change (FR-010), `roleSource:
  "sso"` → `sso_pending` with the three refusals (OD-6).
- [ ] T012 [P] `--oidc-role-rules` flag/env, both-forms startup error, any-role default.
- [ ] T013 [P] i18n message keys for the new refusals in `i18n/locales/en/app.json`.
- [ ] T014 Envtest handler tests for T009–T011 (new test functions).

## Phase 4: Chart

- [ ] T015 `values.yaml` `api.oidc.roleRules` + any-role `defaultRole` docs;
  `templates/api.yaml` `toJson` flag and `fail` on both forms.

## Phase 5: Dashboard (after T003)

- [ ] T016 Types in `web/src/lib/config.ts` and `web/src/lib/api.ts`.
- [ ] T017 Rule editor in `AdminSettings.tsx` (provider form, Helm override card, read-only
  Helm card), old-form → rules on save, duplicate-group warning.
- [ ] T018 Users page badge and "Return to SSO control" action.
- [ ] T019 Vitest coverage for T017–T018 and MSW handlers in `web/src/test/handlers.ts`.
- [ ] T020 Browser smoke test of both screens against the exported design.

## Phase 6: E2E and docs

- [ ] T021 E2E in the `api-auth` bucket (fake OIDC): custom-role rule, hand-set role kept,
  return to SSO control applies at next sign-in, Helm-style missing role refused; register
  in `test/e2e/buckets.sh`, stay within the bucket's login budget.
- [ ] T022 [P] `docs/oidc.md`, `docs/security.md`, `api/specs.md`, `web/specs.md`,
  `CHANGELOG.md`.
- [ ] T023 [P] Website docs PR (`users-auth-rbac.mdx`, `helm-values-reference.mdx`,
  `user-administration.mdx`), then submodule bump after it merges.

## Phase 7: Close

- [ ] T024 CI green on the implementation PR, every review comment fixed, valgul approves
  and merges.
- [ ] T025 Archive to `specs/done_034-sso-group-role-rules`.
