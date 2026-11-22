# Spec 034 T002: existing tests that need to change

Checked on master `9b80d161`. These are all the existing **Go** tests that encode behavior spec 034 replaces. Every other existing test stays as it is: the SSO re-evaluation tests, the mapping-off "manual promotion must survive re-login" test (`oidc_rolemap_test.go:260-292`), the `resolveOrLinkUser` / `syncUserRole` / `MountRoles` callers (signatures kept), and every e2e test.

Dashboard tests (`AdminSettings*.test.tsx`, `Users*.test.tsx`) are listed separately once the Pencil design lands, because what changes there depends on the design.

Option A is the spec as merged: every sign-in decision uses the new audit reason format. Option B keeps today's audit text for role changes, so syslog/webhook consumers that parse it keep working, and uses the new format only for the new outcomes. B needs a small amendment to `contracts/audit-events.md`.

## 1. `api/internal/auth/oidc_rolemap_test.go` `TestComputeRole` (line 41, call at 111) — A and B

- Before: `role, deny := computeRole(tc.groups, tc.pol)`
- After: `d := decideRole(tc.groups, tc.pol); role, deny := d.Role, d.Deny`
- Why: `computeRole` becomes `decideRole`, which also returns the matched rule (data-model.md). The table cases and expected values do not change.

## 2. `oidc_rolemap_test.go` `TestGetMatchedGroup` (line 462, call at 488) — A and B

- Before: `got := getMatchedGroup(tc.groups, tc.pol)`
- After: `got := decideRole(tc.groups, tc.pol).MatchedGroup()` (returns `"none"` when the fallback applied, as today)
- Why: same replacement. Expected values do not change.

## 3. `oidc_rolemap_test.go` `TestEffectiveHelmPolicy_MostPrivilegedWins` (1041, call at 1060) and `TestEffectiveHelmPolicy_SC008_ByteIdenticalWithNilOverride` (1071, calls at 1085 and 1089) — A and B

- Before: `role, deny := computeRole(groups, effectivePolicy)` (and `directRole, directDeny := computeRole(...)`)
- After: the same through `decideRole(...).Role` and `.Deny`
- Why: same replacement. Expected values and the pointer-equality check do not change. The old per-role override still converts to admin > operator > viewer rules (research R-2), so "most privileged wins" still holds for the old form.

## 4. `oidc_rolemap_test.go` `TestHandleCallback_HelmOverrideMappingsReEvaluateWithoutHelmMappings` (1204), login 2 at 1255-1267 — A and B

- Before: when the last-manager guard skips the demotion, the test asserts **no** audit event (`want still 1`).
- After: the guard skip writes one event with `result=guard_last_manager`, so the count becomes 2 and login 3 expects 3. A also changes the reason assertion at 1282 (see 5).
- Why: FR-016 and SC-005 require every sign-in decision to be explained in the audit log. Today a skipped demotion only shows up as a warning in the server log.

## 5. Reason-string assertions — **A only**

- `oidc_rolemap_test.go` `TestHandleCallback_FirstLoginAuditEventEmitted` (801, checks 836-846): `provider=helm`, `from=new_user`, `to=admin`, `matched=gp-admins` → `provider=helm`, `source=new`, `role=admin`, `group=gp-admins`, `result=applied`
- `oidc_rolemap_test.go` `TestHandleCallback_ReLoginAuditEventEmitted` (853, checks 897-907): `from=viewer`, `to=operator`, `matched=gp-ops` → `source=sso`, `role=operator`, `group=gp-ops`, `result=applied`
- `oidc_rolemap_test.go:1282`: `"oidc role assigned: provider=helm matched=none from=admin to=viewer"` → `"oidc role decision: provider=helm rule=default group=- role=viewer source=sso result=applied"`
- `registry_test.go:336` `TestRegistry_DashboardProviderAuditsRoleAssignment`: `"oidc role assigned: provider=corp matched=corp-admins from=new_user to=admin"` → `"oidc role decision: provider=corp rule=1 group=corp-admins role=admin source=new result=applied"`
- Under B none of these change: role changes keep `oidc role assigned: provider=… matched=… from=… to=…`.

## 6. `oidc_rolemap_test.go` `TestHandleCallback_AuditEventRecordsCorrectPath` (1290, direct calls at 1349-1372) — A and B

- Before: builds a `RoleAssignmentOutcome{PreviousRole, NewRole, Applied, MatchedGroup}` and calls `o.emitRoleAssignmentAudit(...)` twice.
- After: builds the new decision outcome (same fields plus `Result`) and calls `o.emitRoleDecisionAudit(...)`. The path assertions do not change.
- Why: the audit helper now covers refusals and kept/admitted hand-set roles, which the old outcome type cannot express.

## 7. `api/internal/handlers/config_validators_test.go` `TestValidateAuth`, case `"bad defaultRole"` (line 89) — A and B

- Before: `"defaultRole":"root"` must fail with `"defaultRole must be one of"`.
- After: the pure validator accepts any non-blank name (OD-7). The case becomes `"defaultRole":"   "` (blank), which must fail with `"defaultRole must not be blank"`. The new store-aware test T012 checks that an unknown role such as `root` is refused (`server.config.auth_role_unknown`).
- Why: whether a role exists can only be checked against the database, so that check moves to the save path that has the store (research R-5).

## Sign-off

valgul signed off on 2026-11-13 and chose **option A**. Only the seven changes above may be made to existing tests.
