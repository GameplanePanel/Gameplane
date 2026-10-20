# Research: Default telemetry destination, extended telemetry, and telemetry dashboard

**Feature**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md) | **Date**: 2026-10-06

Each entry records a decision, why it was made, and what else was considered. Facts about current code cite `file:line` as of `master` at `d2cc7956`.

## Baseline facts

- **Reporter.** `api/internal/telemetry/telemetry.go` is a ticker loop. Its first report goes out one full interval (24 h) after process start (`Run`, `time.NewTicker(r.interval)`). An API that restarts more often than daily therefore never reports, which violates FR-007. It reads the admin toggle from `config.telemetry.sendMetrics` (`enabled()`), and counts GameServers and GameTemplates on the local cluster only (`count()`).
- **Wiring.** `api/cmd/main.go:392` starts the reporter with a hard-coded `24*time.Hour`. `--telemetry-endpoint`/`GAMEPLANE_TELEMETRY_ENDPOINT` defaults to `""`, which means off (`api/cmd/main.go:541`).
- **Chart.** `charts/gameplane/templates/api.yaml:323-329` auto-wires `http://gameplane-telemetry-receiver.<ns>.svc:8080/ingest` when `api.telemetry.receiver.enabled` is set and `api.telemetry.endpoint` is empty. The receiver is a `Deployment` with `replicas: {{ $r.replicas }}`, `readOnlyRootFilesystem`, and no volume. Its NetworkPolicy admits port 8080 only from the API pod, plus an optional Prometheus namespace (`charts/gameplane/templates/telemetry-receiver.yaml`).
- **Receiver.** `telemetry-receiver/main.go` uses only stdlib and `client_golang`. Its strict token-walking decoder (`decodePayload`) rejects any key other than `version`, `servers` and `templates`. Metrics are kept in memory only.
- **API storage.** SQLite is the default and PostgreSQL is optional. New migrations go in `api/internal/db/migrations/common/` as portable SQL; the latest is `014_sessions_digest_reset.sql`. `Store.Migrate` (`api/internal/db/db.go:99`) is called by both `serve` (`api/cmd/main.go:163`) and `bootstrap-admin` (`api/cmd/bootstrap.go:47`).
- **API RBAC.** The API's ClusterRole already grants `get/list/watch` on `nodes`, all Gameplane CRDs including `clusters`, `modulesources` and `backupschedules`, and discovery (`charts/gameplane/templates/api.yaml:15-65`). The handlers already list nodes (`api/internal/handlers/cluster.go:187`) and read `ServerVersion()` (`cluster.go:446`).
- **API replicas.** `charts/gameplane/values.yaml:120` says "Keep at 1", because SQLite is single-writer and some locks are per-process.
- **Admin permissions.** `/admin/config` splits `config:read` (GET) from `config:manage` (`api/internal/rbac/rbac.go:209-210`). Any other `/admin/*` route falls through to the admin wildcard (`rbac.go:217`).
- **Module provenance.** A module-managed GameTemplate carries `gameplane.local/module-name` and `gameplane.local/module-source` labels (`operator/api/v1alpha1/module_types.go:135-139`). The chart's default ModuleSource (`defaultModuleSource.name: default`, OCI `ghcr.io/gameplanepanel/gameplane-modules`) lists the official catalog in `defaultModuleSource.oci.modules` (`charts/gameplane/values.yaml:548-584`) and verifies bundles against the chart-shipped cosign key.
- **Web.** There is no i18n framework yet (spec 019 is still Draft), so UI strings are English literals. `AppLayout.tsx:213` already mounts a sticky global banner (`SafeModeBanner`), which is the placement precedent. `AdminSettings.tsx:1545-1576` holds the current single-switch `TelemetrySection`.
- **E2E.** No telemetry E2E exists today. The buckets are `operator api-auth api-roles api-rbac api-agent api-mods ratelimit bot-fast bot-heavy multicluster upgrade` (`test/e2e/buckets.sh:357`). Every bucket shares one Helm install in `deploy/kind/e2e.sh:301`, and `Env.PortForward` exists (`test/e2e/env.go:342`).
- **Egress.** The chart restricts egress only in the games namespace (`networkpolicies.yaml`). The API pod's egress is unrestricted, so the default destination needs no chart egress change.

---

## R1. One source of truth for the report contract

**Decision**: Add a new stdlib-only Go module, `telemetryschema/`. It holds the basic and extended report types, every enumeration and band, the band functions, the strict decoder (moved out of the receiver's `decodePayload` and extended), sanitisation, and the embedded official-module catalog. `api` and `telemetry-receiver` both import it through `replace ../telemetryschema`, following the netguard and gameaction precedent (`api/go.mod:5-16`, `api/Dockerfile:7-9`).

**Rationale**:
- FR-013 requires a *fixed, published* set of categories. Having one package define the categories that the API sends and the receiver accepts means the two sides cannot drift.
- SC-006 requires the preview to match the payload, which is easier when both are built from the same types.
- The receiver stays a single static binary, so it can still be run standalone.

**Alternatives considered**:
- *Duplicate the enumerations in both modules and test them against a shared fixture.* This is cheaper up front, but tests that read across modules are brittle and drift is only caught after the fact.
- *Have the API import a sub-package of the receiver module.* This inverts the dependency (client depends on server) and pulls the receiver's `sqlite` and `prometheus` dependencies into the API's module graph.

**Cost**: a 16th Go module must be added to `go.work`, `Makefile` `GO_MODULES`, both module matrices in `.github/workflows/ci.yaml` (`:457` and `:549`), the CI path filters, Dependabot, the `CLAUDE.md` repo map and coverage list, and `docs/agent-architecture.md`. It also needs its own `specs.md` (constitution IV). The coverage gate is **90**, matching gameproto (90) and slightly below netguard and gameaction (91), which are the other pure-logic shared libraries. This cost is recorded in plan.md under Complexity Tracking.

## R2. Wire format and backward compatibility

**Decision**: The three basic fields stay at the top level, unchanged. The extended tier is one optional object, `ext`, with its own `schema` integer (starting at `1`). The full shape is in [contracts/report-schema.md](contracts/report-schema.md). The same `POST <endpoint>` URL carries both tiers.

Fallback for older receivers (FR-016, SC-013):
- If a report that carries `ext` gets HTTP **400**, the API immediately re-POSTs the same report without `ext`, once.
- The API then records `ext_unsupported_until = now + 7 days` for that endpoint, so it doesn't send two POSTs every day.
- A receiver that understands `ext` but not a newer `ext.schema` value also answers 400, so the same fallback applies.
- Identity refusals use different codes, so they never trigger this fallback: **403** for a bad signature, stale send time or replay, and **409** for an ID claimed by another key (R20).

**Rationale**:
- Old receivers reject unknown keys (`decodePayload` default branch), so a 400 is a guaranteed and observable signal.
- Keeping one URL means operators who configured a full `/ingest` URL don't have to reconfigure anything.

**Alternatives considered**:
- *A versioned path such as `/v2/ingest`.* Deriving it from an operator-supplied URL is fragile, and the old receiver's 404 would need the same fallback logic anyway.
- *Negotiating through a header.* Old receivers ignore headers, so they would still reject the body.

## R3. Receiver persistence

**Decision**: Store data in SQLite, using `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`, and already vetted because the API uses it at `api/go.mod:41`).
- **Location.** The file is `$DATA_DIR/telemetry.db`. When `DATA_DIR` is unset, the receiver uses an in-memory database and logs a warning, so a standalone `docker run` with no volume keeps working, but nothing survives a restart.
- **Chart.** The chart always sets `DATA_DIR=/data` and mounts a PVC by default (`receiver.persistence.enabled: true`, 1 Gi). It uses `strategy: Recreate`, and the template fails if `replicas > 1` while persistence is enabled.
- **Writes.** All writes go through one serialized writer, with WAL mode on.

**Rationale**:
- FR-020 and SC-009 require data to survive restarts.
- 12–24 months of daily aggregates plus about 10k activity records amounts to a few MB.
- SQLite fits a single-replica collector and the distroless image, and needs no new infrastructure (constitution: Kubernetes primitives, meaning a PVC, first).

**Alternatives considered**:
- *PostgreSQL.* This would add an operational dependency for every self-hoster.
- *A JSON file rewritten atomically.* Rewriting it on every ingest doesn't scale to 10k activity records.
- *Prometheus remote storage.* That stores raw-ish time series and cannot do the per-install deduplication from R5.

## R4. Aggregation model (FR-020)

**Decision**: Pre-aggregate per UTC day at ingest time. The tables are listed in [data-model.md](data-model.md) §Receiver.
- **Fleet sizes.** These are stored as an **exact-value histogram** per day and metric, with values capped at 1000 (`1001` means "over 1000"). The FR-024 size bands and the exact median are both derived when the data is read.
- **Extended dimensions.** These go in one generic `daily_dim(day, dim, value, installs)` table, whose `dim` and `value` come only from `telemetryschema` enumerations.
- **Games.** These go in `daily_game(day, module, installs, servers)`.
- **Cumulative total.** A running total of reports is kept in `meta`, so the public summary's "total since collection began" (FR-029) is unaffected by retention sweeps.

**Rationale**:
- Every dashboard view becomes a scan of at most 365 rows per dimension, which keeps SC-010 (under 3 s) comfortable.
- Bounded enumerations bound the row count, so hostile input can't grow tables without limit (Edge Cases: malicious values).
- An exact histogram gives the real median that US4 scenario 3 asks for; band-only storage would only give a median band.

**Alternatives considered**:
- *Storing reports and aggregating at read time.* FR-014 forbids storing raw reports.
- *Storing bands only.* This loses the exact median.

**Range semantics**:
- **Basic "per day" views** chart daily values over the range.
- **Extended install-share views** (environment, games, features) show the sum of daily install counts divided by the sum of daily extended reports over the range, which is a share weighted by install-days. Each view is labelled "share of reporting installs, averaged over the range".
- **By-install version adoption** (US5 scenario 6) uses activity records, so it covers at most the activity-expiry window (OD-4). The dashboard states that window.

## R5. Unique installs without linking attributes (FR-014, FR-015)

**Decision**:
- **Install ID.** The API generates a UUIDv4 from `crypto/rand`.
- **Transformed ID.** The receiver stores only `HMAC-SHA256(pepper, installID)`, hex-encoded. The pepper comes from `ID_PEPPER`, a Secret. If that is unset, the receiver generates 32 random bytes on first start and keeps them in `meta`.
- **Activity record.** Each record holds the transformed ID, `key_fp` (the claim, R20), `first_seen`, `last_seen`, `last_sent_at` (replay guard, R20) and `last_version`. Nothing else.
- **Per-day deduplication.** For an extended report that has passed the R20 signature and claim checks:
  - If no record exists, the receiver inserts one, which also claims the ID, and counts the report as `new_installs++` and `active_installs++`.
  - If `last_seen < today`, it updates the record and counts `active_installs++`.
  - If `last_seen == today`, the report is a **duplicate**. It is answered with 204 but changes no aggregate, basic or extended; only `daily_basic.duplicates` is incremented.
- **Lapsed installs.** An hourly rollover finalizes each completed day D. It sets `lapsed_installs[D] = count(activity where last_seen = D − 30)` and then deletes records whose `last_seen` is older than the expiry (OD-4, ruled: 90 days; the enforced minimum is 31).

**Rationale**:
- An HMAC with a secret pepper means the stored value can't be matched against an ID taken from an install without that pepper.
- No extended attribute is ever written next to the ID, as FR-014 requires.
- Per-day deduplication stops restarts, retries and replays from inflating counts (Edge Cases: frequent restarts, spoofing).
- SC-008 (within 1 %) holds because uniques are exact distinct counts, not estimates.

**Alternatives considered**:
- *HyperLogLog sketches.* These are approximate (around 1–2 % error) and can't give exact new and lapsed counts.
- *Plain SHA-256 of the ID.* An attacker who knows the ID can link it. That is low risk, but the pepper costs nothing.

## R6. Per-source limits and source addresses (FR-014, FR-021, FR-032)

**Decision**: All limits are in memory only and keyed by source, which is never written to disk. A source is the IPv4 address (IPv4-mapped IPv6 included) or the IPv6 /64, so one host can't rotate through its prefix.
- **Ingest.** At most `INGEST_SOURCE_DAILY_LIMIT` *accepted* reports per source per UTC day, default **20**. Over the limit the receiver returns `429`.
- **Public summary.** A token bucket of 60 requests per minute per source, burst 10.
- **Dashboard login.** 5 attempts per minute per source.
- **Source IP.** This is the TCP peer address. `X-Forwarded-For` is trusted only when the peer falls inside `TRUSTED_PROXY_CIDRS` (default empty).
- **Cleanup.** Daily counters reset at UTC midnight. Token buckets are purged after 10 idle minutes (daily counters are not, or an idle source would regain its budget).
- **Capacity.** Each limiter tracks at most 100,000 sources and evicts the least-recently-used one to admit a new source. Two other designs were rejected:
  - Letting sources past the cap through untracked was a bypass (security review of 4c742e7d).
  - Pooling them into one overflow budget let an attacker who filled the table deny every new legitimate source (security review of de5d0974).

  With eviction, a source's budget can only be reset by an attacker who controls more distinct IPv4 addresses or IPv6 /64s than the cap. At that scale per-source limiting no longer applies. The dashboard token is at least 32 characters (R8), which keeps guessing it infeasible at any rate, and proof-of-work (R21) is the cost that does scale with volume.

**Rationale**:
- A default of 20 tolerates CGNAT and shared egress (several homelabs behind one address) while capping how much one source can distort a day.
- Trusting proxy headers only from configured networks stops clients from spoofing their source by setting `X-Forwarded-For` themselves.

**Alternatives considered**:
- *A limit of 1 per source per day.* This breaks every install behind a shared NAT.
- *Proof-of-work or registration.* This can't be done without per-install secrets (spec Assumptions).

## R7. Private dashboard delivery (FR-022–FR-028)

**Decision**:
- **Rendering.** The receiver renders the dashboard itself with `html/template`. CSS and templates are embedded with `go:embed`, and charts are **inline SVG** generated server-side. There is **no JavaScript**.
- **Range selection.** The range is chosen with plain links (`?range=7|30|90|365`, default 30).
- **Listener.** The dashboard has its own listener, `DASHBOARD_LISTEN_ADDR` (default `:8081`), separate from the public listener (`:8080`: ingest, healthz, public summary). Templates use no inline `style` attributes or `<style>` blocks, because the CSP sets `style-src 'self'`; SVG presentation attributes are fine. Prometheus `/metrics` also moves to the dashboard listener, behind the token (spec Q8), so no aggregate other than the public summary is readable from the public port.
- **CSP.** `default-src 'none'; style-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'`.
- **Chart design.** Designs come from frames in `telemetry-receiver/telemetry-dashboard.pen` (spec Q10), a separate Pencil file seeded with a copy of the HeroUI design, exported to `telemetry-receiver/design-export/`. The rendered HTML recreates the HeroUI look in plain CSS, because there is no React (constitution II, by analogy). The `dataviz` skill is applied when the frames are designed and again when the SVG is implemented.

**Rationale**:
- Rendering in-process keeps the receiver one static binary with no build toolchain, as required for self-hosting (FR-033).
- Without JavaScript, the CSP can be very strict.
- A separate port lets the project's provider expose only `:8080` publicly while the dashboard stays private at the network level as well.

**Alternatives considered**:
- *Reusing the `web/` React app.* This would couple the receiver's release to the dashboard build, and the dashboard isn't deployed next to a public provider.
- *A Grafana dashboard.* This needs Grafana and Prometheus history, can't express the dedupe-aware install views, and would expose the provider's metrics endpoint.

## R8. Dashboard access credential (FR-022, FR-023, FR-028)

**Decision**: There is one operator credential, `DASHBOARD_TOKEN`, which comes from a Secret.
- **No token.** If `DASHBOARD_TOKEN` is unset, the dashboard listener **is not started** at all, so the dashboard is unavailable rather than open.
- **Strength (FR-041, Q11).** The receiver exits at startup when `DASHBOARD_TOKEN` is set but shorter than 32 characters, the same way it refuses retention below the minimum. The runbook generates the token from 32 random bytes. Per-source login limits (R6) can be outrun by anyone holding many IPv6 /64s, so the token's length, not the limiter, is what makes guessing infeasible (security review of 0819cec1).
- **Browser login.** `POST /login` compares the token in constant time. On success it sets a cookie containing `expiry || HMAC-SHA256(K, expiry)`, where `K = HMAC-SHA256(DASHBOARD_TOKEN, "gameplane-telemetry-session")`. The cookie is `HttpOnly; Secure; SameSite=Strict; Path=/` with a 12 h lifetime. Because `K` is derived from the token, replacing the token invalidates every session, and the stored data is untouched.
- **CSRF.** `POST /login` and `POST /logout` require a same-origin `Origin` (or `Referer`) header in addition to SameSite.
- **Scripts.** The JSON views endpoint also accepts `Authorization: Bearer <token>`.
- **Refusals.** Any unauthenticated HTML request is redirected to `/login`. Any unauthenticated JSON request gets `401 {"error":"unauthorized"}`. A wrong token re-renders the login page with "Invalid credentials". No response varies with whether any data exists.

**Rationale**:
- Single-credential access is a spec Assumption.
- A stateless signed cookie needs no session table, and it rotates naturally with the token.
- The generic responses follow the same privacy posture as Gameplane's own login (CLAUDE.md core rule 3).

**Alternatives considered**:
- *HTTP Basic auth.* Browsers cache it, and it can't be logged out of.
- *OIDC.* This is out of scope, since there are no multi-user accounts on the provider.

## R9. Public summary (FR-029–FR-032)

**Decision**: `GET /v1/summary` on the public listener, returning only the five values in [contracts/receiver-http.md](contracts/receiver-http.md).
- **When disabled.** If `PUBLIC_SUMMARY` is not `true` (the default), the route returns 404.
- **Caching.** The response is computed from a snapshot cached for 5 minutes, sent with `Cache-Control: public, max-age=3600` and an `ETag`.
- **CORS.** `Access-Control-Allow-Origin: *`, because the response is read-only and carries no credentials.
- **Rate limit.** As in R6.

**Rationale**:
- Badge and website use cases need cross-origin reads that can be cached.
- The snapshot cache keeps public polling off the database (FR-032, US6 scenario 4).

**Alternatives considered**:
- *A shields.io badge format.* It's a different representation of the same data, and can be added later without changing the data contract.

## R10. Telling a new install from an existing one

**Decision**:
- **Fresh check.** `Store.Migrate` checks whether `schema_migrations` held **zero rows** when it started; if so, the database is fresh.
- **Seeding.** After applying migrations, `Migrate` runs a one-time seeding step in the same process. If no `telemetry_state` row exists, it inserts one:
  - **fresh:** `consent_source='default'`, and `config.telemetry = {"sendMetrics":true,"extended":true}`.
  - **existing:** `consent_source='legacy'`. Any saved `config.telemetry` is kept, with `extended:false` added; with nothing saved, the config row stays absent, which means off.

**Rationale**:
- The check runs inside `Migrate`, which both entrypoints call, so it's correct whichever runs first. That includes `bootstrap-admin` running before the first `serve`, which would otherwise make a fresh install look existing.
- Reinstalling over an existing PVC, or reusing a populated PostgreSQL database, counts as existing, as the spec's Edge Cases require.

**Alternatives considered**:
- *Counting users.* Wrong after `bootstrap-admin`.
- *A Helm hook that marks fresh installs.* Breaks for `--reuse-values` and non-Helm installs.
- *A pure-SQL migration.* Portable SQL can't read the existing JSON config value.

## R11. Consent, notice and state storage (FR-003–FR-005, FR-012, FR-017–FR-019)

**Decision**: Three stores, split by who may write them.
- **`config.telemetry`** (existing key → JSON) holds the admin's choice, `{sendMetrics, extended}`. It is still edited via `PUT /admin/config/telemetry` (`api/internal/handlers/config.go:56`). Saving `sendMetrics: false` also saves `extended: false` (FR-003, spec Q7), and the UI disables the extended switch and shows it off while basic is off. A post-save hook for the `telemetry` section (following the `auth` hook at `api/internal/handlers/config.go:131`) does three things in the same transaction:
  - sets `consent_source='admin'`
  - deletes the install ID when extended goes off
  - creates one when extended goes on

  When the operator has disabled telemetry, the handler returns `409`.
- **`telemetry_state`** (new singleton table) holds machine state: consent source, install ID, notice-shown time, schedule, delivery status, and the extended-fallback marker. No admin config endpoint can write it.
- **`telemetry_notice_acks`** (new table, keyed by `user_id`) records each admin's notice dismissal.
- **Notice condition.** The notice is pending for a user who holds `config:manage` when all of these hold:
  - a destination is in effect, meaning the kind isn't `none` or `disabled`
  - `consent_source = 'default'`
  - the user has no ack row
- **Report gate.** Reports are allowed only when `sendMetrics` is true, the destination kind is not `disabled` or `none`, and either `consent_source` isn't `'default'` or `notice_shown_at` is set.

**Rationale**:
- The install ID can't be written through the generic config endpoint, so no config write can set or spoof it.
- The Admin Settings section keeps its existing save flow.
- "Shown" is recorded when the dashboard actually renders the notice (`POST .../notice {"action":"seen"}`), which matches the spec's "shown" wording more closely than recording when the API serves it.

**Alternatives considered**:
- *Everything in `config.telemetry`.* An admin `PUT /admin/config/telemetry` could then set the ID, and the delivery state would show up in GET `/admin/config`.
- *Gating reports on dismissal instead of display.* Stricter than the spec ("within 1 hour of the notice being *shown*"), and it would leave telemetry stuck if an admin never clicks.

## R12. Scheduling (FR-007, SC-005)

**Decision**: The schedule is persisted, and replicas claim each slot.
- **Polling.** The reporter checks every `min(5m, interval/4)`.
- **Gate open.** When the gate first opens (notice seen, or admin consent), it sets `next_due_at = now + U(0, min(15m, interval/4))`.
- **Claiming a slot.** A process claims a due slot with:
  ```sql
  UPDATE telemetry_state
  SET next_due_at = ?, last_attempt_at = ?
  WHERE id = 'singleton' AND next_due_at = ?
  ```
  It proceeds only if exactly one row changed.
- **After success.** `next_due_at = now + interval + U(0, interval/48)`.
- **After failure.** Back off 1 h, 2 h, 4 h, and so on, capped at `interval`, tracked in `consecutive_failures`. Reports are never queued or replayed.
- **Interval setting.** `--telemetry-interval` / `GAMEPLANE_TELEMETRY_INTERVAL` (default `24h`, minimum `1m`; chart value documented as testing-only).

**Rationale**:
- Persisting the schedule survives restarts.
- The conditional UPDATE is portable to SQLite and Postgres and stays safe even if someone ignores "Keep at 1" for API replicas.
- A spacing of at least `interval` after every *accepted* report gives the "at most one per 24 h" guarantee.
- Jitter spreads load on the default provider.

**Alternatives considered**:
- *Keeping the ticker.* Defeated by restarts.
- *Leader election.* Too heavy for a once-a-day POST.

## R13. Resolving the destination (FR-001, FR-002, FR-005, FR-006, FR-009, FR-017)

**Decision**: The API resolves exactly one destination kind, in this order:

| Condition (first match wins) | Kind | Endpoint |
|---|---|---|
| `--telemetry-disabled` / `GAMEPLANE_TELEMETRY_DISABLED=true` | `disabled` | none |
| endpoint flag set and `GAMEPLANE_TELEMETRY_BUNDLED=true` (set only by the chart's auto-wiring) | `bundled` | the flag value |
| endpoint flag set | `custom` | the flag value |
| `telemetry.DefaultEndpoint != ""` | `default` | the constant |
| otherwise | `none` | none |

- `telemetry.DefaultEndpoint` is a Go constant in `api/internal/telemetry`, set to `"https://telemetry.gameplane.net/ingest"` (OD-1, ruled 2026-10-07). That makes it the single authoritative place FR-002 asks for. The chart never repeats the URL.
- The `default` kind requires an `https` scheme; a non-https default is a startup error. TLS uses the system roots with full verification, as FR-009 requires.
- `custom` and `bundled` keep today's behaviour: any scheme, because the bundled receiver is plain in-cluster HTTP.
- The chart's new value `api.telemetry.enabled` (default `true`) renders `--telemetry-disabled` when false. The template uses `hasKey`, because `--reuse-values` from beta.8 lacks the key (F-214 precedent at `api.yaml:394-399`).

**Rationale**: SC-004 requires a single setting for either action: `api.telemetry.enabled=false` disables telemetry, and `api.telemetry.endpoint=…` redirects it. The `none` kind keeps today's behaviour exactly until a domain exists.

## R14. Where each extended field comes from (FR-011)

No new RBAC is needed; every source is already readable (see the baseline facts).

| Field | Source | Notes |
|---|---|---|
| `env.k8s` | `Discovery().ServerVersion()` Major/Minor | Digits are kept. Values not matching `^1\.[0-9]{1,3}$` are sent as `other`. |
| `env.distro` | R16 heuristics | |
| `env.arch` | `node.Status.NodeInfo.Architecture` across local nodes | Sorted and unique; any non-enumerated value is sent as `other`. |
| `env.nodes` | local node count | Bands: `1`, `2-3`, `4-10`, `11-50`, `51+`. The spec's "50+" means more than 50. |
| `games` | local GameServers → `spec.templateRef` → GameTemplate labels | See R15. |
| `features.wakeOnConnect` | any local GameServer with `spec.idle.wakeOnConnect` | `operator/api/v1alpha1/gameserver_types.go:152`, `:218` |
| `features.tunnels` | set of `spec.tunnel.provider` where `tunnel.enabled` | Enumeration: `frp`, `tailscale`, `playit`. |
| `features.capture` | API `cfg.captureFeatureEnabled` | |
| `features.backups` | at least one BackupSchedule in the cluster | |
| `features.sso` | Helm OIDC configured (`cfg.oidcIssuer != ""`), or a DB `auth` provider of kind `oidc`, `google` or `github` that is enabled | |
| `features.auditForwarding` | `cfg.auditWebhookURL != ""`, or the S3 sink is configured (`cfg.auditS3Endpoint != "" && cfg.auditS3Bucket != ""`, `api/cmd/main.go:234`) | stdout logging doesn't count |
| `features.clusters` | 1 (local) plus the number of `Cluster` CRs | Bands: `1`, `2-3`, `4-10`, `11+` |
| `features.db` | `cfg.dbDriver` | `sqlite` or `postgres` |
| `features.language` | `"en"` until spec 019 ships a language setting | Spec 019 owns adding real values to the enumeration. |

**Counting scope**: servers, templates and games are counted on the **local cluster only**, the same as today's basic `servers` and `templates`. FR-010 keeps the basic semantics unchanged, and remote reads can fail. Multi-cluster use shows up through `features.clusters`.

**Superseded by R23 (amendment 2026-10-14)**: from extended schema 2, the counts, games, cluster-derived features and environment fields cover every reachable managed cluster, not the local cluster only (OD-11, OD-15, OD-16). The table above describes schema 1.

## R15. Official modules and look-alikes (FR-011, FR-034)

**Decision**:
- **On the install.**
  - The chart passes `--official-module-source=<defaultModuleSource.name>` (`GAMEPLANE_OFFICIAL_MODULE_SOURCE`), and only when `defaultModuleSource.enabled`.
  - A GameServer counts as running official module `m` only if its template has all three of:
    - `managed-by=Module`
    - `module-source` equal to that source's name
    - `module-name = m`, where `m` appears in the source's `spec.oci.modules[].name` (or the git index)
  - Everything else is summed under `custom`: manual templates, uploads, other sources, and look-alike names. No other name ever leaves the cluster.
- **On the receiver.** `telemetryschema` embeds `catalog.txt`, which is generated from `charts/gameplane/values.yaml` `defaultModuleSource.oci.modules`. The receiver folds any key not in that list into `custom`, which bounds cardinality and blocks spoofed names.
- **Drift check.** A new script, `hack/check-telemetry-catalog.sh`, fails `make lint` and CI when the list and the chart differ. It runs alongside `hack/check-specs.sh`.

**Known limitation**: a module added to the catalog after a receiver was built counts as `custom` on that receiver until the receiver is updated. This is documented.

## R16. Kubernetes distribution detection

**Decision**: The first match wins, using signals the API can already read:

| Distribution | Signal |
|---|---|
| `k3s` | `gitVersion` contains `+k3s` |
| `rke2` | `gitVersion` contains `+rke2` |
| `k0s` | `gitVersion` contains `+k0s` |
| `eks` | `gitVersion` contains `-eks-` |
| `gke` | `gitVersion` contains `-gke.` |
| `aks` | node label `kubernetes.azure.com/cluster` |
| `openshift` | node label `node.openshift.io/os_id` |
| `microk8s` | node label `microk8s.io/cluster` |
| `minikube` | node label `minikube.k8s.io/name` |
| `kind` | node `spec.providerID` starts with `kind://` |
| `doks` | `providerID` starts with `digitalocean://` |
| `talos` | `nodeInfo.osImage` starts with `Talos` |
| `other` | anything else, including plain kubeadm |

**Rationale**: no new RBAC and no kube-system reads are needed, and the list is fixed and published (FR-013). Plain kubeadm can't be told apart cheaply, so it falls into `other`, which is accepted.

## R17. E2E strategy (constitution I)

**Decision**:
- **New `telemetry` bucket.** It contains one top-level test, `TestTelemetryLifecycle`, which calls `t.Parallel()` and runs ordered subtests. It is the bucket's only test, because later steps run `helm upgrade` on that bucket's own cluster. The receiver's ingest limit is raised for this cluster (`INGEST_SOURCE_DAILY_LIMIT=0`, meaning unlimited) and `api.telemetry.interval=1m`. The subtests, in order:
  - The fresh install's notice is pending.
  - Recording it as seen makes the first report arrive at the bundled receiver.
  - Duplicate reports on the same day are deduplicated.
  - Turning extended off makes the next report basic-only.
  - Resetting the ID shows up as a new install.
  - An API pod restart causes no extra report.
  - Pointing at a custom destination running the pinned `v0.2.0-beta.8` receiver image gets basic reports accepted, via the extended fallback.
  - The public summary returns exactly five keys.
  - A report reusing the install's ID but signed with another key gets 409. An unsigned report, a tampered body and a replayed body each get 403. None of them change any figure (SC-014).
  - A second test-only client claims a freshly reset ID before the install's first report. The install's next attempt then returns 409, it rotates its ID, and its report is accepted (SC-015).
  - An unauthenticated dashboard request reveals nothing.
  - `api.telemetry.enabled=false` leads to zero further reports over 3 intervals, and the settings show "disabled by operator".
- **Upgrade bucket.** Add an assertion that a beta.8 install with no saved choice upgrades to `sendMetrics=false`, `extended=false`, and no pending notice.
- **No test or dev install may ever reach the project provider.** `deploy/kind/e2e.sh` and `deploy/kind/up.sh` set `api.telemetry.receiver.enabled=true` explicitly, and the CI `helm template` jobs assert this. Without it, every CI and dev install would report to the project provider once OD-1 sets the default.

**Rationale**:
- The lifecycle steps depend on each other's state, so one sequential test is the honest shape.
- An isolated bucket keeps the cluster-wide `helm upgrade` away from other buckets and the login budget (CLAUDE.md: about 7 admin logins per bucket) untouched.
- Test-only configuration lives in chart values, not in production code paths.

## R18. Admin UI surface (US1, US3; constitution II)

**Decision**:
- **Notice.** A new `TelemetryNotice` banner, mounted in `AppLayout` next to `SafeModeBanner` and shown only to holders of `config:manage`. It records "seen" when it mounts and offers three actions: "Keep sharing", "Turn off extended", and "Turn off all". Each action records an acknowledgement, and the banner also links to Admin Settings.
- **Settings.** `AdminSettings` `TelemetrySection` is rewritten to show:
  - the destination line (kind and host, with a link to the data-handling statement on the project website, per the OD-2 ruling)
  - two switches; the extended switch is disabled while basic is off
  - the install ID with a "Reset ID" action, confirmed in a dialog
  - the live preview, as read-only formatted JSON
  - the delivery status line
  - when the operator has disabled telemetry, both switches are disabled and an explanatory message is shown
- **Copy.** The subtitle "No server names, player counts, or identifying data" changes to say that the extended tier includes a random install ID (FR-019).
- **Design.** The notice and settings frames are designed first in `design.pen` and exported to `design-export/` (core rule 1). The receiver's pages are designed in `telemetry-receiver/telemetry-dashboard.pen` and exported to `telemetry-receiver/design-export/`. The frames are:
  - the notice banner
  - the Telemetry settings section in its default, custom, bundled, disabled and none states
  - the receiver's dashboard login, overview (basic plus extended), empty-state and refusal pages

## R19. The language field before spec 019

**Decision**: `features.language` is always sent as `"en"`, and the enumeration is `["en"]`. When spec 019 lands, it adds its packs to the `telemetryschema` enumeration and wires the configured default. This cross-spec dependency is recorded in plan.md.

**Rationale**: FR-011 lists the field and FR-013 requires a fixed set. Sending a constant is honest about the current product state, and 019 extends it without a schema bump, because new enumeration values on an older receiver fold into `other`.

## R20. Signed reports and install-ID claims (FR-035–FR-038, US7)

**Decision**:
- **Algorithm.** Ed25519 (`crypto/ed25519`) with HKDF-SHA256 (`crypto/hkdf`). Both are stdlib (Go 1.24+), so `telemetryschema` stays stdlib-only and owns signing and verification for both sides.
- **Stable local value (Q5).**
  - `telemetry_state.signing_secret` holds 32 bytes from `crypto/rand`, base64-encoded. It is created the first time an install ID is created and kept for the install's lifetime, including across ID resets and extended off/on.
  - No endpoint, log, audit event or report ever exposes it.
  - It is never derived from the cluster's identity (kube-system UID, node names and so on), which keeps FR-012 intact.
- **Key derivation.** The key is:

  ```text
  seed = HKDF-SHA256(ikm = signing_secret, salt = installId, info = "gameplane-telemetry-signing-v1", L = 32)
  priv = ed25519.NewKeyFromSeed(seed)
  ```

  It is recomputed on every send and never stored. Because `installId` is part of the derivation, a reset (or an automatic rotation) yields a key that can't be linked to the previous one (US7 scenario 5).
- **What's on the wire.**
  - `ext.key` holds the base64url (unpadded) 32-byte public key.
  - `ext.sentAt` holds the RFC 3339 UTC send time.
  - The header `Gameplane-Telemetry-Signature: ed25519=<base64url signature>` signs the exact request body bytes. Signing raw bytes avoids JSON canonicalisation pitfalls, and because the key and send time are inside the body, both are covered.
  - Basic-only reports carry neither the fields nor the header (FR-038).
- **Receiver checks.** These run in order for reports that carry `ext`. They happen before the limiter counts the report as accepted and before any write. Every refusal has a fixed body and changes nothing:
  1. If the header is missing or malformed, or the signature doesn't verify against `ext.key` over the body: **403** `{"error":"bad_signature"}`.
  2. If `sentAt` is outside `[now − 36h, now + 1h]`: **403** `{"error":"stale"}`.
  3. The receiver looks up `activity[HMAC(pepper, installId)]`.
     - **Absent**: it claims the ID by inserting a record with `key_fp = SHA-256(ext.key)` and `last_sent_at = sentAt`.
     - **Present**: if `key_fp` differs, **409** `{"error":"id_claimed"}`. If `sentAt ≤ last_sent_at`, **403** `{"error":"replay"}`. Otherwise it updates the record.
  4. Aggregation and per-day deduplication then follow R5.
- **API behaviour (Q6).**
  - **409**: generate a new install ID (keep `signing_secret`), set `last_id_rotation_at`, rebuild and re-sign the report, and re-POST once in the same attempt. A second 409 in a row is recorded as `failed` with normal backoff, and the ID isn't rotated again until the next attempt.
  - **403**: recorded as `failed` with normal backoff. The ID is never rotated (Edge Cases: install clock wrong).
- **Claim lifetime.** The claim is the activity record. It expires when the record does (90 days, OD-4) and is re-established by the next valid report (FR-038).

**Rationale**:
- A claim on first use needs no project-issued secret and no registration step, which suits anonymous installs.
- Per-ID derived keys make "claim" and "reset unlinkability" the same mechanism.
- Signing the raw body covers every field, including the tier contents.
- Strictly increasing `sentAt` per ID, combined with the 36 h window, blocks replays of captured reports. Per-day deduplication alone would still let a capture be replayed on the next day.
- Storing the key's fingerprint rather than the key itself avoids keeping a second linkable identifier; the full key arrives with every report anyway.

**What signing does not solve**: anyone can invent fresh IDs and keys and claim them, so fabricated installs are still possible. Per-source limits (R6) and the "approximate, self-reported" labelling remain the defence. Real attestation would need a secret issued by the project to each install, which the anonymous model rules out (spec Assumptions).

**Alternatives considered**:
- *A secret stored in a Kubernetes Secret.* It survives a database wipe, but the ID doesn't, so there's no gain, and anyone who can read Secrets in the namespace could forge reports (Q5).
- *The kube-system namespace UID.* It isn't secret, needs new `namespaces` RBAC, and conflicts with FR-012 (Q5).
- *HMAC with a key shared with the provider.* The provider would then hold the secret and could forge reports itself, and the secret would have to be distributed.
- *Counting only the basic part on a mismatch.* An impersonator would still inflate basic counts (Q6).
- *Mutual TLS client certificates.* This needs a CA and per-install issuance, and breaks for plain-HTTP bundled receivers.

## R21. Proof-of-work on report ingestion (FR-039, FR-040, Q12, OD-5)

**Decision**: An Anubis-style challenge round trip on the public listener, off by default (`INGEST_POW=false`).
- **Challenge.** `GET /v1/challenge` returns `{"challenge": "<token>", "bits": n, "expiresAt": "<RFC 3339>"}` with `Cache-Control: no-store`, or `404` when proof-of-work is off. The token is `base64url(payload) "." base64url(mac)`, unpadded:
  - `payload` is the version byte `1`, the issue time (Unix seconds, 8 bytes big-endian), `bits` (1 byte) and 16 random bytes.
  - `mac` is the first 16 bytes of `HMAC-SHA256(K, payload)`. `K` is 32 random bytes generated at process start and kept only in memory.

  A challenge expires 15 minutes after it is issued. A restart invalidates outstanding challenges, and senders recover through the one resend of FR-040.
- **Solution.** The sender finds a decimal `nonce` (an unsigned 64-bit integer) such that `SHA-256(token ":" nonce)` starts with at least `bits` zero bits, and sends the header `Gameplane-Telemetry-PoW: <token>:<nonce>` with the report. `telemetryschema` owns the hash check and the solver, so both sides share one definition, and caps `bits` at `MaxPoWBits = 26` (OD-5).
- **Verification.** It runs on `/ingest` after `AUTH_TOKEN` and before the body is read, so a refused report costs the provider one HMAC and one hash. The checks run in this order: header present and well formed, MAC valid, not expired, enough leading zero bits (as stated inside the token, so a sender can't lower it), not already used. A missing header gives **428** `{"error":"pow_required"}`; any other failure gives **428** `{"error":"pow_invalid"}`; both increment `gameplane_telemetry_refused_total{reason}`. A challenge is marked used as soon as its solution verifies, whatever happens to the report afterwards. Used challenges are remembered in memory until they expire. The set holds at most 1,000,000 entries; when it is full, new reports get **503** `{"error":"pow_busy"}` with `Retry-After: 60`.
- **Adaptive difficulty.** Each challenge's `bits` is computed when it is issued, from `r`, the number of challenges issued in the trailing 60 seconds including this one, and the normal rate `T` (`INGEST_POW_TARGET_PER_MIN`):
  - `target` is `0` when `r ≤ T`, and otherwise `min(MAX, ceil(2 · log2(r / T)))`.
  - The issued difficulty is `max(MIN, target, peak − ⌊(now − peakAt) / 5 min⌋)`. `peak` and `peakAt` record the last time `target` exceeded the decayed value. So increases apply to the very next challenge, and decreases come at one bit per 5 minutes.
  - The sender's expected work is `2^bits` hashes. A flood's total cost therefore grows roughly with the cube of its rate (`r · (r/T)²`), while normal traffic pays nothing. Ten times the normal rate gives 7 bits, a hundred times gives 14, and a thousand times gives 20 (SC-016).
  - `gameplane_telemetry_pow_bits` (a gauge) exposes the current difficulty, and `gameplane_telemetry_pow_challenges_total` counts issued challenges.
- **Challenge rate limit.** `/v1/challenge` has its own per-source token bucket (10 per minute, burst 5, keyed as in R6), so one source can't raise the difficulty for everyone. Many sources still can, up to `MAX`. That costs legitimate installs at most a few seconds of work per daily report, and it decays once the flood stops.
- **Sender (the API reporter).**
  - Before each POST, it requests the challenge URL: the endpoint URL with its last path segment replaced by `v1/challenge` (`https://host/prefix/ingest` becomes `https://host/prefix/v1/challenge`).
  - Any response other than `200` means the provider doesn't require proof-of-work, and the report is sent without it (FR-040, SC-013).
  - A `200` with `bits` above `MaxPoWBits` is recorded as `failed` with normal backoff.
  - Solving runs in the reporter goroutine on one core, with a deadline 30 seconds before the challenge expires.
  - A **428** response gets one new challenge and one resend in the same attempt; a second 428 is `failed`. The 409 rotation resend (R20) also fetches a fresh challenge, because challenges are single use.
- **Configuration.** `INGEST_POW` (default `false`), `INGEST_POW_TARGET_PER_MIN` (`60`), `INGEST_POW_MIN_BITS` (`0`) and `INGEST_POW_MAX_BITS` (`22`, at most `MaxPoWBits`), per OD-5. The Helm values `api.telemetry.receiver.ingestPow.{enabled,targetPerMinute,minBits,maxBits}` render them through `hasKey` guards (F-214), with `enabled: false`.

**Rationale**:
- The cost lands on what an attacker scales (reports), not on addresses, which a /48 holder has in abundance (R6).
- Legitimate senders are servers reporting once a day, so a few seconds of work during a flood goes unnoticed, and under normal load there is none.
- Issuing challenges is stateless (MAC'd), so the challenge endpoint can't exhaust memory. Only verified solutions, each of which cost work, occupy memory.
- The provider chooses the difficulty, so an operator under attack can respond by lowering `T` or raising `MIN`, without any install upgrading.
- Before this feature there was no default destination, so no existing install reports to the project's provider and none needs exempting. Self-hosted providers keep proof-of-work off by default, so older installs pointed at them keep working.

**What it does not solve**: SHA-256 proof-of-work is cheap on GPUs and dedicated hardware. It deters cheap bulk fabrication, not a determined, well-resourced attacker. Fabricated installs remain possible (R20), and dashboard figures stay labelled approximate.

**Alternatives considered**:
- *Proof-of-work bound to the report body, with no round trip (hashcash).* It saves a request, but replay protection still needs provider state, and raising the difficulty would need a client upgrade.
- *A memory-hard function such as Argon2.* It resists GPUs better, but `telemetryschema` must stay stdlib-only, and a Raspberry Pi install would pay far more.
- *Proof-of-work on the dashboard login.* It needs JavaScript, which the dashboard's CSP forbids (R7), and the 32-character credential (R8) already makes guessing infeasible.
- *A fixed difficulty.* Either every install pays all the time, or the difficulty is too low to matter under attack.

## R22. Version trend: ordering and storage (FR-042, OD-7–OD-10)

**Decision**:
- **Ordering** uses `golang.org/x/mod/semver`, already used in the workspace (`operator/internal/controller/semver.go`, `operator/internal/oci`) at v0.41.0. A label without a leading `v` is compared as `v` + label, so `0.3.0` and `v0.3.0` order alike. A label `semver.IsValid` rejects (`pr617`, `dev`, `edge-<sha>`, `other`, `invalid`) is unordered (OD-8).
- **Classification at ingest**, on a non-duplicate extended report for an existing activity record whose `last_version` differs from the new label: both semver and new > old is `upgraded`, new < old is `downgraded`, otherwise `unordered`. Labels that differ but compare equal under semver (`0.3.0` and `v0.3.0`) are not a change. The write shares the transaction that updates the activity record, so a refused or duplicate report changes nothing.
- **Stale at read time** from `daily_install_version`, so the "newest release in the range" (OD-7) follows the selected range instead of being frozen at ingest time.

**Rationale**: Daily counts keep FR-014 (no attribute linked to an ID) while still giving a per-day trend. Storing installs per version per day costs at most the bounded label set (`maxVersionLabels` = 128, plus `other` and `invalid`) per day.

**Alternatives considered**:
- A per-install version history table: rejected, it would link versions to an ID over time (FR-014).
- Stale at ingest against the newest version seen so far: rejected, the result would depend on arrival order and ignore the selected range.
- A hand-written semver parser in `telemetryschema`: rejected, the `x/mod` package is the reference implementation and is already vetted in this workspace.

## R23. Standalone and multicluster reporting (FR-043–FR-045, OD-11–OD-15)

**Decision**:
- **Counting across clusters** goes through the API's cluster registry (`kube.Registry`, `api/internal/kube/registry.go`), which already holds one client per managed cluster: the local cluster when there is one, and every registered remote that `kube.WatchClusters` loaded. `telemetry.Deps` gains the registry; `Collect` lists GameServers, GameTemplates and backup schedules on each client from `Registry.IDs()` and sums them. This replaces the local-only `listItems` path, whose comment says "The basic counts cover the local cluster only".
- **Unreachable clusters** (OD-12): a cluster whose list call fails is skipped for that report, the same way a failed list already yields 0 today. Each list call keeps a short per-cluster timeout so one dead cluster can't hold the report past its slot. The clusters band still counts registered clusters, not reachable ones.
- **Deployment marker** (OD-13): `ext.env.deployment` is `standalone` when the API runs with `--standalone` (`kube.Client.IsStandalone()`), `cluster` otherwise. The extended schema becomes `2`. `telemetryschema.Decode` accepts schema `1` without the field and schema `2` with it, so a new receiver keeps accepting older installs; an older receiver answers schema `2` with 400, and the existing basic-only fallback (FR-016, `api/internal/telemetry/telemetry.go`) applies. The receiver stores the marker as a `daily_dim` dimension, so no new table is needed.
- **Environment across clusters** (OD-15): the API reads each reachable cluster's server version and nodes, and sends `k8s` and `distro` as sorted sets and `arch` as the union. The receiver already writes one `daily_dim` row per architecture present; `k8s` and `distro` take the same path, and a schema 1 string is a one-value set. The node band is computed from the total node count of the reachable clusters (OD-16).
- **Order of release**: the receiver and `telemetryschema` learn schema `2` in the same change that makes the API send it. The default provider has to run the new receiver before the first release that sends schema `2`, so the fallback only affects self-hosted receivers that weren't upgraded.

**Rationale**: The registry is the one place that already knows every managed cluster and its credentials, so telemetry counts what the panel manages without a second discovery path. A marker in `env` reuses the enumerated-dimension pipeline (sanitising, `daily_dim`, share bars) end to end.

**Alternatives considered**:
- Each member cluster reports for itself: rejected, the user asked for reporting through the central panel (OD-11).
- Skip the report until every cluster answers: rejected by OD-12.
- A new top-level `ext.deployment` field instead of `env.deployment`: rejected, it is an environment attribute and the dashboard shows it with the other environment breakdowns.
- Infer standalone on the receiver from `env.k8s = other`: rejected, a cluster install whose version can't be read also sends `other`.

