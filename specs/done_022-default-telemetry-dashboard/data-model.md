# Data Model: Default telemetry destination, extended telemetry, and telemetry dashboard

**Feature**: [spec.md](spec.md) | **Research**: [research.md](research.md)

There are three places where data lives:

- **Wire**: the report itself, shared by both sides. Field-level rules are in [contracts/report-schema.md](contracts/report-schema.md).
- **API**: the install's own database, SQLite or PostgreSQL, through portable migration `015`.
- **Receiver**: the provider's SQLite file.

All timestamps are RFC 3339 UTC strings bound from Go. All days are UTC calendar days written `YYYY-MM-DD`.

---

## Wire (package `telemetryschema`)

### Report

| Field | Type | Rule |
|---|---|---|
| `version` | string | Required. Sanitised on the receiver to `invalid` unless it matches `^[A-Za-z0-9][A-Za-z0-9._+-]{0,31}$`. Unchanged from today. |
| `servers` | int | Required, ≥ 0. Unchanged. |
| `templates` | int | Required, ≥ 0. Unchanged. |
| `ext` | ExtendedPart | Optional. Present only while the extended tier is on (FR-011). |

### ExtendedPart

| Field | Type | Rule |
|---|---|---|
| `schema` | int | Required, `1`, or `2` from the standalone and multicluster amendment (FR-045), which adds `env.deployment`. A receiver rejects values above the one it supports with 400. |
| `installId` | string | Required. Lowercase UUIDv4. A malformed ID makes the receiver drop `ext` and count the report as basic only. |
| `env` | Env | Required. |
| `games` | Games | Required. |
| `features` | Features | Required. |
| `key` | string | Required. Base64url (unpadded) 32-byte Ed25519 public key derived for this `installId` (R20). |
| `sentAt` | string | Required. RFC 3339 UTC send time. Accepted window `[now − 36h, now + 1h]`. Must be strictly later than the ID's last accepted `sentAt`. |

**Signature**: the header `Gameplane-Telemetry-Signature: ed25519=<base64url>` covers the exact body bytes. It is required whenever `ext` is present and must be absent on basic-only reports (FR-035, FR-038).

- **Env**:
  - `k8s` (enumerated pattern `1.N`, otherwise `other`)
  - `distro` (enumeration)
  - `arch` (non-empty sorted set from an enumeration)
  - `nodes` (band)
  - `deployment` (`cluster` or `standalone`; schema 2 only, FR-045)
  - in schema 2, `k8s` and `distro` are sorted sets over the reachable managed clusters, and `arch` is their union (OD-15); `nodes` is one band over all their nodes (OD-16)
- **Games**:
  - `official`: a map from catalog module name to its server count (int ≥ 1; zero entries are omitted)
  - `custom`: int ≥ 0
- **Features**:
  - booleans: `wakeOnConnect`, `capture`, `backups`, `sso`, `auditForwarding`
  - `tunnels` (sorted set from an enumeration, may be empty)
  - `clusters` (band)
  - `db` (enumeration)
  - `language` (enumeration)

Validation and sanitisation are identical on both sides because both import the same package (research R1):

- **Structural errors** reject the report with 400:
  - wrong JSON types
  - unknown keys
  - duplicate keys
  - negative counts
  - more than one JSON value
- **Out-of-set values** are kept and become `other`. Unknown module keys are folded into `custom`.

---

## API (install side)

### `config` row, key `telemetry` (existing table, extended value)

| Field | Type | Meaning |
|---|---|---|
| `sendMetrics` | bool | Basic tier consent. Existing field. |
| `extended` | bool | Extended tier consent. New. Saving `sendMetrics: false` always stores `extended: false` too (FR-003). |

If the row is absent, both tiers are off. The row is written by `PUT /admin/config/telemetry`, by the notice actions, and by the one-time seeding step (R10).

### `telemetry_state` (new table, exactly one row, `id = 'singleton'`)

| Column | Type | Null | Meaning |
|---|---|---|---|
| `id` | TEXT PK | no | Always `'singleton'`. |
| `consent_source` | TEXT | no | `default` (seeded on a fresh install), `legacy` (seeded on upgrade), or `admin` (an admin made an explicit choice). |
| `install_id` | TEXT | yes | UUIDv4. Present only while `config.telemetry.extended` is true (FR-012). |
| `notice_shown_at` | TEXT | yes | When an admin's dashboard first rendered the notice. Set once. |
| `next_due_at` | TEXT | yes | When the next report is due. NULL while the gate is closed. |
| `last_attempt_at` | TEXT | yes | When the last POST was attempted. |
| `last_success_at` | TEXT | yes | When a report was last accepted (2xx). |
| `last_outcome` | TEXT | no | `never` (default), `ok`, or `failed`. |
| `consecutive_failures` | INTEGER | no | Default 0. Drives backoff (R12). |
| `ext_unsupported_until` | TEXT | yes | Fallback marker (R2). |
| `ext_unsupported_endpoint` | TEXT | yes | The endpoint the fallback marker applies to. The marker is cleared when the destination changes. |
| `signing_secret` | TEXT | yes | 32 random bytes, base64. Created with the first install ID and kept for the install's lifetime. Never returned by any endpoint, logged or audited (FR-012, FR-035). |
| `last_id_rotation_at` | TEXT | yes | When the reporter last replaced the ID after a 409 `id_claimed` (FR-037). |

No HTTP endpoint writes this table directly. It is written by:

- the seeding step
- the post-save hook for the `telemetry` config section
- the notice endpoint
- the install-ID reset endpoint
- the reporter

### `telemetry_notice_acks` (new table)

| Column | Type | Meaning |
|---|---|---|
| `user_id` | BIGINT PK | The admin who dismissed the notice (`users.id`). Deleted in Go when the user is removed (migrations README rule 3). |
| `acked_at` | TEXT | When the notice was dismissed. |
| `action` | TEXT | `keep`, `extended-off`, or `all-off`. |

### Migration `015_telemetry_state.sql` (portable, `common/`)

The migration creates the two tables above with `CREATE TABLE`. It contains:

- no `AUTOINCREMENT`
- no `datetime(...)`
- `last_outcome` with `DEFAULT 'never'`
- `consecutive_failures` with `DEFAULT 0`

Seeding is **not** in SQL. It is the Go post-migration step from R10, because it has to read the existing JSON config value.

### Derived values (not stored)

- **Destination**: `{kind: default|custom|bundled|disabled|none, host}`, resolved from flags at startup (R13).
- **Effective consent**:
  - `basicOn = sendMetrics && kind ∉ {disabled, none}`
  - `extendedOn = basicOn && extended`
- **Gate open**: `basicOn && (consent_source ≠ 'default' || notice_shown_at ≠ NULL)`.
- **Notice pending (per user)**: all of these hold:
  - the user has `config:manage`
  - `kind ∉ {disabled, none}`
  - `consent_source = 'default'`
  - the user has no ack row
- **Preview**: the exact `Report` the reporter would build now (SC-006). It is produced by the same `Collect` function.

### State transitions: consent

```text
                fresh DB (R10)                 upgrade (R10)
                     │                              │
                     ▼                              ▼
        ┌──────────────────────────┐   ┌──────────────────────────┐
        │ default                  │   │ legacy                   │
        │ basic=on  ext=on         │   │ basic=saved|off  ext=off │
        │ reports gated on notice  │   │ no notice                │
        └──────────┬───────────────┘   └───────────┬──────────────┘
          notice "seen" opens the gate             │
          (consent_source stays default)           │
                   │  any admin choice: settings save or notice action
                   └──────────────┬────────────────┘
                                  ▼
                       ┌──────────────────────┐
                       │ admin                │
                       │ basic / ext as saved │
                       └──────────────────────┘
```

- **Install ID**:
  - `extended` changes from false to true → generate a new ID.
  - `extended` changes from true to false → set the ID to NULL.
  - Reset → replace the ID. The old ID is never sent again (US3 scenarios 4 and 5).
  - A 409 `id_claimed` from the receiver → replace the ID automatically and set `last_id_rotation_at` (FR-037).
  - The signing key is never stored. It is derived from `signing_secret` and the current ID on each send, so every change of ID changes the key (R20).
- **Operator disabled** (`kind = disabled`) overrides every state. Toggles are refused with 409, and nothing is sent (FR-005).

### State transitions: schedule (R12)

```text
gate closed ──gate opens──▶ due at now+U(0,15m) ──claim──▶ attempt
     ▲                                                       │
     │ gate closes (next_due_at = NULL)       2xx ◀──────────┤
     │                                         │             │ error / non-2xx
     │                                         ▼             ▼
     └──────────────────────────────── due at now+interval+jitter
                                       (or now+backoff after an error)
```

Responses inside one attempt:

- **400 with `ext` present**: re-POST without `ext` once, and set `ext_unsupported_*` (R2).
- **409 `id_claimed`**: replace the install ID (keeping `signing_secret`), set `last_id_rotation_at`, re-sign and re-POST once. A second 409 counts as an error (R20).
- **403** (`bad_signature`, `stale` or `replay`): counts as an error, with normal backoff. The ID is never rotated.

---

## Receiver (provider side, SQLite at `$DATA_DIR/telemetry.db`)

| Table | Key | Columns | Written by |
|---|---|---|---|
| `meta` | `key` | `value` | Startup and ingest. Keys: `schema_version`, `pepper` (only when `ID_PEPPER` is unset), `collection_started`, `reports_total`, `rollover_through`. |
| `daily_basic` | `day` | `reports`, `duplicates`, `servers_sum`, `templates_sum` | Ingest (every accepted report; `duplicates` instead of `reports` for per-ID same-day repeats). |
| `daily_version` | `(day, version)` | `reports` | Ingest. `version` is the sanitised value or `invalid`. |
| `daily_fleet` | `(day, metric, value)` | `reports` | Ingest. `metric` is `servers` or `templates`. `value` is the exact count 0–1000, or `1001` for more than 1000. |
| `daily_ext` | `day` | `ext_reports`, `active_installs`, `new_installs`, `lapsed_installs` | Ingest for the first three. Rollover for `lapsed_installs`. |
| `daily_dim` | `(day, dim, value)` | `installs` | Ingest of a non-duplicate extended report. See the dimension list below. |
| `daily_game` | `(day, module)` | `installs`, `servers` | Ingest. `module` is a catalog name or `custom`. |
| `daily_install_version` | `(day, version)` | `installs` | Ingest of a non-duplicate extended report (FR-042). `version` is the same bounded label as `daily_version`. |
| `daily_version_change` | `(day, kind)` | `installs` | Ingest of a non-duplicate extended report whose version differs from `activity.last_version` (FR-042). `kind` is `upgraded`, `downgraded` or `unordered` (R22). |
| `activity` | `id_hmac` | `key_fp` (SHA-256 of the claiming public key), `first_seen`, `last_seen`, `last_sent_at`, `last_version` | Ingest: inserting a row *is* the claim (R20). Deleted by expiry, and the claim expires with it (FR-038). |

`daily_dim` dimensions:

- `k8s`, `distro`, `arch` (one row per value present; schema 1 `k8s` and `distro` count as a one-value set), `nodes`
- `deployment` (FR-045; a schema 1 report has no marker and counts as `other`, OD-14)
- `tunnel` (one row per provider present)
- `clusters`, `db`, `language`
- `feature` with value `wakeOnConnect`, `capture`, `backups`, `sso` or `auditForwarding` (a row only when the value is true)

Every `value` is a `telemetryschema` enumeration member or `other`, so the row count per day is bounded by the size of those sets.

### Invariants

- **Nothing raw is stored.** There are no raw reports, source addresses or untransformed install IDs (FR-014, SC-012). Source addresses exist only in the in-memory limiters (R6).
- **Proof-of-work state is memory only.** The challenge MAC key, the issue-rate window and the used-challenge set (R21) are never written to the database, and a restart discards them.
- **Extended attributes are never keyed by ID.** `activity` has no column for any of them (FR-014).
- **Refused reports leave no trace.** A report refused by the proof-of-work, signature, window, claim or replay checks changes no table (FR-036, FR-039). Only the in-memory limiter and the `rate_limited` and `refused` metrics see it.
- **No full public keys are stored.** `key_fp` is enough to check the claim, because the key arrives with every report.
- **Version changes are counts only.** `daily_version_change` and `daily_install_version` hold no ID, so no version history can be traced back to an install (FR-014, FR-042).
- **One extended contribution per install per day.** At most one extended report per `id_hmac` per day changes any aggregate (R5).
- **The running total never decreases.** `reports_total` is only ever incremented, so retention sweeps don't change the public "total since collection began".

### Lifecycle jobs (hourly, idempotent, resumable through `rollover_through`)

1. **Finalise each completed day D** after `rollover_through`: set `daily_ext[D].lapsed_installs = count(activity where last_seen = D − 30 days)`.
2. **Expire activity records** where `last_seen < today − ACTIVITY_EXPIRY_DAYS`. This uses OD-4 (ruled: 90 days), with an enforced minimum of 31.
3. **Delete `daily_*` rows** where `day < today − RETENTION_DAYS`. This uses OD-3 (ruled: 730 days, i.e. 24 months), with an enforced minimum of 365 (FR-020).

### Derived views (computed at read time, [contracts/receiver-http.md](contracts/receiver-http.md))

The range R is 7, 30, 90 or 365 days and ends on today, the current UTC day, which is still in progress (OD-6). Today's figures change until the day closes, and its lapsed-installs value stays 0 until the lifecycle job finalises the day.

| View | Computation |
|---|---|
| Reports per day | `daily_basic.reports` series over R |
| Version adoption (reports) | `daily_version` summed over R. Top 10 by count, then `Other` (the rest) and `Invalid`. |
| Fleet distribution and median | `daily_fleet` summed over R. Bands `0,1,2,5,10,25,50,100,250,250+` are applied at read. The exact median comes from the cumulative counts. |
| Latest-day totals | `daily_basic.servers_sum`, `templates_sum` for today (the as-of day, in progress) |
| Unique active installs | `count(activity where last_seen ≥ asOf − {0, 6, 29})` |
| New and lapsed installs | `daily_ext.new_installs` and `lapsed_installs` series over R |
| Version adoption (installs) | `activity.last_version` for records with `last_seen` inside `min(R, ACTIVITY_EXPIRY_DAYS)`. The window used is shown. |
| Version trend (FR-042) | Per day D in R: `upgraded`, `downgraded` and `unordered` from `daily_version_change[D]`; `stale[D]` = Σ `daily_install_version[D].installs` over semver versions lower than N, where N is the newest release in `daily_install_version` over R (OD-7): the highest semver version, pre-releases included (OD-10). Versions that aren't semver are never stale (OD-8). |
| Environment, games, feature shares | Σ `installs` ÷ Σ `daily_ext.ext_reports` over R (install-day weighted, research R4) |
| Extended coverage (FR-026) | Σ `ext_reports` ÷ Σ `reports` over R |
| Public summary (FR-029) | `reports` for asOf; Σ `reports` over the 30 days ending asOf; `meta.reports_total`; `count(activity where last_seen ≥ asOf − 29)`; `asOf` |

For the dashboard, `asOf` is today (the current UTC day, in progress); the public summary keeps the latest complete day. When no `daily_basic` row exists in R, every view returns its empty state instead of zeros (FR-027).
