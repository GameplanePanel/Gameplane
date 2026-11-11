# Data model: spec 034 SSO group role rules

## Role rule (config, JSON)

| Field | Type | Rules |
|---|---|---|
| `group` | string | Required, trimmed, not blank. Matched exactly (case-sensitive) against the groups claim values. |
| `role` | string | Required. Must name a row in `roles` at save time (dashboard); checked again at sign-in (Helm). |

A provider's rules are an ordered array. Order is priority: the first rule whose group the
user is in wins. Duplicate groups are allowed but the dashboard warns that later ones never
match.

## Provider role policy (auth config row, `providers[]`)

Existing fields kept: `scopes`, `groupsClaim`, `defaultRole`, `roleMappings` (old form).

New field: `roleRules: RoleRule[]`.

| Situation | Meaning |
|---|---|
| `roleRules` set (even empty array) and `roleMappings` absent | New form. Empty array = mapping off. |
| `roleMappings` set and `roleRules` absent | Old form. Read as rules: every `admin` group → admin, then every `operator` group → operator, then every `viewer` group → viewer. Same result as today. |
| Both set | Invalid: save refused (OD-1). |
| Neither set | Mapping off (today's behaviour): new users get viewer, sign-in never touches roles. |

`defaultRole`: `""` (viewer), `"deny"`, or the name of any existing role (OD-7). Still
requires rules or old mappings to be set.

## Helm provider

Helm values `api.oidc.roleRules: [{group, role}]` beside the old `api.oidc.roleMappings`.
The chart passes rules as one JSON flag, `--oidc-role-rules` (env
`GAMEPLANE_OIDC_ROLE_RULES`), so group names with commas or spaces survive. If both the new
flag and any `--oidc-role-mapping-*` flag are set, the API exits at startup with an error
(OD-1). `--oidc-default-role` accepts any role name or `deny`; whether that role exists is
checked at sign-in (OD-2).

## Helm override (auth config row, `helmOverride`)

New fields: `roleRules: RoleRule[]` and `defaultRole: string`.

- `roleRules` present → it replaces the whole Helm rule list (OD-3). `defaultRole` present
  → it replaces the Helm fallback.
- Old `helmOverride.roleMappings` (per-role) still loads and behaves as today; the next
  dashboard save writes the effective list as `roleRules` and drops `roleMappings`. Setting
  both is refused.
- "Reset to Helm default" removes `roleRules`, `defaultRole` and `roleMappings` from the
  override.

## User role source (database)

Migration `api/internal/db/migrations/common/020_user_role_source.sql` (portable SQL):

```sql
ALTER TABLE users ADD COLUMN role_source TEXT NOT NULL DEFAULT 'manual';
UPDATE users SET role_source = 'sso' WHERE id IN (SELECT user_id FROM oidc_links);
```

| Value | Meaning | Set by |
|---|---|---|
| `sso` | Sign-in applies the rule result. | First SSO sign-in (new user); migration for SSO-linked users (OD-4); sign-in after `sso_pending`. |
| `sso_pending` | Returned to SSO control, not signed in since. Keeps current role; next sign-in applies rules and moves it to `sso`. | "Return to SSO control" (OD-6). |
| `manual` | Sign-in never changes the role and never refuses the user (OD-5). | Any admin role change via the users API; local users; migration for users with no SSO link. |

## Sign-in decision (per login)

Inputs: the user's groups, the effective policy, the user's stored `role_source` (none for a
new user).

1. Walk the rules; take the first match, else the fallback.
2. Result is one of: `role R` (exists), `deny` (fallback is deny), `missing R` (the matched
   rule or fallback names a role that does not exist).
3. New user: `role R` → create with R and `sso`; `deny` or `missing` → refuse (403, no user
   created).
4. Existing `sso` / `sso_pending`: `role R` → apply R (last-manager guard unchanged), set
   `sso`; `deny` or `missing` → refuse, role and source unchanged.
5. Existing `manual`: always let in with the stored role; nothing written to the user row.
6. Mapping off: today's behaviour (new → viewer, existing untouched, source untouched).

Every outcome writes one audit event (see `contracts/audit-events.md`).

## Role deletion guard

`DELETE /roles/{name}` is refused with `server.roles.in_use_by_sso` while the name appears in
any provider's `roleRules` or `defaultRole`, the old `roleMappings` (built-in roles only, so
never), `helmOverride.roleRules` / `helmOverride.defaultRole`, or the Helm policy loaded at
startup.
