# telemetry-receiver

The **telemetry-receiver** [optional] is the collection endpoint for Gameplane's
usage telemetry. The API's reporter (`api/internal/telemetry`) POSTs a small
JSON report once a day while the admin's telemetry switches are on. There
are two tiers:

```json
{ "version": "0.3.0", "servers": 3, "templates": 7 }
```

The basic tier is that and nothing else. The extended tier adds an `ext`
object: a random install ID, environment categories, game and feature
counts, the install's public signing key and the send time, and the report
is signed. See [`docs/install.md`](../docs/install.md#telemetry) for the
exact fields and [`docs/security.md`](../docs/security.md#telemetry) for the
threat model.

The receiver validates each report strictly, then folds it into **daily
aggregates and expiring activity records**. It never stores raw reports or
source addresses. The result is shown on a private dashboard.

You can run it three ways: the Helm chart's bundled receiver for your own
install, a provider for other installs (see
[`docs/telemetry-provider.md`](../docs/telemetry-provider.md)), or as the
project's default receiver, which the maintainers operate.

## Listeners

| Listener | Variable (default) | Serves |
|---|---|---|
| Public | `LISTEN_ADDR` (`:8080`) | `POST /ingest`, `GET /v1/summary` (only when `PUBLIC_SUMMARY=true`), `GET /v1/challenge` (only when `INGEST_POW=true`), `GET /healthz` |
| Dashboard | `DASHBOARD_LISTEN_ADDR` (`:8081`) | login page, dashboard, `/api/v1/views`, `/metrics`, static assets |

The dashboard listener starts only when `DASHBOARD_TOKEN` is set. Keep it
on a private network. The public listener answers `404` for `/metrics` and
everything else.

## Behavior

| Route | Method | Response |
|---|---|---|
| `/ingest` | POST | `204` accepted (also a same-day duplicate) · `400` malformed or invalid · `401` bad or missing token (when `AUTH_TOKEN` is set) · `403` `bad_signature`, `stale` or `replay` · `409` `id_claimed` · `413` body over 16 KiB · `428` `pow_required` or `pow_invalid` · `429` per-source daily limit · `503` `pow_busy` |
| `/v1/summary` | GET | `200` five headline counts · `404` unless `PUBLIC_SUMMARY=true` · `429` rate limit |
| `/v1/challenge` | GET | `200` a proof-of-work challenge · `404` unless `INGEST_POW=true` · `429` rate limit |
| `/healthz` | GET | `200 ok` |

Extended reports must carry a valid `Gameplane-Telemetry-Signature`. The
receiver binds each install ID to the first key that uses it and refuses a
different key, a send time outside `[now - 36h, now + 1h]` and a replayed
send time. A refused report changes no stored data.

### Proof-of-work

With `INGEST_POW=true`, `/ingest` requires a header
`Gameplane-Telemetry-PoW: <challenge>:<nonce>`, where `challenge` comes from
`GET /v1/challenge` and `SHA-256(challenge ":" nonce)` starts with the
challenge's number of zero bits. Challenges are single use and expire after
15 minutes. The difficulty is 0 (or `INGEST_POW_MIN_BITS`) while at most
`INGEST_POW_TARGET_PER_MIN` challenges a minute are issued, rises with the
request rate up to `INGEST_POW_MAX_BITS`, and falls one bit every five
minutes. Installs refuse challenges above 26 bits. Installs that find no
challenge endpoint send without proof-of-work. It is off by default; a
public provider turns it on. See
[`docs/telemetry-provider.md`](../docs/telemetry-provider.md#proof-of-work-on-ingest).

### Dashboard

Server-rendered HTML with inline SVG charts, no JavaScript, for the last 7,
30, 90 or 365 days (`?range=`), ending on today (UTC), which is still in
progress. It needs the dashboard token: a browser
logs in at `/login` and gets a 12-hour session cookie; scripts can send
`Authorization: Bearer <token>` to `/api/v1/views`. `/metrics` accepts only
the Bearer token. An unauthenticated request never sees a figure: the HTML
redirects to `/login` and the JSON answers `401 {"error":"unauthorized"}`.

Versions appear twice. "Reports by version" counts basic reports, so one
install that reports daily adds one per day. "Versions by install" counts each
extended-reporting install once, on its last version. Under it, "Version
trend" shows per day how many installs upgraded, downgraded, changed between
versions that have no semver order (such as `pr617` or `dev`), or ran a semver
version lower than the newest one seen in the range. The trend is stored as
daily counts only (`daily_install_version`, `daily_version_change`), never per
install.

The environment cards count an install once under each value it reports
across the clusters its panel manages, so Kubernetes version, distribution
and architecture shares can add up to more than 100%. "Deployment" shows
whether the panel runs on a cluster or standalone; installs on an older
version that doesn't report it count as other.

### Metrics

On the dashboard listener, Bearer token required:

```
gameplane_telemetry_reports_total{version="0.3.0"}  # reports by reported version
gameplane_telemetry_servers                         # histogram of GameServer counts
gameplane_telemetry_templates                       # histogram of GameTemplate counts
gameplane_telemetry_duplicates_total                # same-day repeat extended reports
gameplane_telemetry_extended_reports_total          # extended reports counted
gameplane_telemetry_rate_limited_total{route}       # ingest, login, summary, challenge
gameplane_telemetry_refused_total{reason}           # bad_signature, stale, id_claimed, replay, pow_required, pow_invalid, pow_busy
gameplane_telemetry_pow_challenges_total            # proof-of-work challenges issued
gameplane_telemetry_pow_bits                        # difficulty a challenge issued now would carry
```

Version strings with an invalid character set or more than 32 characters
are counted under `version="invalid"`. The receiver retains the first 128
distinct valid versions per process. Further versions count under
`version="other"`; previously retained versions keep their own counters.
The two bucket names are reserved. This caps the metric at 130 series,
including concurrent ingestion. Restarting the receiver resets the budget
and all counters; the durable figures live in the SQLite store.

## Configuration (environment variables)

| Variable | Default | Meaning |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | Public listener. |
| `DASHBOARD_LISTEN_ADDR` | `:8081` | Dashboard listener. |
| `DASHBOARD_TOKEN` | *(empty)* | Dashboard credential, from a Secret. Empty means no dashboard listener. When set it must be at least 32 characters, or the receiver exits at startup; generate one from 32 random bytes. |
| `AUTH_TOKEN` | *(empty)* | When set, `/ingest` requires an exactly-matching `Authorization` header (compared constant-time). Use the same Secret on the API side (`GAMEPLANE_TELEMETRY_AUTH`). Leave empty on a public provider. |
| `DATA_DIR` | *(empty)* | SQLite directory (`telemetry.db`). Empty means in-memory, with a startup warning; everything is lost on restart. |
| `PUBLIC_SUMMARY` | `false` | `true` enables `GET /v1/summary`. |
| `TRUSTED_PROXY_CIDRS` | *(empty)* | Comma-separated CIDRs whose `X-Forwarded-For` is trusted for the source address. |
| `INGEST_SOURCE_DAILY_LIMIT` | `20` | Accepted reports per source per UTC day. `0` means unlimited. |
| `RETENTION_DAYS` | `730` | Daily aggregate retention. Minimum `365`. |
| `ACTIVITY_EXPIRY_DAYS` | `90` | Activity record expiry. Minimum `31`. |
| `ID_PEPPER` | *(empty)* | HMAC pepper for install IDs, from a Secret. Empty means one is generated and kept in the database. |
| `INGEST_POW` | `false` | `true` requires proof-of-work on `/ingest` and serves `/v1/challenge`. |
| `INGEST_POW_TARGET_PER_MIN` | `60` | Normal challenge rate; no work is required at or below it. Must be at least 1. |
| `INGEST_POW_MIN_BITS` | `0` | Lowest difficulty issued. |
| `INGEST_POW_MAX_BITS` | `22` | Highest difficulty issued. At least `INGEST_POW_MIN_BITS` and at most 26. |

A value below a minimum, an unparsable number or a malformed
`TRUSTED_PROXY_CIDRS` entry stops the receiver at start with an error.

## Dependencies

`github.com/prometheus/client_golang` (metrics), `modernc.org/sqlite` (pure
Go SQLite, no cgo) and the in-repo `telemetryschema` module (the shared
report contract, signing and proof-of-work helpers; standard library
only). Nothing else.

## Run via the Helm chart

Set `api.telemetry.receiver.enabled=true` and the chart deploys the
receiver next to the API and points the API's `--telemetry-endpoint` at
it automatically (unless `api.telemetry.endpoint` is set to an external
URL). Aggregates go to a PVC at `/data`; the receiver runs one replica.
Set `api.telemetry.receiver.dashboard.tokenSecretRef.name` to enable the
dashboard. Proof-of-work is `api.telemetry.receiver.ingestPow.*`. See
`docs/install.md`.

## Run standalone

```sh
docker run --rm -p 8080:8080 -p 8081:8081 \
  -v telemetry-data:/data -e DATA_DIR=/data \
  -e DASHBOARD_TOKEN="$(openssl rand -base64 32)" \
  ghcr.io/gameplanepanel/gameplane/telemetry-receiver:edge
```

Point any Gameplane install at it with
`--telemetry-endpoint=https://telemetry.example.com/ingest` (or
`GAMEPLANE_TELEMETRY_ENDPOINT`); the admin toggle still gates whether
anything is sent. Terminate TLS in front of `:8080` and keep `:8081`
private: see the [provider runbook](../docs/telemetry-provider.md).
