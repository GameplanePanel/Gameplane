# Implementation Plan: Default telemetry destination, extended telemetry, and telemetry dashboard

**Branch**: `feat/default-telemetry-dashboard` (not yet created; the spec-kit script derived `022-default-telemetry-dashboard`) | **Date**: 2026-10-06 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/022-default-telemetry-dashboard/spec.md`

## Summary

Telemetry gets a compiled-in default destination and two consent tiers, and the receiver becomes a small provider with durable aggregates, a private dashboard and a public summary.

**Destination.** The API gains one authoritative default destination, `telemetry.DefaultEndpoint`. It is set to https://telemetry.gameplane.net/ingest (OD-1, ruled 2026-10-07). An operator can still choose a custom destination, the bundled receiver, or a hard **disable** (`api.telemetry.enabled=false`).

**Consent.** Consent splits into a **basic** tier and an **extended** tier:
- **Fresh installs** (detected inside `Store.Migrate`) seed both tiers on, but nothing is sent until an admin's dashboard has displayed the first-login notice.
- **Upgraded installs** keep their saved basic choice, and extended stays off.

**Reporter.** The reporter switches to a persisted, claim-based schedule that survives restarts and sends at most one report per 24 h. It also falls back to basic-only for receivers older than this feature.

**Extended report.** It adds:
- a random install ID
- environment categories
- per-official-module server counts, with everything else summed as `custom`
- feature-adoption flags

All of these come from a new shared stdlib-only module, `telemetryschema/`, so the API and the receiver validate the same fixed categories.

**Signed reports (US7).** Every extended report is signed with Ed25519. The key is derived (HKDF) from a random secret kept in the install's database, combined with the current install ID, so resets produce unlinkable keys. The receiver binds each ID to the first key that uses it, and refuses, without counting anything, reports that are unsigned, tampered, stale, replayed or signed by another key. When an install's ID is claimed by someone else, it replaces the ID automatically and resends (research R20).

**Receiver.** `telemetry-receiver` stores only **daily aggregates** plus **expiring activity records** (an HMAC of the install ID, first and last seen, last version) in SQLite on a PVC. Per-ID, per-day deduplication and in-memory per-source limits keep the counts honest. It serves:
- a **private, token-protected, server-rendered dashboard** on its own port
- an **opt-in public summary** of five counts

The Admin Settings Telemetry section and the new notice banner are designed in `design.pen` first. The receiver's pages are designed in their own Pencil file, `telemetry-receiver/telemetry-dashboard.pen`, which starts from a copy of the HeroUI design. Prometheus `/metrics` moves behind the dashboard token, and a runbook documents how to run the project's receiver.

## Technical Context

**Language/Version**: Go 1.26 for `api`, `telemetry-receiver` and the new `telemetryschema`. TypeScript (strict) and React 19 for `web`. Helm 3 templates.

**Primary Dependencies**: existing ones only, apart from the receiver gaining `modernc.org/sqlite`, which the API already uses.
- API: chi, client-go (dynamic and typed), `modernc.org/sqlite` / `pgx`.
- Receiver: `prometheus/client_golang`, plus `modernc.org/sqlite` (new to the receiver), stdlib `html/template` and `embed`.
- Web: HeroUI v3, TanStack Query. No new web dependency.

**Storage**:
- API: migration `015_telemetry_state.sql` in `common/` (portable to SQLite and PostgreSQL), plus the existing `config` key `telemetry`.
- Receiver: SQLite file at `$DATA_DIR/telemetry.db` on a RWO PVC, or in-memory when `DATA_DIR` is unset.

**Testing** (CI only, per constitution VI). Signing adds tests for derivation vectors, verification failure cases, and claim, replay and window rules:
- `go test` with coverage gates: `telemetryschema` 90 (new), `telemetry-receiver` 70, `api` 80.
- API envtest handler suites.
- vitest for the web.
- `helm template` assertions in `ci.yaml`.
- Kind E2E: new `telemetry` bucket, plus an assertion in the `upgrade` bucket.
- New lint script `hack/check-telemetry-catalog.sh`.

**Target Platform**: Kubernetes (k3s, kind, managed), with linux/amd64 and arm64 distroless images. The default provider is the same receiver image, run by the project maintainers (OD-2).

**Project Type**: Multi-component: Go services, a React dashboard, a Helm chart, and a new shared Go library module.

**Performance Goals**:
- Ingest: 10,000 reports per day (about 0.1 req/s average, bursty) at the default provider.
- Dashboard: the 365-day view within 3 s (SC-010), backed by pre-aggregated daily rows.
- Public summary: served from a 5-minute snapshot.
- Install side: one collection pass per report or preview, with a bounded list of local CRs and nodes. Signing costs one HKDF and one Ed25519 signature per send.

**Constraints**:
- No raw reports, source IPs or untransformed IDs are ever persisted (FR-014, SC-012).
- Every stored category is bounded by an enumeration.
- At most one accepted report per 24 h (FR-007).
- Every extended report is signed, and the signing secret never leaves the install (FR-035).
- `https` with full verification for the default destination (FR-009).
- The receiver stays a single static binary with a read-only root filesystem.
- The dashboard has no JavaScript and a strict CSP.
- `--reuse-values` from beta.8 must render unchanged.

**Scale/Scope**:
- About 10k reporting installs.
- 12–24 months of daily aggregates.
- About 10k activity records.
- 5 new API routes or route variants.
- 1 rewritten settings section, plus 1 new banner.
- About 5 new frames in `design.pen`, and about 4 in `telemetry-receiver/telemetry-dashboard.pen`.
- 1 new Go module.
- 1 new protocol element: signed extended reports with a claim on first use (R20).

No NEEDS CLARIFICATION remains. Every technical unknown is resolved in [research.md](research.md) (R1–R20). OD-1 (the domain) is ruled (2026-10-07): telemetry.gameplane.net. OD-2, OD-3 and OD-4 are ruled (2026-10-06).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Gate | Pre-design | Post-design |
|---|---|---|---|
| **I. E2E-tested delivery** | Every user-facing path has a Kind E2E in a registered bucket. | Planned. | **PASS**: new `telemetry` bucket with `TestTelemetryLifecycle` (`t.Parallel()`, unique names, sequential subtests because they mutate cluster-wide Helm state); `upgrade` bucket assertion (R17). Registered in `test/e2e/buckets.sh` and the CI matrix. |
| **II. Design-first UI** | Dashboard UI is designed in `design.pen` via the Pencil MCP before React or HTML is written, and touched nodes are exported to `design-export/`. | Planned. | **PASS**: frames for the notice banner, the five Telemetry settings states, and the receiver login, overview, empty and refusal pages (R18). `.pen` files are touched only through Pencil MCP. The export lands in the same commit. The receiver pages use a third Pencil source, `telemetry-receiver/telemetry-dashboard.pen` (spec Q10), exported to `telemetry-receiver/design-export/`. Principle II names only `design.pen` and `website.pen`, so this file follows the same rules by analogy: Pencil MCP only, design before code, export in the same commit. |
| **III. Best practice / no suppression** | No `//nolint` or `eslint-disable`; `%w` wrapping; strict TS; no CRD edits without codegen. | — | **PASS**: no CRD type changes, so CLAUDE.md rule 7 doesn't apply. The new module gets the standard lint config, and nothing is excluded. |
| **IV. Spec-driven; per-module `specs.md`** | Behaviour changes update `specs.md` in the same change. | — | **PASS** (planned): `telemetryschema/specs.md` (new), `telemetry-receiver/specs.md` and README (invariants rewritten: "daily aggregates and expiring activity records, never raw reports", "basic or extended report"), `api/specs.md`, `web/specs.md`. |
| **V. Delegate via Workflow** | Implementation goes through Workflow scripts, haiku first, review one tier up, `model:` set on every `agent()`. | — | **PASS** (process): the tasks phase produces scout briefs; implementation waves are split by component (see Delivery slices). |
| **VI. CI does the heavy lifting** | Locally only `go build` and `tsc --noEmit`. | — | **PASS**: quickstart lists only compile checks locally; everything else is CI. |
| CLAUDE.md core rule 3: login privacy | Unauthenticated views reveal nothing. | — | **PASS**: the receiver's refusal invariant (contracts/receiver-http.md). The Gameplane `/login` is untouched, and the notice appears only after authentication. |
| CLAUDE.md core rule 9: K8s primitives first | — | — | **PASS**: PVC for receiver data; Secrets for the dashboard token and pepper; NetworkPolicy for the dashboard port. |
| CLAUDE.md core rule 10: operator authority | — | — | **PASS**: the reporter only reads CRs; nothing bypasses reconciliation. This is the reporter's existing placement in the API. |
| CLAUDE.md system override 10: unsettled values | — | — | **PASS**: the domain is ruled (OD-1). Retention (730 days) and activity expiry (90 days) are ruled values (OD-3, OD-4). |

**Result**: no violations. The one structural addition, a 16th Go module, is recorded under Complexity Tracking as a deliberate cost rather than a violation.

## Project Structure

### Documentation (this feature)

```text
specs/022-default-telemetry-dashboard/
├── spec.md
├── OPEN-DECISIONS.md        # OD-1..OD-5 ruled
├── plan.md                  # this file
├── research.md              # R1–R20
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── report-schema.md     # wire format, enums, bands, compatibility
│   ├── receiver-http.md     # ingest, summary, dashboard routes, auth, env
│   ├── api-telemetry-http.md# admin routes, config PUT semantics, notice
│   └── install-config.md    # Helm values, API flags, rendering table
├── checklists/requirements.md
└── tasks.md                 # /speckit-tasks (not created here)
```

### Source Code (repository root)

```text
telemetryschema/                         NEW stdlib-only module (R1)
├── go.mod                               module github.com/GameplanePanel/gameplane/telemetryschema
├── report.go                            Report / ExtendedPart / Env / Games / Features, JSON encode
├── decode.go                            strict decoder (moved from telemetry-receiver/main.go decodePayload, extended)
├── enums.go                             distro, arch, tunnel, db, language sets; NodeBand / ClusterBand / FleetBands
├── catalog.go, catalog.txt              embedded official module list (R15)
├── sign.go                              DeriveKey (HKDF → Ed25519), Sign, Verify, signature header codec (R20)
├── *_test.go, .testcoverage.yml (90)
└── specs.md

telemetry-receiver/
├── main.go                              config + two listeners (slimmed)
├── ingest.go                            /ingest: auth, size cap, decode, signature/window/claim/replay checks (R20), limiter, dedupe, aggregate write
├── store.go                             SQLite schema, migrations, writes, rollover/expiry/retention jobs
├── views.go                             view model for the dashboard + public summary
├── dashboard.go                         token login, signed cookie, CSP, handlers
├── summary.go                           /v1/summary snapshot cache, CORS, ETag
├── ratelimit.go                         per-source daily counter + token buckets, trusted proxies
├── web/                                 go:embed templates + CSS + inline-SVG chart partials (no JS)
├── go.mod                               + modernc.org/sqlite, + replace ../telemetryschema
├── Dockerfile                           + COPY telemetryschema/
├── README.md, specs.md                  rewritten invariants/config
└── *_test.go                            incl. synthetic-population accuracy (SC-008), 365-day view timing (SC-010)

api/
├── cmd/main.go                          new flags (install-config.md), reporter wiring
├── Dockerfile                           + COPY telemetryschema/
├── go.mod                               + require/replace telemetryschema
├── internal/telemetry/
│   ├── telemetry.go                     Reporter: destination resolution, gate, schedule claim, sign + send, 400 fallback, 409 ID rotation
│   ├── collect.go                       builds the Report (also used by the preview)
│   ├── distro.go                        R16 heuristics
│   └── *_test.go
├── internal/db/
│   ├── migrations/common/015_telemetry_state.sql
│   ├── telemetry.go                     state/ack queries, claim UPDATE
│   └── db.go                            Migrate: fresh detection + one-time seed (R10)
├── internal/handlers/
│   ├── telemetry.go                     GET /admin/telemetry, POST install-id, notice GET/POST
│   ├── config.go                        telemetry validator (extended ⇒ sendMetrics) + post-save hook + 409
│   └── telemetry_envtest_test.go
├── internal/rbac/rbac.go                /admin/telemetry entries before the admin wildcard
└── specs.md

web/src/
├── lib/api.ts, lib/config.ts            Telemetry client; TelemetryCfg.extended
├── components/ui/TelemetryNotice.tsx    banner (+ .test.tsx)
├── components/AppLayout.tsx             mounts the notice for config:manage holders
└── routes/AdminSettings.tsx             TelemetrySection rewrite (+ tests)

charts/gameplane/
├── values.yaml                          install-config.md keys
├── templates/api.yaml                   --telemetry-disabled, BUNDLED, interval, official source
├── templates/telemetry-receiver.yaml    PVC, Recreate, env, dashboard port, NetworkPolicy
└── testdata/                            reuse-values fixture assertions

test/e2e/telemetry_e2e_test.go           TestTelemetryLifecycle (R17)
test/e2e/buckets.sh                      + telemetry bucket; upgrade bucket assertion
deploy/kind/{e2e.sh,up.sh}               explicit bundled receiver
hack/check-telemetry-catalog.sh          catalog drift check (wired into make lint)

design.pen → design-export/{json,screenshots}/   notice, settings states
telemetry-receiver/telemetry-dashboard.pen → telemetry-receiver/design-export/{json,screenshots}/   receiver login, overview, empty
docs/{install.md,architecture.md,security.md,telemetry-provider.md}, CHANGELOG.md, docs/agent-architecture.md, CLAUDE.md (repo map + coverage list)
go.work, Makefile (GO_MODULES), .github/workflows/ci.yaml (matrices, path filters, e2e bucket), .github/dependabot.yml
website/ (submodule): platform-settings-telemetry.mdx, helm-values-reference.mdx, air-gapped-installation.mdx
```

**Structure Decision**: the work spreads across existing components, plus one new root-level library module, `telemetryschema/`, which follows the netguard and gameaction pattern. No other new top-level directories are added.

## Delivery slices (input for `/speckit-tasks`)

The slices are implementation waves on **one feature branch and one PR**, in this order. The PR stays a draft until OD-1 is ruled (Merge gate), so none of this reaches `master` before the domain exists.

1. **Contract module.** Add `telemetryschema`, move the receiver's decoder into it with no behaviour change, and add `sign.go` (key derivation, signing and verification, R20). Wire the new module through CI, the Makefile, `go.work`, Dependabot and the docs maps. Add the catalog and its drift check.
2. **Receiver provider** (US4, US5, US6, and US7 on the provider side; the signature, window, claim and replay checks come before any write):
   - SQLite store, extended decode, dedupe, limits, and rollover/retention
   - the dashboard (after its frames in `telemetry-receiver/telemetry-dashboard.pen`), `/metrics` moved behind the token, and the public summary
   - chart persistence, dashboard and NetworkPolicy changes
   - receiver `specs.md` and README
3. **API reporter and consent** (US1, US2, US3, and US7 on the install side: `signing_secret`, signing, rotation on 409):
   - migration 015, the seed inside Migrate, and destination resolution
   - the scheduler, claim and fallback, and collection
   - the admin routes, RBAC and config hook
   - chart flags; explicit receiver settings in the e2e and dev scripts
4. **Web** (US1, US3 UI): the `design.pen` frames and export first, then the notice banner and the settings section.
5. **E2E and docs:** the `telemetry` bucket, the upgrade assertion, FR-008 docs and CHANGELOG, `docs/security.md` threat-model additions, and website pages (through the submodule's own PR flow).

Slices 2 and 3 can run in parallel once slice 1 is committed. Slice 4 needs slice 3's routes. Slice 5 needs everything before it.

### Amendment slice (2026-10-14): version trend (FR-042)

Spec 022 is reopened for one more slice on a new branch and PR; the shipped feature is unchanged until it lands.

6. **Version trend** (US5 scenarios 7–9, FR-042, OD-7–OD-10, research R22):
   - receiver storage: `daily_install_version` and `daily_version_change` (created with `CREATE TABLE IF NOT EXISTS`, so existing providers gain them on restart; no schema-version bump), plus their retention sweep
   - version ordering through `golang.org/x/mod/semver`, a new direct dependency of `telemetry-receiver`
   - classification in the extended ingest transaction, and the `extended.versionTrend` view block
   - the design in `telemetry-receiver/telemetry-dashboard.pen` first (rename "Versions" to "Reports by version", add the trend chart under "Versions by install"), done on the local machine where Pencil runs, then the templates

### Amendment slice (2026-10-14): standalone and multicluster reporting (FR-043–FR-045)

7. **Reporting through the central panel** (OD-11–OD-13, research R23; OD-11 to OD-16 all ruled):
   - `telemetryschema`: extended schema `2` with `env.deployment` (`cluster`, `standalone`); `Decode` accepts schemas `1` and `2`
   - receiver: store `deployment` as a `daily_dim` dimension and show it as an environment breakdown, designed first in `telemetry-receiver/telemetry-dashboard.pen` on the local machine
   - API: `telemetry.Deps` gains the cluster registry; `Collect` sums servers, templates and per-module game counts, and combines the cluster-derived features (any-cluster for wake-on-connect and backups, union for tunnels), over every reachable managed cluster, skips unreachable ones, and sets the marker
   - API notice field list and the web `TelemetryInfo` type gain `env.deployment`; the admin preview prints the report as JSON, so it needs no design change
   - docs: the extended field list wherever it is published (`docs/`, the website data-handling statement through the submodule's PR flow)

## Cross-spec dependencies

- **019 localization-language-packs** (Draft) owns adding languages to the `telemetryschema` `language` enumeration and wiring the configured default. Until then `language` is always `en` (R19).

## Merge gate (OD-1, ruled 2026-10-07)

OD-1 is ruled: the domain is telemetry.gameplane.net, and `telemetry.DefaultEndpoint = "https://telemetry.gameplane.net/ingest"`. The merge gate is satisfied. The CI job `telemetry-default-gate` keeps enforcing that the default stays an https URL (FR-002, FR-009).

## Release checklist

- **OD-2 (ruled).** The project maintainers run the provider. The data-handling statement page on the project website is published, states 24-month retention (OD-3) and 90-day activity expiry (OD-4), and is linked from the notice, the settings and the upgrade notes through one URL constant.
- **FR-008.** The upgrade notes include the warning for installs whose basic toggle was already on.

## Complexity Tracking

There are no constitution violations. One deliberate structural cost is recorded so reviewers can weigh it:

| Addition | Why needed | Simpler alternative rejected because |
|---|---|---|
| 16th Go module `telemetryschema/` (CI matrices, coverage gate 90, Dependabot, `go.work`, docs maps, `specs.md`) | FR-013 requires one fixed, published category set that both sides apply identically, and SC-006 requires the preview to match the payload. | Duplicating the enumerations relies on cross-module fixture tests that drift silently. Importing a receiver sub-package from the API inverts the dependency and drags SQLite and Prometheus into the API's module graph (research R1). |
| Receiver persistence (SQLite plus PVC) and a web UI in a component that was previously stateless | FR-020 (durable history) and FR-022 (a dashboard served by the receiver, clarification Q2). | Prometheus can't hold per-install deduplication state or survive restarts without extra infrastructure. Reusing the React app couples the receiver's release to the dashboard (research R3, R7). |
