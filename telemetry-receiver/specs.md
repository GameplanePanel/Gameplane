# telemetry-receiver — Specification

**Status:** pre-v1 (v0.3.0)  
**Module / package:** github.com/GameplanePanel/gameplane/telemetry-receiver  
**Go version:** 1.26

## Purpose

Optional collector for the usage telemetry that Gameplane installs report daily. It runs as the chart's bundled in-cluster receiver, as a self-hosted provider for other installs, or as the project's default receiver. It accepts small JSON reports (a basic tier and a signed extended tier), validates them strictly, folds them into daily aggregates and expiring activity records, and shows the result on a private dashboard.

## Responsibilities

- **Two HTTP listeners**:
  - public (`LISTEN_ADDR`, default `:8080`): `/ingest` (POST), `/v1/summary` (GET), `/v1/challenge` (GET), `/healthz` (GET)
  - dashboard (`DASHBOARD_LISTEN_ADDR`, default `:8081`): started only when `DASHBOARD_TOKEN` is set; serves `/login`, `/logout`, `/`, `/api/v1/views`, `/metrics` and `/static/*`
- **Payload validation**: delegated to `telemetryschema.Decode`: exactly one JSON object, exact case-sensitive keys, duplicates and nulls rejected, trailing non-whitespace rejected, unknown fields rejected, negative counts rejected, oversized bodies (>16 KiB) rejected. A report is a basic report (`version`, `servers`, `templates`) or a basic report with an `ext` part; both are strictly validated
- **Extended identity checks**: an extended report must carry a valid `Gameplane-Telemetry-Signature` (Ed25519 over the exact body bytes, under the key in `ext.key`). The receiver also refuses a send time outside `[now - 36h, now + 1h]` (`stale`), a key that differs from the one that first claimed the install ID (`id_claimed`) and a send time not later than the last accepted one (`replay`)
- **Proof-of-work (optional)**: with `INGEST_POW=true`, `/ingest` requires a solved challenge from `/v1/challenge` before the body is read; difficulty adapts to the challenge rate
- **Aggregation and storage**: daily aggregates and expiring per-install activity records in SQLite (`$DATA_DIR/telemetry.db`); an install ID is stored only as `HMAC-SHA256(pepper, installID)`
- **Per-source limits**: accepted reports per source per UTC day (`INGEST_SOURCE_DAILY_LIMIT`), login attempts, summary reads and challenge requests, all held in memory; source addresses come from the TCP peer, or from `X-Forwarded-For` only when the peer is inside `TRUSTED_PROXY_CIDRS`
- **Dashboard**: server-rendered HTML with inline SVG charts and no JavaScript, behind one operator token; the same view model is served as JSON at `/api/v1/views`. Every range ends on today (UTC), which is still in progress
- **Public summary (optional)**: `GET /v1/summary` returns five counts when `PUBLIC_SUMMARY=true`
- **Version sanitization**: invalid syntax is bucketed as `"invalid"`. Retain at most 128 distinct strings matching `^[A-Za-z0-9][A-Za-z0-9._+-]{0,31}$` per process for the Prometheus label; additional versions share `"other"`. The two bucket names are reserved, for at most 130 series. Retained labels are never evicted; restart resets the budget with the counters.
- **Metrics**: Prometheus series on the dashboard listener only, behind the token
- **Lifecycle**: an hourly job finalizes completed UTC days, expires activity records and deletes daily aggregates older than `RETENTION_DAYS`
- **Structured logging**: logs each accepted report via `log/slog` with version/servers/templates fields (and `extended`); source addresses are never logged
- **Request timeouts**: whole request read is time-bounded (15 s read timeout, 10 s header timeout)

## Non-goals / boundaries

The receiver trusts nothing it is sent: reports are self-reported and unauthenticated by design on a public provider, so figures are approximate and the dashboard labels them that way. It does not issue credentials to installs, does not run alongside a second writer on the same database (single replica), and has no multi-user accounts: one dashboard token is the only credential. The API's consent switches (`Admin Settings → Telemetry`) decide whether an install sends anything; the receiver cannot see or enforce them. Refer to `telemetry-receiver/README.md` for configuration and run instructions and to `docs/telemetry-provider.md` for the provider runbook.

## Directory & package layout

Single package `main`:

```
telemetry-receiver/
├── main.go             # config (loadConfig, validate), server wiring, both listeners, graceful shutdown
├── ingest.go           # POST /ingest: auth, proof-of-work gate, body read, basic reports
├── ingest_ext.go       # extended reports: signature, window, claim, replay, activity records
├── pow.go              # proof-of-work: challenges, difficulty, verification, GET /v1/challenge
├── store.go            # SQLite schema, meta, write transactions, retention and lifecycle job
├── ratelimit.go        # per-source limiters, source-address derivation
├── summary.go          # GET /v1/summary and its snapshot cache
├── dashboard.go        # dashboard listener: login, sessions, views JSON, metrics, static files
├── views.go            # dashboard view model (BuildViews)
├── charts.go, charts_ext.go  # inline SVG chart rendering
├── web/                # embedded HTML templates and static CSS/icon
├── *_test.go           # unit tests
├── go.mod / go.sum     # Dependencies: prometheus/client_golang, modernc.org/sqlite, telemetryschema (+ transitives)
├── .testcoverage.yml   # Coverage gate: 70% total
├── Dockerfile          # Multi-stage: build→distroless:nonroot, CGO_ENABLED=0
├── telemetry-dashboard.pen, design-export/  # dashboard designs and their export
├── README.md           # User-facing: behavior, configuration, run examples
└── .gitignore          # Standard Go ignores
```

## External interface / contracts

The full contract is `specs/done_022-default-telemetry-dashboard/contracts/receiver-http.md`; the report format is `contracts/report-schema.md`.

### Public listener (`LISTEN_ADDR`)

| Route | Method | Success response | Failures |
|-------|--------|------------------|----------|
| `/ingest` | POST | `204 No Content` (also for a same-day duplicate extended report, which only counts in `duplicates`) | `400` structural error; `401` missing/wrong Authorization (when `AUTH_TOKEN` set); `403` `bad_signature`, `stale` or `replay`; `409` `id_claimed`; `413` body >16 KiB; `428` `pow_required` or `pow_invalid`; `429` per-source daily limit; `503` `pow_busy` with `Retry-After: 60` |
| `/v1/summary` | GET | `200` JSON with exactly `asOf`, `reportsLatestDay`, `reports30d`, `reportsTotal`, `activeInstalls30d` | `404` unless `PUBLIC_SUMMARY=true`; `429` per-source rate limit |
| `/v1/challenge` | GET | `200` JSON `{challenge, bits, expiresAt}`, `Cache-Control: no-store` | `404` unless `INGEST_POW=true`; `429` (10 per minute, burst 5 per source) |
| `/healthz` | GET | `200 OK`, body `"ok"` | Never errors |

`/ingest` runs its checks in this order: `AUTH_TOKEN`, proof-of-work, body read, decode, extended identity checks, per-source daily limit, store. A refused report changes no stored data. Every other path, including `/metrics`, answers `404`.

### Dashboard listener (`DASHBOARD_LISTEN_ADDR`)

| Route | Method | Auth | Result |
|-------|--------|------|--------|
| `/login` | GET, POST | none; POST takes form field `token`, same-origin `Origin` required | form; on success `303` to `/` and a session cookie; "Invalid credentials" otherwise; `429` after 5 attempts per minute per source |
| `/logout` | POST | session, same-origin | `303` to `/login`, clears the cookie |
| `/` | GET | session | overview for `?range=7\|30\|90\|365` (default 30); unauthenticated gets `303` to `/login` |
| `/api/v1/views` | GET | session cookie or `Authorization: Bearer <DASHBOARD_TOKEN>` | `200` JSON view model; otherwise `401 {"error":"unauthorized"}` |
| `/metrics` | GET | Bearer token only | Prometheus text; otherwise `401 {"error":"unauthorized"}` |
| `/static/*` | GET | none | embedded CSS and icon only |

The session cookie is `gp_telemetry_session` (`HttpOnly; Secure; SameSite=Strict; Path=/`, 12 hours); its key derives from `DASHBOARD_TOKEN`, so replacing the token invalidates every session. Every dashboard response carries a strict `Content-Security-Policy`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` and `Cache-Control: no-store`.

### Configuration (environment variables)

| Variable | Default | Meaning |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | Public listener |
| `DASHBOARD_LISTEN_ADDR` | `:8081` | Dashboard listener |
| `DASHBOARD_TOKEN` | *(empty)* | Dashboard credential. Empty means no dashboard listener. When set it must be at least 32 characters or the receiver exits at startup |
| `AUTH_TOKEN` | *(empty)* | When set, `/ingest` requires an exactly-matching `Authorization` header (constant-time compare) |
| `DATA_DIR` | *(empty)* | SQLite directory. Empty means in-memory (a warning is logged and everything is lost on restart) |
| `PUBLIC_SUMMARY` | `false` | `true` enables `/v1/summary` |
| `TRUSTED_PROXY_CIDRS` | *(empty)* | Comma-separated CIDRs whose `X-Forwarded-For` is trusted; a malformed entry stops startup |
| `INGEST_SOURCE_DAILY_LIMIT` | `20` | Accepted reports per source per UTC day; `0` means unlimited; negative is refused |
| `RETENTION_DAYS` | `730` | Daily aggregate retention; minimum `365` |
| `ACTIVITY_EXPIRY_DAYS` | `90` | Activity record expiry; minimum `31` |
| `ID_PEPPER` | *(empty)* | HMAC pepper for install IDs. Empty means one is generated and kept in the database |
| `INGEST_POW` | `false` | `true` requires proof-of-work on `/ingest` and serves `/v1/challenge` |
| `INGEST_POW_TARGET_PER_MIN` | `60` | Normal challenge rate: at or below it no work is required. Must be at least 1 |
| `INGEST_POW_MIN_BITS` | `0` | Lowest difficulty issued. Must not be negative |
| `INGEST_POW_MAX_BITS` | `22` | Highest difficulty issued. At least `INGEST_POW_MIN_BITS` and at most 26 (the hardest challenge an install solves) |

An unparsable or out-of-range value stops the receiver at start with an `invalid configuration` error.

### Proof-of-work

Challenges are stateless: `base64url(payload) "." base64url(mac)`, where the payload holds a version byte, the issue time, the difficulty in bits and 16 random bytes, and the MAC is the first 16 bytes of HMAC-SHA256 under a 32-byte key generated at process start and kept in memory. A challenge expires after 15 minutes. The sender finds a decimal nonce so that `SHA-256(challenge ":" nonce)` starts with at least `bits` zero bits and sends `Gameplane-Telemetry-PoW: <challenge>:<nonce>`.

Difficulty: with `r` challenges issued in the trailing 60 seconds (counting the new one) and `T = INGEST_POW_TARGET_PER_MIN`, the target is `0` when `r <= T`, otherwise `min(MAX, ceil(2*log2(r/T)))`. The issued difficulty is `max(MIN, target, peak - floor((now - peakAt) / 5 min))`, so it rises on the next challenge and falls one bit per five minutes.

Verification (before the body is read): header present and well formed, MAC valid, not expired, enough leading zero bits as stated in the token, not already used. A missing header is `428 pow_required`; any other failure is `428 pow_invalid`. A verified solution is marked used at once, whatever happens to the report. The used set is in memory, pruned when entries expire, and capped at 1,000,000; when full, `/ingest` answers `503 pow_busy` with `Retry-After: 60`. A restart invalidates outstanding challenges.

### Prometheus metrics (dashboard listener, Bearer token)

```
gameplane_telemetry_reports_total{version="0.3.0"}      # counter, by version label
gameplane_telemetry_servers_bucket{le="…"}              # histogram (buckets: 0,1,2,5,10,25,50,100,250,+Inf), plus _sum and _count
gameplane_telemetry_templates_bucket{le="…"}            # histogram (same buckets), plus _sum and _count
gameplane_telemetry_duplicates_total                    # same-day repeat extended reports
gameplane_telemetry_extended_reports_total              # extended reports counted in the aggregates
gameplane_telemetry_rate_limited_total{route}           # route: ingest, login, summary, challenge
gameplane_telemetry_refused_total{reason}               # bad_signature, stale, id_claimed, replay, pow_required, pow_invalid, pow_busy
gameplane_telemetry_pow_challenges_total                # challenges issued
gameplane_telemetry_pow_bits                            # gauge: difficulty a challenge issued now would carry (0 when proof-of-work is off)
```

All series are in memory and reset on restart; the durable figures are the aggregates in SQLite.

## Key invariants

- **Stores daily aggregates and expiring activity records only, never raw reports or addresses**: the schema (`meta`, `daily_basic`, `daily_version`, `daily_fleet`, `daily_ext`, `daily_dim`, `daily_game`, `daily_install_version`, `daily_version_change`, `activity`) holds counts, bands and categories per UTC day, plus one activity record per install keyed by `HMAC-SHA256(pepper, installID)`. Source addresses live in limiter memory only and are never written to the store or the logs. No extended attribute is stored next to an install ID. The version trend (spec 022 FR-042) keeps only daily counts: installs per reported version, and installs that upgraded, downgraded or changed version without a semver order. No version history is linked to an install ID. Since spec 022 FR-045, `daily_dim` also holds the deployment (`cluster`, `standalone`, or `other` for schema 1 reports), and one row per Kubernetes minor version and distribution an install reports (OD-15).
- **Basic or extended report, both strictly validated**: a body is accepted only when it decodes as exactly one basic report or one basic report with a valid `ext` part; anything else is `400`. Out-of-set values in extended categories fold to `other`.
- **Extended reports must be signed**: a report with `ext` and a missing or invalid signature is refused (`403 bad_signature`) before any write, and an install ID is bound to the first valid key that claims it.
- **Refusal invariant (dashboard)**: an unauthenticated dashboard response never contains a figure, a date other than page chrome, a version, category or module name, or anything that varies with whether data exists. A wrong token gets the same "Invalid credentials" login page whether or not the receiver is empty.
- **No token, no dashboard**: the dashboard listener is not started unless `DASHBOARD_TOKEN` is set, and a token shorter than 32 characters stops startup.
- **Version label bounded**: malformed strings become `version="invalid"`; after 128 distinct valid versions, new versions become `version="other"`. Concurrent requests share the same budget, and all accepted reports remain counted.
- **Refused reports change nothing**: signature, window, claim, replay and proof-of-work refusals write no data; a proof-of-work refusal costs one HMAC and one hash.
- **Single writer**: SQLite writes are serialized by one mutex; run one replica per database.
- **Graceful shutdown**: catches SIGINT/SIGTERM and shuts both listeners down with a 5s context timeout; the lifecycle job stops before the store closes.

## Data & persistence

SQLite at `$DATA_DIR/telemetry.db` (WAL, 5 s busy timeout). Aggregates are kept `RETENTION_DAYS` (default 730) and activity records `ACTIVITY_EXPIRY_DAYS` (default 90); an hourly job sweeps both. `meta` holds the schema version, collection start, the running report total and, unless `ID_PEPPER` is set, the pepper. Prometheus series, limiter state and the proof-of-work key and used set are in memory only. With `DATA_DIR` empty the whole store is in memory.

## Security considerations

- **Input boundary**: strict token-walking decode with exact case-sensitive keys, a 16 KiB body cap and per-field patterns or fixed lists; hostile text cannot create unbounded categories or labels.
- **Label safety**: version sanitization keeps the Prometheus label set bounded.
- **Authentication**: `AUTH_TOKEN` and the dashboard token are compared in constant time; the dashboard token needs at least 32 characters because per-source login limits can be outrun by a holder of many IPv6 /64s, so length is what makes guessing infeasible; the session cookie is HMAC-signed with a key derived from the token.
- **Proof-of-work**: raises the cost of bulk fabricated reports in proportion to request rate; it does not stop a determined, well-resourced sender (SHA-256 is cheap on dedicated hardware) and does not prove an install is real.
- **Signing**: stops impersonation of an existing install and replays; it does not stop invented installs.
- **Container hardening**: distroless image, nonroot user (UID 65532), no shell or package manager; static binary built with `CGO_ENABLED=0`.
- **Dependencies**: `github.com/prometheus/client_golang`, `modernc.org/sqlite` (pure Go SQLite, no cgo) and the in-repo `telemetryschema` module (stdlib only); no serialization frameworks and no dynamic code loading.

## Testing & coverage

Coverage gate: **70%** (`.testcoverage.yml`).

Test suite covers:
- **Validation and ingest**: malformed, unknown-field, negative and oversized bodies; per-source daily limit; duplicate and replay handling
- **Signing and claims**: bad signature, stale send time, claimed ID, replay
- **Proof-of-work**: challenge issue and verification, difficulty rise and decay, used-set cap, `pow_busy`
- **Dashboard**: login, session cookie, CSRF origin check, refusal invariant, views JSON, metrics Bearer-only
- **Summary and limiters**: snapshot cache, rate limits, source-address derivation
- **Store and lifecycle**: schema, retention sweep, rollover, activity expiry
- **Config loading**: defaults, environment overrides, minimums and the 32-character token check
- **Cardinality**: version label budget

Untested (main process wiring): `main()` signal handling and server startup errors — not unit-testable without spawning goroutines, deferred to integration tests.

## References

- **`telemetry-receiver/README.md`** — configuration table, behavior, run via Helm or Docker
- **`docs/telemetry-provider.md`** — provider runbook (TLS, secrets, proof-of-work tuning, backups)
- **`docs/security.md`** — Telemetry threat model
- **`telemetryschema/specs.md`** — shared report contract, signing and proof-of-work helpers
- **`api/internal/telemetry`** — the API's reporter that POSTs to this endpoint
- **`charts/gameplane/`** — Helm chart integration (`api.telemetry.receiver.*`)
- **`specs/done_022-default-telemetry-dashboard/`** — feature spec and contracts
- **`CLAUDE.md`** — project architecture summary
