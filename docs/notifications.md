# Notifications

Gameplane can push cluster events — a server going unhealthy, a backup or
restore finishing — to external sinks: **Discord**, **Slack**, **ntfy**,
**SMTP** (email), or a **generic webhook**. Sinks are configured in the
dashboard under **Admin Settings → Notifications** (persisted via
`PUT /admin/config/notifications`); their credentials live in Kubernetes
Secrets, never in the database.

Like the [audit sinks](install.md#audit-log), notifications **mirror, they
never gate**: delivery is best-effort from a bounded in-memory queue, and a
slow or down sink never blocks reconciliation or an API request.

## Events

| Event | Fires when | On by default |
|---|---|---|
| `server.unhealthy` | a GameServer's phase escalates to `Failed` (bad image, persistent crash-loop, non-zero exit), or a previously-healthy server loses its agent heartbeat | yes |
| `server.recovered` | a server with an outstanding `server.unhealthy` alert becomes healthy again — ordinary starts never fire it | yes |
| `backup.failed` | a Backup enters phase `Failed` | yes |
| `backup.succeeded` | a Backup enters phase `Succeeded` | no |
| `restore.failed` | a Restore enters phase `Failed` | yes |
| `restore.succeeded` | a Restore enters phase `Succeeded` | no |

A sink with no explicit event filter receives the defaults above (failures
plus the paired recovery). User-intended transitions — stopping, suspending —
never notify.

**Restart semantics:** only transitions observed while the API pod is running
are notified. A backup that starts *and* fails entirely while the API is down
is missed by the sinks (it is still visible in the dashboard, `kubectl`, and
the `GameplaneBackupFailed` Prometheus alert). There is deliberately no
cross-restart watermark.

## Sink credentials (Secrets)

The **Add sink** form takes the credential material directly — the webhook
URL, ntfy topic + token, or SMTP settings — and the API stores it as a
Secret named `gameplane-notify-<sink>` in the **control-plane namespace**
(`PUT /admin/notifications/sinks/{name}/secret`, permission
`config:manage`). The sink's `configRef` is set to that Secret
automatically, and deleting the sink in the dashboard also deletes the
Secret (only API-created ones — see below).

Each sink references its Secret by name (`configRef`). Two labels bound
what the API may touch:

- `gameplane.local/notification-sink: "true"` — the API refuses to *read
  or overwrite* Secrets without it, so granting someone `config:manage`
  does not let them exfiltrate arbitrary control-plane Secrets by pointing
  a sink at one.
- `gameplane.local/managed-by: gameplane-api` — set on Secrets the API
  created from the form. Deletes over HTTP require it, so Secrets created
  with `kubectl` or a GitOps pipeline are never deleted through the
  dashboard.

Expected keys per kind:

| Kind | Keys |
|---|---|
| `discord` | `url` — the Discord webhook URL |
| `slack` | `url` — the Slack incoming-webhook URL |
| `ntfy` | `url` — the topic URL, e.g. `https://ntfy.sh/my-topic` (self-hosted servers work the same); optional `authorization` (the form's *access token* is stored as `Bearer <token>`) |
| `webhook` | `url`; optional `authorization` (sent verbatim as the `Authorization` header) |
| `smtp` | `host`; `from`; `to` (comma-separated); optional `port` (default `587`), `username` + `password` (AUTH PLAIN), `tls` = `starttls` (default) \| `implicit` \| `none` |

Pre-creating a labelled Secret with `kubectl` still works — the notifier
only cares about the label, not who created it. Write the sink row with a
matching `configRef` via `PUT /admin/config/notifications`:

```sh
# A Discord sink Secret named "team-alerts", managed outside the dashboard:
kubectl -n gameplane-system create secret generic team-alerts \
  --from-literal=url='https://discord.com/api/webhooks/…'
kubectl -n gameplane-system label secret team-alerts \
  gameplane.local/notification-sink=true
```

Note: `net/smtp` refuses to send AUTH PLAIN credentials over an unencrypted
connection, so `tls: none` only works for unauthenticated relays.

## Payloads

- **discord** — one embed, red for failures / green for recoveries and
  successes, timestamped.
- **slack** — a plain `{"text": …}` message (works with every
  incoming-webhook variant and most compatibles, e.g. Mattermost).
- **ntfy** — a plain-text POST to the topic URL with the metadata in ntfy
  headers: `Title` is the event headline, `Priority` is `high` for
  failures and `default` otherwise, `Tags` picks the emoji.
- **webhook** — the full structured event:

  ```json
  {
    "type": "backup.failed",
    "ts": "2026-07-04T10:12:00Z",
    "kind": "Backup",
    "name": "nightly",
    "namespace": "gameplane-games",
    "message": "restic exited with code 1",
    "instance": "prod",
    "test": false,
    "language": "en",
    "title": "[prod] backup failed: gameplane-games/nightly",
    "text": "[prod] backup failed: gameplane-games/nightly — restic exited with code 1"
  }
  ```

- **smtp** — a plain-text mail with the same fields.

`instance` is **Admin Settings → General → Instance name**, so alerts from
different Gameplane installs are tellable apart.

## Language

Each destination can have its own language. Set `"language": "<tag>"` on the
sink in `PUT /admin/config/notifications`, or pick it in the destination form
when that is available. Leave it out to use the instance default language
(**Admin Settings → Language**, the `i18n` config section), which falls back to
English. A tag that this release does not offer is rejected with `400` and the
message id `server.i18n.unknown_language`. The headline (`title`) and the fixed
sentences (for example the test-send body) are translated; the Kubernetes
`reason` and `message` of an event are quoted verbatim in English. The test
send (`POST /admin/notifications/sinks/{name}/test`) uses the destination's
language.

`language`, `title` and `text` are additive: every other field keeps its name
and value. `language` is the language the text was rendered in.

## Testing a sink

`POST /admin/notifications/sinks/{name}/test` (permission `config:manage`)
delivers a synthetic event to the *persisted* sink synchronously and returns
the real outcome: `200 {"delivered":true}`, `404` (unknown sink), `422`
(no `configRef` yet), or `502` with the delivery error. The endpoint is
rate-limited (~12/min per IP) since every hit dials out.

## Delivery semantics and observability

Failed deliveries are retried twice (2 s, then 8 s backoff) on network
errors and 5xx; a 4xx response (revoked webhook, bad token) is not retried.
Outcomes are counted at `/metrics`:

```
gameplane_notify_deliveries_total{kind="discord|slack|smtp|webhook|ntfy|queue",
                                  result="sent|failed|dropped|skipped_no_secret"}
```

Watch `failed`/`dropped`/`skipped_no_secret` — a growing delta means alerts
admins are counting on aren't arriving. Sink dials are guarded by the same
SSRF dial-guard the operator uses for module sources; see
[security](security.md#notifications).
