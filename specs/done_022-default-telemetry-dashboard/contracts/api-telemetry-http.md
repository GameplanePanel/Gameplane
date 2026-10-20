# Contract: Gameplane API telemetry endpoints

**Requirements**: FR-003–FR-005, FR-012, FR-017–FR-019, FR-035, FR-037. **Research**: R10–R13, R18, R20.

All routes are behind session auth. RBAC entries are added to `api/internal/rbac/rbac.go` before the `{segment: "admin", perm: "*"}` fallback:

```go
{method: "GET", segment: "admin", prefix: "/admin/telemetry", perm: "config:read"},
{segment: "admin", prefix: "/admin/telemetry", perm: "config:manage"},
```

## `GET /admin/telemetry` (`config:read`)

```json
{
  "destination": { "kind": "default", "host": "telemetry.example.org" },
  "operatorDisabled": false,
  "consent": { "basic": true, "extended": true, "source": "default" },
  "installId": "3f1c2a9e-8b4d-4e57-9a61-0c2d7e5b9f10",
  "preview": { "version": "0.3.0", "servers": 3, "templates": 7, "ext": { "...": "see report-schema.md" } },
  "status": { "lastAttemptAt": "2026-10-06T09:12:44Z", "lastSuccessAt": "2026-10-06T09:12:44Z", "lastOutcome": "ok", "lastIdRotationAt": null }
}
```

- **`destination.kind`** is one of `default`, `custom`, `bundled`, `disabled` or `none`. `destination.host` is only the URL host, never the path, query or credentials, and is `null` for `disabled` and `none` (FR-017).
- **`consent`** holds the stored choice. When `operatorDisabled` is true, the stored values are reported but have no effect.
- **`installId`** is `null` whenever extended is off (FR-012).
- **`preview`** is the exact report the reporter would send now, built by the same function (SC-006). It is `null` when basic is off or the destination is `disabled` or `none`. When collection fails partway, the fields that failed are reported as they would be sent: `0` for counts, `other` for categories.
- **`status.lastOutcome`** is `never`, `ok` or `failed`. A failure detail is never exposed; the UI renders a fixed phrase (US3 scenario 2).
- **`status.lastIdRotationAt`** is when the reporter last replaced the ID because the receiver said it was claimed by another key (FR-037), or `null`. The UI renders "Install ID replaced on <date>: the destination reported it was in use by another key."
- **The preview's `ext`** includes `key` (the public key for the current ID) and `sentAt` (the preview time). The signature header isn't shown.
- **`signing_secret` is never returned** by this or any other endpoint (FR-012).

## `PUT /admin/config/telemetry` (`config:manage`, existing route `PUT /admin/config/{section}`, `api/internal/handlers/config.go:56`)

Body: `{"sendMetrics": bool, "extended": bool}`.

| Case | Result |
|---|---|
| `sendMetrics: false` (whatever `extended` says) | Saved as `extended: false` as well, then continues as "Valid" below (FR-003). |
| Operator disabled telemetry (`kind = disabled`) | `409` "telemetry is disabled by the operator" |
| Valid | `200`. In the same transaction: the config row is saved, `consent_source` becomes `admin`, the install ID is created or deleted to match `extended`, and the schedule opens (`next_due_at` set as in R12) or closes (`NULL`). |

The response body is unchanged from the current section save.

## `POST /admin/telemetry/install-id` (`config:manage`)

Rotates the install ID.

- `200 {"installId": "<new uuid>"}` on success.
- `409` when extended is off (there is no ID to reset).

The old ID is discarded and never sent again (US3 scenario 4).

## `GET /admin/telemetry/notice` (`config:manage`; a user without it gets `{"pending": false}`)

```json
{
  "pending": true,
  "destination": { "kind": "default", "host": "telemetry.example.org" },
  "fields": {
    "basic": ["version", "servers", "templates"],
    "extended": ["installId", "env.k8s", "env.distro", "env.arch", "env.nodes", "env.deployment", "games", "features"]
  }
}
```

`pending` is computed as in data-model.md under "Notice pending". When it is `false`, the other keys are omitted.

## `POST /admin/telemetry/notice` (`config:manage`)

Body: `{"action": "seen" | "keep" | "extended-off" | "all-off"}`.

| Action | Effect |
|---|---|
| `seen` | Sets `notice_shown_at` if it isn't set yet, and opens the schedule (`next_due_at = now + U(0, min(15m, interval/4))`, research R12). Records no ack. Idempotent. |
| `keep` | Ack (`action=keep`). Consent unchanged. |
| `extended-off` | Ack. `extended=false`, `consent_source=admin`, install ID deleted. |
| `all-off` | Ack. `sendMetrics=false`, `extended=false`, `consent_source=admin`, install ID deleted, schedule closed. |

- Every action returns `204`.
- Every action returns `409` when the notice isn't pending for the caller, except `seen`, which is a no-op in that case.

## Audit

Each mutating request above is audited through the existing middleware, as all `/admin/*` writes are. Install IDs are never written to audit `target` or `reason`.

## Web client (`web/src/lib/api.ts`)

- `Telemetry.get()`
- `Telemetry.resetInstallId()`
- `Telemetry.notice()`
- `Telemetry.ack(action)`
- `TelemetryCfg` in `web/src/lib/config.ts` gains `extended: boolean`.
