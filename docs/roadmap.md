# Roadmap to v1

## Current release

- **Version:** `v0.3.0` (pre-v1), released 2026-10-05. See the [changelog](../CHANGELOG.md).
- **Status as of 2026-10-14:** The CRDs, operator, API, agent, and dashboard are
  feature-complete for the v1 scope and stabilized for external testing, but the
  project is not yet recommended for unattended production. This page tracks what
  stands between v0.3 and a v1 GA release.
- **What works today:** see [`README.md`](../README.md) and [`architecture.md`](architecture.md).

It is a living document: items move out of it as they ship, and anything
discovered along the way gets added.

---

## Shipped in v0.3.0 and since

### Since v0.3.0 (Unreleased)

- Independent central panel: Docker Compose runs the API and dashboard without a host Kubernetes cluster; a standalone Helm profile runs without the central operator or workloads ([changelog](../CHANGELOG.md)).
- Credential maintenance: standalone key custody, offline `rotate-panel-key`, `PUT /clusters/{name}/kubeconfig`, and `PUT`/`DELETE /clusters/{name}/gateway` ([changelog](../CHANGELOG.md)).
- Extended telemetry tier: a second, separately consented tier with a random install ID and fixed-set categories; signed reports ([changelog](../CHANGELOG.md)).
- Telemetry receiver dashboard and an optional public `GET /v1/summary` ([changelog](../CHANGELOG.md)).
- Proof-of-work on telemetry ingestion, off by default ([changelog](../CHANGELOG.md)).
- Telemetry provider runbook at `docs/telemetry-provider.md` ([changelog](../CHANGELOG.md)).
- Optional Steam display names on player lists, via a Steam Web API key ([changelog](../CHANGELOG.md)).
- Nuclear Option player lists are structured (`steamId`, `faction`) ([changelog](../CHANGELOG.md)).
- New optional `GameTemplate` field `spec.rcon.authentication` (default `true`) allows consoles with no RCON Secret ([changelog](../CHANGELOG.md)).
- Standalone credential protection: keys kept outside database volumes, transactional rotation ([changelog](../CHANGELOG.md)).
- Remote workload boundaries: verified TLS, dial-time unsafe-destination rejection, bounded responses; `GAMEPLANE_REMOTE_ALLOWED_CIDRS` / `api.remoteAllowedCIDRs` ([changelog](../CHANGELOG.md)).
- Removed: the Hell Let Loose module is withdrawn from the official catalog ([changelog](../CHANGELOG.md)).
- Upgrade-note behaviour changes: telemetry is on by default for new installs after a first-login notice; installs with a saved basic toggle start sending to the default receiver after upgrade; an empty `api.telemetry.endpoint` means the project default receiver; the receiver `/metrics` moved to `:8081` and needs the dashboard token; the bundled receiver stores data on a PVC by default; receiver `DASHBOARD_TOKEN` must be at least 32 characters ([changelog](../CHANGELOG.md)).
- Breaking: selected-cluster module and template grants need a cluster-wide grant for remote catalogs ([changelog](../CHANGELOG.md)).
- Feature catalog: [FEATURES.md](../FEATURES.md) lists every capability with its maturity and default, mirrored on the website and kept in step by `make check-features-sync` (spec 024, PR #703).

### Shipped in v0.3.0

- Configurable backup/restore Job retry limit and deadline Helm values ([changelog](../CHANGELOG.md)).
- Multicluster gateways and unified server management across clusters ([changelog](../CHANGELOG.md)).
- User theme customization: presets, custom colors with a WCAG AA check, custom CSS overlay, safe mode ([changelog](../CHANGELOG.md)).
- Network capture sidecar: admin-only, on-demand PCAPNG capture from running game pods ([changelog](../CHANGELOG.md)).
- Module authoring toolkit `gp-module` (scaffold, validate, preview, package) and the dashboard Modules builder ([changelog](../CHANGELOG.md)).
- 13 new top Steam game modules ([changelog](../CHANGELOG.md)).
- Nuclear Option dedicated server support ([changelog](../CHANGELOG.md)).
- Install-time config: Helm-seeded OIDC role mappings and a default StorageClass for game-data PVCs ([changelog](../CHANGELOG.md)).
- Share link expiry options (presets, custom date, no expiry) replacing the 90-day cap ([changelog](../CHANGELOG.md)).
- Console WebSocket robustness on non-default ports, and input queueing ([changelog](../CHANGELOG.md)).
- Existing PVC for SQLite via `api.storage.existingClaim` ([changelog](../CHANGELOG.md)).
- REST and CLI console protocols for games with HTTP admin APIs or PTY-only consoles ([changelog](../CHANGELOG.md)).
- Mod archive path confinement (zip-slip and symlink guards) ([changelog](../CHANGELOG.md)).
- Design and visual polish: HeroUI foundation and brand re-theme ([changelog](../CHANGELOG.md)).
- Experimental PostgreSQL backend works end to end ([changelog](../CHANGELOG.md)).
- Owner-only server operations (transfer, collaborators, wipe, delete) need the owner or an admin ([changelog](../CHANGELOG.md)).
- Data wipe failures fail the Job and set `DataWipe=False`; backup/restore quiesce lifecycle fixed, and restore removes files created after the snapshot ([changelog](../CHANGELOG.md)).
- SQLite DSN gets `busy_timeout(5000)` by default; agent metrics on a separate unauthenticated `:9090` listener that the PodMonitor scrapes ([changelog](../CHANGELOG.md)).
- Multi-cluster request scoping, OIDC role re-evaluation, bootstrap-admin reset, cluster registration removal, mod bundle integrity, and tunnel credential handling hardened ([changelog](../CHANGELOG.md)).
- Upgrade note: every game pod gains an empty capture `emptyDir`, so existing GameServers roll once on upgrade ([changelog](../CHANGELOG.md)).

### Earlier releases (before v0.3.0)

#### Multi-cluster: dashboard cluster selector (PR #107) (shipped v0.2.0-beta.6) <!-- doc-versions: historical -->

The initial dashboard selector routed requests to one cluster at a time. The
[unified dashboard](unified-dashboard.md) now combines authorized resources with
optional location filters; selection remains in node inventory administration.
Server operations retain their own cluster and namespace. Pod logs and PTY
WebSockets use that cluster's Kubernetes API.

The [optional private agent gateway](multicluster-agent-gateway.md) supports RCON,
game-file logs, file/player operations, module actions and agent-based mods.
Remote capture downloads and cleanup bind files to GameServer and NetworkCapture
UIDs. Registry browsing, modpacks and ID-list configuration use the selected
Kubernetes client and template. Upgrade the gateway, operator, agents and capture
sidecars together; direct Kubernetes connectivity remains required.

Dedicated gateway health in the dashboard remains follow-up work. The existing
cluster-health indicator reports Kubernetes connectivity only.

#### Multi-cluster: dual-cluster e2e coverage (PR #104) (shipped v0.2.0-beta.6) <!-- doc-versions: historical -->

A real `kind ×2` e2e test (multicluster bucket) asserting `?cluster=` dispatch
lands a GameServer on cluster B and that a viewer scoped to cluster A cannot see it.

#### Audit log integrity (tamper evidence) (PR #103, dashboard banner PR #108) (shipped v0.2.0-beta.6) <!-- doc-versions: historical -->

Complete end-to-end. Backend: migration `005_audit_chain.sql`,
`Auditor.insertChained`/`Auditor.Verify`, and `GET /admin/audit/verify`. The
dashboard integrity banner — surfacing a verification break to the admin on the
audit page — shipped in PR #108.

**Design (as implemented):**

- Migration `005_audit_chain.sql` added `prev_hash` + `hash` columns.
- On insert (inside a transaction, serialized per-process by a mutex — see the
  single-writer caveat on `insertChained`) compute
  `hash = SHA-256(prev_hash || canonical(row))`, and upsert `audit.head`
  (the newest row's id + hash) in the same transaction.
- `Verify()` walks the chain and reports the first break, exposed as an
  admin-only `GET /admin/audit/verify`. It also checks `audit.head` against
  the newest surviving row, so truncating the tail (`DELETE ... WHERE id > N`,
  which leaves every surviving link internally consistent) is detected too —
  previously a known blind spot.
- `Prune` writes a checkpoint (the newest about-to-be-deleted row's id + hash,
  in the `config` table under `audit.checkpoint`) before deleting, so
  verification resumes from the checkpoint rather than the genesis row.

**What this does and does not guarantee:** the chain is unkeyed, and the
checkpoint/head it verifies against live in the same DB-writable `config`
table. It reliably catches naive in-DB tampering (`UPDATE`/`DELETE`, including
tail truncation) and accidental corruption — it does not, and cannot, defeat a
sophisticated attacker with DB write access who also recomputes and rewrites
the checkpoint/head to match. The external sinks (stdout/webhook/S3) remain the
append-only record of last resort such an attacker cannot retroactively alter.
An HMAC-keyed chain (key held outside the database) would close that
remaining gap and is a candidate future hardening, not yet implemented.

Threat-model context lives in [`security.md`](security.md#audit-log-integrity).

#### Upgrade testing (`e2e upgrade` bucket) (shipped v0.2.0-beta.8) <!-- doc-versions: historical -->

`helm upgrade` was the most dangerous operation a user performs and the only
one CI never exercised. It now runs on every PR, on both amd64 and arm64:
`deploy/kind/upgrade.sh` installs the **previous release's published chart and
published GHCR images**, `TestUpgrade_FromPreviousRelease` seeds a running
GameServer with data plus an admin user, upgrades to the working-tree chart,
and asserts the CRD schema was updated, the server and its volume bytes
survived, and the pre-upgrade admin can still log in (proving migrations ran
against a populated SQLite volume).

The CRD assertion is self-calibrating: it computes which schema properties the
working tree declares that the installed release lacks, then requires the
pre-upgrade hook to have added exactly those — so it cannot rot into a vacuous
pass as the schema evolves.

This closes the CRD-upgrade caveat as a *tested* path rather than a documented
one. Note the caveat's own docs were stale and are corrected: the chart's
`crds.autoApply` pre-upgrade hook has been applying CRDs automatically for a
while, but [`install.md`](install.md#helm-crd-caveat) still told users to run
`kubectl apply` by hand.

Not yet covered: upgrades skipping several releases at once, and Postgres [experimental].

#### Idle auto-sleep (backend PR #180, dashboard PR #182) (shipped v0.2.0-beta.8) <!-- doc-versions: historical -->

Opt-in per server (`spec.idle`): the operator scales a GameServer to zero once
it has reported no online players for `afterMinutes`, and brings it back on a
`wakeWindows` cron tick or an explicit `POST /servers/{name}:wake`. On a
single-node homelab the overnight reservation of several idle servers is the
dominant cost; this releases it without touching the data volume.

**Design (as implemented):**

- Sleeping drives the existing `softStop` path, so the module-declared stop
  sequence runs and the world saves before the pod goes away.
- The sleep marker is an operator-owned annotation, **not** `spec.suspend`.
  That field is the user's own power switch (the `:start`/`:stop` verbs patch
  it), so overloading it would make an automatic sleep indistinguishable from a
  deliberate stop — and a wake window would then resurrect a server its owner
  had turned off. Wake uses a request/ack token pair like the restart primitive.
- Phase reuses `Suspended`; the `IdleAsleep` condition reason, `status.idle`,
  and an `-o wide` print column distinguish it from a manual stop.
- A stale heartbeat or an absent player count both freeze the idle clock rather
  than reading as empty — unknown is not zero. Each refusal records a reason on
  `status.idle`, so a server that can never sleep explains itself.
- `gameplane_gameservers_idle{state=asleep|awake}` reports the saving.
- The dashboard (PR #182) renders a slept server as **Asleep** rather than
  `Suspended`, and replaces its Start button with **Wake**. That substitution
  is a bug fix, not decoration: an asleep server has `spec.suspend` still
  `false`, so the Start button the phase used to offer patched a field to the
  value it already held — a silent no-op that never woke anything. Only
  `:wake` clears the operator's marker.
- The server's Overview surfaces `status.idle.reason` (capitalized) as the
  sleep card's own state line — including while still asleep, if a wake
  window failed to parse — so a server that isn't accumulating idle time
  says why instead of looking broken. The one reason meaning the trigger can
  never fire ("this game reports no player count") gets a plainer "Will
  never sleep" label instead, with the operator's reason underneath it.

**Follow-up:** `status.idle.nextWakeTime`. The dashboard lists the configured
wake windows as raw cron strings, because the operator computes the next tick
only for its own requeue and never persists it — and re-deriving it in the
browser would duplicate scheduling semantics the operator owns (rule 10).
Persisting it on status is the right fix, and is a CRD change rather than a
dashboard one.

#### Wake-on-connect for idle auto-sleep (shipped v0.2.0-beta.8) <!-- doc-versions: historical -->

A sentinel [optional] component holds advertised ports while a server is asleep and wakes
it on a genuine connection attempt. Opt-in per server via `spec.idle.wakeOnConnect`
(default false), with per-port protocol awareness to avoid corrupting handshakes.

**Design (as implemented):**

- New optional component: `sentinel/` (distroless Docker image, configured via
  `operator.sentinelImage`). While a GameServer is armed (`spec.idle.wakeOnConnect=true`)
  and asleep, the operator runs it as a 1-replica Deployment `<gs>-waker`.
- The sentinel holds the game's advertised ports (read from `spec.networking`
  and `GamePort.advertise`) and switches traffic between the game pod and itself
  via Service endpoint surgery: when asleep, the Service points to the sentinel;
  when waking, it points back to the game pod (via a `<gs>-game-direct` ClusterIP
  Service the game pod advertises to).
- New shared module: `gameproto/` for Minecraft and Terraria real handshake
  parsing (`Consumed` field lets callers reconstruct the client stream for lossless
  replay). Every other game uses `generic` — a packets-in-window heuristic.
- New CRD fields: `GameServer.spec.idle.wakeOnConnect` (bool, default false) and
  `GamePort.wakeProtocol` (enum: `minecraft|terraria|generic|none`, default `generic`).
- Modules: `minecraft-java` and `terraria` declare their parser.
- Server-list pings are answered in place without waking (parsing distinguishes
  a join from a status query); the player's client gets a protocol-native
  "waking up, try again" message if the game misses the deadline (25s default).
- UDP-only games (Valheim, Factorio, etc.) hold no connection, so they use the
  generic heuristic and wake-and-drop — the client retries.
- Hostport mode is deliberately asymmetric: the sentinel cannot hold the host port
  the game pod needs to schedule, so on wake it bounces held clients and exits.
  Service-backed modes (ClusterIP/NodePort/LoadBalancer) proxy through until Ready.
- Costs one small pod per sleeping server, which is why it is opt-in.

**Honest limitations:** Only Minecraft and Terraria get real parsing; tModLoader
also declares the Terraria parser. The other 26 of the 29 catalog modules use the
`generic` default (they hold no connection to parse), and hostport is asymmetric as described.

#### Read-only MCP server (PR #105) (shipped v0.2.0-beta.6) <!-- doc-versions: historical -->

An [MCP](https://modelcontextprotocol.io) server letting an AI assistant read
current cluster state and *propose* fixes — strictly read-only, no writes.

A new optional component [optional] (distroless Docker image, Helm toggle) exposing only
`List` / `Get` / `Watch` over the Gameplane CRDs plus Pods, Events, and pod logs.
"Propose a fix" returns suggested YAML or `kubectl` invocations as text — no
create/update/delete/patch tool exists. See [`mcp-server/README.md`](../mcp-server/README.md).

#### Module signing: active for official bundles (ECDSA P-256, Rekor-logged) (shipped v0.2.0-beta.8) <!-- doc-versions: historical -->

The keyed-cosign signing mechanism is implemented and e2e-proven, and
`ModuleSource.spec.verify` can require a valid signature. It is now **active**
for official bundles with Sigstore Rekor transparency logging: `cosign.pub`
(ECDSA P-256) is committed under `signing/` (and baked into the chart for
module verification), and the Helm chart is signed for the first time.
`publish-edge` signs the rolling images, `release` signs the tagged images,
the chart, and the official bundles, and `republish-modules` re-signs bundles
out of band — all by digest with the provisioned `COSIGN_PRIVATE_KEY`, all
recording their signatures in the public Rekor transparency log, and all
fail-closed when the key is absent. The operator's verify path remains
offline/keyed, preserving air-gapped functionality. See
[Signing official bundles](module-authoring.md#signing-official-bundles).

#### Hermetic module images (gameplane-module#35, docs in #178) (shipped v0.2.0-beta.8) <!-- doc-versions: historical -->

Every shipped module's default image is now pinned by digest, so a server
binary can no longer change underneath a user on pod restart with no version
bump or changelog. This was not hypothetical: `terraria-latest` moved from
Terraria 1.4.4.9 to 1.4.5.6, changing the network protocol version and
breaking the e2e Terraria bot.

Eight games also gained a real `spec.versions` list, so a floating tag now
survives only where drift is the user's explicit, labelled choice. The
module repo's `validate.py` enforces the rule (`spec.image` and any
`default: true` entry are an error if unpinned, with a `# gameplane:floating`
opt-out), and a monthly workflow refreshes the pins. Authoring rules live in
[Pinning images by digest](module-authoring.md#pinning-images-by-digest).

---

## In progress

Status as of 2026-10-14. Task counts come from each spec's `tasks.md`.

- **Spec 002, Nuclear Option module and load-balancer IP pool override** (`specs/002-nuclear-option-ip-pool/`): 136 of 143 tasks done. Blocked: the server does not bind UDP 7777, so no join client can be built until a real-client packet capture documents the handshake (maintainer decision, 2026-10-11). Unblocked by: that packet capture. The join test, its bucket and the coverage row wait on it.
- **Spec 009, code scanning remediation and Dependabot** (`specs/009-remediate-security-dependabot/`): 80 of 82 tasks done. Blocked: TypeScript 7 is blocked because the `@typescript-eslint` peer range stops before TypeScript 7, and ESLint 10 is blocked because `eslint-plugin-react` does not accept `^10`. Unblocked by: a `@typescript-eslint` release that accepts TypeScript 7 (then T055/T056 and PR #272), and an `eslint-plugin-react` release that accepts `^10` (then PR #386, and #387 if open).
- **Spec 013, expand test coverage** (`specs/done_013-expand-test-coverage/`): 39 of 62 tasks done. No external blocker is named. Open work is the live web e2e specs, the optional-components e2e, the Postgres e2e, docs and checks, and the archive step. Some tasks wait for the first green CI run of their job, and the archive waits until every PR is merged. Open PRs for this spec are in progress.
- **Spec 019, app localization and community language packs** (`specs/019-localization-language-packs/`): 0 of 105 tasks checked; the four design decisions are settled. The implementation is on open PR #668 and is not yet on `master`. Unblocked by: merging PR #668, which needs human approval. The first step is the `i18n/` module and checker.
- **Spec 021, edit game configuration from server Settings** (`specs/done_021-settings-game-config/`): done. All 24 tasks complete; merged in PR #568 and PR #652, then archived.
- **Spec 022, default telemetry destination, extended telemetry and telemetry dashboard** (`specs/done_022-default-telemetry-dashboard/`): 109 of 119 tasks done. The shipped feature is unchanged until the version-trend amendment (2026-10-14, T101 to T110) lands. Blocked: T104 needs Pencil design work on the machine where Pencil runs; T109 needs the user's sign-off before editing existing tests `lifecycle_test.go:11` and `charts_test.go:83`; T110 waits for the PR to merge. Unblocked by: the design work, that sign-off, and the merge.
- **Spec 023, change-based test selection** (`specs/023-change-based-test-selection/`): 0 of 52 tasks done; no open decisions remain. No blocker is named. T001 creates the `ci: full-run` label. T052 needs 30 merged PRs to measure runner minutes.
- **Spec 025, standalone panel and API** (`specs/025-standalone-panel/`, see [standalone panel](standalone-panel.md)): implemented and merged in PR #630; all 51 tasks done, with known gaps listed in the spec. The folder was renumbered from `023-standalone-panel` and written after the fact. Next step: archive to `done_025`.

---

## Blocking v1

Nothing hard-blocks v1 anymore. What stands between v0.3 and a v1 GA is the
production-readiness hardening below — tracked items, not code gaps.

---

## Wanted for v1, not blocking

### Production-readiness hardening (planned)

- A documented backup/restore **drill** — a runbook an operator can follow.
  The *coverage* half of this item is already done and was stale here:
  `TestRestore_RoundTrip` (`test/e2e/restore_e2e_test.go`) writes a marker into
  a server's PVC, backs it up, wipes it, restores, and asserts the bytes came
  back — against the real in-cluster restic-server that `ensureResticRepo`
  provisions. What is missing is the human-facing runbook, not the test.
- Resource-limit guidance sized from real workloads rather than defaults.

### Postgres driver: production readiness (experimental)

SQLite is the only production-tested driver. The Postgres driver (build tag
`-tags postgres`) now works end to end (#518): migrations 001–012 have
hand-written Postgres equivalents (`api/internal/db/migrations/postgres/`),
every later migration is one portable file both drivers run
(`migrations/common/`), the Postgres connection rewrites `?` placeholders to
`$n`, and runtime timestamps are generated in Go. The `api (postgres)` CI job
builds the api with the tag and runs `api/internal/db`'s tests against a real
PostgreSQL server. It stays **experimental** until the rest lands: running the
handler/auth/audit test suites and the kind e2e and upgrade suites against
Postgres, a published image built with `-tags postgres`, and multi-replica
safety (the user-management lock and the audit hash chain assume a single API
process).

---

## Known gaps tracked for completion

### Users page: service accounts and identity-provider tabs (planned, v1.1)

The Users page has two placeholder tabs (`web/src/routes/Users.tsx`,
`ServiceAccountsTab` and `IdpTab`). **Service accounts** (machine-to-machine
API tokens) don't exist yet and are tracked for v1.1. **Identity providers**
has no controls of its own: OIDC providers are already managed under Admin
Settings → Authentication (see [OIDC provider setup](oidc.md)), or through
the `api.oidc.*` Helm values. The tab's own UI is tracked for v1.1.

### Game module template specifications (planned)

Constitution IV requires a `specs.md` file per game module documenting the
template's authoring and configuration — the game module itself, not its
join-protocol wire format. Those specification files belong in the
`gameplane-module` submodule repo (e.g., `modules/minecraft-java/specs.md`),
are tracked there in the module repository rather than in this main repo, and
are a known incomplete item for v1. As of 2026-10-14, 26 of the 29 modules have
a `specs.md`; `enshrouded`, `minecraft-java` and `v-rising` do not yet. See the
module submodule's project board for status.

---

## Out of scope for v1

- **A hosted/managed Gameplane (not planned).** The project targets self-hosted clusters, from
  a single-node k3s homelab to multi-node production.

---

## Game server join protocol coverage

Every Gameplane-shipped game module now has a recorded, verified coverage
status for its join-protocol E2E testing. See [`docs/game-coverage.md`](game-coverage.md) for the
canonical per-module status:

- **2 modules** have real join coverage in CI (`minecraft-java`, `terraria`).
- **25 modules** are blocked on undocumented or partially-documented
  wire-protocol formats, each with a named unblocking artifact (packet capture,
  reverse-engineering session, or anti-cheat analysis). They are candidates for
  future protocol work once documentation becomes available.
- **2 modules** are permanently out-of-scope-by-design due to architectural
  constraints (`valheim` routes through Steam Datagram Relay;
  `dayz` requires BattlEye anti-cheat validation before join completion).

Total: 29 modules.
