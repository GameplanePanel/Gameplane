# Contract: API surface for server security overrides

**Requirements**: FR-004, FR-005, FR-006, FR-007, FR-008, FR-009, FR-010, SC-003. **Research**: R10, R11, R13. **Data model**: [data-model.md](../data-model.md).

No new route. The override travels in the GameServer object through the existing handlers, which already pass `spec` and `status` through unchanged:

| Route | Permission (rule table) | Security-specific behavior |
|---|---|---|
| `POST /servers` | `servers:write` | Validates `spec.security` as a create (live is absent). |
| `PUT /servers/{name}` | `servers:write` | Validates `spec.security` against the stored object. |
| `POST /servers/{name}:clone` | `servers:write` | The clone carries the source's `spec.security`; validated as a create, so a caller without `servers:security` cannot clone a server that has an override (research R10). |
| `GET /servers`, `GET /servers/{name}` | `servers:read` | Return `spec.security` and `status.security` unmodified. |

(All `/servers` paths take the usual `?cluster=` and namespace parameters.)

The `security` config section is also mirrored by this API into the ConfigMap the operator reads ([cluster-setting.md](cluster-setting.md)). The enforcement lives in `validateAndProtectGameServer` (`api/internal/handlers/resources.go:538`), called from `resources.go:218` (create), `:290` (update) and `lifecycle.go:243` (clone). `MountResources` (`resources.go:51`) gains the audit writer (`*audit.Auditor`) and reads the `security` config section through the `*db.Store` it already receives.

## Request

The body is the GameServer object. Only `spec.security` is described here; its shape is in [crd-gameserver-security.md](crd-gameserver-security.md).

```json
{ "spec": { "templateRef": { "name": "minecraft-java" },
            "security": { "runAsUser": { "value": 2000 },
                          "runAsGroup": { "useImageDefault": true },
                          "volumeOwnershipCheck": "EveryStart" } } }
```

Semantics, in the order the handler applies them (the first failure decides):

| # | Condition | Result |
|---|---|---|
| 1 | `spec.security` deep-equals the stored one (or both are absent) | Accepted; no permission needed. |
| 2 | Absent in the request, present in the stored object, caller lacks `servers:security` | The stored value is copied back into the request (a client that drops the field cannot wipe it by accident); then continues as case 1. |
| 3 | Any other difference, caller lacks `servers:security` on that cluster and namespace | **403** `server.security.forbidden`. Nothing is written. |
| 4 | Shape invalid (id not a whole number, outside 0 to 4294967295, both or neither of `value`/`useImageDefault`, `useImageDefault: false`, unknown member, bad enum) | **422** `server.security.invalid`. |
| 5 | A new or changed root value (see below) and `allowRootOverrides` is false | **403** `server.security.root_disabled`. |
| 6 | Otherwise | Accepted and stored; the response is the stored object. |

A **root value** is `runAsUser` or `runAsGroup` with `value: 0` or `useImageDefault: true`, or `fsGroup` with `value: 0` (FR-005 and the 2026-11-13 clarifications). A root value that is already stored and unchanged does not trigger case 5; removing or replacing one is always allowed. A stored root override is enforced later, by the reconciler, which refuses to start the server while the setting is off (OD-4 settled A, FR-005a; [cluster-setting.md](cluster-setting.md)). The API does not stop the save.

Saving never restarts the server and the response does not say it did (FR-008); `status.security.pending` in a following `GET` reports the queued change.

## Errors

Both bodies follow the existing `httperr` rules (`api/internal/httperr/httperr.go`): plain text, or an RFC 9457 `application/problem+json` document with `messageId` and `args` for callers that ask for it.

| Status | `messageId` | English text | `args` |
|---|---|---|---|
| 403 | `server.security.forbidden` | `changing security settings requires the servers:security permission` | none |
| 403 | `server.security.root_disabled` | `root values (user 0, group 0, filesystem group 0, or the image default for user or group) are not allowed until an administrator enables root overrides` | `{"setting":"runAsUser"}` (the first offending setting) |
| 422 | `server.security.invalid` | `invalid security override: {detail}` | `{"setting":"fsGroup","detail":"must be between 0 and 4294967295"}` |

The three messages are added to `api/internal/messages/entries_server.go` and to `i18n/locales/en/app.json` (FR-014), next to `server.forbidden`.

Existing generic mapping: `writeServerValidationError` (`api/internal/handlers/resources_snapshot.go:24`) maps a plain error to 403; the new errors carry their own status, so it passes them through.

## Response fields the dashboard reads

`status.security` as in [crd-gameserver-security.md](crd-gameserver-security.md). Users without `servers:read` get none of it, as for every server field. `spec.security` is not redacted: the numbers are not secrets.

## Dashboard behavior tied to the contract

- Without `servers:security` (the user's permission set is already known to the dashboard) the Advanced section shows `desired`, `applied`, sources and `pending` read-only (US4 scenario 1) and never sends `spec.security` changes.
- The server refusals above are shown inline; a refused root value says an administrator can allow it in Settings.
- Risky-value confirmation is dashboard-only (OD-6, settled C): the API accepts a risky value from any `servers:security` holder and refuses nothing on that basis; the audit row records that the value was marked risky ([audit-events.md](audit-events.md)).
- A server held back by the reconciler (`SecurityAllowed=False`, `RootOverrideDisallowed`) shows the reason from its status condition, with the text that an administrator can allow root overrides in Settings, or the override can be cleared.

## Tests

- Handler envtest `api/internal/handlers/resources_security_envtest_test.go` (create, update, clone, permission, root rule, invalid shapes, field restore, audit row).
- Unit tests for the validator function and for the three message IDs (`messages_test.go`, `usage_test.go` verify every ID is used and has an English entry).
- E2E: `TestAPI_SecurityOverride_PermissionAndRootRule` (research R16).
