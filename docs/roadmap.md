# Roadmap to v1

Gameplane is currently **`v0.3.0`**. The CRDs, operator, API, agent, and
dashboard are feature-complete for the v1 scope and stabilized for external
testing, but the project is not yet recommended for unattended production. This
page tracks what stands between v0.3 and a v1 GA release.

It is a living document: items move out of it as they ship, and anything
discovered along the way gets added. For what already works today, see
[`README.md`](../README.md) and [`architecture.md`](architecture.md).

---

## Shipped toward v1

These items have shipped and are reflected in current `main`.

### Multi-cluster: dashboard cluster selector (PR #107) (shipped v0.2.0-beta.6) <!-- doc-versions: historical -->

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

### Multi-cluster: dual-cluster e2e coverage (PR #104) (shipped v0.2.0-beta.6) <!-- doc-versions: historical -->

A real `kind ×2` e2e test (multicluster bucket) asserting `?cluster=` dispatch
lands a GameServer on cluster B and that a viewer scoped to cluster A cannot see it.

### Audit log integrity (tamper evidence) (PR #103, dashboard banner PR #108) (shipped v0.2.0-beta.6) <!-- doc-versions: historical -->

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

### Upgrade testing (`e2e upgrade` bucket) (shipped v0.2.0-beta.8) <!-- doc-versions: historical -->

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

### Idle auto-sleep (backend PR #180, dashboard PR #182) (shipped v0.2.0-beta.8) <!-- doc-versions: historical -->

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

### Wake-on-connect for idle auto-sleep (shipped v0.2.0-beta.8) <!-- doc-versions: historical -->

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

**Honest limitations:** Only Minecraft and Terraria get real parsing; the other
14 shipped modules use generic (they are UDP-only and have no connection to
hold). Hostport is asymmetric as described.

### Read-only MCP server (PR #105) (shipped v0.2.0-beta.6) <!-- doc-versions: historical -->

An [MCP](https://modelcontextprotocol.io) server letting an AI assistant read
current cluster state and *propose* fixes — strictly read-only, no writes.

A new optional component [optional] (distroless Docker image, Helm toggle) exposing only
`List` / `Get` / `Watch` over the Gameplane CRDs plus Pods, Events, and pod logs.
"Propose a fix" returns suggested YAML or `kubectl` invocations as text — no
create/update/delete/patch tool exists. See [`mcp-server/README.md`](../mcp-server/README.md).

### Module signing: active for official bundles (ECDSA P-256, Rekor-logged) (shipped v0.2.0-beta.8) <!-- doc-versions: historical -->

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

### Hermetic module images (gameplane-module#35, docs in #178) (shipped v0.2.0-beta.8) <!-- doc-versions: historical -->

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

## Explicitly out of scope for v1

- **A hosted/managed Gameplane (not planned).** The project targets self-hosted clusters, from
  a single-node k3s homelab to multi-node production.

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
are a known incomplete item for v1. See the module submodule's project board
for status.

---

## Game server join protocol coverage

Every Gameplane-shipped game module now has a recorded, verified coverage
status for its join-protocol E2E testing. See [`docs/game-coverage.md`](game-coverage.md) for the
canonical per-module status:

- **2 modules** have real join coverage in CI (`minecraft-java`, `terraria`).
- **26 modules** are blocked on undocumented or partially-documented
  wire-protocol formats, each with a named unblocking artifact (packet capture,
  reverse-engineering session, or anti-cheat analysis). They are candidates for
  future protocol work once documentation becomes available.
- **2 modules** are permanently out-of-scope-by-design due to architectural
  constraints (`valheim` routes through Steam Datagram Relay;
  `dayz` requires BattlEye anti-cheat validation before join completion).
