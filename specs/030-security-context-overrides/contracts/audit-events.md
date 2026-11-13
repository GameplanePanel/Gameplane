# Contract: audit events for security overrides

**Requirements**: FR-005, FR-010, SC-005. **Research**: R11, R12.

No schema change. Rows use the existing `audit_events` columns (`api/internal/audit/audit.go:820-830`): `ts`, `actor`, `method`, `path`, `target`, `status`, `ip`, `reason`. They are hash-chained, forwarded to the stdout, webhook and S3 sinks, shown on the audit page and in the CSV and JSON exports like every other row. The structured detail rides in `reason`, which the chain includes whenever it is non-empty (`audit.go:318-340`).

Each handler writes its row with `Auditor.WriteSync` and then calls `audit.MarkRecorded`, so the request has exactly one row (the capture precedent, `api/internal/handlers/capture.go:1252-1258`). If the explicit write fails, `MarkRecorded` is not called and the middleware's generic row is the fallback, so the trail never has a hole (`audit.go:594-611`).

## Events

| Event | `method` `path` | `target` | `status` | `reason` |
|---|---|---|---|---|
| Override set, changed or removed (create, update, clone) | the request's own, for example `PUT /servers/survival` | server name | the response status (200 or 201) | `security:{"before":{...},"after":{...}}` |
| Refused: permission | the request's own | server name | 403 | `security_forbidden` |
| Refused: root rule | the request's own | server name | 403 | `security_root_disabled:runAsUser` (the first offending setting) |
| Refused: invalid shape | the request's own | server name | 422 | `security_invalid:fsGroup` |
| Cluster setting flipped | `PUT /admin/config/security` | `security` | 200 | `allowRootOverrides:false->true` |

A request that changes other fields but leaves `spec.security` unchanged writes no security row; the generic middleware row stands.

## `reason` for a change

`security:` followed by compact JSON with only the settings that changed. Values use the spec's notation:

| Stored override | JSON value |
|---|---|
| not set | `null` |
| a number | the number |
| use image default | `"image"` |
| ownership check | `"EveryStart"` or `"OnlyWhenWrong"` |

```text
security:{"before":{"runAsUser":null},"after":{"runAsUser":2000}}
security:{"before":{"runAsGroup":1000},"after":{"runAsGroup":"image"}}
security:{"before":{"volumeOwnershipCheck":null},"after":{"volumeOwnershipCheck":"EveryStart"}}
```

On a create `before` is `{}` and `after` lists every setting the request set. The string is at most a few hundred bytes (four small settings). It never contains the module's risky reason or any credential.

If OD-6 is ruled option C, the change row gains `"risky":["runAsUser"]` after `after`; nothing else changes.

## Who, when, before, after (SC-005)

- who: `actor`; when: `ts`; before and after: `reason`; where: `path` and `target`; from where: `ip`.
- The audit page and exports show `reason` today; a table rendering of the JSON is optional follow-up and not required by the spec.

## Tests

- Unit: a function builds `reason` from two security blocks (all four settings, not set, number, image default).
- Handler envtest: each event above produces exactly one row with the expected fields, and the hash chain still verifies (`GET /admin/audit/verify`).
