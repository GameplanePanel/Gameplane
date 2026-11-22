# Contract: users and roles API

## `userDTO` (all user responses)

New field `roleSource`: `"sso" | "sso_pending" | "manual"`. Always present. The dashboard
shows it only for users whose `provider` is not `local`.

## `PATCH /users/{id}`

- Body with `role` (existing): unchanged checks (role exists, `allowRoleGrant`,
  `allowTargetChange`, self-demotion and last-manager guards). On success also sets
  `role_source = 'manual'`, even when the role value is unchanged (FR-010).
- New body field `roleSource`: only `"sso"` is accepted. Sets `role_source = 'sso_pending'`
  and leaves `role` unchanged (OD-6). Refusals:
  - `400 server.users.role_source_invalid`: any other value.
  - `400 server.users.role_source_with_role`: `roleSource` and `role` in one request.
  - `400 server.users.role_source_not_sso`: the user has no SSO link.
- `roleSource` needs the same permission and target checks as a role change. Sessions are
  not ended (the role does not change yet).

## `DELETE /roles/{name}`

New refusal before the existing in-use check:
`409 server.roles.in_use_by_sso` with `{provider}` naming the first provider (or `helm`)
whose rules, fallback or override uses the role.
