# Contract: auth config section and Helm flags

## `PUT /config/auth` body (additions only)

```json
{
  "providers": [
    {
      "name": "authentik",
      "kind": "oidc",
      "enabled": true,
      "issuer": "https://auth.example/application/o/gameplane/",
      "clientID": "gameplane",
      "groupsClaim": "groups",
      "roleRules": [
        { "group": "sys-admin", "role": "admin" },
        { "group": "gameplane login only", "role": "login-only" }
      ],
      "defaultRole": "deny"
    }
  ],
  "helmOverride": {
    "roleRules": [ { "group": "ops", "role": "operator" } ],
    "defaultRole": "viewer"
  }
}
```

Validation (refusals are `400` with an i18n message; key names are fixed in the plan):

| Check | Applies to |
|---|---|
| `roleRules[i].group` trimmed, not blank | providers, helmOverride |
| `roleRules[i].role` names an existing role | providers, helmOverride |
| `defaultRole` is `""`, `"deny"` or an existing role | providers, helmOverride |
| `defaultRole` set needs `roleRules` or `roleMappings` | providers (unchanged rule) |
| `roleRules` and `roleMappings` not both present | providers, helmOverride (OD-1) |
| local provider carries none of these fields | providers (unchanged rule) |

Role existence needs the store, so the auth validator gains store access (today it is a
pure `func([]byte)`).

## `GET /config/auth`

Returns the row as stored. `installTimeSettings.oidcHelmProvider` gains `roleRules` (the
effective Helm seed as an ordered list, whether it came from `--oidc-role-rules` or the old
per-role flags), alongside the existing `roleMappings` for one release so older dashboards
still render.

## Helm values and API flags

```yaml
api:
  oidc:
    defaultRole: deny            # "", deny, or any role name
    roleRules:
      - group: sys-admin
        role: admin
      - group: gameplane login only
        role: login-only
    roleMappings: {}             # old form; must stay empty when roleRules is set
```

- Chart: `{{- with .Values.api.oidc.roleRules }}- {{ printf "--oidc-role-rules=%s" (toJson .) | quote }}{{- end }}`.
- Chart fails rendering (`fail`) when both `roleRules` and any `roleMappings` list are set.
- API: `--oidc-role-rules` / `GAMEPLANE_OIDC_ROLE_RULES` is a JSON array of `{group, role}`;
  malformed JSON, blank group or blank role exits at startup. Both new and old flags set
  exits at startup (OD-1).
