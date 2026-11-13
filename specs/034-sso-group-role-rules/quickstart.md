# Quickstart: validate spec 034

How to prove the feature works end to end. In CI this is the E2E test from
[research.md](research.md) R-9. The manual run below uses a Kind dev cluster
(`make dev-up`) with an OIDC provider that sends a `groups` claim (Authentik, Keycloak, or
the E2E fake IdP).

Contracts: [auth-config.md](contracts/auth-config.md),
[users-api.md](contracts/users-api.md), [audit-events.md](contracts/audit-events.md).
Decision table: [data-model.md](data-model.md), "Sign-in decision".

## Prerequisites

- A Gameplane install built from the feature branch (`make images dev-load dev-install`).
- An admin session (local admin or an SSO admin).
- IdP users: `alice` in groups `sys-admin` and `gameplane login only`, `bob` in
  `gameplane login only` only, and `carol` in no group.

## 1. Custom role as a rule target (US1)

1. Create a custom role `login-only` with no permissions (Users → Roles).
2. In Admin Settings, add or edit the SSO provider with rules
   `sys-admin → admin`, `gameplane login only → login-only`, and fallback "Refuse sign-in".
3. Sign in as `bob`. **Expect**: account created with role `login-only`, role source
   "From SSO"; audit reason `rule=2 … result=applied`.
4. Sign in as `alice`. **Expect**: role `admin` (rule 1 is first).
5. Move the `login-only` rule to the top and sign in as `alice` again. **Expect**: role
   `login-only` (order decides, not privilege).
6. Sign in as `carol`. **Expect**: generic "login not permitted", no user created; audit
   `result=refused_deny`.
7. Try to save a rule pointing at role `does-not-exist`. **Expect**: 400, nothing saved.
8. Try to delete role `login-only`. **Expect**: 409 `server.roles.in_use_by_sso`.

## 2. A role set by hand survives sign-in (US2)

1. On the Users page, change `bob` to `operator`. **Expect**: badge "Set by hand".
2. Sign in as `bob`. **Expect**: still `operator`; audit `result=kept_manual`.
3. Remove `bob` from every IdP group and sign in. **Expect**: let in as `operator` despite
   the "Refuse sign-in" fallback (OD-5); audit `result=admitted_manual`.

## 3. Return to SSO control (US3)

1. On `bob`, choose "Return to SSO control" and confirm. **Expect**: role still `operator`,
   badge "From SSO (applies at next sign-in)", `roleSource: "sso_pending"` from
   `GET /users`.
2. Put `bob` back in `gameplane login only` and sign in. **Expect**: role `login-only`,
   badge "From SSO".
3. Try the action on a local (password) user via the API. **Expect**: 400
   `server.users.role_source_not_sso`; the dashboard shows no badge or action for them.

## 4. Upgrade keeps everything (US4)

1. On a `master` build, configure Helm `api.oidc.roleMappings.admin: [sys-admin]` and
   `viewer: [gameplane login only]`, sign in as `alice` and `bob`, and set a local user's
   role by hand.
2. Upgrade to the feature build. **Expect**: no role changes; `alice` and `bob` show "From
   SSO", the local user shows no badge (`roleSource: "manual"`).
3. Sign in as `alice` and `bob` again. **Expect**: same roles as before the upgrade.
4. Set both `roleMappings` and `roleRules` in Helm values and run `helm upgrade`.
   **Expect**: the chart fails to render with a message naming both keys.

## 5. Helm typo fails closed (unit-tested; manual only)

1. Set Helm `api.oidc.roleRules: [{group: sys-admin, role: admn}]` and upgrade.
2. Sign in as a new SSO user in `sys-admin`. **Expect**: refused; audit
   `result=refused_missing_role` naming rule 1 and role `admn`. An existing hand-set user in
   the same group still gets in.
