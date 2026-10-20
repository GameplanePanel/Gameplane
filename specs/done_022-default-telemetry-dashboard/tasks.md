---

description: "Task list for feature 022: default telemetry destination, extended telemetry, signed reports, and telemetry dashboard"
---

# Tasks: Default telemetry destination, extended telemetry, and telemetry dashboard

**Input**: Design documents from `specs/022-default-telemetry-dashboard/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md), [OPEN-DECISIONS.md](OPEN-DECISIONS.md)

**Tests**: Test tasks are required, not optional. Constitution Principle I needs E2E coverage for every user-facing path, the spec's Assumptions list the E2E paths, and CLAUDE.md sets per-module coverage gates (telemetryschema 90, telemetry-receiver 70, api 80, web 92/76/82/92).

**Organization**: Tasks are grouped by user story (US1–US7 from spec.md), so each story can be built and verified on its own on top of Phases 1–2.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on unfinished tasks)
- **[Story]**: the user story the task belongs to (US1–US7)

## Execution rules (CLAUDE.md, binding on every task)

- **Verification.** Locally, run only `go build ./...` (inside the module) and `cd web && npx tsc --noEmit`. Never run `go test`, `npm test`, `make test|lint|cover`, envtest or E2E locally (rule 8). CI is the verification authority.
- **Delegation.** Implementation runs through `Workflow` scripts. Agents start at `haiku`, set `model:` on every `agent()`, and get reviewed one tier up. One scout writes a brief per phase with exact `file:line` and before/after code, and fixers edit blind from that brief (rules 13, 18).
- **Design first.** Any task that changes `design.pen` or `telemetry-receiver/telemetry-dashboard.pen` uses Pencil MCP only, never generic file tools (rule 2). Ask the user to open the file and save in the Pencil UI, then run the `design-export` skill, and commit the export with the design (rule 1). The receiver file exports to `telemetry-receiver/design-export/{json,screenshots}/`.
- **Commits.** Commit each logical unit signed (`git commit -s`) with conventional prefixes and the `Co-Authored-By` / `Claude-Session` trailers. Never amend pushed commits (rule 11).
- **Tests.** Don't delete or weaken a test. Tests whose behaviour this feature intentionally changes are listed in T002 and need sign-off first (system override 1).
- **Merge gate.** OPEN-DECISIONS OD-1 was ruled (2026-10-07): the default domain is telemetry.gameplane.net, and the merge gate is satisfied (plan.md, Merge gate).

---

## Phase 1: Setup (shared infrastructure)

**Purpose**: Create the branch, get test sign-off, and wire the new `telemetryschema` module into the build.

- [X] T001 Create branch `feat/default-telemetry-dashboard` from `master` and commit `specs/022-default-telemetry-dashboard/` (`.specify/feature.json` is git-ignored, so it stays local) as `docs(specs): add 022 default telemetry dashboard spec`.
- [X] T002 Get **explicit human sign-off** (system override 1) to rewrite these existing tests so they assert the new behaviour. Rewrites must keep or raise coverage and never delete a case without a replacement:
  - `api/internal/telemetry/telemetry_test.go`: `TestReportOnce_EnabledPostsAnonymousCounts`, `TestReportOnce_SendsAuthHeader`, `TestReportOnce_DisabledSkipsPost`. The reporter's constructor and send path change (research R12).
  - `api/internal/telemetry/telemetry_branches_test.go`: `TestNew_DefaultsInterval`, `TestEnabled_Branches`, `TestCount_UnknownKind`, `TestReportOnce_EndpointErrorPropagates`.
  - `web/src/routes/AdminSettings.test.tsx`, test "shows telemetry toggle and saves setting". `findByRole("switch")` assumes one switch, and there will be two (FR-019).
  - `web/src/routes/AdminSettings_sections.test.tsx`, test "toggles telemetry and saves". It may need the new `GET /admin/telemetry` handler.

  - `telemetry-receiver/main_test.go` (added 2026-10-06 for spec Q8): the `metrics()` helper (lines 38–50) calls the dashboard handler with `Authorization: Bearer <token>` instead of the public `routes()`, `TestMetricsMethodNotAllowed` (line 138) targets that handler, and a new case asserts that `GET /metrics` on the public `routes()` returns `404`. The `decodePayload` tests stay unchanged, because T017 keeps a `decodePayload` wrapper.
  - `.github/workflows/ci.yaml`, the "observability scrape TLS/reachability" step (about lines 773–812): the receiver ServiceMonitor and NetworkPolicy renders also set `api.telemetry.receiver.dashboard.tokenSecretRef.name`, the ServiceMonitor check expects `port: dashboard` and `bearerTokenSecret`, the NetworkPolicy check expects the Prometheus namespace on port 8081, and a new check asserts no receiver ServiceMonitor renders without a token.

  - `api/internal/handlers/config_test.go` `TestConfig_GetEmpty` (added 2026-10-07): Migrate now seeds the fresh-install telemetry default, so it becomes `TestConfig_GetFreshInstallDefaults` and asserts exactly the seeded `telemetry` key. The `telStore` and `bareStore` helpers in the reporter tests above upsert or remove the seeded row, with no assertion changes.

  - `api/internal/handlers/config_validators_test.go` `TestValidateTelemetry` (added 2026-10-07): the canonical output gains `"extended":false`, and a new case checks that `{"sendMetrics":false,"extended":true}` normalises to both false.

  **Signed off 2026-10-06** by the user ("Approve all", then "Approve" for the `/metrics` changes; "Assert the seeded default" for `TestConfig_GetEmpty` and "Assert the new shape" for `TestValidateTelemetry` on 2026-10-07). Record the sign-off in the PR description.
- [X] T003 Create `telemetryschema/go.mod` (`module github.com/GameplanePanel/gameplane/telemetryschema`, `go 1.26.0`, no requires) and `telemetryschema/doc.go` with a package comment: "the shared telemetry report contract used by api and telemetry-receiver; see specs/022-default-telemetry-dashboard/contracts/report-schema.md".
- [X] T004 Add `./telemetryschema` to the `use` block in `go.work`, and add `telemetryschema` to `GO_MODULES` in `Makefile:43` (after `gameproto`).
- [X] T005 [P] Add `telemetryschema` to both Go module matrices in `.github/workflows/ci.yaml` (the `module:` lists at about lines 457 and 549). Add `telemetryschema/**` everywhere `telemetry-receiver/**` appears as a path filter (about line 119), and to the api and telemetry-receiver image path filters in `.github/workflows/publish-edge.yaml`. Add a `gomod` entry for `/telemetryschema` in `.github/dependabot.yml`, copied from the `/telemetry-receiver` entry at about line 176.
- [X] T006 [P] Create `telemetryschema/.testcoverage.yml` (copy the `telemetry-receiver/.testcoverage.yml` format with `total: 90` and an updated header comment) and a non-empty `telemetryschema/specs.md` skeleton with the sections Purpose, Responsibilities, External interface (linking `specs/022-default-telemetry-dashboard/contracts/report-schema.md`), Key invariants, and Testing & coverage, so that `hack/check-specs.sh` passes.
- [X] T007 [P] Update the repository docs maps:
  - `CLAUDE.md`: in the repository map add `telemetryschema/   shared telemetry report contract (api, telemetry-receiver)`; change "links all 15 Go modules" to 16; add `telemetryschema 90` to the coverage minimums; add a row to the Architecture table.
  - `docs/agent-architecture.md`: add a `telemetryschema/specs.md` row next to Telemetry-Receiver (line 29).
- [X] T008 [P] Make the module-list checks accept the new module. Inspect how `hack/check-ci-report-coverage.sh`, `hack/check-publish-edge-paths.sh` and `hack/check-specs.sh` enumerate modules, and add `telemetryschema` to any list they read. Edit the lists only, not the checks' logic.
- [X] T009 Wire the dependency:
  - Add `require github.com/GameplanePanel/gameplane/telemetryschema v0.0.0` and `replace github.com/GameplanePanel/gameplane/telemetryschema => ../telemetryschema` to `api/go.mod` and `telemetry-receiver/go.mod` in the same change as their first import (T026, T017), following the netguard pattern at `api/go.mod:5-9`. `test/e2e/go.mod` gets the same lines in T088, with the test-only signing client.
  - Add `COPY telemetryschema/ ./telemetryschema/` to `api/Dockerfile` next to `COPY netguard/` (line 7).
  - In `telemetry-receiver/Dockerfile`, copy `telemetryschema/` before `go mod download` so that the `replace` path resolves.

**Checkpoint**: an empty `telemetryschema` module builds and is wired into CI, and the test sign-off is recorded.

---

## Phase 2: Foundational (blocking prerequisites)

**Purpose**: The shared report contract, install-side state and collection, and receiver storage. Every user story depends on them.

**⚠️ CRITICAL**: No user-story work starts until this phase is done.

### Report contract (`telemetryschema/`)

- [X] T010 [P] Create `telemetryschema/report.go`:
  - Types `Report{Version string; Servers, Templates int; Ext *Extended}`, `Extended{Schema int; InstallID string; Env Env; Games Games; Features Features; Key string; SentAt time.Time}`, `Env{K8s, Distro string; Arch []string; Nodes string}`, `Games{Official map[string]int; Custom int}`, and `Features{WakeOnConnect bool; Tunnels []string; Capture, Backups, SSO, AuditForwarding bool; Clusters, DB, Language string}`.
  - JSON tags exactly as in `contracts/report-schema.md`.
  - `Encode(Report) ([]byte, error)`, which emits sorted map keys and sorted arrays so the output is deterministic.
- [X] T011 [P] Create `telemetryschema/enums.go`:
  - The enumerations from the field-rules table in `contracts/report-schema.md`: Distros, Arches, Tunnels, DBs, `Languages = ["en"]`, NodeBands and ClusterBands.
  - `VersionRE` (moved from `telemetry-receiver/main.go:52`).
  - `K8sMinorRE = ^1\.[0-9]{1,3}$`.
  - `NodeBand(n int) string` (1, 2-3, 4-10, 11-50, 51+), `ClusterBand(n int) string` (1, 2-3, 4-10, 11+), and `FleetBands = []float64{0,1,2,5,10,25,50,100,250}`.
  - `SanitizeEnum(set, v) string`, which returns `other` for unknown values.
- [X] T012 Create `telemetryschema/decode.go`, with `Decode(body []byte) (Report, DecodeInfo, error)`:
  - Port the strict token-walking logic from `decodePayload` in `telemetry-receiver/main.go:85-186`: exactly one object, exact case-sensitive keys, no duplicates or nulls, nothing trailing.
  - Extend it with the optional `ext` object, validated against the field-rules table in `contracts/report-schema.md`.
  - Structural errors wrap `ErrInvalidPayload` with `%w`. `ext.schema > 1` returns `ErrUnsupportedSchema`.
  - Out-of-set values become `other`. Unknown `games.official` keys are added to `Custom`, using the catalog from T013.
  - A malformed `installId` sets `DecodeInfo.ExtDropped = true` and returns `Ext = nil`.
- [X] T013 [P] Create `telemetryschema/catalog.txt` (one module name per line, copied from `charts/gameplane/values.yaml` `defaultModuleSource.oci.modules` at about line 583) and `telemetryschema/catalog.go` (`//go:embed catalog.txt`, `IsOfficial(name string) bool`, `Catalog() []string`).
  - Add `hack/check-telemetry-catalog.sh` (POSIX sh). It extracts the values.yaml list and diffs it with `catalog.txt`, exiting 1 with the diff on mismatch.
  - Add a `check-telemetry-catalog` target to `Makefile`, and add it to the `lint:` prerequisites at `Makefile:266`. CI runs it as a `lint (netguard)` step, and the `specs` path filter covers the script, `telemetryschema/catalog.txt` and `charts/gameplane/values.yaml`.
- [X] T014 [P] Create `telemetryschema/sign.go` (research R20):
  - `const SignatureHeader = "Gameplane-Telemetry-Signature"`.
  - `NewSecret() ([]byte, error)`: 32 bytes from `crypto/rand`.
  - `DeriveKey(secret []byte, installID string) (ed25519.PrivateKey, error)`: `crypto/hkdf` with SHA-256, salt = installID, info = `gameplane-telemetry-signing-v1`, 32-byte seed, then `ed25519.NewKeyFromSeed`.
  - `PublicKeyString(priv) string`: base64url, unpadded.
  - `Sign(priv, body []byte) string`: returns `ed25519=<b64url sig>`.
  - `Verify(header, publicKey string, body []byte) error`, wrapping `ErrBadSignature`.
  - `KeyFingerprint(publicKey string) string`: hex SHA-256 of the raw key.
- [X] T015 [P] Create `telemetryschema/decode_test.go`, `enums_test.go`, `catalog_test.go` and `sign_test.go`, covering:
  - every reject case already covered by `telemetry-receiver/main_test.go` `TestDecodePayloadRequiresExactKeys`
  - every `ext` rule, including `other` folding, unknown modules going to `Custom`, `ExtDropped`, and schema 2 being rejected
  - band boundaries 1/2/3/4/10/11/50/51 and clusters 10/11
  - catalog membership
  - a fixed `DeriveKey` vector, sign/verify round trip, a single-byte tamper failing, and different IDs deriving different keys
- [X] T016 [P] Create `telemetryschema/specs.md` with the full content, replacing the T006 skeleton: types, enumerations, bands, the signature scheme, invariants (no free text, bounded categories), and the compatibility table from `contracts/report-schema.md`.
- [X] T017 Change `telemetry-receiver/main.go` to delegate decoding to `telemetryschema.Decode`:
  - Keep `decodePayload(body []byte) (payload, error)` as a thin wrapper, so the existing `main_test.go` tests keep compiling and passing unchanged.
  - The wrapper returns `errInvalidPayload` for any report that carries `ext`. This keeps today's behaviour until T072.
  - Replace the local `versionRE` with `telemetryschema.VersionRE`.

### Install-side state (API database)

- [X] T018 Create `api/internal/db/migrations/common/015_telemetry_state.sql` with tables `telemetry_state` and `telemetry_notice_acks`, with exactly the columns in `data-model.md` § API, including `signing_secret` and `last_id_rotation_at`. Follow the portability rules in `api/internal/db/migrations/README.md`: no `AUTOINCREMENT`, no `datetime(...)`, no `INSERT OR`, and no comment line ending in `;`.
- [X] T019 Change `Store.Migrate` in `api/internal/db/db.go:99`:
  - Before applying anything, count rows in `schema_migrations`. `fresh := count == 0`.
  - After applying, call `s.seedTelemetryState(ctx, fresh)`.
- [X] T020 Create `api/internal/db/telemetry.go`:
  - `seedTelemetryState` (research R10), idempotent through `INSERT ... ON CONFLICT (id) DO NOTHING`:
    - **fresh**: `consent_source='default'`, and upsert the config key `telemetry` = `{"sendMetrics":true,"extended":true}`.
    - **otherwise**: `consent_source='legacy'`. If the config key `telemetry` exists, rewrite it with `extended:false` added.
  - Helpers:
    - `TelemetryState` struct with `GetTelemetryState` and `UpdateTelemetryState`
    - `ClaimTelemetryDue(ctx, expected, next, now string) (bool, error)`, using `UPDATE telemetry_state SET next_due_at=?, last_attempt_at=? WHERE id='singleton' AND next_due_at=?`
    - `SetInstallID` and `ClearInstallID`
    - `EnsureSigningSecret`, which creates the secret once and never returns it outside the package API used by `api/internal/telemetry`
    - `MarkNoticeShown`, which sets the value only if NULL
    - `InsertNoticeAck`, `HasNoticeAck` and `DeleteNoticeAcksForUser`
- [X] T021 Call `DeleteNoticeAcksForUser` from the existing account-removal path in `api/internal/db/users.go`, in the same place other per-user rows are deleted (migrations README rule 3).
- [X] T022 [P] Create `api/internal/db/telemetry_test.go`, covering:
  - fresh versus existing seeding, including running `Migrate` twice (the bootstrap-admin then serve order), an existing DB with `sendMetrics:true`, and an existing DB with no telemetry row
  - two concurrent `ClaimTelemetryDue` calls where exactly one wins
  - notice ack lifecycle and user deletion

  `TestSharedMigrationsArePortable` in `api/internal/db/migrations_test.go` must pass for 015 without edits.

### Install-side destination and collection (`api/internal/telemetry/`)

- [X] T023 Create `api/internal/telemetry/destination.go`:
  - `const DefaultEndpoint = ""`, with a comment: "Project default telemetry destination. Empty until specs/022…/OPEN-DECISIONS.md OD-1 is ruled; the feature PR must not merge while empty (hack/check-telemetry-default.sh)."
  - `type Destination struct{ Kind, URL, Host string }`
  - `ResolveDestination(disabled bool, endpoint string, bundled bool) (Destination, error)`, implementing the research R13 table. A `default` kind whose scheme isn't `https` returns an error.
- [X] T024 [P] Create `hack/check-telemetry-default.sh` (POSIX sh). It reads the `DefaultEndpoint` value from `api/internal/telemetry/destination.go` and exits 1 unless it is non-empty and starts with `https://`. Add a separate job `telemetry-default-gate` to `.github/workflows/ci.yaml` that runs only this script. Don't add it to `make lint`.
- [X] T025 Add flags in `api/cmd/main.go`:
  - `--telemetry-disabled` / `GAMEPLANE_TELEMETRY_DISABLED`
  - `--telemetry-interval` / `GAMEPLANE_TELEMETRY_INTERVAL` (default `24h`; a value below `1m` is a startup error)
  - `--official-module-source` / `GAMEPLANE_OFFICIAL_MODULE_SOURCE`
  - `GAMEPLANE_TELEMETRY_BUNDLED`, read from the environment only

  Change the `--telemetry-endpoint` help text at line 541 to "URL to POST anonymous usage metrics to (empty = the project default)". Resolve the destination once at startup through `telemetry.ResolveDestination`, and exit on error.
- [X] T026 Create `api/internal/telemetry/collect.go` with `Collect(ctx, deps) (telemetryschema.Report, error)`. `deps` holds the kube client, store, config flags, version and official source.
  - **Basic.** Keep the current local-cluster counts from `count()` in `telemetry.go:146`.
  - **Extended.** Build it only when `extendedOn`, from the research R14 table: discovery `ServerVersion`, local nodes, GameServers, GameTemplate labels, BackupSchedules, `Cluster` CRs, `cfg.captureFeatureEnabled`, the OIDC flag or a DB `auth` provider, the audit webhook or S3 sink, and `cfg.dbDriver`. `language` is `"en"`.
  - **Signing fields.** `Key` = `PublicKeyString(DeriveKey(secret, installID))` and `SentAt = now`.
  - **Partial failures.** A failure for one field yields `0` or `other`, not an error.
- [X] T027 [P] Create `api/internal/telemetry/official.go`. Following research R15, a GameServer counts as official module `m` only when its template has all of:
  - label `gameplane.local/managed-by=Module`
  - label `gameplane.local/module-source` equal to `--official-module-source`
  - label `gameplane.local/module-name=m`, with `m` in that ModuleSource's `spec.oci.modules[].name` (or its git index), and `telemetryschema.IsOfficial(m)`

  Everything else, including look-alike names, adds to `Custom`.
- [X] T028 [P] Create `api/internal/telemetry/distro.go` with `detectDistro(gitVersion string, nodes []corev1.Node) string`, implementing the research R16 table in order.
- [X] T029 [P] Create `api/internal/telemetry/collect_test.go`, `official_test.go`, `distro_test.go` and `destination_test.go`, using the fake dynamic and typed clients:
  - every R14 field, including partial-failure fallbacks
  - official, custom, upload-source and look-alike modules
  - every R16 row plus kubeadm becoming `other`
  - every R13 row, plus a non-https default being rejected

### Receiver storage (`telemetry-receiver/`)

- [X] T030 Create `telemetry-receiver/store.go`:
  - Open `modernc.org/sqlite` (add it to `telemetry-receiver/go.mod`) at `$DATA_DIR/telemetry.db`. When `DATA_DIR` is empty, use a private in-memory database per store (`:memory:` with `SetMaxOpenConns(1)`, not a process-wide shared cache, so the many `newServer(config{})` calls in the existing tests stay isolated) and log a warning. `newServer(cfg config) *server` keeps its signature and opens an in-memory store; `main` opens the configured store and passes it in through a second constructor.
  - Enable WAL, with a single writer goroutine or a mutex.
  - Create every table in `data-model.md` § Receiver, including `activity.key_fp` and `activity.last_sent_at`.
  - Initialise `meta` keys `schema_version`, `collection_started` and `reports_total`. Generate and store `pepper` when `ID_PEPPER` is unset.
- [X] T031 Extend `loadConfig` in `telemetry-receiver/main.go` with every variable in the `contracts/receiver-http.md` Configuration table. Enforce `RETENTION_DAYS ≥ 365` and `ACTIVITY_EXPIRY_DAYS ≥ 31` (OD-3, OD-4), failing with a startup error. Add the second listener scaffold, which starts only when `DASHBOARD_TOKEN` is set. Keep `TestLoadConfigDefaults` passing.
- [X] T032 Add receiver persistence to the chart:
  - In `charts/gameplane/values.yaml`, add the `api.telemetry.receiver` keys `persistence`, `dashboard`, `publicSummary`, `pepperSecretRef`, `retentionDays: 730`, `activityExpiryDays: 90`, `ingestSourceDailyLimit: 20` and `trustedProxyCIDRs`, per `contracts/install-config.md`.
  - In `charts/gameplane/templates/telemetry-receiver.yaml`:
    - a PVC `gameplane-telemetry-receiver-data`, or an `emptyDir` when persistence is disabled, mounted at `/data`, with `DATA_DIR=/data`
    - `strategy: Recreate`
    - a `fail` when `replicas > 1` and persistence is enabled
    - env passthrough for the retention, expiry, limit and proxy settings
  - Read every new key through a `hasKey` or `dig` guard (F-214 precedent at `templates/api.yaml:394-399`).
- [X] T033 [P] Create `telemetry-receiver/store_test.go`, covering schema creation, reopening the same `DATA_DIR` (aggregates and pepper survive, SC-009), and in-memory mode.

**Checkpoint**: the contract, state, collection and storage foundations exist, and user stories can start.

---

## Phase 3: User Story 1 — New installs report by default, with an informed opt-out (P1) 🎯 MVP

**Goal**: A fresh install reports to the resolved destination by default, but only after an admin's dashboard has shown the notice. The notice lets the admin turn off extended or everything.

**Independent Test**: Fresh Kind install with the bundled receiver. Sign in, and the notice is pending. Once it has been shown, a report reaches the receiver within one interval. "Turn off all" stops all reports. An upgraded beta.8 install stays off with no notice.

- [X] T034 [US1] Rewrite `api/internal/telemetry/telemetry.go` (research R12, data-model § schedule):
  - The `Reporter` takes the `Destination`, interval, store, collect deps and auth header.
  - The tick is `min(5m, interval/4)`.
  - It evaluates "Gate open" (data-model § Derived values). While the gate is closed, `next_due_at` stays NULL. When it opens, `next_due_at = now + U(0, min(15m, interval/4))`.
  - It claims each slot with `ClaimTelemetryDue`, then builds the report with `Collect`, encodes it, and signs it with `telemetryschema.Sign` when `Ext != nil`.
  - It POSTs with `Content-Type: application/json`, the `Authorization` header when configured, and the signature header.
  - **400 with ext present**: re-POST without `ext` once, and set `ext_unsupported_until = now+7d` and `ext_unsupported_endpoint`.
  - **2xx**: set `last_success_at` and `last_outcome='ok'`, reset `consecutive_failures`, and set `next_due_at = now + interval + U(0, interval/48)`.
  - **Other errors**: set `last_outcome='failed'`, and back off `min(1h·2^n, interval)`.
  - Never queue or replay reports.
- [X] T035 [US1] Create `api/internal/telemetry/consent.go` with `ApplyConsent(ctx, tx, dest, interval, basic, extended bool, source string)` (it needs the destination for `ErrOperatorDisabled` and the interval to schedule the first slot; SQLite's single connection means it uses the tx helpers in a new `api/internal/db/telemetry_tx.go`), shared by the config hook and the notice actions. It:
  - writes the config key `telemetry`
  - sets `consent_source`
  - creates or clears the install ID (creating the signing secret through `EnsureSigningSecret` when an ID is created)
  - opens or closes the schedule

  It returns `ErrOperatorDisabled` when the destination kind is `disabled`.
- [X] T036 [US1] **Closes an interim consent gap (security review of 1ab7ee6b): until this task lands, fresh installs are seeded `sendMetrics:true` while the old reporter checks only that flag, so it can send before the notice. This task and T034 must ship in the same PR as Phase 2.** Replace the reporter start at `api/cmd/main.go:392` (`telemetry.New(..., 24*time.Hour)`) with the new constructor, using the resolved destination and the interval flag.
- [X] T037 [US1] Extend `api/internal/handlers/config.go` for the `telemetry` section:
  - Add `Extended bool \`json:"extended"\`` to `telemetryCfg` (line 724).
  - Normalise instead of rejecting: when `sendMetrics` is false, store `extended: false` too (FR-003, spec Q7). There is no 422 for this case.
  - In `put`, add a `telemetry` post-save hook next to the `auth` hook at line 131. It calls `telemetry.ApplyConsent(..., "admin")` in the same transaction as the config upsert, and returns `409` "telemetry is disabled by the operator" on `ErrOperatorDisabled`.
- [X] T038 [US1] Create `api/internal/handlers/telemetry.go` with `GET /admin/telemetry/notice` and `POST /admin/telemetry/notice`, exactly as in `contracts/api-telemetry-http.md` (the pending computation from data-model § Derived values; the four actions through `ApplyConsent`; `204` or `409`). Mount it in `api/cmd/main.go` next to the config handlers. In `api/internal/rbac/rbac.go`, add `{method: "GET", segment: "admin", prefix: "/admin/telemetry", perm: "config:read"}` and `{segment: "admin", prefix: "/admin/telemetry", perm: "config:manage"}` **before** `{segment: "admin", perm: "*"}` (line 217). The GET handler itself returns `{"pending":false}` to callers without `config:manage`.
- [X] T039 [P] [US1] Rewrite the signed-off tests from T002 in `api/internal/telemetry/telemetry_test.go` and `telemetry_branches_test.go`, and add new cases using a fake clock and `httptest`:
  - no POST while the source is `default` and the notice hasn't been seen
  - the first POST within 15 minutes of `MarkNoticeShown`
  - spacing of at least the interval
  - a new Reporter on the same database doesn't send early (restart)
  - the 400 fallback, then 7 days of basic-only
  - backoff growth and cap
  - two Reporters sharing one database send one POST per slot
  - the auth header is still sent
- [X] T040 [P] [US1] Create `api/internal/handlers/telemetry_envtest_test.go`, covering:
  - the notice is pending only for `config:manage` holders, a `default` source, and a destination other than `none` or `disabled`
  - `seen` sets `notice_shown_at` once
  - the effects of `keep`, `extended-off` and `all-off`
  - `409` when not pending
  - `PUT /admin/config/telemetry` with `{sendMetrics:false, extended:true}` returning 200 and storing `extended:false` with the ID deleted; 200 with side effects (ID created and deleted, `consent_source=admin`); and 409 when the operator has disabled telemetry

  The existing `api/internal/handlers/config_test.go:144-156` must pass unchanged.
- [X] T041 [US1] Design the "Telemetry notice" banner in `design.pen` through Pencil MCP, with states for the default and the bundled destination. It lists the basic fields and the extended fields (stating that a random install ID is included), shows the destination host, offers the actions "Keep sharing", "Turn off extended" and "Turn off all", links to Admin Settings, and links to the data-handling statement. Ask the user to save, run the `design-export` skill (`design-export/json/<id>.json`, `design-export/screenshots/<id>.png`, `design-export/MANIFEST.md`), and commit the design and export together.
- [X] T042 [P] [US1] In `web/src/lib/api.ts`, add `Telemetry.notice()` and `Telemetry.ack(action: "seen" | "keep" | "extended-off" | "all-off")` with response types from `contracts/api-telemetry-http.md`. Add an MSW default handler for `GET /admin/telemetry/notice` (`{pending:false}`) wherever the shared test handlers live (the same place the `/admin/config` defaults come from; `web/src/test/`).
- [X] T043 [US1] Create `web/src/components/ui/TelemetryNotice.tsx`, implementing the T041 design. It uses TanStack Query for `Telemetry.notice()` and posts `seen` once on mount with `void`. Each action calls `ack` and invalidates the `["telemetry"]` and `["admin-config"]` queries. Mount it in `web/src/components/AppLayout.tsx` beside the `SafeModeBanner` block (line 213), only for users who hold `config:manage`, using the existing permission helper the layout uses for admin navigation.
- [X] T044 [P] [US1] Create `web/src/components/ui/TelemetryNotice.test.tsx`, and add an `AppLayout` test case, covering:
  - it renders only while pending
  - `seen` is posted exactly once
  - each action posts the right body and hides the banner
  - it is hidden for users without `config:manage`
- [X] T045 [US1] In `deploy/kind/e2e.sh` (the helm command at about line 301) and the `Makefile` `dev-install` target (`deploy/kind/up.sh` has no helm command), add the telemetry-receiver image to the E2E image set (`docker-bake.hcl`, `.github/actions/build-e2e-images/action.yml`, the `e2e.sh` load loop and `make e2e-images`), and add `--set api.telemetry.receiver.enabled=true` with the comment "never let CI or dev installs reach the project provider (spec 022 R17)".
- [X] T046 [US1] Register a new bucket `telemetry`, using the `e2e-test-authoring` skill:
  - in `test/e2e/buckets.sh`: add it to `bucket_names` (line 357), add a `list_bucket` case, and add `bucket_telemetry()` returning `TestTelemetryLifecycle`
  - in the e2e matrix in `.github/workflows/ci.yaml` (about line 1140): `parallel: 1`, `test_timeout: 25m`, `job_timeout: 60`
- [X] T047 [US1] Create `test/e2e/telemetry_e2e_test.go` with `TestTelemetryLifecycle`. It calls `t.Parallel()` and runs ordered `t.Run` subtests (research R17).
  - **Setup.** Create the Secret `telemetry-dashboard`, then run `helm upgrade --reuse-values` (pattern: `test/e2e/upgrade_e2e_test.go:118`) with these values:
    - `api.telemetry.interval=1m`
    - `api.telemetry.receiver.dashboard.tokenSecretRef.name=telemetry-dashboard`
    - `api.telemetry.receiver.publicSummary.enabled=true`
    - `api.telemetry.receiver.ingestSourceDailyLimit=0`
  - **Subtests:**
    - `notice_pending_on_fresh_install`
    - `seen_opens_gate_and_first_report_arrives`, asserted through the receiver's `/metrics` `gameplane_telemetry_reports_total` on the dashboard port 8081 with `Authorization: Bearer <token>`, over `Env.PortForward` (`test/e2e/env.go:342`). That route comes from T067; the bucket runs on the finished branch, so the ordering is safe
    - `all_off_stops_reports`, followed by turning telemetry back on through `PUT /admin/config/telemetry`
    - `api_restart_does_not_resend_early`
  - Keep admin logins to 2 or fewer (bucket login budget).
- [X] T048 [US1] In `test/e2e/upgrade_e2e_test.go` (`TestUpgrade_FromPreviousRelease`), add assertions after the upgrade: `GET /admin/telemetry/notice` returns `pending:false`, and the `GET /admin/config` `telemetry` section is absent or `{sendMetrics:false}` (US1 scenario 5).

**Checkpoint**: US1 is demonstrable end to end against the bundled receiver, which still counts reports in memory only.

---

## Phase 4: User Story 2 — Operators can redirect or hard-disable telemetry (P1)

**Goal**: One install-time setting disables or redirects telemetry. The UI and the API reflect the operator's choice.

**Independent Test**: With `api.telemetry.enabled=false`, no report arrives over 3 intervals, the settings show "disabled by the operator", and `PUT` returns 409. With a custom endpoint, reports arrive only there. An old (beta.8) receiver still gets basic reports.

- [X] T049 [US2] Change the chart:
  - `charts/gameplane/values.yaml`: add `api.telemetry.enabled: true` and `api.telemetry.interval: 24h`, with comments from `contracts/install-config.md`. Rewrite the comment block at lines 231-235 to say that an empty endpoint means the project default.
  - `charts/gameplane/templates/api.yaml` (lines 323-329):
    - render `--telemetry-disabled` when `enabled` is false (`hasKey` guard, default true)
    - render `GAMEPLANE_TELEMETRY_BUNDLED=true` when auto-wiring the receiver
    - render `--official-module-source={{ .Values.defaultModuleSource.name }}` when `defaultModuleSource.enabled`
    - render `GAMEPLANE_TELEMETRY_INTERVAL` from `api.telemetry.interval`
- [X] T050 [P] [US2] Add `helm template` assertions to `.github/workflows/ci.yaml` (next to lines 775-804):
  - `enabled=false` renders `--telemetry-disabled`
  - the receiver renders `--telemetry-endpoint=http://gameplane-telemetry-receiver…` and `GAMEPLANE_TELEMETRY_BUNDLED`
  - a custom endpoint renders without `BUNDLED`
  - defaults render no endpoint
  - `charts/gameplane/testdata/upgrade-from-v0.2.0-beta.8-values.yaml` renders with `--reuse-values` semantics without failing
- [X] T051 [US2] Add `GET /admin/telemetry` to `api/internal/handlers/telemetry.go`. It returns `destination`, `operatorDisabled` and `consent`, with `installId`, `preview` and `status` set to `null` until T057.
- [X] T052 [US2] Design the Telemetry section of Admin Settings in `design.pen` through Pencil MCP, as one frame per destination state: `default`, `custom`, `bundled`, `disabled` (both switches disabled, with a "disabled by the operator" message) and `none`. Include, for use by US3 and US7:
  - the basic switch and the extended switch
  - the install-ID row with a "Reset ID" action and a confirm dialog
  - the read-only preview JSON block
  - the status line, in the never, ok, failed and "ID replaced on <date>" variants
  - the new subtitle that names the install ID

  Ask the user to save, run `design-export`, and commit.
- [X] T053 [US2] Change `TelemetrySection` in `web/src/routes/AdminSettings.tsx` (lines 1545-1576) to query `Telemetry.get()` (new in `web/src/lib/api.ts`) and render the destination line and the `disabled` and `none` states from the T052 design. Keep the existing basic switch's accessible name "Enable telemetry" or "Disable telemetry".
- [X] T054 [P] [US2] Tests:
  - Create `web/src/routes/AdminSettings_telemetry.test.tsx` covering each destination state, and that the switches are disabled and saving is blocked when the operator has disabled telemetry.
  - Extend `api/internal/handlers/telemetry_envtest_test.go` with every `destination.kind` and `operatorDisabled`.
  - Add the MSW default for `GET /admin/telemetry`.
- [X] T055 [US2] Add these subtests to `test/e2e/telemetry_e2e_test.go`:
  - `custom_destination_receives_only`: deploy a second receiver Deployment and Service with a unique name in the release namespace, `helm upgrade --reuse-values --set api.telemetry.endpoint=<it>`, and assert that its counter rises and the bundled receiver's doesn't.
  - `old_receiver_gets_basic`: the custom destination runs the beta.8 image `ghcr.io/valgulnecron/gameplane/telemetry-receiver:<beta.8 release tag>`. The scout confirms the tag format from `.github/workflows/release.yaml`. Assert that reports are accepted (FR-016, SC-013).
  - `operator_disabled_sends_nothing`: this must be the **last** subtest. Run `--set api.telemetry.enabled=false` and assert zero new reports over 3 intervals, `PUT /admin/config/telemetry` returning 409, and the notice not pending.

**Checkpoint**: US1 and US2 together cover the P1 install-side behaviour.

---

## Phase 5: User Story 3 — Admins see where data goes and exactly what is sent (P2)

**Goal**: Admin Settings shows the destination, both tier switches, the install ID with reset, a live preview identical to the payload, and the delivery status.

**Independent Test**: For each destination, with extended on and off, the preview matches what the receiver recorded. A reset shows a new ID. Turning extended off removes the ID.

- [X] T056 [US3] Add `POST /admin/telemetry/install-id` to `api/internal/handlers/telemetry.go`. It rotates through `SetInstallID(newUUIDv4)`, returns `200 {"installId"}`, and returns `409` when extended is off.
- [X] T057 [US3] Complete `GET /admin/telemetry`:
  - `installId` (null when extended is off)
  - `preview`, which is the `Collect` output and null when basic is off or the kind is `disabled` or `none`
  - `status` with `lastAttemptAt`, `lastSuccessAt`, `lastOutcome` and `lastIdRotationAt`

  Never include `signing_secret`.
- [X] T058 [P] [US3] In `web/src/lib/config.ts`, add `extended: boolean` to `TelemetryCfg` (line 86). In `web/src/lib/api.ts`, give `Telemetry.get()` its full response type and add `Telemetry.resetInstallId()`. In `web/src/test/factories.ts`, change the default at line 305 to `telemetry: { sendMetrics: false, extended: false }`.
- [X] T059 [US3] Finish `TelemetrySection` in `web/src/routes/AdminSettings.tsx` from the T052 design:
  - the extended switch (accessible name "Enable extended telemetry" or "Disable extended telemetry"), disabled and shown off while basic is off; turning basic off also clears extended in the form draft (FR-019, spec Q7)
  - the install-ID row with "Reset ID" and its confirm dialog
  - the preview JSON block
  - the status line with fixed phrases
  - the subtitle and helper copy from FR-019, replacing "No server names, player counts, or identifying data."

  Saves go through the existing `useSectionForm` and `PUT /admin/config/telemetry`.
- [X] T060 [P] [US3] Rewrite the T002-approved cases in `web/src/routes/AdminSettings.test.tsx` and `AdminSettings_sections.test.tsx` for two switches, and extend `AdminSettings_telemetry.test.tsx` with these cases: extended disabled and shown off while basic is off, turning basic off clears extended in the saved body, the reset flow, preview rendering, and status phrases. In `api/internal/handlers/telemetry_envtest_test.go`, add a case where the preview equals the body the reporter POSTs to an `httptest` receiver at the same instant, ignoring `ext.sentAt` (SC-006).
- [X] T061 [US3] Add these subtests to `test/e2e/telemetry_e2e_test.go`, before the US2 subtests:
  - `extended_off_sends_basic_only`
  - `reset_id_counts_as_new_install`, asserted through the receiver's views once US5 lands (until then, through the `gameplane_telemetry_extended_reports_total` metric on port 8081 with the Bearer token)
  - `preview_matches_received`, comparing the preview's version, servers and templates with the receiver's recorded latest-day values

**Checkpoint**: P1 and P2 install-side stories are complete.

---

## Phase 6: User Story 4 — Private dashboard of aggregate telemetry (P2)

**Goal**: The receiver persists basic aggregates and serves a token-protected, server-rendered dashboard that reveals nothing to unauthenticated visitors.

**Independent Test**: Feed synthetic basic reports across several days, sign in, and check every figure for 7, 30, 90 and 365 days. Every unauthenticated response is identical whether data exists or not. Rotating the token invalidates sessions without losing data.

- [X] T062 [P] [US4] Create `telemetry-receiver/ratelimit.go` (research R6):
  - a per-source accepted-reports-per-UTC-day counter, where `INGEST_SOURCE_DAILY_LIMIT=0` means unlimited
  - a generic token bucket
  - idle purge after 10 minutes and a reset at midnight
  - `sourceIP(r *http.Request, trusted []netip.Prefix) netip.Addr`, which honours `X-Forwarded-For` only when the peer is inside `TRUSTED_PROXY_CIDRS`
  - addresses are never written to the store or the logs
- [X] T063 [US4] Move the ingest handler from `telemetry-receiver/main.go` to a new `telemetry-receiver/ingest.go`, keeping auth, the size cap and status codes. Add the per-source limit (`429` plus `gameplane_telemetry_rate_limited_total{route="ingest"}`). Write the basic aggregates (`daily_basic.reports`, `servers_sum` and `templates_sum`; `daily_version`; `daily_fleet` with values capped at 1001; `meta.reports_total`) in one transaction. Keep the existing Prometheus series; they are served from the dashboard listener (T067). The existing `main_test.go` ingest tests and `cardinality_test.go` (added upstream in #578) must pass unchanged; only the T002-approved `/metrics` changes apply. Those tests call `newServer(config{})` and then `s.ingest` and `s.reg` directly, 160 times from one source, so the zero-value `config` must mean an in-memory store and no per-source limit (the 20-per-day default is applied by `loadConfig`, not by the zero value), `newServer` keeps its signature, and the 128-label `versionLabel` budget stays.
- [X] T064 [US4] In `telemetry-receiver/store.go`, add an hourly lifecycle job that is idempotent and resumes from `meta.rollover_through`. It deletes `daily_*` rows older than `RETENTION_DAYS`, and is started from `serve`. US5 adds the lapsed and expiry steps.
- [X] T065 [US4] Create `telemetry-receiver/views.go` with `BuildViews(ctx, store, rangeDays int, now time.Time) (Views, error)` for the basic block in the `contracts/receiver-http.md` `/api/v1/views` shape:
  - `reportsPerDay`
  - versions as the top 10, then `Other`, then `Invalid`
  - fleet bands and the exact median
  - `latestDay` totals
  - `empty`
  - `asOf` = yesterday (UTC). **Superseded 2026-10-08 (OD-6):** the dashboard ends on today (UTC, in progress); `/v1/summary` keeps yesterday.

  Invalid ranges fall back to 30.
- [X] T066 [US4] Load the `dataviz` skill, then design the receiver pages in `telemetry-receiver/telemetry-dashboard.pen` (seeded with a copy of the HeroUI design, spec Q10) through Pencil MCP: the login page (also used for the refusal, with "Invalid credentials"), the overview's basic section with the range selector (7, 30, 90, 365), the empty state, and the "approximate, self-reported" labelling (FR-026). Ask the user to open that file, then save it, run `design-export` into `telemetry-receiver/design-export/`, and commit.
- [X] T067 [US4] Create `telemetry-receiver/dashboard.go`, mounted on `DASHBOARD_LISTEN_ADDR` only when `DASHBOARD_TOKEN` is set. It implements these routes from `contracts/receiver-http.md`:
  - `GET /login` and `POST /login`, comparing the token in constant time, with a login limit of 5 per minute per source and a same-origin `Origin`/`Referer` check
  - `POST /logout`
  - `GET /`
  - `GET /api/v1/views`, accepting the cookie or `Authorization: Bearer`
  - `GET /metrics`, accepting `Authorization: Bearer` only, serving the existing Prometheus registry; remove `/metrics` from the public `routes()` so that it answers `404` there (FR-030, spec Q8)
  - `/static/*`

  The cookie is `gp_telemetry_session` = `expiry || HMAC(K, expiry)`, where `K = HMAC-SHA256(DASHBOARD_TOKEN, "gameplane-telemetry-session")`, with `HttpOnly; Secure; SameSite=Strict; Max-Age=43200`. Unauthenticated HTML requests get `303` to `/login`, and unauthenticated JSON requests get `401 {"error":"unauthorized"}`. Every response carries the CSP, `nosniff`, `no-referrer` and `no-store` headers.
- [X] T068 [US4] Create `telemetry-receiver/web/`, embedded with `go:embed`: `layout.html`, `login.html` and `overview.html` (basic section), `style.css`, and inline-SVG chart partials (columns for reports per day, horizontal bars for versions, a histogram for fleet sizes), following the T066 design (pink theme, dark first, light values from the same tokens). No inline `style` attributes or `<style>` blocks: the CSP sets `style-src 'self'`. Include the logo PNG from `website/design-assets/gameplane-icon.png` as a static asset. There is no JavaScript, and the range is chosen with links.
- [X] T069 [US4] Change the chart for the dashboard, in `charts/gameplane/templates/telemetry-receiver.yaml`:
  - `DASHBOARD_TOKEN` from `dashboard.tokenSecretRef` when it is named
  - Service port `dashboard` (8081) when the token is named
  - a NetworkPolicy rule for port 8081 admitting `dashboard.ingressFrom` peers; move the `serviceMonitors.scrapeNamespaceSelector` rule (`telemetry-receiver.yaml:107-116`) from port 8080 to 8081
  - `INGEST_SOURCE_DAILY_LIMIT` and `TRUSTED_PROXY_CIDRS` env
  - in `templates/servicemonitors.yaml`, render the receiver ServiceMonitor only when `dashboard.tokenSecretRef.name` is set, with `port: dashboard`, `path: /metrics` and `bearerTokenSecret: {name: <ref>, key: token}`
  - create `templates/NOTES.txt`, which prints a warning when `serviceMonitors.enabled` and the bundled receiver are on but no dashboard token is named (no receiver ServiceMonitor is rendered)
  - apply the T002-approved changes to the `ci.yaml` "observability scrape" step
  - a `CHANGELOG.md` note under T094: receiver metrics now need the dashboard token
- [X] T070 [P] [US4] Create `telemetry-receiver/views_test.go`, `dashboard_test.go` and `ratelimit_test.go`, covering:
  - the exact figures for a synthetic multi-day dataset in every range
  - the empty state
  - the **refusal invariant**: every unauthenticated response body and status is byte-identical between an empty store and a populated one
  - rotating the token invalidates an old cookie and keeps the data
  - Origin checks
  - the CSP header
  - the login limit
  - the ingest 429 response
  - `X-Forwarded-For` honoured only from trusted peers
  - `GET /metrics` returns `404` on the public listener, `401` on the dashboard listener without the Bearer token, and `200` with it
- [X] T071 [US4] Add these subtests to `test/e2e/telemetry_e2e_test.go`:
  - `dashboard_refuses_unauthenticated`, where `/` returns 303 to a login page that contains no digits from the data and `/api/v1/views` returns 401
  - `dashboard_shows_reports`, which signs in with the Secret's token through `Env.PortForward` on 8081 (setting the `Cookie` header by hand, since the session cookie is `Secure` and the port-forward is plain HTTP). Views end at yesterday (UTC), so same-day reports from earlier subtests are not in them: it asserts that `/api/v1/views` returns `200` with `asOf` = yesterday (**superseded 2026-10-08, OD-6:** views end on today and the check asserts `asOf` = today) and a well-formed body, and that `/metrics` (Bearer) counts the earlier reports. The exact figures are covered by `views_test.go`.

**Checkpoint**: the provider keeps history and shows basic views privately.

---

## Phase 7: User Story 5 — Extended telemetry shows how Gameplane is used (P3)

**Goal**: The receiver accepts extended reports, deduplicates them per install per day, and shows install, environment, game and feature views.

**Independent Test**: A synthetic population (known IDs, environments, games and features over several weeks, with resets, upgrades and lapses) produces exact extended figures, and a 30-day unique count within 1% (SC-008).

- [X] T072 [US5] In `telemetry-receiver/ingest.go`, accept `ext`, removing the T017 reject-on-ext in the `decodePayload` wrapper's caller. Keep the wrapper's tested behaviour for the basic path. With `id_hmac = HMAC-SHA256(pepper, installId)` (research R5):
  - **Record absent**: insert the record, `new_installs++`, `active_installs++`.
  - **`last_seen < today`**: update the record, `active_installs++`.
  - **Same day**: a duplicate. Increment `daily_basic.duplicates` and `gameplane_telemetry_duplicates_total`, and write nothing else.
  - **Non-duplicate**: write `daily_ext.ext_reports`, `daily_dim` (every dimension listed in data-model § Receiver) and `daily_game` (`installs++`, `servers += n`, with `custom` for the rest), and increment `gameplane_telemetry_extended_reports_total`.
- [X] T073 [US5] Extend the lifecycle job in `telemetry-receiver/store.go`. For each finalised day D, set `lapsed_installs[D] = count(activity where last_seen = D − 30)`, then delete activity rows with `last_seen < today − ACTIVITY_EXPIRY_DAYS`.
- [X] T074 [US5] In `telemetry-receiver/views.go`, add the `extended` block:
  - coverage
  - `installs.active1d`, `active7d` and `active30d`
  - `newPerDay` and `lapsedPerDay`
  - `versionsByInstall` with `windowDays = min(range, ACTIVITY_EXPIRY_DAYS)`
  - shares for `env.*`, `games`, `features`, `tunnels`, `clusters`, `db` and `language`, weighted by install-days as in research R4

  The block is `null` when the range has no extended reports.
- [X] T075 [US5] In `telemetry-receiver/telemetry-dashboard.pen`, add the overview's extended section: install KPIs, new and lapsed trends, environment, game and feature breakdowns, and the coverage note. Load the `dataviz` skill for charts and categorical colours. Ask the user to open that file, then save it, run `design-export` into `telemetry-receiver/design-export/`, and commit.
- [X] T076 [US5] Add `telemetry-receiver/web/overview_extended.html` and its chart partials, following the T075 design. When `extended` is null, render the "no reports in this range included extended data" message.
- [X] T077 [P] [US5] Create `telemetry-receiver/population_test.go`, `ingest_ext_test.go` and `views_perf_test.go`:
  - **Synthetic population** with a fake clock: resets, upgrades and lapses. Assert exact new, lapsed and active counts, and a 30-day unique count within 1% (SC-008).
  - **Dedupe**: same ID, same day.
  - **Schema check**: `activity` has exactly the columns `id_hmac, key_fp, first_seen, last_seen, last_sent_at, last_version` (FR-014, SC-012).
  - **Performance**: seed 365 days at 10,000 reports per day as aggregates, and assert `BuildViews(365)` takes under 3 s (SC-010).
- [X] T078 [US5] Add the subtest `extended_counted_once_per_day` to `test/e2e/telemetry_e2e_test.go`. Two reports from the same ID on the same day give `active1d = 1` and duplicates ≥ 1. After `extended_off_sends_basic_only` (T061), `ext_reports` doesn't increase.

**Checkpoint**: extended data is collected and visible. Signature enforcement follows in US7.

---

## Phase 8: User Story 6 — Public summary API (P3)

**Goal**: An opt-in, unauthenticated, cacheable endpoint exposes exactly five headline values.

**Independent Test**: With the summary enabled, the response has exactly five keys, plus cache and CORS headers. When disabled it returns 404. Heavy polling doesn't slow ingest.

- [X] T079 [US6] Create `telemetry-receiver/summary.go` with `GET /v1/summary` on the public listener, exactly as in `contracts/receiver-http.md`:
  - `404` unless `PUBLIC_SUMMARY=true`
  - a snapshot recomputed at most every 5 minutes
  - `Cache-Control: public, max-age=3600`, `ETag` with 304 support, and `Access-Control-Allow-Origin: *`
  - a token bucket of 60 per minute with burst 10 per source (`429` plus the `route="summary"` metric)

  `reportsTotal` comes from `meta.reports_total`.
- [X] T080 [P] [US6] In `charts/gameplane/templates/telemetry-receiver.yaml`, render `PUBLIC_SUMMARY` from `publicSummary.enabled` (`hasKey` guard, default false).
- [X] T081 [P] [US6] Create `telemetry-receiver/summary_test.go`, covering exactly five keys (decode into a map and assert its length), 404 when disabled, the headers, ETag and 304, the limiter, and that `reportsTotal` is unchanged after a retention sweep.
- [X] T082 [US6] Add the subtest `public_summary_five_keys` to `test/e2e/telemetry_e2e_test.go`, through `Env.PortForward` on 8080.

---

## Phase 9: User Story 7 — No one can report under another install's ID (P3)

**Goal**: The receiver enforces signatures, the send-time window, first-use claims and replay rules before any write, and installs heal themselves after a claim conflict.

**Independent Test**: Every forgery case is refused with no figure changed (SC-014). An install whose ID was claimed first by someone else rotates its ID and is accepted in the same attempt (SC-015).

- [X] T083 [US7] In `telemetry-receiver/ingest.go`, add the checks in the order of the `contracts/report-schema.md` § Signature and claim table, **before** the limiter counts the report as accepted and before any write:
  - `telemetryschema.Verify`, which fails with 403 `bad_signature`
  - the window `[now−36h, now+1h]`, which fails with 403 `stale`
  - the claim: insert with `key_fp` and `last_sent_at`, or compare `key_fp`, which fails with 409 `id_claimed`
  - `sentAt > last_sent_at`, which fails with 403 `replay`

  Responses use fixed JSON bodies, and `gameplane_telemetry_refused_total{reason}` is incremented. Basic-only reports skip all of these checks.
- [X] T084 [US7] In `api/internal/telemetry/telemetry.go`, handle a 409 `id_claimed` inside the same attempt: replace the install ID (keeping `signing_secret`), set `last_id_rotation_at`, re-`Collect`, re-sign and re-POST once. A second 409 is recorded as `failed`. A 403 is recorded as `failed` with normal backoff, and never rotates.
- [X] T085 [US7] In `TelemetrySection` in `web/src/routes/AdminSettings.tsx`, render the status variant "Install ID replaced on <date>: the destination reported it was in use by another key" when `status.lastIdRotationAt` is set, using the T052 design.
- [X] T086 [P] [US7] Create `telemetry-receiver/ingest_sign_test.go`, covering the full forgery matrix: another key gives 409; an unsigned report, a one-byte tamper, a stale `sentAt`, a future `sentAt` and a byte-identical replay each give 403. In every case, assert that every table's row counts are unchanged (SC-014). Cover claim expiry together with re-claim (FR-038).
- [X] T087 [P] [US7] Add cases to `api/internal/telemetry/telemetry_test.go`: a 409 then 204 sequence rotates the ID and sends with a new key in the same attempt (SC-015); 409 twice gives `failed`; 403 doesn't rotate. Add a web test case for the rotation status line in `web/src/routes/AdminSettings_telemetry.test.tsx`.
- [X] T088 [US7] Create `test/e2e/telemetry_client_test.go`, a test-only helper that builds and signs reports with `telemetryschema` and a throwaway secret. Add these subtests to `TestTelemetryLifecycle`, before the US2 subtests:
  - `forged_other_key_gets_409`
  - `unsigned_tampered_replayed_get_403`, asserting that the views are unchanged
  - `claimed_id_rotates_and_recovers`: reset the ID, claim the new ID with the helper's key, wait one interval, then assert that `lastIdRotationAt` is set, `installId` has changed, and `lastOutcome = "ok"`

---

## Phase 9b: Report cost and credential strength (FR-039–FR-041, Q11, Q12, OD-5)

**Goal**: When a provider turns proof-of-work on, every report costs its sender work that follows the request rate, and the dashboard credential can't be short enough to guess.

**Independent Test**: Against a receiver with `INGEST_POW=true`, the install's reports are accepted after solving challenges, reports without a valid solution get 428 and change nothing, and the difficulty follows the curve in research R21 (SC-016). A receiver with a 31-character `DASHBOARD_TOKEN` refuses to start (quickstart S10).

- [X] T101 [P] [US7] Create `telemetryschema/pow.go` (stdlib only):
  - `PoWHeader = "Gameplane-Telemetry-PoW"` and `MaxPoWBits = 26` (OD-5)
  - `PoWOK(challenge string, nonce uint64, bits int) bool`: `SHA-256(challenge + ":" + decimal nonce)` starts with at least `bits` zero bits
  - `SolvePoW(ctx, challenge string, bits int) (uint64, error)`: single goroutine, checks `ctx` periodically, and errors when `bits` is negative or above `MaxPoWBits`
  - `FormatPoW(challenge string, nonce uint64) string` and `ParsePoW(header string) (string, uint64, error)`: split at the last `:`; the nonce is plain decimal; reject an empty or over-long challenge

  Add `telemetryschema/pow_test.go` (coverage gate 90) and the PoW row to `telemetryschema/specs.md`.
- [X] T102 [US7] In `telemetry-receiver`, create `pow.go` per research R21:
  - challenge issue and MAC
  - the adaptive difficulty (60-second issue window, `ceil(2·log2(r/T))`, peak decay of one bit per 5 minutes)
  - verification order, and the used-challenge set with expiry, a 1,000,000-entry cap and `pow_busy`
  - the `/v1/challenge` handler with its own per-source bucket (10 per minute, burst 5)
  - `gameplane_telemetry_pow_bits`, `gameplane_telemetry_pow_challenges_total`, and the `pow_required`/`pow_invalid` refusal reasons

  Parse `INGEST_POW*` in `main.go` with the OD-5 defaults, and exit at startup on out-of-range values. In `ingest.go`, check proof-of-work after `AUTH_TOKEN` and before the body is read, only when `INGEST_POW=true`.
- [X] T103 [US7] In `telemetry-receiver/main.go`, exit at startup when `DASHBOARD_TOKEN` is set but shorter than 32 characters (FR-041), with an error naming the minimum. If an existing test starts the receiver with a shorter token, stop and ask for sign-off rather than changing it. Raise the E2E dashboard token constant to at least 32 characters.
- [X] T104 [P] [US7] Create `telemetry-receiver/pow_test.go`, covering:
  - the curve: at or below `T` a challenge carries `MIN`; at 10×, 100× and 1,000× it carries 7, 14 and 20 bits, capped at `MAX`; it decays one bit per 5 minutes (SC-016)
  - a tampered MAC, a wrong difficulty, an expired challenge, a reused challenge and a missing header each give 428 with the right reason and change no table
  - `pow_busy` at a test-lowered cap
  - `/v1/challenge` returning 404 when off and 429 past the per-source bucket
  - PoW checked before the body is read and after `AUTH_TOKEN`

  Add config tests for the `INGEST_POW*` ranges and the token minimum.
- [X] T105 [US7] In `api/internal/telemetry/telemetry.go`, per research R21:
  - before each POST, request the challenge URL (the endpoint with its last path segment replaced by `v1/challenge`), solve with `telemetryschema.SolvePoW` under a deadline 30 seconds before `expiresAt`, and send `PoWHeader`
  - send without the header on any non-200
  - record `failed` (normal backoff) when `bits` exceeds `MaxPoWBits`
  - on 428, get one new challenge and resend once in the same attempt; a second 428 is `failed`
  - the 409 rotation resend (T084) fetches a fresh challenge
- [X] T106 [P] [US7] Create `api/internal/telemetry/telemetry_pow_test.go`, covering:
  - challenge URL derivation (with and without a path prefix)
  - a 404 provider gets reports with no header
  - a challenge provider gets a header that `telemetryschema.PoWOK` accepts
  - 428 then 204 resends once with a new challenge; 428 twice is `failed`
  - `bits = 27` is `failed` with no POST
  - the 409 rotation resend carries a fresh challenge
- [X] T107 [P] [US7] In the chart:
  - add `api.telemetry.receiver.ingestPow.{enabled,targetPerMinute,minBits,maxBits}` (defaults `false`, `60`, `0`, `22`) to `values.yaml`, rendered as `INGEST_POW*` in `templates/telemetry-receiver.yaml` through `hasKey` guards (F-214)
  - fail rendering when `targetPerMinute < 1`, or `maxBits < minBits` or `maxBits > 26`
  - add `helm template` assertions for the default, an enabled render and each failure to `.github/workflows/ci.yaml`
- [X] T108 [US7] Add the S10 subtests to `TestTelemetryLifecycle`, after `custom_destination_receives_only` and before `old_receiver_gets_basic`:
  - `pow_receiver_accepts_solved_reports`: deploy a custom receiver with `INGEST_POW=true`, `INGEST_POW_MIN_BITS=8` and the dashboard token, point the API at it, then assert that `gameplane_telemetry_pow_challenges_total` rises and `lastOutcome = "ok"`
  - `pow_refuses_missing_and_invalid`: a POST with no header gets 428 `pow_required`, a reused solution gets 428 `pow_invalid`, and the reports counter is unchanged

  Extend the deploy helper with the extra env rather than duplicating it.
- [X] T109 [P] [US7] Update the docs:
  - `docs/telemetry-provider.md`: turning on `INGEST_POW` for the project's provider, tuning `INGEST_POW_TARGET_PER_MIN` from `/metrics`, reading `gameplane_telemetry_pow_bits`, and generating `DASHBOARD_TOKEN` from 32 random bytes with its 32-character minimum
  - `docs/security.md` § Telemetry: proof-of-work, what it does and doesn't stop, and the credential minimum
  - `CHANGELOG.md` [Unreleased]: the new receiver settings, and the token minimum as an upgrade note

---

## Phase 10: Polish and cross-cutting concerns

- [X] T089 [P] Rewrite `telemetry-receiver/specs.md` and `telemetry-receiver/README.md` to match the shipped behaviour:
  - the two listeners, the configuration table and the dashboard
  - invariants: "stores daily aggregates and expiring activity records only, never raw reports or addresses", "basic or extended report, both strictly validated", and "extended reports must be signed"
  - the dependency list (`modernc.org/sqlite` and `telemetryschema`)
- [X] T090 [P] Update `api/specs.md`: the Telemetry responsibility line at line 21, the new flags in the line 81 list, the `/admin/telemetry*` routes in the admin route list near line 171, and the migration 015 tables. Update `web/specs.md` for the notice banner and the Telemetry section.
- [X] T091 [P] Rewrite `docs/install.md` § Telemetry (from line 415) per FR-008:
  - new installs share basic and extended data by default after the notice
  - the exact fields of each tier, including the install ID and signing
  - who is affected on upgrade, with a warning for installs whose basic toggle was already on
  - `api.telemetry.enabled=false`, a custom endpoint, the bundled receiver, the dashboard token and the public summary

  Update `docs/architecture.md:408-410`.
- [X] T092 [P] Add to the `docs/security.md` threat model: the public unauthenticated ingest and summary, the pseudonymous install ID, signing (what it does and doesn't stop: fabricated installs), dashboard authentication and the refusal invariant, source-address handling, and the pepper.
- [X] T093 [P] Create `docs/telemetry-provider.md`, a runbook for running the project's receiver (OD-2): deploying the image, TLS termination, keeping `:8081` private, the `DASHBOARD_TOKEN` and `ID_PEPPER` Secrets and their rotation, `PUBLIC_SUMMARY=true`, `TRUSTED_PROXY_CIDRS`, retention (`RETENTION_DAYS`, `ACTIVITY_EXPIRY_DAYS`), scraping `/metrics` with the token, and backing up and restoring `telemetry.db`. Link it from `docs/install.md` § Telemetry and `telemetry-receiver/README.md`. (The README link lands with T089.)
- [X] T094 [P] Add a `CHANGELOG.md` entry with the FR-008 upgrade notes, breaking-ish behaviour notes ("empty `api.telemetry.endpoint` now means the project default"; "the receiver's `/metrics` moved to the dashboard port and needs the dashboard token"), and links to the docs.
- [X] T095 Add a single statement-URL constant for the data-handling statement (OD-2), for example in the module `web/src` already uses for docs links. If there is none, create `web/src/lib/links.ts`. Use it in `TelemetryNotice.tsx` and `TelemetrySection`.
- [X] T096 Make the website changes in the `website/` submodule, following its own `CLAUDE.md` and PR flow (default branch `main`): (2026-10-08: GameplanePanel/website#13 merged; root pointer bumped to website main 299251d.)
  - a data-handling statement page (OD-2: maintainers operate it; 24-month aggregates, OD-3; 90-day activity expiry, OD-4; signing; no raw reports or IPs)
  - updates to `src/content/docs/platform-settings-telemetry.mdx`, `helm-values-reference.mdx` and `air-gapped-installation.mdx` (set `api.telemetry.enabled=false`)

  Then commit the submodule pointer bump in the root.
- [X] T097 Run the `security-review` skill over the branch diff and fix the confirmed findings through a small fix workflow. (2026-10-08: no confirmed findings; one candidate rejected at confidence 4/10.)
- [X] T098 Push the branch and open the PR **as a draft**, using the `ship-branch` skill. Add labels through REST (rule 14): `type: feature`, `area: api`, `area: web`, `area: chart`, `area: e2e`, `area: shared` and `area: specs`. The PR body states:
  - the OD-1 merge gate
  - the T002 sign-off list
  - that `telemetry-default-gate` is expected to fail until OD-1 is ruled
- [X] T099 Once OD-1 is ruled: set `DefaultEndpoint` in `api/internal/telemetry/destination.go`, mark OD-1 RULED in `specs/022-default-telemetry-dashboard/OPEN-DECISIONS.md`, update the destination copy in `design.pen` (with re-export) and the docs, confirm `telemetry-default-gate` passes, and mark the PR ready for review.
  The docs written under T091-T094 name no receiver host. Once OD-1 is ruled, add the host to:
  - `docs/install.md` § Telemetry: the sentence on an empty `api.telemetry.endpoint`, and the Upgrading admonition
  - `docs/architecture.md`: the "API → telemetry-receiver" bullet
  - `docs/telemetry-provider.md`: the intro paragraph and the "Public summary" paragraph
  - `CHANGELOG.md` [Unreleased] > Upgrade Notes: the first three bullets
  - the statement URL `https://valgulnecron.github.io/gameplane-website/telemetry/` (in `docs/install.md`, `docs/security.md`, `docs/telemetry-provider.md` and `CHANGELOG.md`) if the website gets a custom domain; it must match the T095 constant
- [X] T100 After merge: `git mv specs/022-default-telemetry-dashboard specs/done_022-default-telemetry-dashboard` and update in-repo references as a `docs:` commit (rule 16, constitution IV). Then delete the remote and local branch (rule 12). (2026-10-08: archived after GameplanePanel/Gameplane#617 merged; branches deleted.)

---

## Phase 11: Amendment 2026-10-14 — version trend (US5, FR-042)

**Goal**: The dashboard shows, per day, how many installs upgraded, downgraded, changed version without an order, or stayed behind the newest release (OD-7–OD-10, research R22). The basic version panel is renamed "Reports by version" (OD-9).

**Independent Test**: A synthetic population with known upgrades, downgrades, unordered changes (`pr617` → `v0.3.0`) and installs left on older releases gives exact daily counts for every series (SC-017).

**Branch and PR**: a new branch from `master` and a new PR; the merged 022 PR (#617) is not reused.

- [X] T111 [US5] In `telemetry-receiver/store.go`, add `daily_install_version (day, version, installs)` and `daily_version_change (day, kind, installs)` to the `CREATE TABLE IF NOT EXISTS` list (data-model § Receiver), and add both to the retention `DELETE FROM … WHERE day < ?` list near line 293.
- [X] T112 [P] [US5] Create `telemetry-receiver/versionorder.go`: `classifyChange(old, new string) (kind string, changed bool)` and `isSemver(label string) bool`, using `golang.org/x/mod/semver` (v0.41.0, as in `operator/go.mod`) with a `v` prefix added to labels that lack one (R22). Add the dependency to `telemetry-receiver/go.mod` with `make tidy`; confirm with `go build ./...`.
- [X] T113 [US5] In `telemetry-receiver/ingest_ext.go`, inside the transaction of a non-duplicate extended report (the insert near line 197 and the update near line 225): increment `daily_install_version[today, label]`; when the record existed and `classifyChange(last_version, label)` reports a change, increment `daily_version_change[today, kind]`. A new record is never a change (FR-042). Duplicates and refused reports write nothing.
- [X] T114 [US5] Design first, on the local machine where Pencil runs: in `telemetry-receiver/telemetry-dashboard.pen`, rename the basic "Versions" card to "Reports by version" and add a "Version trend" card under "Versions by install" in the extended section, with four daily series (upgraded, downgraded, changed (unordered), stale) and the newest release named in the caption. Load the `dataviz` skill for the chart form and colours. Ask the user to save in the Pencil UI, run `design-export` into `telemetry-receiver/design-export/`, and commit the export with the design (rules 1, 2).
- [X] T115 [US5] In `telemetry-receiver/views.go`, add `extended.versionTrend` (`contracts/receiver-http.md`): one entry per day in R with `upgraded`, `downgraded` and `unordered` from `daily_version_change`, and `newestRelease`. `null` with the rest of `extended` when the range has no extended reports.
- [X] T116 [US5] In `telemetry-receiver/views.go`, compute `stale` per day and `newestRelease` from `daily_install_version` over R (data-model § Derived views): N is the highest semver version in R, pre-releases included (OD-10); `stale[D]` sums installs on semver versions below N. Non-semver versions are never stale (OD-8).
- [X] T117 [US5] After T114: in `telemetry-receiver/charts.go:350` change the card title to "Reports by version" and update its caption in `web/overview.html:37`; add the trend chart to `charts_ext.go` and `web/overview_extended.html` under "Versions by install", following the T114 design.
- [X] T118 [P] [US5] Docs: describe the trend, the two tables and the "Reports by version" rename in `telemetry-receiver/specs.md` and `README.md`, and mention the two new daily counts wherever `docs/telemetry-provider.md` or the `website/` data-handling statement describe what the receiver stores (the statement change goes through the submodule's own PR flow). (2026-10-14: README and specs.md updated; `docs/telemetry-provider.md` doesn't list stored tables, and the website statement's "Counts per day by version … never linked to an install ID" already covers both new counts, so neither changed.)
- [X] T119 [US5] Tests (CI only, rule 8): (2026-10-15: new cases in `versiontrend_test.go`; `lifecycle_test.go` extended with the two tables after the user's sign-off; `charts_test.go:83` needed no change.)
  - new `telemetry-receiver/versionorder_test.go`: semver up, down, equal across `v` prefixes, pre-releases, and every non-semver label as unordered
  - new cases in `ingest_ext_test.go` and `population_test.go` for SC-017, including duplicates and refused reports leaving both tables unchanged, and `views_test.go` for `versionTrend` with and without extended data
  - **needs the user's sign-off before editing** (existing tests whose expectations change): `lifecycle_test.go:11` adds the two tables to `dailyTables` so the retention sweep covers them; `charts_test.go:83` if it asserts the old "Versions" title
- [X] T120 Push the branch and open the PR with the `ship-branch` skill; labels `type: feature`, `area: shared`, `area: specs` through REST (rule 14). After merge, `git mv specs/022-default-telemetry-dashboard specs/done_022-default-telemetry-dashboard`, update in-repo references, and delete the branch (rules 12, 16). The re-archive waits until Phase 12 is done too.

**Checkpoint**: the trend is visible and every series matches SC-017.

---

## Phase 12: Amendment 2026-10-14 — standalone and multicluster reporting (FR-043–FR-045)

**Goal**: A central or standalone panel reports once for every cluster it manages, skips clusters it can't reach, and says whether it is a cluster install or a standalone panel (OD-11–OD-13, research R23).

**Independent Test**: SC-018.

- [X] T121 [P] In `telemetryschema/`: raise `SchemaVersion` to `2`, add `Env.Deployment` and the enumeration `Deployments = {"cluster", "standalone"}` in `enums.go`, and make `decodeEnv` require `deployment` on schema 2 and refuse it on schema 1 (unknown key). Schema 1 reports decode with `Deployment` set to `other` (OD-14). In schema 2, `env.k8s` and `env.distro` become non-empty sorted sets like `env.arch` (OD-15), and a schema 1 string decodes as a one-value set; `env.nodes` stays one band (OD-16). Update `telemetryschema/specs.md`.
- [X] T122 [US5] Design first, on the local machine where Pencil runs: in `telemetry-receiver/telemetry-dashboard.pen`, add a "Deployment" share card to the environment breakdowns of frame `i0xK1E`, matching the other environment cards. Export it into `telemetry-receiver/design-export/` and commit the export with the design (rules 1, 2).
- [X] T123 [US5] Receiver, after T121: add `dimDeployment` to the `daily_dim` writes in `ingest_ext.go` and write one row per `k8s` and `distro` value as for `arch`, `Env.Deployment` to `views.go` (`extended.env.deployment`), and after T122 the card in `charts_ext.go` and `web/overview_extended.html`. Update `telemetry-receiver/specs.md` and `README.md`.
- [X] T124 API, after T121: give `telemetry.Deps` the cluster registry (`api/cmd/main.go:352`), and in `api/internal/telemetry/collect.go` list servers, templates and backup schedules on every cluster in `Registry.IDs()` with a per-cluster timeout, sum them, skip clusters whose list fails (FR-043, FR-044), and set `Env.Deployment` from `IsStandalone()` (FR-045). Read the server version and nodes of every reachable cluster: `k8s` and `distro` as sets, `arch` as the union (OD-15), `nodes` as one band over their total node count (OD-16). Bump `extSchema` to `2`.
- [X] T125 [P] Add `env.deployment` to `noticeExtendedFields` in `api/internal/handlers/telemetry.go`, to the `env` type in `web/src/lib/api.ts`, and to the extended field list in `docs/` and the website data-handling statement (submodule PR flow, then a pointer bump).
- [X] T126 Tests (CI only, rule 8): new tests for schema 1 and 2 decoding, the deployment dimension, cross-cluster counting with one unreachable cluster, and the standalone marker (SC-018). Existing tests whose expectations change (for example a schema version or field list they assert) need the user's sign-off before editing, with the exact before and after.
- [X] T127 Ship through the open PR or a new one, as the user decides when the phase starts; labels `type: feature`, `area: api`, `area: shared`, `area: specs` (plus `area: web` for T125) through REST (rule 14).

---

## Dependencies and execution order

### Phase dependencies

- **Phase 1** has no prerequisites. T002 (sign-off) must be done before T039, T060 and T087 touch existing tests.
- **Phase 2** depends on Phase 1 and blocks every story.
  - Within it, the contract tasks T010–T016 come before T017, T026 and T030.
  - T018 → T019 → T020 → T021 and T022.
  - T023 → T024 and T025.
- **US1 (Phase 3)** depends on Phase 2.
- **US2 (Phase 4)** depends on Phase 2. T051 and T053 build on the US1 handler file and section. Its E2E subtests (T055) must stay last in `TestTelemetryLifecycle`.
- **US3 (Phase 5)** depends on US2's T051–T053 (the same handler and section).
- **US4 (Phase 6)** depends only on Phase 2, so it can run in parallel with US1–US3.
- **US5 (Phase 7)** depends on US4: the ingest, store and views files.
- **US6 (Phase 8)** depends on US4: aggregates and `meta.reports_total`.
- **US7 (Phase 9)**:
  - T083 and T086 depend on US5 (ext ingest)
  - T084 and T087 depend on US1 (the reporter)
  - T085 depends on US3
  - T088 depends on T083 and T084
- **Phase 9b**:
  - T101 first
  - T102–T104 depend on T101 and the US7 receiver checks (T083)
  - T105 and T106 depend on T101 and T084
  - T107 is independent
  - T108 depends on T102, T105 and T107
  - T109 depends on T102 and T103
- **Polish (Phase 10)** depends on every story. T099 also waits for OD-1, and T100 waits for the merge.
- **Amendment (Phase 11)** depends only on the shipped feature. T111–T113 and T115 can start at once; T114 runs on the local machine; T117 waits for T114; T119 waits for T111–T117 and the sign-off it names.
- **Amendment (Phase 12)**: T121 first; T122 runs on the local machine; T123 waits for T121 and T122; T124 and T125 wait for T121; T126 waits for T121–T125.

### Story order (graph)

```text
Setup → Foundational ─┬─ US1 → US2 → US3 ─────────────┐
                      └─ US4 → US5 ─┬─ US6            ├─ US7 → 9b → Polish → (OD-1) → T099 → merge → T100
                                    └─────────────────┘
```

### Within each story

1. Design (`design.pen` or `telemetry-receiver/telemetry-dashboard.pen`, then export).
2. Data and state.
3. Server logic.
4. HTTP and UI.
5. Unit and envtest tests.
6. E2E subtests.

Commit after each logical unit.

## Parallel opportunities

- **Phase 1**: T005, T006, T007 and T008 together, after T004.
- **Phase 2**:
  - T010, T011, T013 and T014 in parallel, then T012, then T015 and T016.
  - The API state chain (T018–T022) runs alongside the contract work.
  - T027, T028 and T029 in parallel, after T023.
  - The receiver store (T030–T033) runs alongside the API tasks.
- **Across stories**: once Phase 2 is done, the **install-side track** (US1 → US2 → US3) and the **provider track** (US4 → US5 → US6) run as two parallel workflows.

Parallel example for US4:

```text
Wave A (parallel): T062 ratelimit.go | T065 views.go | T066 telemetry-dashboard.pen frames
Wave B:            T063 ingest.go (needs T062) | T067 dashboard.go
Wave C:            T068 templates (needs T066, T067) | T069 chart
Wave D (parallel): T070 unit tests | T071 E2E subtests
```

Parallel example for US1:

```text
Wave A (parallel): T034 reporter | T035 consent.go | T041 notice design
Wave B (parallel): T037 config hook | T038 notice handlers | T042 web client
Wave C:            T043 TelemetryNotice (needs T041, T042) | T036 wiring
Wave D (parallel): T039 | T040 | T044 | T045 | T046, then T047 and T048
```

## Implementation strategy

- **MVP (US1 + US2, both P1).** Default-on with the notice gate, the operator disable and redirect, and E2E against the bundled receiver. This alone fixes today's "telemetry never works" problem, including the restart bug.
- **Then:**
  - US3, for transparency
  - US4, the provider's history and private dashboard, which can be built alongside US1–US3
  - US5, US6 and US7 together, because signing (US7) must ship with extended acceptance (US5): an unsigned extended tier must not reach `master`
- **Single PR.** Everything lands through one draft PR that stays unmergeable until OD-1 is ruled (plan.md, Merge gate). Run CI after each phase to catch regressions early.
