# Findings

Every finding blocks the release until it is `verified`, `closed-already-fixed`, `not-a-defect` or `out-of-scope` (FR-005, OD-002). Severity (research R3) only orders the fix work: S1 data loss, security-boundary break, or upgrade/rollback corruption; S2 core path broken with no workaround; S3 degraded behaviour or a workaround exists; S4 cosmetic, documentation or wording. Columns are fixed by [contracts/audit-records.md](../contracts/audit-records.md#findingsmd).

Security findings that are not yet fixed are held off-git until their fix merges ([OD-019](../OPEN-DECISIONS.md#od-019-security-findings-stay-unpushed-until-fixed--resolved-2026-09-24)), so the ID sequence below can have gaps.

| ID | Title | Component | Origin | Severity | Status | Fix | Re-verified | Note |
|----|-------|-----------|--------|----------|--------|-----|-------------|------|
| F-001 | Dependency bump (#406) broke upload-dialog unit tests and keyboard-focus e2e | web/ | imported:#414 | S3 | imported | #406 | | fix merged in #406; confirm live in T049 |
| F-002 | Visual-diff residuals after feature 014 conformance (7 screens) | web/ | imported:#377 | S4 | imported | #378 | | fix merged in #378; confirm live in T049 |
| F-003 | Design exports mismatched shipped UI: DMnEi mislabel, J5pjJ3 composite frame, oversized chips/badges | web/ | imported:#376 | S4 | imported | #378 | | fix merged in #378; confirm live in T049 |
| F-004 | e2e `page.route()` overrides silently no-op under mock-target MSW service worker | web/ | imported:#375 | S3 | imported | #378 | | fix merged in #378; confirm live in T049 |
| F-005 | Live share-links e2e failure traced to missing `/shares` rule in Vite dev proxy | web/ | imported:#373 | S3 | imported | #371 | | fix merged in #371 (dev/CI only; production is same-origin, unaffected); confirm live in T049 |
| F-006 | CI `dump-cluster-state` redaction misses quoted/JSON-embedded secret values | .github/ | imported:#306 | S1 | imported | #327 | | fix merged in #327 (same fix also imported as F-013); confirm live in T049 |
| F-007 | Console WebSocket rejected (403) on non-default ports; `send()` before open threw | web/ | imported:#416 | S2 | imported | #416 | | merged fix; confirm live in T049 |
| F-008 | Module catalog tag filters unioned instead of intersected | web/ | imported:#411 | S3 | imported | #411 | | merged fix; confirm live in T049 |
| F-009 | Vertical tab list rendered as an oval | web/ | imported:#410 | S4 | imported | #410 | | merged fix; confirm live in T049 |
| F-010 | Console terminal grew without bound | web/ | imported:#409 | S3 | imported | #409 | | merged fix; confirm live in T049 |
| F-011 | Preset share links didn't expire at end of day like custom dates | web/ | imported:#396 | S3 | imported | #396 | | merged fix; confirm live in T049 |
| F-012 | Security-audit hardening: RBAC, share/tunnel credential handling, GameServer reconciler validation (bundled fix) | api/, operator/, charts/gameplane/, images/ | imported:#350 | S1 | imported | #350 | | bundled fix PR, not a single tracked issue; imported by title + file list only per task scope; confirm live in T049 |
| F-013 | CI `dump-cluster-state`: redact quoted/JSON-embedded secret values (fix for F-006) | .github/ | imported:#327 | S1 | imported | #327 | | fixes the redaction gap reported in #306 (F-006); merged fix; confirm live in T049 |
| F-014 | Share links not scoped to their originating cluster in multi-cluster installs | api/ | imported:SECURITY_AUDIT.md#1 | S1 | imported | #350 | | fixed together with F-012 in PR #350 (`api/internal/handlers/shares.go`, `api/internal/db/shares.go`, migration `009_share_links_cluster.sql`); confirm live in T049 |
| F-015 | GameServer env-var Secret/ConfigMap references not restricted to server-owned objects | api/, operator/ | imported:SECURITY_AUDIT.md#2 | S1 | imported | #350 | | fixed together with F-012 in PR #350 (`api/internal/handlers/resources.go`, `operator/internal/controller/gameserver_controller.go`); confirm live in T049 |
| F-016 | Audit syslog bridge had no ingress NetworkPolicy restricting senders to the API | charts/gameplane/ | imported:SECURITY_AUDIT.md#3 | S3 | imported | #350 | | fixed together with F-012 in PR #350 (`charts/gameplane/templates/audit-syslog-bridge.yaml`); confirm live in T049 |
| F-017 | steamcmd base image referenced by a mutable `:latest` tag instead of a pinned digest | images/ | imported:SECURITY_AUDIT.md#4 | S3 | imported | #350 | | fixed in PR #350 (`images/common/steamcmd/Dockerfile:42`); digest since bumped again by PR #380 (Dependabot) — pin-by-digest control unaffected; confirm live in T049 |
| F-018 | Tunnel credential Secret delete/update/mount lacked ownership checks | api/, operator/ | imported:SECURITY_AUDIT.md#5 | S1 | imported | #350 | | fixed together with F-012 in PR #350 (`api/internal/handlers/tunnelcreds.go`, `api/internal/handlers/resources.go`, `operator/internal/controller/gameserver_tunnel.go`); confirm live in T049 |
| F-019 | Telemetry receiver had no ingress NetworkPolicy restricting senders to the API | charts/gameplane/ | imported:SECURITY_AUDIT.md#6 | S3 | imported | #350 | | fixed together with F-012 in PR #350 (`charts/gameplane/templates/telemetry-receiver.yaml`); confirm live in T049 |
| F-020 | Nuclear Option's assumed UDP 7777 game-join port is not bound on the live server | modules/, docs/ | imported:specs/002-nuclear-option-ip-pool/spec.md | S4 | imported | | | believed not-a-defect: matches FR-004's documented contingency (see Justification); T049 confirms |
| F-021 | Helm-seeded OIDC role mappings documented while the feature sits under CHANGELOG Unreleased | docs/ | imported:specs/012-docs-refresh-and-outreach/OPEN-DECISIONS.md#OD-8 | S4 | imported | #340 | | believed fixed by #340: per OD-8's ruling, `docs/oidc.md:21` carries the unreleased qualifier; confirm in T049 |
| F-022 | `cli` rcon.protocol semantics: confirm the implementation matches the recorded Option A ruling | operator/, agent/, web/ | imported:specs/015-top-steam-game-modules/OPEN-DECISIONS.md#1-cli-rconprotocol-semantics-t005-t019-t025 | S3 | imported | | | 015 OD §1 records "Resolution: Option A Confirmed"; T049 checks operator `resolveRCON`, agent and `rconAvailable` against it |
| F-023 | Open question: Factorio template's `rcon.protocol: source` vs. an earlier contract claim of `none` | modules/ | imported:specs/015-top-steam-game-modules/OPEN-DECISIONS.md#2-factorio-protocol-finding-t005 | S3 | imported | | | shipped template retained pending final maintainer confirmation |
| F-024 | Open question: Project Zomboid template's `rcon.protocol: source` vs. an earlier contract claim of `none` | modules/ | imported:specs/015-top-steam-game-modules/OPEN-DECISIONS.md#3-project-zomboid-protocol-finding-t005 | S3 | imported | | | shipped template retained pending final maintainer confirmation |
| F-025 | Open question: generic `rest` rcon wire contract (FiveM txAdmin / Farming Simulator 25) not yet given a final ruling | agent/ | imported:specs/015-top-steam-game-modules/OPEN-DECISIONS.md#5-generic-rest-wire-contract-t005-t018-t047-t052 | S3 | imported | | | contract documented and implemented in `agent/internal/rcon/rest.go`; pending final maintainer sign-off |
| F-026 | `hack/check-doc-versions.sh` recognises only `-beta.N` versions and matches single-digit minor/patch components where its header documents `[0-9]+` | hack/ | review:hack | S3 | fixed-unverified | #420 | | |
| F-027 | `docs/install.md` has no rollback procedure | docs/ | review:docs | S3 | open | | | fix in T063 after the live rollback (T062) |
| F-028 | `charts/gameplane/values.yaml` git module source still pinned to `ref: v0.2.0-beta.6` | charts/gameplane/ | review:charts/gameplane | S3 | open | | | T054: pin to a new gameplane-module v0.3.0 tag after INV-MOD rows pass |
| F-029 | CI upgrade baseline still `0.2.0-beta.5` across `deploy/kind/upgrade.sh`, `.github/workflows/ci.yaml`, `.claude/agents/ci-triager.md` | deploy/ | review:deploy | S3 | fixed-unverified | #422 | | |
| F-030 | CLAUDE.md repository map says "14 Go modules" and omits `gp-module/` | root docs | review:root-docs | S4 | fixed-unverified | #421 | | |
| F-031 | GameServer examples in tunnels.md use spec.template instead of templateRef.name | docs/ | review:docs | S4 | fixed-unverified | #468 | | all three examples fail kubectl apply |
| F-032 | README, roadmap and comparison cite 16 game modules but repo has 30 | docs/ | review:docs | S4 | fixed-unverified | #494 | | module count drift since v0.2.0-beta.8 |
| F-033 | security.md misstates capture feature default as "true" when it is "false" | docs/ | review:docs | S4 | fixed-unverified | #494 | | contradicts install.md and architecture.md |
| F-034 | comparison-sources.md evidence citations no longer match CLAUDE.md line numbers | docs/ | review:docs | S4 | fixed-unverified | #494 | | lines drifted after CLAUDE.md trim; also one nonexistent values.yaml key |
| F-035 | dependencies.md lacks 6 Go modules: capture-sidecar, gameproto, gp-module, sentinel, svcutil, tunnel | docs/ | review:docs | S4 | fixed-unverified | #494 | | six missing from coverage statement and per-module sections |
| F-036 | contributing.md per-component test list omits 6 Go modules (same list as F-035) | docs/ | review:docs | S4 | fixed-unverified | #494 | | aggregate make test covers them, but per-component list is incomplete |
| F-037 | website games page lists 16 games but 30 now shipped | website/ | review:website | S4 | open | | | fix with v0.3 catalog, same change as F-028/T054 |
| F-038 | website comparison.mdx says "16 official modules" but 30 now shipped | website/ | review:website | S4 | open | | | fix with v0.3 catalog, same change as F-028/T054 |
| F-039 | website VERSION constant still beta.7; beta.8 published 2026-08-22 | website/ | review:website | S4 | open | | | affects homepage install cmd, hero badge, footer, FAQ, roadmap; also changelog.mdx |
| F-040 | fast game set definitions conflict: fastGameSet (6) vs buckets.sh (3) vs specs.md (3 or 4) | test/e2e/ | review:test-e2e | S4 | fixed-unverified | #482 | | factorio, tmodloader, beammp inconsistently classified |
| F-041 | e2e/internal/specs.md depth table has 16 rows but repo has 29 probe packages | test/e2e/ | review:test-e2e | S4 | fixed-unverified | #482 | | docs outdated; docs/game-coverage.md is current source |
| F-042 | api-roles bucket admin login count undercounted in docs: real count 7, cited 5–6 | test/e2e/ | review:test-e2e | S4 | fixed-unverified | #482 | | bucket at ~7 ceiling; docs say ≤~5; no test failure (429 retry absorbs) |
| F-043 | CHANGELOG.md "[Unreleased]" missing ~60 PRs of user-facing changes since v0.2.0-beta.8 | root docs | review:root-docs | S4 | fixed-unverified | #423 | | backfilled by the rc.1 CHANGELOG PR (OD-014) |
| F-044 | Quiesce-state persisted before unquiesce completes | operator | review:operator | S3 | fixed-unverified | #454 | | |
| F-045 | Auto-scheduled backups never get the quiesce default | operator | review:operator | S3 | fixed-unverified | #454 | | |
| F-046 | Pinned module versions stuck in Pulling loop | operator | review:operator | S3 | fixed-unverified | #451 | | |
| F-047 | Template reference change wedges the server | operator | review:operator | S3 | fixed-unverified | #451 | | |
| F-048 | Backup with quiesce can be deleted before unquiesce | operator | review:operator | S3 | fixed-unverified | #454 | | |
| F-049 | Restore does not delete post-snapshot files | operator | review:operator | S3 | fixed-unverified | #454 | | |
| F-050 | Deleted managed GameTemplate not recreated on next reconcile | operator | review:operator | S3 | fixed-unverified | #451 | | |
| F-051 | Capture expiration waits for unreachable sidecar | operator | review:operator | S4 | fixed-unverified | #490 | | |
| F-052 | UDP tunnel forwarding sends TCP | operator | review:operator | S3 | fixed-unverified | #428 | | |
| F-053 | Fixed-name deletes run without ownership check | operator | review:operator | S4 | fixed-unverified | #515 | | |
| F-054 | Wipe always succeeds even if it failed | operator | review:operator | S3 | fixed-unverified | #436 | | |
| F-055 | TunnelHostnameIgnored condition not removed | operator | review:operator | S4 | fixed-unverified | #490 | | |
| F-056 | Address not validated before sending to MetalLB/Cilium | operator | review:operator | S4 | fixed-unverified | #490 | | |
| F-057 | Idle window parse error appears only in status, not as condition | operator | review:operator | S4 | fixed-unverified | #490 | | |
| F-058 | Four unused capture configuration fields with misleading comments | operator | review:operator | S4 | fixed-unverified | #490 | | |
| F-059 | Operator specs.md contradicts working code | operator | review:operator | S4 | fixed-unverified | #490 | | |
| F-060 | Seven CLI flags missing from specs.md table | operator | review:operator | S4 | fixed-unverified | #490 | | |
| F-061 | Operator specs.md dependencies stale vs go.mod | operator | review:operator | S4 | fixed-unverified | #490 | | |
| F-062 | Seven spec.md statements contradict code | operator | review:operator | S4 | fixed-unverified | #490 | | |
| F-063 | Operator config/ dev path samples fail end-to-end | operator | review:operator | S4 | fixed-unverified | #490 | | |
| F-064 | OCI manifest body unverified, breaking signature guarantee | operator | review:operator | S1 | fixed-unverified | #427 | | |
| F-065 | Module spec.digest ignored in convergence check | operator | review:operator | S3 | fixed-unverified | #427 | | |
| F-066 | OCI and cosign fetches bypass netguard guard | operator | review:operator | S3 | fixed-unverified | #427 | | |
| F-067 | Capture with empty filter always fails | operator | review:operator | S3 | fixed-unverified | #474 | | |
| F-068 | Playit tunnel egress and address reporting incomplete | operator | review:operator | S3 | fixed-unverified | #534 | | |
| F-069 | Tunnel credential docs show unowned Secret creation | operator | review:operator | S3 | fixed-unverified | #534 | | |
| F-070 | Volume-snapshot restore never finishes with owned Secrets | operator | review:operator | S3 | fixed-unverified | #515 | | |
| F-071 | Operator RBAC docs contradict split design | operator | review:operator | S4 | fixed-unverified | #513 | | |
| F-072 | Operator dev RBAC manifests bind nonexistent roles | operator | review:operator | S4 | fixed-unverified | #513 | | |
| F-073 | ModuleSource spec.oci.insecure doc promises TLS-skip that code forbids | operator | review:operator | S4 | fixed-unverified | #427 | | |
| F-074 | requestTimeout(60s) exempts WebSocket/SSE only; upload/capture proxies cut at 60s | api/ | review:api | S3 | fixed-unverified | #444 | | |
| F-075 | bodyLimit(1 MiB) deadcaps /mods/upload and /files/write; declared limits ignored | api/ | review:api | S3 | fixed-unverified | #444 | | |
| F-076 | config row missing returns 500 instead of 200 on idempotent reset | api/ | review:api | S4 | fixed-unverified | #477 | | |
| F-077 | Plain errors mapped to 500 instead of hand-written 400/409 | api/ | review:api | S4 | fixed-unverified | #477 | | |
| F-078 | WriteSync enqueues reason; webhook/S3/CSV sinks drop it | api/ | review:api | S3 | fixed-unverified | #512 | | |
| F-079 | User delete leaves orphaned oidc_links on SQLite (no cascade) | api/ | review:api | S3 | fixed-unverified | #473 | | |
| F-080 | Dashboard-managed OIDC providers skip audit on role assignment | api/ | review:api | S3 | fixed-unverified | #471 | | |
| F-081 | kubeconfig and join command address is in-cluster ClusterIP (unreachable) | api/ | review:api | S3 | fixed-unverified | #464 | | |
| F-082 | RevokeShareLink returns 500 on unknown id instead of 404 | api/ | review:api | S4 | fixed-unverified | #477 | | |
| F-083 | 201 responses have Content-Type text/plain instead of json | api/ | review:api | S4 | fixed-unverified | #477 | | |
| F-084 | Tunnel credentials merge-patch leaves stale key on provider switch | api/ | review:api | S4 | fixed-unverified | #505 | | |
| F-085 | Backfilled theme preference timestamps in SQLite format not RFC 3339 | api/ | review:api | S4 | fixed-unverified | #477 | | |
| F-086 | Spec lists 7 capture endpoints but code has 8; documentation gaps only | api/ | review:api | S4 | fixed-unverified | #477 | | |
| F-087 | Spec capture/audit/RBAC statements contradictory | api/ | review:api | S4 | fixed-unverified | #477 | | |
| F-088 | Spec session/limits/roles statements contradictory | api/ | review:api | S4 | fixed-unverified | #477 | | |
| F-089 | Spec dependencies and flags stale vs. go.mod and main.go | api/ | review:api | S4 | fixed-unverified | #477 | | |
| F-090 | MountRegistry handlers lack rejectRemoteCluster guard; RBAC bypass | api/ | review:api | S1 | fixed-unverified | #430 | | |
| F-091 | ClientIPFromXFF never checks RemoteAddr; per-IP limits collapse | api/ | review:api | S3 | fixed-unverified | #472 | | |
| F-092 | OIDC role sync keyed to Helm base only; override-only mappings skip re-eval | api/ | review:api | S3 | fixed-unverified | #471 | | |
| F-093 | /events requires servers:read only; streams all object kinds | api/ | review:api | S3 | fixed-unverified | #469 | | |
| F-094 | Share-link revoke WHERE clause doesn't match ownership check | api/ | review:api | S4 | fixed-unverified | #473 | | |
| F-095 | User delete doesn't cascade oidc_links/preferences on SQLite | api/ | review:api | S3 | fixed-unverified | #473 | | |
| F-096 | enableLocalLogin drops helmOverride from auth config silently | api/ | review:api | S3 | fixed-unverified | #471 | | |
| F-097 | --force password reset doesn't evict sessions | api/ | review:api | S3 | fixed-unverified | #471 | | |
| F-098 | captureDownload proxies to home cluster even for remote requests | api/ | review:api | S3 | fixed-unverified | #430 | | |
| F-099 | Spec claims netguard on registry fetches; code uses plain http.Client | api/ | review:api | S4 | fixed-unverified | #504 | | |
| F-100 | Module archive extraction has no total decompressed budget | api/ | review:api | S4 | fixed-unverified | #504 | | |
| F-101 | DeleteFunc ignores tombstones; cluster revocation waits for watcher | api/ | review:api | S4 | fixed-unverified | #463 | | |
| F-102 | File write operations lose previous file on error | agent | review:agent | S1 | fixed-unverified | #433 | | |
| F-103 | WebSocket and file streams drop after 30s context timeout | agent | review:agent | S3 | fixed-unverified | #437 | | |
| F-104 | RCON reply size check rejects packets 4087 bytes or larger | agent | review:agent | S3 | fixed-unverified | #437 | | |
| F-105 | gameVersion field holds template identifier instead of version | agent | review:agent | S3 | fixed-unverified | #448 | | |
| F-106 | Player count reports non-standard values for unknown state | agent | review:agent | S3 | fixed-unverified | #448 | | |
| F-107 | Quiesce rollback skipped when first command fails | agent | review:agent | S4 | fixed-unverified | #465 | | |
| F-108 | File delete follows symlinks and deletes target | agent | review:agent | S3 | fixed-unverified | #433 | | |
| F-109 | Agent spec and code contracts differ in 9 places | agent | review:agent | S4 | fixed-unverified | #465 | | |
| F-110 | Agent dependencies stale vs go.mod; intentional pin note false | agent | review:agent | S4 | fixed-unverified | #465 | | |
| F-111 | Agent doc comments contradict current code | agent | review:agent | S4 | fixed-unverified | #465 | | |
| F-112 | WebRcon dial error leaks RCON secret in error text | agent | review:agent | S3 | fixed-unverified | #516 | | |
| F-113 | Upload destination symlink escape possible via existing link | agent | review:agent | S3 | fixed-unverified | #511 | | |
| F-114 | Six spec statements contradict code security controls | agent | review:agent | S4 | fixed-unverified | #516 | | |
| F-115 | New file truncates existing file | web | review:web | S1 | open | | | |
| F-116 | Multi-namespace server features 404 | web | review:web | S2 | fixed-unverified | #429 | | |
| F-117 | Settings save merges entire spec, losing concurrent changes | web | review:web | S3 | open | | | |
| F-118 | Tunnel create blocked by missing Secret | web | review:web | S2 | open | | | |
| F-119 | Tunnel enable flows fail validation | web | review:web | S2 | open | | | |
| F-120 | Share links resolve hangs on extended wake | web | review:web | S3 | open | | | |
| F-121 | WebSocket reconnect after close leaks connections | web | review:web | S3 | fixed-unverified | #438 | | |
| F-122 | Settings dirty state persists on tab switch | web | review:web | S3 | open | | | |
| F-123 | Revoked share links show as active | web | review:web | S3 | open | | | |
| F-124 | Node selector never applied to running server | web | review:web | S3 | open | | | |
| F-125 | Safe mode exits on in-app navigation | web | review:web | S3 | fixed-unverified | #440 | | |
| F-126 | Six mutations have no error handlers | web | review:web | S3 | fixed-unverified | #455 | | |
| F-127 | Default namespace not used on server create | web | review:web | S3 | open | | | |
| F-128 | Modules hash navigation fails | web | review:web | S4 | open | | | |
| F-129 | Add cluster links to unusable page | web | review:web | S4 | open | | | |
| F-130 | Namespace row actions disabled | web | review:web | S4 | fixed-unverified | #429 | | |
| F-131 | Invalid sections don't block save | web | review:web | S4 | open | | | |
| F-132 | Reset text editor preserves old content | web | review:web | S4 | open | | | |
| F-133 | Invite dialog retains previous entry | web | review:web | S4 | open | | | |
| F-134 | Identity provider tab contradicts code | web | review:web | S4 | open | | | |
| F-135 | API timestamps use key instead of ts | web | review:web | S4 | fixed-unverified | #480 | | |
| F-136 | Typo: Baning | web | review:web | S4 | fixed-unverified | #480 | | |
| F-137 | Promise handlers not awaited | web | review:web | S4 | fixed-unverified | #455 | | |
| F-138 | Dead code in exports and branches | web | review:web | S4 | fixed-unverified | #489 | | #480 removes the provably dead code; kept (test-only use, removal needs sign-off): RequireRole, hasRole, themeExportToUpdate, Modules.get, Schedules.get, BackupDestinations.get, Restores.remove |
| F-139 | specs.md contradicts implemented features | web | review:web | S4 | open | | | |
| F-140 | specs.md lists unimplemented UI | web | review:web | S4 | open | | | |
| F-141 | Component version claims drift from reality | web | review:web | S4 | fixed-unverified | #480 | | #480 fixes the React/TS/Vite version facts only; web/specs.md still has stale line refs into api.ts/ServerDetail.tsx/Login.tsx and the "nine sub-views"/"11 sections" counts |
| F-142 | Secret removal race with unsaved config | web | review:web | S3 | fixed-unverified | #431 | | |
| F-143 | Role mapping reset doesn't persist | web | review:web | S2 | fixed-unverified | #431 | | |
| F-144 | Installed module verification badge overstates confidence | web | review:web | S3 | fixed-unverified | #509 | | |
| F-145 | Permission gates don't match API enforcement | web | review:web | S3 | fixed-unverified | #510 | | |
| F-146 | netguard usage poorly documented | netguard/ | review:netguard | S4 | fixed-unverified | #517 | | |
| F-147 | IsAllowed permits AWS/Alibaba cloud instance metadata addresses | netguard/ | review:netguard | S3 | fixed-unverified | #517 | | |
| F-148 | IsPublic accepts reserved/special-purpose addresses and IPv4-compatible form | netguard/ | review:netguard | S4 | fixed-unverified | #517 | | |
| F-149 | Parameter length cap is bytes, not characters | gameaction/ | review:gameaction | S4 | fixed-unverified | #491 | | |
| F-150 | Required parameter with default accepts empty value | gameaction/ | review:gameaction | S4 | fixed-unverified | #491 | | |
| F-151 | Template spec claims non-deterministic function exists | gameaction/ | review:gameaction | S4 | fixed-unverified | #491 | | |
| F-152 | gameaction specs.md says Go 1.25, module is 1.26 | gameaction/ | review:gameaction | S4 | fixed-unverified | #486 | | |
| F-153 | Classify error returns differ from spec promise | gameproto/ | review:gameproto | S4 | fixed-unverified | #470 | | |
| F-154 | Terraria version string exceeds documented 32 KB cap | gameproto/ | review:gameproto | S4 | fixed-unverified | #470 | | |
| F-155 | BuildStatusResponse skips JSON validation contract | gameproto/ | review:gameproto | S4 | fixed-unverified | #470 | | |
| F-156 | Example code in package doc doesn't compile | gameproto/ | review:gameproto | S4 | fixed-unverified | #470 | | |
| F-157 | gameproto specs.md layout, tests, deps, versions stale | gameproto/ | review:gameproto | S4 | fixed-unverified | #470 | | |
| F-158 | Adding WakeProtocol needs CRD enum change not documented | gameproto/ | review:gameproto | S4 | fixed-unverified | #470 | | |
| F-159 | Config enum field validation uses wrong field name | gp-module | review:gp-module | S3 | fixed-unverified | #441 | | |
| F-160 | CRD type enum missing boolean value | gp-module | review:gp-module | S3 | fixed-unverified | #441 | | |
| F-161 | Schema validation skips required CRD fields | gp-module | review:gp-module | S3 | fixed-unverified | #441 | | |
| F-162 | Preview lacks version-specific environment layer | gp-module | review:gp-module | S3 | fixed-unverified | #441 | | |
| F-163 | Module archetype docs contradict validation requirement | gp-module | review:gp-module | S4 | fixed-unverified | #479 | | |
| F-164 | Documented CLI and make commands fail | gp-module | review:gp-module | S4 | fixed-unverified | #479 | | |
| F-165 | Image pin command doesn't exist | gp-module | review:gp-module | S4 | fixed-unverified | #479 | | |
| F-166 | Preview flag and output format don't match contract | gp-module | review:gp-module | S4 | fixed-unverified | #479 | | |
| F-167 | Specs file lists nonexistent files and dependencies | gp-module | review:gp-module | S4 | fixed-unverified | #479 | | |
| F-168 | Version mismatch warning not implemented | gp-module | review:gp-module | S4 | fixed-unverified | #479 | | |
| F-169 | SteamCMD preset default image runs as root | gp-module | review:gp-module | S3 | fixed-unverified | #502 | | |
| F-170 | Docs claim adoption that didn't happen | svcutil | review:svcutil | S4 | fixed-unverified | #481 | | |
| F-171 | Specs outdated: sections missing and test count wrong | svcutil | review:svcutil | S4 | fixed-unverified | #481 | | |
| F-172 | Exponential backoff overflows after 64 retries | tunnel | review:tunnel | S3 | fixed-unverified | #428 | | |
| F-173 | Tailscale config drops backing service settings | tunnel | review:tunnel | S2 | fixed-unverified | #428 | | |
| F-174 | Playit endpoint not written to GameServer status | tunnel | review:tunnel | S3 | fixed-unverified | #447 | | |
| F-175 | Troubleshooting docs use wrong label selector | tunnel | review:tunnel | S4 | fixed-unverified | #468 | | |
| F-176 | Specs file misses sections, lists stale dependency | tunnel | review:tunnel | S4 | fixed-unverified | #468 | | |
| F-177 | FRP config server address and port names not escaped | tunnel | review:tunnel | S3 | fixed-unverified | #508 | | |
| F-178 | Tailscale tags not applied at device registration | tunnel | review:tunnel | S3 | fixed-unverified | #508 | | |
| F-179 | Connected session cut on game-pod Ready | sentinel | review:sentinel | S3 | fixed-unverified | #446 | | |
| F-180 | TCP listen error not logged while waiting for async UDP error | sentinel | review:sentinel | S3 | fixed-unverified | #446 | | |
| F-181 | Startup error exits 0 instead of non-zero | sentinel | review:sentinel | S4 | closed-already-fixed | #446 | | re-verified in #467 (group 41): sentinel main already exits non-zero via log.Fatalf on a run() error since #446 |
| F-182 | Close errors logged on healthy proxied connections | sentinel | review:sentinel | S4 | fixed-unverified | #467 | | |
| F-183 | Hostport hold-window asymmetry undocumented | sentinel | review:sentinel | S4 | fixed-unverified | #467 | | |
| F-184 | UDP source keying and cooldown packet counting misdocumented | sentinel | review:sentinel | S4 | fixed-unverified | #467 | | |
| F-185 | Four specs.md statements contradict code | sentinel | review:sentinel | S4 | fixed-unverified | #467 | | |
| F-186 | Dependencies, tests and doc references don't resolve | sentinel | review:sentinel | S4 | fixed-unverified | #467 | | |
| F-187 | Retained captures evict pod when passing 1 GiB total | capture-sidecar | review:capture-sidecar | S3 | fixed-unverified | #483 | | |
| F-188 | Environment variables ignored; works by flag-default coincidence | capture-sidecar | review:capture-sidecar | S4 | fixed-unverified | #487 | | |
| F-189 | Specs.md lists 4 endpoints but code has 6; delete endpoint undocumented | capture-sidecar | review:capture-sidecar | S4 | fixed-unverified | #487 | | |
| F-190 | Four specs.md contradictions | capture-sidecar | review:capture-sidecar | S4 | fixed-unverified | #487 | | |
| F-191 | Gopacket dependency drift: fork vs upstream, version mismatch | capture-sidecar | review:capture-sidecar | S4 | fixed-unverified | #487 | | |
| F-192 | 409 error message names wrong capture on status-poll failure | capture-sidecar | review:capture-sidecar | S4 | fixed-unverified | #487 | | |
| F-193 | /healthz route requires mTLS despite comments calling it unauthenticated | capture-sidecar | review:capture-sidecar | S4 | fixed-unverified | #487 | | |
| F-194 | Record lost after peer closes connection | audit-syslog-bridge | review:audit-syslog-bridge | S3 | fixed-unverified | #507 | | |
| F-195 | specs.md references wrong files for webhook sender | audit-syslog-bridge | review:audit-syslog-bridge | S4 | fixed-unverified | #466 | | |
| F-196 | Untested reconnect and write deadline in specs.md | audit-syslog-bridge | review:audit-syslog-bridge | S4 | fixed-unverified | #466 | | |
| F-197 | specs.md says Go 1.25 but go.mod is 1.26 | audit-syslog-bridge | review:audit-syslog-bridge | S4 | fixed-unverified | #486 | | |
| F-198 | RFC 5424 APP-NAME validation missing | audit-syslog-bridge | review:audit-syslog-bridge | S4 | fixed-unverified | #466 | | |
| F-199 | Body read has no time deadline | audit-syslog-bridge | review:audit-syslog-bridge | S4 | fixed-unverified | #507 | | |
| F-200 | specs.md says Go 1.25 but go.mod is 1.26 | telemetry-receiver | review:telemetry-receiver | S4 | fixed-unverified | #486 | | |
| F-201 | specs.md claims untested HTTP method guards | telemetry-receiver | review:telemetry-receiver | S4 | fixed-unverified | #492 | | |
| F-202 | Missing required fields accepted without validation | telemetry-receiver | review:telemetry-receiver | S4 | fixed-unverified | #501 | | |
| F-203 | Body size limit and trailing content not validated | telemetry-receiver | review:telemetry-receiver | S4 | fixed-unverified | #501 | | |
| F-204 | Pod logs truncation keeps old end instead of newest | mcp-server | review:mcp-server | S3 | fixed-unverified | #439 | | |
| F-205 | Examples use nonexistent label keys | mcp-server | review:mcp-server | S4 | fixed-unverified | #493 | | |
| F-206 | Malformed labelSelector returns full list silently | mcp-server | review:mcp-server | S4 | fixed-unverified | #493 | | |
| F-207 | Docs claim 7 CRDs but 9 exist | mcp-server | review:mcp-server | S4 | fixed-unverified | #493 | | |
| F-208 | specs.md dependency versions stale | mcp-server | review:mcp-server | S4 | fixed-unverified | #493 | | |
| F-209 | Chart comment references nonexistent file path | mcp-server | review:mcp-server | S4 | fixed-unverified | #493 | | |
| F-210 | README example fails on typical Linux host | mcp-server | review:mcp-server | S4 | fixed-unverified | #493 | | |
| F-211 | Package boundary read-only guarantee overstated | mcp-server | review:mcp-server | S4 | fixed-unverified | #500 | | |
| F-212 | Namespace deleted on helm uninstall, losing GameServers and volumes | charts/gameplane/ | review:charts/gameplane | S1 | fixed-unverified | #425 | | |
| F-213 | Pre-upgrade hook fails for non-default release name | charts/gameplane/ | review:charts/gameplane | S3 | fixed-unverified | #443 | | |
| F-214 | helm upgrade --reuse-values fails with nil-pointer errors on new keys | charts/gameplane/ | review:charts/gameplane | S3 | fixed-unverified | #443 | | |
| F-215 | Backup and Restore Job pods can't reach restic repository | charts/gameplane/ | review:charts/gameplane | S2 | fixed-unverified | #432 | | |
| F-216 | PodMonitor scrape for agent metrics always down (TLS mismatch) | charts/gameplane/ | review:charts/gameplane | S3 | fixed-unverified | #476 | | |
| F-217 | Telemetry receiver /metrics endpoint unreachable (NetworkPolicy) | charts/gameplane/ | review:charts/gameplane | S3 | fixed-unverified | #476 | | |
| F-218 | CRD schema not updated on reinstall (pre-upgrade hook is upgrade-only) | charts/gameplane/ | review:charts/gameplane | S3 | fixed-unverified | #443 | | |
| F-219 | Default module catalog omits 14 spec-015 modules | charts/gameplane/ | review:charts/gameplane | S3 | open | | | |
| F-220 | CRD doc contradicts upgrade procedure | charts/gameplane/ | review:charts/gameplane | S4 | fixed-unverified | #485 | | |
| F-221 | Capture buffer default (5 GiB) too large for 1 GiB emptyDir | charts/gameplane/ | review:charts/gameplane | S4 | fixed-unverified | #485 | | |
| F-222 | Module source git.ref doc says main, but values pins v0.2.0-beta.6 | charts/gameplane/ | review:charts/gameplane | S4 | open | | | |
| F-223 | OIDC displayName config key missing from chart | charts/gameplane/ | review:charts/gameplane | S4 | fixed-unverified | #485 | | |
| F-224 | S3 region doc says path-style, code uses virtual-hosted | charts/gameplane/ | review:charts/gameplane | S4 | fixed-unverified | #485 | | |
| F-225 | podSecurity.enforceRestricted doc promises per-pod opt-in that doesn't exist | charts/gameplane/ | review:charts/gameplane | S4 | fixed-unverified | #485 | | |
| F-226 | Image list doc incomplete (12 images, lists 3) | charts/gameplane/ | review:charts/gameplane | S4 | fixed-unverified | #485 | | |
| F-227 | Pre-auth /metrics endpoint exposed through public ingress | charts/gameplane/ | review:charts/gameplane | S3 | fixed-unverified | #461 | | |
| F-228 | insecure: true means plain HTTP, not just skip verification | charts/gameplane/ | review:charts/gameplane | S3 | fixed-unverified | #512 | | |
| F-229 | Empty groupsClaim doc says disabled; code defaults to 'groups' | charts/gameplane/ | review:charts/gameplane | S4 | fixed-unverified | #503 | | |
| F-230 | Bring-your-own CA Secret path fails at install, unused spec fields | charts/gameplane/ | review:charts/gameplane | S3 | fixed-unverified | #514 | | |
| F-231 | NetworkPolicy allows all private ports 443/6443; anti-SSRF hole | charts/gameplane/ | review:charts/gameplane | S3 | fixed-unverified | #514 | | |
| F-232 | dev-up re-run targets wrong kubectl context | deploy/ | review:deploy | S3 | fixed-unverified | #452 | | |
| F-233 | e2e.sh comment/message lists wrong image names | deploy/ | review:deploy | S4 | fixed-unverified | #478 | | |
| F-234 | Docker registry container name conflict blocks bootstrap if stopped manually | deploy/ | review:deploy | S3 | fixed-unverified | #452 | | |
| F-235 | ingress-nginx manifest fetched from floating main branch, not pinned | deploy/ | review:deploy | S4 | fixed-unverified | #506 | | |
| F-236 | Unicode punctuation stripped from anchor slug | hack/ | review:hack | S4 | fixed-unverified | #478 | | |
| F-237 | CLAUDE.md says dev-load rebuilds when it only loads | hack/ | review:hack | S4 | fixed-unverified | #452 | | |
| F-238 | capture-sidecar-setcap-proof omitted from report tally | .github/workflows/ | review:github-workflows | S3 | fixed-unverified | #450 | | |
| F-239 | publish-edge paths miss input dependencies | .github/workflows/ | review:github-workflows | S3 | fixed-unverified | #450 | | |
| F-240 | doc gates skip checks when appVersion or renamed docs change | .github/workflows/ | review:github-workflows | S3 | fixed-unverified | #450 | | |
| F-241 | .golangci.yml edit skips lint job | .github/workflows/ | review:github-workflows | S3 | fixed-unverified | #450 | | |
| F-242 | ratelimit bucket missing from report bucketSet | .github/workflows/ | review:github-workflows | S4 | fixed-unverified | #450 | | |
| F-243 | coverage report posts hard-coded success | .github/workflows/ | review:github-workflows | S4 | fixed-unverified | #450 | | |
| F-244 | gp-module missing from dependabot gomod | .github/workflows/ | review:github-workflows | S3 | fixed-unverified | #450 | | |
| F-245 | actionlint doc command uses wrong extension glob | .github/workflows/ | review:github-workflows | S4 | fixed-unverified | #494 | | |
| F-246 | Image push precedes signature check, leaving unsigned window | .github/workflows/ | review:github-workflows | S3 | fixed-unverified | #462 | | |
| F-247 | Signing-secret confinement relies on convention only | .github/workflows/ | review:github-workflows | S3 | fixed-unverified | #462 | | |
| F-248 | DELETE /clusters deletes arbitrary control-plane Secret | api/ | review:api | S3 | fixed-unverified | #463 | | |
| F-249 | roles.update can strip users:manage with no last-user-manager guard | api/ | review:api | S3 | fixed-unverified | #469 | | |
| F-250 | docs/security.md misstates game container privilege posture | docs/ | review:api | S4 | fixed-unverified | #503 | | |
| F-251 | nginx.conf.template missing client_max_body_size | web/ | review:web | S2 | fixed-unverified | #426 | | |
| F-252 | docs/oidc.md gives clientSecretRef as plain string | charts/gameplane/ | review:charts/gameplane | S3 | fixed-unverified | #485 | | |
| F-253 | README.md / plan.md say "Go 1.25" vs go.mod's 1.26 requirement | root docs | review:root-docs | S4 | fixed-unverified | #486 | | |
| F-254 | sentinel/sentinel untracked binary, no .gitignore entry | sentinel/ | review:sentinel | S4 | fixed-unverified | #467 | | |
| F-255 | make dev-load loads 4 of 12 built images | deploy/ | review:deploy | S3 | fixed-unverified | #452 | | |
| F-256 | Owner-only server operations open to every namespace servers:write holder | api/ | maintainer:HQ-001 | S2 | fixed-unverified | #460 | | S2, not S1: docs/security.md:133-134, the servers:write catalog label and the dashboard hint document this as allowed; HQ-001 (2026-09-24) rules it unintended. Maintainer may raise to S1 |
| F-257 | Release image job times out building the multi-arch operator image, so an RC publishes no operator image, chart or GitHub release | .github/workflows/ | review:.github/workflows | S2 | fixed-unverified | #435 | | seen on the `v0.3.0-rc.1` release run (T014); ID after F-255 assumes F-256 is taken in the held list, so check on the devbox |
| F-258 | A Failed Module rewrites its status twice on every reconcile and re-triggers itself through its own watch | operator | review:operator | S4 | fixed-unverified | #445 | | seen while fixing CI on a hardening PR: an envtest update to a Failed Module lost every RetryOnConflict attempt; same caveat on the ID as F-257 |
| F-259 | Capture download returns 409 right after a user stop although the capture reads Completed | api | ci:e2e | S3 | fixed-unverified | #449, #453 | | seen as sporadic arm64 e2e failures on unrelated PRs (#441); same caveat on the ID as F-257 |
| F-260 | GameServer.Stopped and Restore.Resuming phases declared but never assigned | operator | review:operator | S4 | open | | | |
| F-261 | Capture files deleted through the API keep counting against the sidecar volume budget until the pod restarts | capture-sidecar, api | review:#483 | S3 | fixed-unverified | #487 | | follow-up to F-187 (#483) |
| F-262 | playit tunnel NetworkPolicy adds no egress ports although its comment says all ports are permitted | operator | review:#468 | S3 | fixed-unverified | #488 | | |
| F-263 | Servers page lists only `gameplane-games`; servers in extra namespaces are hidden unless the viewer owns or collaborates on them | web, api | review:OD-021 item 14 | S3 | fixed-unverified | #498 | | blocks procedures/web.md servers-filter-by-namespace |
| F-264 | Dependabot never bumps the action pins inside `.github/actions/`, and they already trail the workflows | .github/actions/ | review:.github/actions | S3 | open | | | |
| F-265 | docker-bake.hcl header names `e2e-images` as its caller; bake runs in `build-e2e-images` | .github/actions/ | review:.github/actions | S4 | open | | | |
| F-266 | steam-install.sh reports a failed update as success once the game is installed, and never retries it | images/ | review:images | S3 | open | | | |
| F-267 | images/README.md Dockerfile template runs `chmod` as the non-root user and fails to build | images/ | review:images | S3 | open | | | |
| F-268 | Documented local `docker run -v game-data:/data` gives a root-owned volume the game user can't write | images/ | review:images | S4 | open | | | |
| F-269 | images/README.md promises exponential backoff; steam-install.sh uses a fixed delay | images/ | review:images | S4 | open | | | |
| F-270 | images/README.md trigger list omits `pull_request`, and its manual `docker build -f Dockerfile` commands fail from the repo root | images/ | review:images | S4 | open | | | |
| F-271 | CI `dump-cluster-state` redaction covered only a fixed set of credential key names | .github/ | review:.github/actions | S3 | fixed-unverified | #545 | | held until #545 merged (OD-019); distinct from F-006/F-013, which fixed quoted/JSON values |
| F-272 | CI `dump-cluster-state` interpolated action inputs directly into `run:` scripts | .github/ | review:.github/actions | S4 | fixed-unverified | #545 | | held until #545 merged (OD-019); no current caller passed untrusted input |
| F-273 | `/events` opened every watch with an empty `resourceVersion`, so each existing object replayed as a synthetic ADDED | api, web | live:sweep-2026-10-04 | S3 | fixed-unverified | #548 | | S3: degraded (request flood, fake notifications), dashboard still usable. live sweep, no B-number recorded in the PR text |
| F-274 | Server header showed the server's age after a pod restart because `status.startedAt` was set only once | operator | live:sweep-2026-10-04 | S4 | fixed-unverified | #548 | | S4: wrong display value only. live sweep, no B-number recorded in the PR text |
| F-275 | Successful sign-ins were audited as `anonymous`, so Recent activity showed every attempt as "anonymous created" | api, web | live:sweep-2026-10-04 | S3 | fixed-unverified | #548 | | S3: audit trail degraded, no data loss. live sweep, no B-number recorded in the PR text |
| F-276 | Servers page "Stopped" tab includes Failed servers | web | live:sweep-2026-10-04 | S4 | not-a-defect | | | Raised in the sweep and left unchanged by #548: `web/src/lib/servers.ts:15-16` documents and tests it as intentional. live sweep, no B-number recorded in the PR text |
| F-277 | Agent temp-file writes (mode 0600) left uploaded files and mods unreadable by the game container | agent | live:sweep-2026-10-04 | S2 | fixed-unverified | #549 | | B1. S2: uploads and mods unusable by the game process; no workaround is stated in the PR (maintainer may lower to S3). A regression from 02e5c74b |
| F-278 | Logs tab showed no recent game log history when opened | web, api, agent | live:sweep-2026-10-04 | S3 | fixed-unverified | #549 | | B10. S3: live logs worked, history was missing |
| F-279 | Backup/restore Jobs ran without the template fsGroup and could retry or hang with no bound | operator | live:sweep-2026-10-04 | S2 | fixed-unverified | #549 | | B2, B5 (the PR text does not say which symptom is which). S2: backups/restores failing or stuck in flight forever (OD-027 covers the provisional limits) |
| F-280 | Deleting a BackupSchedule cascade-deleted its backups | operator | live:sweep-2026-10-04 | S1 | fixed-unverified | #549 | | B6. S1 by R3: data loss (backups removed with their schedule) |
| F-281 | World wipe left the server stopped | operator | live:sweep-2026-10-04 | S3 | fixed-unverified | #549 | | B8. S3: server can be started by hand |
| F-282 | Operator logged expected races as reconciler errors | operator | live:sweep-2026-10-04 | S4 | fixed-unverified | #549 | | B20. S4: log noise |
| F-283 | Unbounded concurrent argon2 hashing let a login burst OOM-kill the API; user timestamps were not RFC 3339 | api | live:sweep-2026-10-04 | S2 | fixed-unverified | #549 | | B4, B12 (one finding; the PR text does not map each to a symptom). S2 by R3: API crash under a login burst, no stated workaround; the timestamp part alone is S4 |
| F-284 | Revoked share links did not show the Revoked chip | web | live:sweep-2026-10-04 | S4 | fixed-unverified | #549 | | B7. S4: cosmetic; design `xCJlu` already specifies the chip |
| F-285 | Cluster page did not show per-node pod usage | web, api, charts/gameplane/ | live:sweep-2026-10-04 | S3 | fixed-unverified | #549 | | B15. S3: missing information, degraded view. Fix widens the `<release>-api-read` ClusterRole (pod list in all namespaces); the maintainer accepted it 2026-10-05 ([OD-028](../OPEN-DECISIONS.md#od-028-api-cluster-wide-pod-list-b15--resolved-2026-10-05)); docs/security.md "API cluster-wide pod list" |
| F-286 | `instanceName` was required in AdminSettings | api, web | live:sweep-2026-10-04 | S4 | fixed-unverified | #549 | | B16. S4: validation strictness |
| F-287 | Event streams in hidden tabs exhausted the browser's 6-connections-per-origin limit | web | live:sweep-2026-10-04 | S3 | fixed-unverified | #549 | | B9. S3: many open tabs starve requests; closing tabs is a workaround |
| F-288 | Assorted small dashboard defects: proxy HTML error pages, game version, transfer audit label, next-backup time, backup repo Secret key, image placeholder | web | live:sweep-2026-10-04 | S4 | fixed-unverified | #549 | | B11. S4: cosmetic/wording, bundled as one commit in the PR |
| F-289 | Terraria and tModLoader servers lack a TCP readiness probe | modules/ | live:sweep-2026-10-04 | S3 | open |  | | B3. Deferred by #549: needs a separate `gameplane-module` PR plus an e2e update. Severity provisional: S3 (details of the symptom are not in the PR text) |
| F-290 | Public share page: "starting up" vs "waking up" copy (item 21c) | web | live:sweep-2026-10-04 | S4 | open |  | | Item 21c. Design-first: needs a Pencil design PR before any code (CLAUDE.md core rule 1). S4: wording |
| F-291 | AdminSettings `instanceName` hint text | web | live:sweep-2026-10-04 | S4 | open |  | | Design-first item deferred by #549 (companion to B16). S4: wording |
| F-292 | Files tab: folder selection and delete (item 18c) | web | live:sweep-2026-10-04 | S3 | open |  | | Item 18c. Design-first: needs a Pencil design PR before any code. Severity provisional (the PR text gives no detail) |
| F-293 | Console history (item 21h) | web | live:sweep-2026-10-04 | S3 | open |  | | Item 21h. Design-first: needs a Pencil design PR before any code. Severity provisional (the PR text gives no detail) |

## Details

### F-001

**Repro / observation**
1. Read `web/src/test/setup.ts` and confirm it aligns `File`/`FormData` with Node's implementations (the fix for the vitest/jsdom `Blob` impl-symbol mismatch documented in the issue's closing comment).
2. Read `web/src/components/modules/UploadModuleDialog.test.tsx` (all 9 cases) and confirm none carry a skip or an inflated timeout as a workaround.
3. Read `web/e2e/specs/keyboardOnly.spec.ts:163` and confirm the ArrowRight/Tab focus assertion is unchanged from before the bump (the regression traced to HeroUI 3.2.5's own `keyboardNavigationBehavior` default, not app code). Then confirm the fix: every `Table.Content` grid passes `keyboardNavigationBehavior="arrow"` explicitly (e.g. `web/src/routes/Servers.tsx:376`; `grep -rn keyboardNavigationBehavior web/src`).
4. Check `web/package.json` carries `jsdom`, `react`/`react-dom`, `@heroui/react` and `@heroui/styles` at or above the versions merged in #406.

**Expected:** the `web` and `web e2e (mock)` CI jobs are green on master with the bumped dependency versions, with no test weakened to get there (Rule 1).

**Actual:** fixed and merged to master via #406 (merge commit `490e873f`); the issue's closing comment cites both jobs `success` on that commit. Not independently re-run in this session; live confirmation is T049's job.

**Evidence:** [issue #414](https://github.com/ValgulNecron/Gameplane/issues/414)

### F-002

**Repro / observation**
1. Read `design-export/MANIFEST.md` and confirm current captures exist for the seven screens named in the issue (Mobile — Servers, Admin Settings — Mod registries, the audit-integrity banner capture, Share page, Server Detail — Modpacks, Server Detail — Capture, Backup detail drawer).
2. Confirm `web/scripts/compare-screenshots.mjs` still carries the scale-normalisation and reference-transparent-pixel handling introduced alongside this residual triage.
3. Re-run (in a session allowed to run CI, not locally, per Rule 8) the `design vs browser visual diff` job and check each listed screen is under the 4% global / 16% 4×4-block thresholds.

**Expected:** all 7 screens pass the visual-diff thresholds.

**Actual:** the issue's closing comment (2026-09-20) reports 109/109 passed on CI run 35528242095, fixed in PR #378. Not independently re-run in this session.

**Evidence:** [issue #377](https://github.com/ValgulNecron/Gameplane/issues/377)

### F-003

**Repro / observation**
1. Read `design-export/MANIFEST.md:495` and confirm the `DMnEi` node is labeled `Gameplane/Dialog/Add Module Source`, not `Backup List Item`.
2. Read `design-export/MANIFEST.md:1240` and confirm `J5pjJ3` was split into a canonical Assigned-state frame plus separate sibling frames for the four alternate address-assignment states.
3. Check the chip/badge components named in the issue (`XL5ZU`, `vStkb`, `uw0dB`, `R65Xyx`, `Rwnu3`, `BV5ei`) via `design-export/json/<id>.json` or a live screenshot compare, and confirm they render at HeroUI's Chip size rather than the earlier oversized LG export.

**Expected:** design exports match the shipped product, per the maintainer's 2026-09-13 ruling that implementation wins on all three items.

**Actual:** the issue's closing comment (2026-09-20) confirms all four parts (including a fourth item found during triage) against master `b2eb5d9c`, fixed in PR #378. Not independently re-run in this session.

**Evidence:** [issue #376](https://github.com/ValgulNecron/Gameplane/issues/376)

### F-004

**Repro / observation**
1. Run `grep -rn "page.route(" web/e2e` and confirm any remaining matches target endpoints MSW does not handle, rather than endpoints it would shadow.
2. Read `web/src/test/handlers.ts` and confirm the cookie-variant handlers named in the issue exist (`e2e_admin_config_variant`, `e2e_admin_config_put_variant`, `e2e_audit_verify_variant`), alongside the pre-existing `e2e_server_variant`.
3. Read `web/e2e/screenshots/slice4.spec.ts` and confirm the five captures named in the issue (nNGDX, BV5ei, QgW58, kIxaJ, dxdEi) select their MSW response via a handler-variant cookie rather than a `page.route()` override. The kIxaJ capture now runs under id `m1hP1j` (`slice4.spec.ts:523`), since #378 replaced the composite kIxaJ frame with the bare banner.

**Expected:** every `web/e2e/` spec that needs a non-default MSW response selects it via a handler-variant cookie, since `page.route()` cannot override a request the mock-target service worker already answers.

**Actual:** fixed in PR #378, whose description states it closes this issue. Not independently re-run in this session.

**Evidence:** [issue #375](https://github.com/ValgulNecron/Gameplane/issues/375)

### F-005

**Repro / observation**
1. Read `web/vite.config.ts:59` and confirm the dev-server proxy table includes `"/shares": { target: apiTarget, changeOrigin: true }`.
2. Run (in a session allowed to run CI/E2E, not locally per Rule 8) `web/e2e/specs/live/share-links.spec.ts` and confirm the "resolve it signed out" step no longer times out waiting on the share-page name heading.

**Expected:** the anonymous share page resolves `/shares/<token>` through the dev proxy to the API, instead of falling through to the SPA's `index.html`; production is same-origin and was never affected.

**Actual:** fixed by commit `f6750cf2` within PR #371; the issue's closing comment confirms `web/vite.config.ts:59` carries the rule on master and both live-e2e architectures pass. Not independently re-run in this session.

**Evidence:** [issue #373](https://github.com/ValgulNecron/Gameplane/issues/373)

### F-006

Security finding (secret handling / CI log exposure) — fixed, so not held under OD-019.

Control: the `redact()` helper, inlined at six sites in `.github/actions/dump-cluster-state/action.yml` (current lines 40, 63, 81, 95, 109, 124), masks credential-shaped values in collected container logs before they reach the CI job log, which is world-readable on this public repository.

Correct behaviour: the delimiter class between a credential key and its value tolerates an optional surrounding quote, so a quoted or JSON-embedded value is masked the same as an unquoted `key: value` / `key=value` pair, while non-secret fields are left intact.

**Repro / observation (defensive — confirms the control is in place; no payload)**
1. Read `.github/actions/dump-cluster-state/action.yml` at each of the six `redact()` sites and confirm the delimiter pattern accepts an optional quote around the `[:=]` separator.
2. Cross-check `specs/done_008-hardened-github-actions/data-model.md` (E5 and the Definition-of-Done row this fix updated) for the documented verification cases: a quoted/JSON credential value masked, and the control-canary plus ordinary `describe`-style lines left untouched.

**Expected:** quoted and JSON-embedded credential values are masked in dump output; non-secret sample lines and the control canary survive.

**Actual:** fixed in PR #327 (imported separately as F-013); the redaction pattern present on master matches the widened form described above. Not independently re-run in this session; live confirmation is T049's job.

**Evidence:** [issue #306](https://github.com/ValgulNecron/Gameplane/issues/306)

### F-007

**Repro / observation**
1. Read `web/nginx.conf.template:118` and confirm it forwards `proxy_set_header Host $http_host;` (not `$host`), so the port survives the dashboard's nginx proxy.
2. Read `web/src/lib/ws.ts:90-102` and confirm `send()` calls made before the socket's first open are queued (bounded) and flushed on open, and that input arriving during a reconnect is dropped rather than replayed.

**Expected:** the console WebSocket upgrade succeeds when the dashboard is served on a non-default port, and input typed before the connection opens is queued and sent once open, rather than throwing or being silently lost.

**Actual:** fixed in PR #416 (merge commit `0566b52a`, source commit `6ea8bd2b`). Not independently re-run in this session.

**Evidence:** [PR #416](https://github.com/ValgulNecron/Gameplane/pull/416)

### F-008

**Repro / observation**
1. Read `web/src/lib/games.ts:54` and confirm catalog tag-filter matching requires every selected tag to be present (`[...wants].every(...)`), not any one of them.
2. Read `web/src/lib/games.test.ts` and `web/src/routes/Modules.test.tsx` for a case selecting two or more tags together.

**Expected:** selecting multiple category tags in the module catalog narrows results to modules matching all selected tags.

**Actual:** fixed in PR #411. Not independently re-run in this session.

**Evidence:** [PR #411](https://github.com/ValgulNecron/Gameplane/pull/411)

### F-009

**Repro / observation**
1. Read `web/src/styles/globals.css:521-522` and confirm the `.tabs__list[data-orientation="vertical"]` rule sets its own rectangular `border-radius`, scoped to vertical orientation, separate from the horizontal pill-shaped tab list.
2. Compare a vertical tab list (e.g. the Server Detail sidebar tabs) in a running install against the design frame.

**Expected:** the vertical tab list renders as a rounded rectangle, not stretched into an oval.

**Actual:** fixed in PR #410. Not independently re-run in this session.

**Evidence:** [PR #410](https://github.com/ValgulNecron/Gameplane/pull/410)

### F-010

**Repro / observation**
1. Read `web/src/routes/tabs/ConsoleShell.tsx:69` and confirm the terminal host's flex wrapper carries `flex-1 min-h-0 p-4` (not `flex-1 p-4`).
2. Open a server console in a running install and observe the terminal's row/pixel height over a few minutes of output; it should stabilise rather than climb (the fix commit records a live measurement of 193→229 rows / 3281→3893px inside a ~1005px viewport before the fix).

**Expected:** the terminal's `ResizeObserver`-driven `fit()` is bounded by the wrapper's flex-allocated height and stops growing.

**Actual:** fixed in PR #409 (merge commit `ba4740b3`, source commit `06db5627`). Not independently re-run in this session; live confirmation is T049's job.

**Evidence:** [PR #409](https://github.com/ValgulNecron/Gameplane/pull/409)

### F-011

**Repro / observation**
1. Read `web/src/routes/tabs/settings/ShareLinks.tsx:103` (`presetDaysToExpiresAt`) and confirm a preset expiry resolves to 23:59:59.999 local on the target calendar date, the same rule `customDateToExpiresAt` applies to a manually chosen date (per `specs/done_017-share-link-expiry/OPEN-DECISIONS.md`).
2. Create a share link with a preset expiry (e.g. "30 days") in a running install and confirm its listed expiry timestamp is end-of-day on the target date, not the current time-of-day N days out.

**Expected:** preset and custom share-link expiries both land at end of the target calendar day.

**Actual:** fixed in PR #396. Not independently re-run in this session.

**Evidence:** [PR #396](https://github.com/ValgulNecron/Gameplane/pull/396)

### F-012

Security finding (authorization/RBAC, secret/credential handling) — fixed, so not held under OD-019. Imported by title and file list only, per this task's scope (PR body not read). Not a single tracked issue: the PR bundles several hardening changes. T049 decides whether this splits into narrower findings or needs further live verification.

Controls touched, per the file list: request authorization (`api/internal/rbac/rbac.go`, `api/internal/rbac/middleware_test.go`), share-link and tunnel-credential handling (`api/internal/handlers/shares.go`, `api/internal/db/shares.go`, migration `api/internal/db/migrations/009_share_links_cluster.sql`, `api/internal/handlers/tunnelcreds.go`), GameServer spec validation in the API on create/update/clone (`api/internal/handlers/resources.go`, `api/internal/handlers/lifecycle.go`), GameServer reconciliation input validation (`operator/internal/controller/gameserver_controller.go`, `gameserver_tunnel.go`), ingress NetworkPolicies (`charts/gameplane/templates/audit-syslog-bridge.yaml`, `charts/gameplane/templates/telemetry-receiver.yaml`) and base-image digest pinning (`images/common/steamcmd/Dockerfile`). The six `SECURITY_AUDIT.md` items this PR fixed are imported separately as F-014 to F-019.

**Repro / observation (defensive — confirms each control is present; no misuse walkthrough)**
1. Confirm `api/internal/rbac/rbac.go` still defines its role/ownership decision functions (exported `Middleware` at line 71; unexported `ownershipRole` at line 352 and `allow` at line 387) and that `api/internal/rbac/middleware_test.go` exercises them.
2. Confirm `api/internal/handlers/shares.go` and `api/internal/handlers/tunnelcreds.go` carry their paired test files, including `resources_security_test.go` (added by this PR) and `tunnelcreds_test.go` (extended by it; the file already existed).
3. Confirm `operator/internal/controller/gameserver_controller.go` and its `gameserver_security_test.go` (added by this PR) exist. Its tests (`TestValidateServerEnvSecrets`, `TestReconcileTunnel_CredentialsSecretOwnership`) are plain unit tests on the controller-runtime fake client, not envtest.

**Expected:** authorization checks, share/tunnel credential handling, and GameServer reconciler input validation all match the hardened behaviour this PR introduced, backed by the tests it added.

**Actual:** fixed (merged) as PR #350. Not independently re-run in this session; live confirmation is T049's job.

**Evidence:** [PR #350](https://github.com/ValgulNecron/Gameplane/pull/350)

### F-013

Security finding (secret handling / CI log exposure) — fixed, so not held under OD-019. This is the fix for F-006; see F-006 for the control description.

**Repro / observation (defensive — confirms the control is in place; no payload)**
1. Read `.github/actions/dump-cluster-state/action.yml` at each `redact()` site (current lines 40, 63, 81, 95, 109, 124) and confirm the delimiter pattern matches an optional quote around the `[:=]` separator.
2. Read `specs/done_008-hardened-github-actions/data-model.md` (E5 and the Definition-of-Done row this PR updated) for the documented verification cases.

**Expected:** quoted/JSON-embedded credential values are masked; non-secret lines and the control-canary marker survive.

**Actual:** merged as PR #327, closing issue #306 (F-006). Not independently re-run in this session; live confirmation is T049's job.

**Evidence:** [PR #327](https://github.com/ValgulNecron/Gameplane/pull/327)

### F-014

Security finding (authorization / multi-cluster isolation) — fixed, so not held under OD-019.

Control: a share link is bound to the cluster it was created on. `CreateShareLink`, `ListShareLinks` and `RevokeShareLink` (`api/internal/handlers/shares.go`, `api/internal/db/shares.go`) require a matching `cluster` column (added by migration `api/internal/db/migrations/009_share_links_cluster.sql`), and `resolveShareHandler` / `startShareHandler` resolve to the cluster recorded on the link rather than the caller's request-time cluster context.

**Repro / observation (defensive — confirms the control is in place; no misuse walkthrough)**
1. Read `api/internal/db/migrations/009_share_links_cluster.sql` and confirm the `share_links` table carries a `cluster` column.
2. Read `api/internal/handlers/shares.go` and confirm `ListShareLinks` and `RevokeShareLink` filter/require the caller's active cluster to match the link's stored `cluster`, and that resolve/start handlers use the link's own `cluster` value to pick the target, not the request's.
3. Cross-check `TestShareLinks_ClusterScoping` in `api/internal/db/shares_test.go` (line 588) and `TestShareClusterScoping` in `api/internal/handlers/shares_test.go` (line 591).

**Expected:** a share link created for a server in one cluster resolves, starts, lists and revokes only against that same cluster, even when a same-named server exists in another cluster the caller can also reach.

**Actual:** fixed together with F-012 in PR #350 (source commit `35693836`, "cluster-bind share links and enforce cluster scoping on resolve and management"). Confirmed present on master at commit `13a859ff`. Not independently re-run in this session; live confirmation is T049's job.

**Evidence:** [PR #350](https://github.com/ValgulNecron/Gameplane/pull/350)

### F-015

Security finding (secret/config handling — cross-tenant object access) — fixed, so not held under OD-019.

Control: a `GameServer`'s `spec.env[*].valueFrom.secretKeyRef` / `configMapKeyRef` may only target a Secret or ConfigMap that the API or operator can prove that GameServer owns. `validateAndProtectGameServer` (`api/internal/handlers/resources.go`) and `validateServerEnvSources` (`operator/internal/controller/gameserver_controller.go`) both require an `OwnerReference` of kind `GameServer` on the referenced object matching the GameServer's name and UID; a name-suffix or label match alone is rejected.

**Repro / observation (defensive — confirms the control is in place; no misuse walkthrough)**
1. Read `api/internal/handlers/resources.go` at `isServerOwnedSecret` (line 564) and `isServerOwnedConfigMap` (line 593), called from `validateAndProtectGameServer` at lines 521 and 536, and confirm they inspect `OwnerReferences`, not name suffixes or labels.
2. Read `operator/internal/controller/gameserver_controller.go:1996` (`validateServerEnvSources`) and confirm it is invoked from the reconcile path (`gameserver_controller.go:1365`) before StatefulSet reconciliation, so a GameServer created directly as a CR (bypassing the API) is still checked.
3. Cross-check `api/internal/handlers/resources_security_test.go` and `operator/internal/controller/gameserver_security_test.go`.

**Expected:** a non-admin GameServer whose `spec.env` references a Secret or ConfigMap without an `OwnerReference` (kind `GameServer`, matching name and UID) to that GameServer is rejected by the API, and is never mounted by the operator even if created directly as a CR.

**Actual:** fixed together with F-012 in PR #350 (source commit `85fd4e4c`, "enforce secret ownership for env vars and tunnel credentials"). Confirmed present on master at commit `13a859ff`. Not independently re-run in this session; live confirmation is T049's job.

**Evidence:** [PR #350](https://github.com/ValgulNecron/Gameplane/pull/350)

### F-016

Security finding (network exposure / audit-chain integrity) — fixed, so not held under OD-019.

Control: the optional `gameplane-audit-syslog-bridge` Service accepts ingress on port 8514 only from pods labelled `app.kubernetes.io/name: gameplane-api`, via a `NetworkPolicy` in `charts/gameplane/templates/audit-syslog-bridge.yaml`. This holds when `networkPolicies.enabled=true` or an equivalent CNI policy is active; the bridge's own token check (`AUTH_HEADER`) still applies independently when configured.

**Repro / observation (defensive — confirms the control is in place; no payload)**
1. Read `charts/gameplane/templates/audit-syslog-bridge.yaml` and confirm a `kind: NetworkPolicy` block exists with an ingress rule scoped to `app.kubernetes.io/name: gameplane-api` on the bridge's port.
2. Confirm the policy is rendered whenever the bridge is enabled (not gated behind an easy-to-miss extra flag beyond `networkPolicies.enabled`).

**Expected:** with `networkPolicies.enabled=true`, only API pods can open a connection to the audit-syslog-bridge port; any other in-cluster workload's connection is dropped at the network layer.

**Actual:** fixed together with F-012 in PR #350 (source commit `286bc9e3`, "add ingress NetworkPolicies for audit-syslog-bridge and telemetry-receiver"). Confirmed present on master at commit `13a859ff`. Not independently re-run in this session; live confirmation is T049's job.

**Evidence:** [PR #350](https://github.com/ValgulNecron/Gameplane/pull/350)

### F-017

Security finding (supply chain / image provenance) — fixed, so not held under OD-019.

Control: `images/common/steamcmd/Dockerfile` pins its base image by digest (`FROM steamcmd/steamcmd@sha256:...`) rather than a mutable tag, so an upstream tag move can't silently change the built image.

**Repro / observation (defensive — confirms the control is in place; no payload)**
1. Read `images/common/steamcmd/Dockerfile:42` and confirm the `FROM` line names a `@sha256:` digest, not `:latest` or another mutable tag.
2. Note the digest on master today (`sha256:a3ea6f87...`) differs from the one the fix originally recorded (`sha256:7178bc46...`) — PR #380 (Dependabot) bumped it again afterward. The control being checked is "pinned by digest", not a specific digest value, so this is expected drift, not a regression.

**Expected:** the upstream base image `FROM` line (`images/common/steamcmd/Dockerfile:42`, the only upstream base under `images/`) names an immutable `@sha256:` digest. `images/games/nuclear-option/Dockerfile:20-21` builds on the in-repo steamcmd image through the `STEAMCMD_BASE_IMAGE` build arg, which `.github/workflows/images.yaml` sets to that image's pushed digest on master.

**Actual:** fixed together with F-012 in PR #350 (source commit `7ad8bfe6`, "pin steamcmd base image to immutable digest"); digest subsequently bumped by PR #380. Confirmed present on master at commit `13a859ff`. Not independently re-run in this session; live confirmation is T049's job.

**Evidence:** [PR #350](https://github.com/ValgulNecron/Gameplane/pull/350)

### F-018

Security finding (secret handling — cross-tenant object access) — fixed, so not held under OD-019.

Control: tunnel credential Secret operations require proof that the Secret belongs to the GameServer being acted on. `tunnelcredsHandler.delete` and `.put` (`api/internal/handlers/tunnelcreds.go`) call `isServerOwnedSecretObject`, which checks the Secret's `OwnerReferences` against the GameServer's name and UID and refuses (403/409) otherwise; `validateAndProtectGameServer` (`api/internal/handlers/resources.go`) covers `spec.networking.tunnel.credentialsSecretRef` the same way; `reconcileTunnel` (`operator/internal/controller/gameserver_tunnel.go`) calls `isServerOwnedSecret` before mounting the Secret into the relay Deployment.

**Repro / observation (defensive — confirms the control is in place; no misuse walkthrough)**
1. Read `api/internal/handlers/tunnelcreds.go:130` and `:286` and confirm both the delete and put paths call `isServerOwnedSecretObject` before acting on an existing Secret.
2. Read `operator/internal/controller/gameserver_tunnel.go:321` and confirm `isServerOwnedSecret` gates the volume mount added to the tunnel relay Deployment.
3. Cross-check `api/internal/handlers/tunnelcreds_test.go` and `operator/internal/controller/gameserver_security_test.go`.

**Expected:** deleting or updating tunnel credentials, and mounting them into the relay Deployment, works only on a Secret owned by that GameServer; anything else is refused.

**Actual:** fixed together with F-012 in PR #350 (source commit `85fd4e4c`). Confirmed present on master at commit `13a859ff`. Not independently re-run in this session; live confirmation is T049's job.

**Evidence:** [PR #350](https://github.com/ValgulNecron/Gameplane/pull/350)

### F-019

Security finding (network exposure / telemetry integrity) — fixed, so not held under OD-019.

Control: the optional telemetry-receiver Service accepts ingress on port 8080 only from pods labelled `app.kubernetes.io/name: gameplane-api`, via a `NetworkPolicy` in `charts/gameplane/templates/telemetry-receiver.yaml`, active when `networkPolicies.enabled=true` or an equivalent CNI policy applies.

**Repro / observation (defensive — confirms the control is in place; no payload)**
1. Read `charts/gameplane/templates/telemetry-receiver.yaml` and confirm a `kind: NetworkPolicy` block restricts ingress to `app.kubernetes.io/name: gameplane-api` on the receiver's port.

**Expected:** with `networkPolicies.enabled=true`, only API pods can post telemetry to the receiver.

**Actual:** fixed together with F-012 in PR #350 (source commit `286bc9e3`, same commit as F-016). Confirmed present on master at commit `13a859ff`. Not independently re-run in this session; live confirmation is T049's job.

**Evidence:** [PR #350](https://github.com/ValgulNecron/Gameplane/pull/350)

### F-020

**Repro / observation**
1. Read `specs/002-nuclear-option-ip-pool/spec.md` FR-004 and its "Network Footprint — AMENDED (2026-08-22)" note (line 200) and the live-verification section around line 237-246: the game port was assumed to be UDP 7777 per third-party documentation, but a live server shows UDP 7778 (query) bound and **UDP 7777 not bound**; server logs (`SteamGameServer.LogOnAnonymous`, `Set Advertise Server: True`) indicate player traffic routes through Steam's game-server networking instead.
2. Read `docs/game-coverage.md:23` and confirm the `nuclear-option` row's join-coverage status.

**Expected (per FR-004's own contingency):** if the assumed join port proves wrong or the protocol undocumented, the module is marked `blocked-doc` in `docs/game-coverage.md`, naming the specific artifact needed to unblock it, rather than shipping a broken or fabricated join test.

**Actual:** `docs/game-coverage.md:23` already lists `nuclear-option` as `blocked-doc` with reason "Undocumented proprietary UDP protocol; join handshake format unknown" — exactly the contingency FR-004 called for. No further action needed; this is the spec's fallback working as designed, not a defect.

**Justification:** FR-004 (`specs/002-nuclear-option-ip-pool/spec.md:139`) explicitly pre-authorizes a `blocked-doc` outcome when "the protocol proves undocumented or the port incorrect", and the live finding at `spec.md:237-246` is the trigger condition; `docs/game-coverage.md:23` shows the module already carries that status.

**Evidence:** [../../002-nuclear-option-ip-pool/spec.md](../../002-nuclear-option-ip-pool/spec.md), [../../../docs/game-coverage.md](../../../docs/game-coverage.md)

### F-021

**Repro / observation**
1. Read `specs/012-docs-refresh-and-outreach/OPEN-DECISIONS.md` OD-8 (line 280) and its ruling (line 302): for every CHANGELOG Unreleased entry docs describe, verify whether it shipped in v0.2.0-beta.8; if truly unreleased, qualify the doc mention with "(unreleased; ships in the next release)" rather than moving the CHANGELOG entry.
2. Read `docs/oidc.md:21` and confirm the Helm-seeded role-mapping note carries that qualifier.
3. Read `CHANGELOG.md:38-41` and confirm the entry is still (correctly) under `## [Unreleased]`.

**Expected:** since Helm-seeded OIDC role mappings genuinely have not shipped in a tagged release, `docs/oidc.md`'s mention of the feature is qualified as unreleased, and the CHANGELOG entry stays under Unreleased.

**Actual:** fixed by PR #340 (commit `a6237289`, "docs: qualify unreleased features in install, oidc and security docs"). `docs/oidc.md:21` on master reads "...`--oidc-role-mapping-{admin,operator,viewer}`) (unreleased; ships in the next release). See..." and `CHANGELOG.md` correctly keeps the entry under `## [Unreleased]`. OD-8 is resolved per its own ruling; not independently re-run in this session.

**Evidence:** [PR #340](https://github.com/ValgulNecron/Gameplane/pull/340)

### F-022

**Repro / observation**
1. Read `specs/015-top-steam-game-modules/OPEN-DECISIONS.md` §1 ("`cli` rcon.protocol Semantics", line 7-24): the maintainer ruled `cli` a first-class `spec.rcon.protocol` enum value, with a described relationship to pod-attach, operator `resolveRCON` implications, and web dashboard `rconAvailable` gating — but the document's own framing (line 3) is that everything in it "remain[s] explicitly open for maintainer ruling".
2. Cross-check `web/src/lib/capabilities.ts:20` (`rconAvailable`) and the operator's `resolveRCON` against the description in §1.

**Expected:** the `cli` protocol's semantics, operator behavior and dashboard gating match the description in §1 (the maintainer's Option A ruling).

**Actual:** the implementation described in §1 (agent-driven local console, no minted RCON Secret for `cli` without an explicit password ref, `rconAvailable` excluding `cli`) matches current code at a glance, and §1 itself records the ruling ("The maintainer ruled...", line 14, under "Resolution: Option A Confirmed", line 16); only the file's preamble (line 3) describes its contents as open. Imported for T049 to confirm the implementation against that ruling; not independently re-verified against every call site in this session.

**Evidence:** [../../015-top-steam-game-modules/OPEN-DECISIONS.md#1-cli-rconprotocol-semantics-t005-t019-t025](../../015-top-steam-game-modules/OPEN-DECISIONS.md#1-cli-rconprotocol-semantics-t005-t019-t025)

### F-023

**Repro / observation**
1. Read `specs/015-top-steam-game-modules/OPEN-DECISIONS.md` §2 ("Factorio Protocol Finding", line 28-45): `modules/factorio/template.yaml` ships `rcon.protocol: source`, while `contracts/engine-matrix-contract.md` previously claimed `rcon.protocol: none`. The recommendation is to retain the shipped `source` config "pending final maintainer confirmation".
2. Read `modules/factorio/template.yaml` and confirm it still ships `rcon.protocol: source`.

**Expected:** the Factorio template's RCON protocol setting is confirmed correct by an explicit maintainer ruling before release, resolving the discrepancy with the earlier contract claim.

**Actual:** the shipped template still uses `rcon.protocol: source` as the finding recommends, but the document records no final ruling — still open. Not independently re-verified against `contracts/engine-matrix-contract.md`'s current text in this session.

**Evidence:** [../../015-top-steam-game-modules/OPEN-DECISIONS.md#2-factorio-protocol-finding-t005](../../015-top-steam-game-modules/OPEN-DECISIONS.md#2-factorio-protocol-finding-t005)

### F-024

**Repro / observation**
1. Read `specs/015-top-steam-game-modules/OPEN-DECISIONS.md` §3 ("Project Zomboid Protocol Finding", line 49-65): `modules/project-zomboid/template.yaml` ships `rcon.protocol: source`, while `contracts/engine-matrix-contract.md` claimed stdin-only (`none`). The recommendation is to retain the shipped `source` config "pending final maintainer confirmation".
2. Read `modules/project-zomboid/template.yaml` and confirm it still ships `rcon.protocol: source`.

**Expected:** the Project Zomboid template's RCON protocol setting is confirmed correct by an explicit maintainer ruling before release.

**Actual:** the shipped template still uses `rcon.protocol: source` as the finding recommends, but the document records no final ruling — still open. Not independently re-verified against `contracts/engine-matrix-contract.md`'s current text in this session.

**Evidence:** [../../015-top-steam-game-modules/OPEN-DECISIONS.md#3-project-zomboid-protocol-finding-t005](../../015-top-steam-game-modules/OPEN-DECISIONS.md#3-project-zomboid-protocol-finding-t005)

### F-025

**Repro / observation**
1. Read `specs/015-top-steam-game-modules/OPEN-DECISIONS.md` §5 ("Generic `rest` Wire Contract", line 89-131): specifies the `RESTAdapter` interface, the `txadmin` and `farming-simulator-25` adapters, a generic fallback adapter, and safety properties (1 MiB response cap, timeouts, 15s auth-failure cooldown, loopback-only `InsecureSkipVerify`) for `agent/internal/rcon/rest.go`.
2. Read `agent/internal/rcon/rest.go` and confirm the adapters and safety properties described are present.

**Expected:** the `rest` protocol's wire contract matches what's documented, and the open item is closed with an explicit maintainer ruling before release.

**Actual:** the contract is documented in full implementation detail and appears built (adapter names, endpoints, auth schemes, response caps match the description), but the document carries no closing ruling — still open. Not independently re-verified line-by-line against `agent/internal/rcon/rest.go` in this session.

**Evidence:** [../../015-top-steam-game-modules/OPEN-DECISIONS.md#5-generic-rest-wire-contract-t005-t018-t047-t052](../../015-top-steam-game-modules/OPEN-DECISIONS.md#5-generic-rest-wire-contract-t005-t018-t047-t052)

### F-026

**Repro / observation**
1. Read `hack/check-doc-versions.sh:19` on master (header comment): `#   A version string v?0\.[0-9]+\.[0-9]+-beta\.[0-9]+ passes if:` — documents that the minor and patch components each accept one or more digits (the major is the literal `0`).
2. Read `hack/check-doc-versions.sh:94,103,151` on master: the comment at line 94 repeats the `[0-9]+` form, but the actual `grep -oE`/`grep -nE` patterns at lines 103 and 151 are `'v?0\.[0-9]\.[0-9]-beta\.[0-9]+'` — only the trailing beta-increment digit group has `+`; the minor and patch groups accept exactly one digit each, and both patterns require a `-beta.N` suffix, so a bare `0.3.0` or a `0.3.0-rc.1` never matches.

**Expected:** the implementation matches its own documented pattern (`[0-9]+` on every digit group), so a version like `0.10.0-beta.1` or `0.2.10-beta.1` is recognized the same way `0.2.0-beta.1` is.

**Actual:** on master (commit `13a859ff`), lines 103 and 151 use `[0-9]` (single digit, no `+`) for the minor and patch components and require a `-beta.N` suffix, while the header at line 19 documents `[0-9]+`. Today's `appVersion` (`0.2.0-beta.8`, `charts/gameplane/Chart.yaml:6`) happens to fit the pattern, so the gap is latent until the chart moves to `0.3.0-rc.N` or `0.3.0`: stale version strings in those forms would go unchecked.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/hack/check-doc-versions.sh#L19-L151](https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/hack/check-doc-versions.sh#L19-L151)

### F-027

**Repro / observation**
1. Read `docs/install.md` on master in full and search for any rollback, roll-back, downgrade or "helm rollback" guidance: none is present.

**Expected:** `docs/install.md` documents how an admin rolls back a failed or unwanted upgrade (e.g. `helm rollback`, any manual steps, and caveats about CRDs/data), matching the release's upgrade/rollback surface.

**Actual:** `docs/install.md` on master (commit `13a859ff`) contains no rollback section or mention.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/docs/install.md](https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/docs/install.md)

### F-028

**Repro / observation**
1. Read `charts/gameplane/values.yaml:473` on master: `ref: v0.2.0-beta.6` under `.Values.defaultModuleSource.git` (used only when `defaultModuleSource.type` is `git`; the chart default at line 461 is `oci`), with an adjacent comment explaining the ref must be bumped "only in lockstep with a chart release that was actually tested against the matching module-repo tag."

**Expected:** for a v0.3.0 release, the default module git source ref points at a module-repo tag that was actually validated against v0.3.0, not a beta.6-era ref.

**Actual:** on master (commit `13a859ff`), `charts/gameplane/values.yaml:473` still pins `ref: v0.2.0-beta.6`.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/charts/gameplane/values.yaml#L473](https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/charts/gameplane/values.yaml#L473)

### F-029

**Repro / observation**
1. Read `deploy/kind/upgrade.sh:36` on master: `FROM_VERSION="${GAMEPLANE_UPGRADE_FROM:-0.2.0-beta.5}"`, with a comment explaining beta.5 (not beta.6) is deliberately the baseline because beta.6 was tagged but never published.
2. Read `.github/workflows/ci.yaml:968-970` on master: the `e2e-upgrade` job's `env.GAMEPLANE_UPGRADE_FROM: 0.2.0-beta.5`, with a comment cross-referencing `upgrade.sh`.
3. Read `.claude/agents/ci-triager.md:65` on master: the `e2e-upgrade` bucket description cites `GAMEPLANE_UPGRADE_FROM=0.2.0-beta.5`.

**Expected:** ahead of a v0.3.0 release, the CI upgrade-test baseline is bumped to the previous actually-published release so the test exercises a genuine two-version jump against the current tree, per each file's own stated rationale for why the constant is explicit rather than auto-resolved.

**Actual:** all three files on master (commit `13a859ff`) still hard-code `0.2.0-beta.5` as the upgrade-from baseline.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/deploy/kind/upgrade.sh#L36](https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/deploy/kind/upgrade.sh#L36)

### F-030

**Repro / observation**
1. Read `CLAUDE.md`'s Repository Map on master: the tree lists `netguard/, gameaction/, gameproto/, operator/, api/, agent/, audit-syslog-bridge/, telemetry-receiver/, sentinel/, capture-sidecar/, mcp-server/, svcutil/, tunnel/` plus `web/`, but no `gp-module/` entry; the tree's `go.work` line comment reads "Go workspace linking all 14 Go modules" (line 88), and the Build section repeats "Compile all 14 Go workspace modules" (line 112).
2. Read `go.work` on master: its `use (...)` block lists 15 modules, including `./gp-module`, alongside `agent, api, audit-syslog-bridge, capture-sidecar, gameaction, gameproto, mcp-server, netguard, operator, sentinel, svcutil, telemetry-receiver, test/e2e, tunnel`.

**Expected:** the repository map and module counts in `CLAUDE.md` list every `go.work` member, including `gp-module/`, so the documented count matches `go.work` exactly.

**Actual:** `CLAUDE.md` on master (commit `13a859ff`) omits `gp-module/` from the repository map tree entirely and states "14 Go modules" / "14 Go workspace modules", undercounting the 15-member `go.work` workspace by one.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/CLAUDE.md#L88](https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/CLAUDE.md#L88), [https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/go.work](https://github.com/ValgulNecron/Gameplane/blob/13a859ff7961d8d1f63198c682374d5dd325cb81/go.work)

### F-031

**Repro / observation**
1. Copy the frp example from `docs/tunnels.md:118-139` into `gs.yaml`. It has `spec.template: minecraft-java`.
2. Run `kubectl apply --dry-run=server -f gs.yaml`.
3. The API server rejects it with an `unknown field "spec.template"` error (or pruning the field and requiring `spec.templateRef: Required value` with validation relaxed). The Tailscale (`:166`) and playit (`:213`) examples fail the same way.
4. Offline check: in `charts/gameplane/crds/gameplane.local_gameservers.yaml`, `spec.properties` has no `template`, and `spec.required` is `[templateRef]`.

**Expected:** `spec.templateRef.name: minecraft-java`, as in `docs/networking.md:64-65`.

**Actual:** all three tunnel walkthroughs give a manifest the API server rejects.

**Evidence:** [evidence/review-docs/verification.md#c-docs-03](evidence/review-docs/verification.md#c-docs-03)

### F-032

**Repro / observation**
1. `ls -d modules/*/ | wc -l` returns 30, and every directory has a `module.yaml`. `docs/game-coverage.md:7-36` has 30 rows: 2 `covered-in-ci`, 2 `out-of-scope-by-design` and 26 `blocked-doc`.
2. `grep -n "16" README.md`: lines 71, 85, 142, 178 and 259 each call the catalog 16 templates or games. Lines 178 and 259 describe the repo contents, which are 30 today.
3. `docs/comparison-sources.md:69` says "16 ready-to-use templates in gameplane-module repository" (checked 2026-09-02). On that date gameplane-module already had 17 modules, because nuclear-option was added 2026-08-23.
4. `docs/roadmap.md:262-270` says "See docs/game-coverage.md for the canonical per-module status" then gives 2/**12**/2. The canonical table gives 2/**26**/2.

**Expected:** counts that match `modules/` and `docs/game-coverage.md`: 30 modules, split 2 covered + 26 blocked-doc + 2 out-of-scope.

**Actual:** seven sentences give 16 (or 2/12/2), which is about half of the real catalog.

**Evidence:** [evidence/review-docs/verification.md#c-docs-01](evidence/review-docs/verification.md#c-docs-01)

### F-033

**Repro / observation**
1. `docs/security.md:281-282`: "**Disable capture** — leave the cluster's capture feature disabled via Helm value `capture.enabled: false` (default is true)."
2. `charts/gameplane/values.yaml:526-527`: `capture:` / `enabled: false`, with the comment "Off by default".
3. `charts/gameplane/templates/operator.yaml:283-291`: the operator gets `--capture-enabled=false` unless `.Values.capture.enabled` is true.
4. `docs/install.md:216` and `docs/architecture.md:304` both say the default is false or disabled.

**Expected:** "(default is false)", or no parenthetical at all, since option 1 then just means "keep the default".

**Actual:** the security doc claims the capture feature, whose sidecar needs `allowPrivilegeEscalation: true`, is on by default. That contradicts the chart and two other docs.

**Evidence:** [evidence/review-docs/verification.md#c-docs-02](evidence/review-docs/verification.md#c-docs-02)

### F-034

**Repro / observation**
1. `wc -l CLAUDE.md` gives 311.
2. `docs/comparison-sources.md:31` cites `CLAUDE.md:368`, and `:40` cites `CLAUDE.md:372`. Both are past the end of the file. CLAUDE.md was 828 lines on 2026-09-02 and was trimmed later.
3. `docs/comparison-sources.md:94` cites "CLAUDE.md:9–20 (Repo map)". `sed -n 9,20p CLAUDE.md` is now the Start-of-Session check. `## Repository Map` is at `CLAUDE.md:52`.
4. `docs/comparison-sources.md:40-42` cites `charts/gameplane/values.yaml:58` as "tunnel.enabled configuration toggle". `values.yaml:58` is `sentinelImage`, and `grep "^tunnel" charts/gameplane/values.yaml` finds nothing. Tunnels are per-GameServer (`spec.networking.tunnel.enabled`); the chart only carries `operator.tunnelImages`.

**Expected:** every evidence citation resolves to the claim it supports, preferably by section anchor.

**Actual:** three CLAUDE.md citations and one values.yaml citation no longer point at the evidence. The values.yaml one describes a Helm key that does not exist.

**Evidence:** [evidence/review-docs/verification.md#c-docs-04](evidence/review-docs/verification.md#c-docs-04)

### F-035

**Repro / observation**
1. `docs/dependencies.md:4-7`: "It covers the 9 Go modules that share `go.work` (`netguard`, `gameaction`, `operator`, `api`, `agent`, `audit-syslog-bridge`, `telemetry-receiver`, `mcp-server`, `test/e2e`)".
2. `go.work` lists 15 modules. The six missing are `capture-sidecar`, `gameproto`, `gp-module`, `sentinel`, `svcutil` and `tunnel`.
3. `grep -n -i "capture-sidecar\|gopacket\|go-pcap\|gameproto\|svcutil\|tunnel\|gp-module" docs/dependencies.md` finds nothing. `sentinel` appears only at `:363`, in a version-alignment note.

**Expected:** every `go.work` module is inventoried. Zero-dependency ones get the same "no third-party deps" row that `netguard` and `gameaction` have.

**Actual:** six modules are missing, including the raw packet-capture library (`capture-sidecar`) that runs with `CAP_NET_RAW`.

**Evidence:** [evidence/review-docs/verification.md#c-docs-05](evidence/review-docs/verification.md#c-docs-05)

### F-036

**Repro / observation**
1. `docs/contributing.md:74-83` lists `cd <m> && go test ./...` for `netguard`, `gameaction`, `operator`, `api`, `agent`, `audit-syslog-bridge`, `telemetry-receiver` and `mcp-server`, then `cd web && npm test`.
2. `Makefile:35` `GO_MODULES` also includes `gameproto`, `gp-module`, `sentinel`, `capture-sidecar`, `svcutil` and `tunnel`.

**Expected:** the per-component list covers every Go module in `GO_MODULES`, or points to `make test` as the complete list.

**Actual:** six modules are missing from the per-component list. The aggregate `make test` in the same section does cover them.

**Evidence:** [evidence/review-docs/verification.md#c-docs-06](evidence/review-docs/verification.md#c-docs-06)

### F-037

**Repro / observation**
1. `grep -c "title:" website/src/pages/games.astro` returns 16. `grep -n -i sixteen website/src/pages/games.astro` returns lines 111, 119 and 148.
2. Compare with `ls -d modules/*/`, which gives 30. Missing from the page: ark-survival-evolved, arma-reforger, beammp, euro-truck-simulator-2, farming-simulator-25, fivem, hell-let-loose, left-4-dead-2, mount-and-blade-2-bannerlord, nuclear-option, squad, team-fortress-2, the-isle and tmodloader.
3. Context: the 16 on the page are exactly the gameplane-module `v0.2.0-beta.6` tag, which is the chart default beta.8 installs. The live site is therefore accurate for today's release and becomes wrong when v0.3 ships 30 modules.

**Expected:** at v0.3 publication, the games page lists all 30 shipped modules (or says "N+ games" with correct N), in the same change as the tracked `values.yaml` module-source ref bump.

**Actual:** the website repo's `main` (commit `d84d449`) lists 16 and says "Sixteen game servers ship out of the box".

**Evidence:** [evidence/review-docs/verification.md#c-website-01](evidence/review-docs/verification.md#c-website-01)

### F-038

**Repro / observation**
1. `sed -n 14p website/src/content/docs/comparison.mdx` shows `| Game catalog | 16 official modules; open template format | …`.
2. `ls -d modules/*/ | wc -l` gives 30. As with F-037, 16 is right for the released beta.8 default catalog and wrong for v0.3.

**Expected:** the v0.3 catalog size, updated in the same website change as F-037.

**Actual:** "16 official modules".

**Evidence:** [evidence/review-docs/verification.md#c-website-02](evidence/review-docs/verification.md#c-website-02)

### F-039

**Repro / observation**
1. `gh release list -R ValgulNecron/Gameplane -L 2` shows `v0.2.0-beta.8`, Pre-release, 2026-08-22. `CHANGELOG.md:56` has `## [0.2.0-beta.8] — 2026-08-22`.
2. `grep -rn "VERSION\|beta\.7" website/src` shows the `beta.7` constant and its render sites: `config.ts:13`, `pages/index.astro:14` (homepage install `chartVersion`), `:77-78`, `components/Footer.astro:71`, `content/docs/faq.mdx:24` and `content/docs/roadmap.mdx:13`; also hardcoded `--version 0.2.0-beta.7` in `getting-started.mdx:24`.
3. `git -C website fetch && git -C website show origin/main:src/config.ts | grep VERSION` gives `v0.2.0-beta.7`, so the website repo itself has not moved on.

**Expected:** the site shows and installs the latest published release (beta.8 now, v0.3.0 at release), and the changelog page includes it.

**Actual:** the homepage install command, getting-started command, badge, footer, FAQ and roadmap all say beta.7, and the changelog page stops at beta.7.

**Evidence:** [evidence/review-docs/verification.md#c-website-03](evidence/review-docs/verification.md#c-website-03)

### F-040

**Repro / observation**
1. `test/e2e/gamebot_helpers_e2e_test.go:24-32`: the comment says "These four ... boot quickly". The slice lists 6 games: minecraft-java, terraria, factorio, garrys-mod, tmodloader and beammp.
2. `test/e2e/buckets.sh:187-195`: `bot-fast`, which is what CI runs, has only Minecraft, Terraria and Garry's Mod. Lines `:214`, `:226`, `:230` and `:240` put factorio, tmodloader and beammp tests in `bot-heavy`, with "exceeds runner disk" reasons.
3. `test/e2e/factorio_bot_e2e_test.go:28-29`: "This is a heavy-set test (opt-in via GAMEPLANE_E2E_GAME_BOT=1 and GAMEPLANE_E2E_GAMES=all)". But factorio is in `fastGameSet`.
4. `test/e2e/internal/specs.md:293-298` lists a 3-game fast set. `:345-354` ("Why the fast set is small") lists 4 games including factorio.
5. `specs/015-top-steam-game-modules/engine-matrix-resolved.md:30-31,34,60` classifies tmodloader, beammp and factorio as `bot-fast`. `buckets.sh` does the opposite.

**Expected:** there is one "fast set" definition, or `fastGameSet` is explicitly documented as a different axis from `bot-fast`. Code, docs, `buckets.sh` and spec 015 agree on which games are fast.

**Actual:** there are four conflicting definitions. Behaviorally, `GAMEPLANE_E2E_GAME_BOT=1 make test-e2e-keep` (with `GAMEPLANE_E2E_GAMES` unset) boots all 6 games sequentially. Three of them are games the repo says exceed a CI runner's disk.

**Evidence:** [evidence/review-test-e2e/verification.md#c-e2e-01](evidence/review-test-e2e/verification.md#c-e2e-01)

### F-041

**Repro / observation**
1. `test/e2e/internal/specs.md:307-324`: the per-game depth table has 16 rows (minecraft-java through satisfactory).
2. `test/e2e/internal/specs.md:326` says "All 16 game modules now have implemented protocol clients". `:370` says the hand-run command "runs all 16 games".
3. `ls -d test/e2e/internal/*/ | grep -vE 'fakeoidc|probe|protocol' | wc -l` prints 29. The 13 directories missing from the table are arma-reforger, ark-survival-evolved, beammp, euro-truck-simulator-2, farming-simulator-25, fivem, hell-let-loose, left-4-dead-2, mount-and-blade-2-bannerlord, squad, team-fortress-2, the-isle and tmodloader.
4. `ls -d modules/*/ | wc -l` prints 30 (the 29 above plus nuclear-option).

**Expected:** the reference doc's table and counts match the 29 probe packages, or the doc points to `docs/game-coverage.md` as the current table.

**Actual:** the doc says 16 games and has 16 rows. The repo has 29 probe packages and 30 modules.

**Evidence:** [evidence/review-test-e2e/verification.md#c-e2e-02](evidence/review-test-e2e/verification.md#c-e2e-02)

### F-042

**Repro / observation**
1. `test/e2e/env.go:494-560`: every `envInstance.APIClient(...)` call makes one `POST /auth/login` and does not cache the session.
2. Count the `APIClient(t, adminUsername, adminPassword)` calls in the 7 tests of `bucket_api_roles` (`buckets.sh:139-148`): one each in `api_roles_e2e_test.go:23`, `:124` and `:150`, `api_owner_collab_e2e_test.go:43`, `api_auth_e2e_test.go:415` and `:971`, and `api_theme_preferences_e2e_test.go:144`. That makes **7** e2e-admin logins.
3. `git show 83aefbe9 -- test/e2e/buckets.sh`: before this commit the bucket held 4 tests. The commit says "a bucket that spends 4". After it the bucket spent 5.
4. `git show df463735:test/e2e/buckets.sh` adds OIDCHelmOverride and says "bringing api-roles to 5 (up from the previous 4)". The real total is 6. `api_auth_e2e_test.go:967` repeats "bring the api-roles bucket to 5".
5. `63697d5b` adds ThemePreferences and says "bringing api-roles to 6". The real total is 7.
6. `.claude/skills/e2e-test-authoring/SKILL.md:17` and `:27` say api-roles should stay at "≤ ~5" admin logins. The bucket is already at 7, and `buckets.sh:29-30` and CLAUDE.md both give "~7 admin logins" as the per-job ceiling.

**Expected:** the running tally in `buckets.sh`, the test doc comments and the skill ceiling all match the real count (7), so the next author knows the bucket is at the ~7 ceiling.

**Actual:** `buckets.sh` says 6, `api_auth_e2e_test.go` says 5 and the skill says the ceiling is ~5. Each number is lower than the real count, so an author would think the bucket has room when it does not. (Not failing today because the 429 retry loop absorbs the overflow.)

**Evidence:** [evidence/review-test-e2e/verification.md#c-e2e-03](evidence/review-test-e2e/verification.md#c-e2e-03)

### F-043

**Repro / observation**
1. `gh pr list --repo ValgulNecron/Gameplane --state merged --search "merged:>2026-08-22" --limit 200` lists approximately 60 PRs merged since v0.2.0-beta.8 was tagged on 2026-08-22.
2. `git show origin/master:CHANGELOG.md | sed -n '1,20p'` shows `## [Unreleased]` with no entries for most of those PRs.
3. Spot-check PR titles from step 1 (e.g., "fix: …", "feat: …") against the current `## [Unreleased]` section: most user-facing changes are missing.

**Expected:** `## [Unreleased]` in `CHANGELOG.md` lists all user-facing changes merged to master since v0.2.0-beta.8.

**Actual:** the section is missing entries for approximately 60 PRs, representing features, fixes, docs and design changes merged after the beta.8 tag.

**Evidence:** `git log` and PR history since 2026-08-22

### F-044

**Repro / observation**
1. On a test cluster, create a GameServer from a template that declares quiesce (for example `minecraft-java`), and a restic Backup with `spec.quiesce: true` (the CRD default).
2. While the Backup is `Running`, cut the operator off from the agent: `kubectl -n gameplane-games delete networkpolicy allow-api-to-agent`.
3. When the restic Job succeeds, `mirrorJobStatus` writes `phase: Succeeded`, then `runUnquiesce` fails and returns `RequeueAfter: 30s`.
4. Restore the NetworkPolicy and observe that the requeued pass returns without retrying unquiesce, and the game keeps auto-save off.

**Expected:** The terminal phase is persisted only after the unquiesce lands, as documented in `docs/architecture.md:154-158`.

**Actual:** One failed unquiesce is final, and auto-save stays off until someone runs the game's resume command or restarts the server.

**Evidence:** [evidence/review-operator/verification.md#c-operator-01](evidence/review-operator/verification.md#c-operator-01)

### F-045

**Repro / observation**
1. Dashboard: Server, then Settings, then Backups. Set a schedule and destination and save.
2. `kubectl -n gameplane-games get backupschedule <gs>-auto -o jsonpath='{.spec.quiesce}'` prints `false`.
3. When the schedule fires, `fire()` copies `quiesce: false` into the Backup, and `maybeQuiesce` returns early.

**Expected:** The auto-managed schedule gets the CRD default (`true`).

**Actual:** Every scheduled backup of a dashboard-configured server runs without quiesce.

**Evidence:** [evidence/review-operator/verification.md#c-operator-02](evidence/review-operator/verification.md#c-operator-02)

### F-046

**Repro / observation**
1. Push two versions of one module (for example `1.0.0` and `1.1.0`) to an OCI registry, and point an OCI ModuleSource at it.
2. Install a Module with `spec.version: "1.0.0"`, then observe the next reconcile.
3. On the next reconcile, the convergence check requires `appliedDigest == entry.Digest`, which can never hold because entry.Digest is only the latest version's digest.
4. `kubectl get module <name> -w` flips between Pulling and Ready without stopping.

**Expected:** A pinned install converges once it is applied. The catalog digest describes `LatestVersion` only, so compare it only when `desiredVersion == entry.LatestVersion`.

**Actual:** Continuous pulls and signature checks, and a Module that flaps between Pulling and Ready.

**Evidence:** [evidence/review-operator/verification.md#c-operator-03](evidence/review-operator/verification.md#c-operator-03)

### F-047

**Repro / observation**
1. Create GameServer `mc` from `minecraft-java` and wait for `Running`.
2. `kubectl -n gameplane-games patch gameserver mc --type merge -p '{"spec":{"templateRef":{"name":"<another installed template>"}}}'`.
3. Operator logs show `reconcile StatefulSet` failing with the apiserver's `spec: Forbidden: updates to statefulset spec for fields other than …`.

**Expected:** `templateRef` is immutable at admission or the selector does not depend on it.

**Actual:** The server is wedged, and only operator logs explain why. Reverting `templateRef` recovers it.

**Evidence:** [evidence/review-operator/verification.md#c-operator-04](evidence/review-operator/verification.md#c-operator-04)

### F-048

**Repro / observation**
1. Start a quiesced restic Backup on a server.
2. While it is `Running` with `quiesce-attempted=true`, press Delete in the dashboard's Backups list, or `kubectl delete backup <name>`.
3. The Backup disappears at once and no unquiesce is sent. The game stays with auto-save off.

**Expected:** Deleting a Backup that the operator quiesced releases the game world.

**Actual:** Auto-save can stay off indefinitely. Restarting the server is the workaround.

**Evidence:** [evidence/review-operator/verification.md#c-operator-05](evidence/review-operator/verification.md#c-operator-05)

### F-049

**Repro / observation**
1. Create server `fac` from the `factorio` module. Take a restic Backup at T0.
2. Keep the server running until a save file appears that is not in the T0 snapshot.
3. Create a Restore of the T0 Backup into `fac`. The Job runs `restic restore <id> --target /`, with no `--delete`.
4. After the resume, the Files tab still lists the post-T0 save with its newer mtime, and the server loads it.

**Expected:** After a restore, the data volume matches the snapshot.

**Actual:** The volume is the snapshot plus every file created after it.

**Evidence:** [evidence/review-operator/verification.md#c-operator-06](evidence/review-operator/verification.md#c-operator-06)

### F-050

**Repro / observation**
1. Module `minecraft-java` is `Ready`.
2. `kubectl delete gametemplate minecraft-java`. The API refuses changes to managed templates, but kubectl does not.
3. The `Owns` watch enqueues the Module. The convergence check returns without reading the template.
4. GameServers that reference it go `Failed`.

**Expected:** `operator/specs.md:29`: changes that bypass the CRD "are not supported — the operator's next reconcile will overwrite them".

**Actual:** The owned template is not recreated or reverted while the Module's status fields still match.

**Evidence:** [evidence/review-operator/verification.md#c-operator-07](evidence/review-operator/verification.md#c-operator-07)

### F-051

**Repro / observation**
1. With the chart's `capture.enabled=true`, run a capture on server `mc` until it is `Completed`, with a short retention.
2. Stop `mc` (`spec.suspend: true`). The pod and its `captures` emptyDir are removed.
3. When the TTL elapses, `expireCapture` marks the capture `Expired` and calls `DeleteCaptureFile`. The call fails because `<gs>-agent` has no endpoints. The function returns `RequeueAfter: 30s` at `:710`.
4. When `mc` starts again, the new sidecar answers 404, and the CR is deleted.

**Expected:** `operator/specs.md:293`: file deletion is best-effort, "never blocks the CR delete".

**Actual:** The CR delete waits on the sidecar until it comes back up.

**Evidence:** [evidence/review-operator/verification.md#c-operator-08](evidence/review-operator/verification.md#c-operator-08)

### F-052

**Repro / observation**
1. Create a GameServer from a UDP game (for example `factorio`, port `game` 34197/UDP) with `networking.tunnel: {enabled: true, provider: frp, frp: {serverAddr: <frps>, remotePorts: [{name: game, remotePort: 30000}]}}`.
2. `kubectl -n gameplane-games get deploy <gs>-tunnel -o jsonpath='{.spec.template.spec.containers[0].env}'` shows `BACKING_SERVICE_PORT=game:30000`. No container port, Service port or protocol is passed.
3. Reading `renderFrpConfig`: every entry becomes `type = "tcp"`, `localPort = 30000`, `remotePort = 30000`.
4. The backing Service exposes 34197/UDP, so frpc forwards TCP to `<gs>.<ns>.svc:30000`, where nothing listens.

**Expected:** `docs/tunnels.md:39`: the protocol comes from the template port's `protocol`.

**Actual:** frp works only when `remotePort` equals the Service port and the port is TCP.

**Evidence:** [evidence/review-operator/verification.md#c-operator-09](evidence/review-operator/verification.md#c-operator-09)

### F-053

**Repro / observation**
1. When the feature is off, each function calls `r.Delete` on a fixed name with no cache read and no `IsControlledBy` check.
2. Take a server with no password config fields, no `configFiles`, no `backupPolicy` and no operator-managed RCON. With apiserver audit logging on, every agent heartbeat produces four `DELETE` calls that return 404.
3. If an object with one of those names exists and is not controlled by the GameServer, it is deleted.

**Expected:** The pattern already used in `reconcileNetworkPolicy`: read from the cache, and delete only when `metav1.IsControlledBy`.

**Actual:** Four live, unowned deletes per reconcile.

**Evidence:** [evidence/review-operator/verification.md#c-operator-10](evidence/review-operator/verification.md#c-operator-10)

### F-054

**Repro / observation**
1. The wipe script always exits 0, so the Job always succeeds, and `ackWipe` sets `gameplane.local/wipe-data-completed` whatever `rm` did.
2. Use a StorageClass where the kubelet does not apply `fsGroup` ownership (hostPath-backed PVs) and a game that writes as another uid. Its subdirectories are mode 0755.
3. The Job runs as uid 65532, cannot empty those directories, and discards the `EACCES`.

**Expected:** A wipe that did not remove the data is reported as failed.

**Actual:** Every wipe is acked as done, whatever `rm` actually did.

**Evidence:** [evidence/review-operator/verification.md#c-operator-11](evidence/review-operator/verification.md#c-operator-11)

### F-055

**Repro / observation**
1. Enable an frp tunnel on `mc` and set `spec.networking.hostname`. `TunnelHostnameIgnored=True` appears.
2. Remove the hostname, or disable the tunnel. The condition still returns when queried.

**Expected:** Conditions follow the current spec, as other conditions do.

**Actual:** A stale `True` condition with an outdated message stays on status.

**Evidence:** [evidence/review-operator/verification.md#c-operator-12](evidence/review-operator/verification.md#c-operator-12)

### F-056

**Repro / observation**
1. With `--address-manager=metallb`, set `expose: LoadBalancer` and `address: "not-an-ip"`. The CRD admits it.
2. The Service gets `metallb.io/loadBalancerIPs: not-an-ip`, and `AddressAssignment` reports a pending reason, not a format error.
3. `grep -rn "ParseIP\|ParseAddr" operator/internal/controller/*.go` finds only playit host validation.

**Expected:** "The operator parses the value during reconciliation and reports a bad one through the AddressAssignment condition".

**Actual:** No format check exists. The bad value goes straight to the address manager.

**Evidence:** [evidence/review-operator/verification.md#c-operator-13](evidence/review-operator/verification.md#c-operator-13)

### F-057

**Repro / observation**
1. Set `spec.idle.wakeWindows: ["61 * * * *"]`. The CRD pattern only checks for five fields.
2. Let the server fall asleep. `status.idle.reason` reads `asleep; wake window invalid: …`.
3. No condition is added.

**Expected:** "reports an unparseable entry as a failed condition" and "surfaced as a condition".

**Actual:** The error appears only in `status.idle.reason`, and only while the server is asleep.

**Evidence:** [evidence/review-operator/verification.md#c-operator-14](evidence/review-operator/verification.md#c-operator-14)

### F-058

**Repro / observation**
1. `grep -rn "CaptureDefaultRetention\b\|CaptureMaxRetention\b\|CaptureDefaultMaxDurationSeconds\|CaptureDefaultMaxSizeBytes" operator` finds, for `GameServerReconciler`, only the field declarations and the assignments in `main.go:385-388`.
2. The reads at `networkcapture_controller.go:264-268` are `NetworkCaptureReconciler`'s own fields.
3. The doc comments claim behaviour, but `main.go:78-79` says these fields are "unused elsewhere".

**Expected:** No unread configuration fields that carry behavioural doc comments.

**Actual:** Four dead fields with misleading comments.

**Evidence:** [evidence/review-operator/verification.md#c-operator-16](evidence/review-operator/verification.md#c-operator-16)

### F-059

**Repro / observation**
1. `cd operator && go build ./...` succeeds. `capture-sidecar/internal/httpserver/handlers.go:197` registers `DELETE /captures/{id}`, and `operator/internal/agent/sidecar_capture.go:231-268` implements `DeleteCaptureFile`. specs.md says "the production build does not compile" and that no delete route exists.
2. `operator/cmd/main.go:439-440` sets the retention fields, which specs.md says are not wired.
3. `GetCaptureStatus` returns an error when disabled, but specs.md says all methods return nil.
4. Ephemeral-container injection is listed as "planned" in specs.md but is implemented in the code.

**Expected:** `operator/specs.md` describes the current code.

**Actual:** Six passages describe a broken build and unwired features that in fact work.

**Evidence:** [evidence/review-operator/verification.md#c-operator-18](evidence/review-operator/verification.md#c-operator-18)

### F-060

**Repro / observation**
1. Extract the `flag.*Var` names from `main.go` (31) and the `` | `--…` `` rows from the table (24), then `comm` the two lists.
2. Missing from the table: `--sentinel-image`, `--tunnel-frp-image`, `--tunnel-tailscale-image`, `--tunnel-playit-image`, `--metallb-namespace`, `--capture-default-max-duration-seconds`, `--capture-default-max-size-bytes`.
3. `main.go:224` defaults `--address-manager` from `GAMEPLANE_ADDRESS_MANAGER`, and `:247` defaults `--game-data-storage-class` from `GAMEPLANE_GAME_DATA_STORAGE_CLASS`. The table mentions neither.

**Expected:** The "CLI flags" table lists every flag and its env default.

**Actual:** 7 of 31 flags are missing, and so are both env defaults.

**Evidence:** [evidence/review-operator/verification.md#c-operator-19](evidence/review-operator/verification.md#c-operator-19)

### F-061

**Repro / observation**
1. specs.md says `Go version: 1.25.0`. `go.mod` says `go 1.26.0`.
2. Compare the table version rows with current `go.mod`: 12 dependency rows are stale.

**Expected:** The versions match `go.mod`.

**Actual:** 12 dependency rows and the Go line are stale. The Go line is also listed as a related site under C-gameaction-04, so fix both together.

**Evidence:** [evidence/review-operator/verification.md#c-operator-20](evidence/review-operator/verification.md#c-operator-20)

### F-062

**Repro / observation**
1. specs.md `:144` says wipe is "delete PVC, recreate". Code runs an `rm` Job against the existing PVC.
2. specs.md `:29` and `:464` describe cleanup finalizers. The only finalizer is on Module. Every other cleanup relies on ownerReference garbage collection.
3. specs.md `:113` and `:218-219` describe a `Resuming` phase. Code clears `suspend` and sets `Succeeded` in the same pass.
4. specs.md `:239` says "Report InstallFailed condition". Failures are reported as `Ready=False` with other reasons.

**Expected:** specs.md matches the code.

**Actual:** At least seven statements contradict it.

**Evidence:** [evidence/review-operator/verification.md#c-operator-21](evidence/review-operator/verification.md#c-operator-21)

### F-063

**Repro / observation**
1. `config/crd/kustomization.yaml` lists 7 of the 9 CRD files in that directory. `gameplane.local_clusters.yaml` and `gameplane.local_networkcaptures.yaml` are missing.
2. The sample GameServer sets `config.TYPE` and `config.VERSION`. The `minecraft-java` `configSchema` has neither, so `materializeConfig` fails the server.
3. The sample ModuleSource uses top-level `url` and `modules`. `spec.type` defaults to `oci`, and the CRD rule rejects the object.
4. The sample Backup sets `repoRef.key: url`, but the operator always reads keys `repo` and `password`.
5. `manager.yaml` passes no agent CA or mTLS flags and mounts nothing.

**Expected:** `operator/specs.md:45-50` presents `config/` as the generated manifests, the manager Deployment and "Sample … CRs for testing". These should apply cleanly against the current CRDs and controllers.

**Actual:** No part of the `config/` dev path works end to end. The Helm chart install does not use these files and is unaffected, hence S4.

**Evidence:** [evidence/review-operator/verification.md#c-operator-22](evidence/review-operator/verification.md#c-operator-22)

### F-064

**Repro / observation (defensive: confirms whether the control holds)**
1. Read `oci/client.go:Pull`. The returned digest is `manifestDesc.Digest` from `FetchReference(ctx, <tag>)`, and the manifest bytes come from `io.ReadAll` on the response body. Nothing computes `digest.FromBytes(manifestBytes)` or compares it.
2. Read `readBlob`: `Blobs().Fetch` followed by `io.ReadAll`, with no digest verification of the body.
3. Read the vendored oras-go: for a tag reference, `generateDescriptor` takes the digest from the response header when one is present and hashes the body only when the header is absent. `blobStore.Fetch` compares only header values. oras-go leaves body verification to the caller.
4. Read `module_controller.go:130`: `verifier.Verify(ctx, entry.Reference, bundle.Digest)` checks the signature for that digest. The template is then materialised from the unverified bytes.

**Expected:** Every byte the operator consumes is bound to the digest that cosign verified.

**Actual:** The manifest body and the layer bodies are not hashed. The signature therefore guarantees nothing about the content that is applied.

**Evidence:** [verification-operator.md#h-operator-01](verification-operator.md#h-operator-01)

### F-065

**Repro / observation (defensive)**
1. Read `:100-102` in module_controller.go. The condition compares `AppliedVersion`, `AppliedTemplate`, `Phase` and the catalog digest. `mod.Spec.Digest` does not appear in it.
2. On a `Ready` Module, a `spec.digest` edit that leaves `spec.version` unchanged returns at `:106`. The pin check at `:136` is never reached.

**Expected:** When `spec.digest` is set and differs from `status.appliedDigest`, the Module is not treated as converged.

**Actual:** The Module stays `Ready` on content that does not match the pin.

**Evidence:** [verification-operator.md#h-operator-02](verification-operator.md#h-operator-02)

### F-066

**Repro / observation (defensive)**
1. `grep -n netguard operator/internal/modsrc/*.go operator/internal/oci/*.go operator/internal/verify/*.go` finds hits only in `modsrc/git.go` and `modsrc/http.go`.
2. `oci.New` builds `retry.NewTransport(&http.Transport{})` with no guarded `DialContext`. `baseCheckOpts` passes only auth and context to go-containerregistry, so its default transport is used.
3. `docs/architecture.md` and `README.md` include OCI in the guard's scope, while `docs/security.md` lists git and http only.

**Expected:** Either the OCI client and the cosign registry client dial through netguard, or `docs/architecture.md` and `README.md:146` are narrowed to match `docs/security.md`.

**Actual:** The OCI and cosign fetch paths have no dial-time guard, and two documents say they do.

**Evidence:** [verification-operator.md#h-operator-03](verification-operator.md#h-operator-03)

### F-067

**Repro / observation (defensive)**
1. The dashboard's filter field starts empty and sends `undefined` when it is left blank. The API then stores `spec.filter: nil`.
2. The operator passes `nc.Spec.Filter` (nil) to `StartCapture`, so the sidecar receives an empty filter and answers 400. `IsTransientError` treats a 4xx as permanent, and the capture goes `Failed`.
3. `grep -rn` across `operator/` and `api/` finds no code that builds a filter from `tmpl.Spec.Ports`.

**Expected:** When `spec.filter` is nil, the operator builds the default filter from the GameTemplate's advertised ports and protocols and sends that.

**Actual:** A capture started without a filter always fails.

**Evidence:** [verification-operator.md#h-operator-04](verification-operator.md#h-operator-04)

### F-068

**Repro / observation (defensive)**
1. Read the `playit` case in gameserver_tunnel.go. It adds no ports, although its comment says "we permit all ports". The resulting rule lists DNS (53/UDP and 53/TCP) plus the template's advertised container ports.
2. `grep -n "gameplane-tunnel" charts/gameplane/templates/networkpolicies.yaml` finds nothing, so no chart policy adds egress for tunnel pods.
3. Read the TODOs: nothing writes a playit address to status yet.

**Expected:** The maintainer decides the intended playit egress scope and the docs describe the actual state of address reporting.

**Actual:** Under the chart defaults, a playit tunnel pod cannot reach its relay (fails closed). The docs also promise an address on status that nothing writes.

**Evidence:** [verification-operator.md#h-operator-05](verification-operator.md#h-operator-05)

### F-069

**Repro / observation (defensive)**
1. The three setup sections in `docs/tunnels.md` create the Secret with `kubectl create secret generic`, which sets no ownerReference.
2. `reconcileTunnel` returns an error for such a Secret, and `Reconcile` returns before the Service, StatefulSet and status steps. The only signal is the operator log line.

**Expected:** The docs describe the supported way to supply credentials: the dashboard or API endpoint, or a Secret created with an ownerReference to the GameServer. The refusal also appears on the GameServer's status or conditions.

**Actual:** Following the docs leaves a GameServer that never gets its Service or StatefulSet, and nothing on its status says why.

**Evidence:** [verification-operator.md#h-operator-06](verification-operator.md#h-operator-06)

### F-070

**Repro / observation (defensive)**
1. A volume-snapshot Restore creates the new server by copying the original's whole spec, including Secret and ConfigMap references.
2. The referenced objects are owned by the original server, so the new server's reconcile is refused before its status is written, and its phase stays empty.
3. `awaitRestoredServer` treats an empty phase as "still starting" and requeues every 10 s with no deadline. The Restore stays `Running`.

**Expected:** The volume-snapshot restore gives the new server objects it owns (copied or re-created with the new owner), or drops or rewrites the inherited references, or fails the Restore with a clear message.

**Actual:** A volume-snapshot restore of any server that uses env Secret or ConfigMap refs, or a tunnel credential Secret, never finishes.

**Evidence:** [verification-operator.md#h-operator-07](verification-operator.md#h-operator-07)

### F-071

**Repro / observation (defensive)**
1. `agent_rbac.go` creates a Role `<sa>-heartbeat` with a single rule: `get` and `patch` on `gameservers/status`, restricted to `resourceNames: [<gs>]`.
2. `agent/client.go` builds an mTLS client. The operator calls the agent. The operator receives no calls from the agent and verifies no agent token.
3. The chart's ClusterRole is read-only for workloads and Secrets. Workload writes sit in the namespaced Role.

**Expected:** `operator/specs.md` and `docs/architecture.md` state the real grants and the real direction and mechanism of authentication.

**Actual:** Both documents describe a different model.

**Evidence:** [verification-operator.md#h-operator-08](verification-operator.md#h-operator-08)

### F-072

**Repro / observation (defensive)**
1. `grep -rn "gameplane-operator-manager\|gameplane-operator-leader-election" operator/config` finds only the binding file. Neither role is defined there.
2. List `role.yaml`'s rules. Write verbs appear cluster-wide on workloads, volumes and CRDs. They come from write verbs in several controllers' kubebuilder markers.
3. The marker comment says the ClusterRole "keeps a compromised operator token from reading Secrets cluster-wide". Both `role.yaml` and the chart's ClusterRole grant `get/list/watch` on `secrets` cluster-wide.
4. `role_namespace.yaml` lacks grants the controllers use.

**Expected:** The dev RBAC manifests reference roles that exist. The kubebuilder markers declare only the cluster-wide reads plus the few cluster-scoped writes, and the namespaced Role matches the chart's.

**Actual:** The dev manifests bind nothing usable, and the generated ClusterRole is broader than the design it documents.

**Evidence:** [verification-operator.md#h-operator-09](verification-operator.md#h-operator-09)

### F-073

**Repro / observation (defensive)**
1. `oci/client.go` sets only `r.PlainHTTP = c.insecure`, and its doc comment says the client "deliberately does not skip TLS verification".
2. `grep -rn InsecureSkipVerify operator | grep -v _test` returns nothing.
3. The CRD description of `spec.oci.insecure` says it "skips TLS verification". The description of the neighbouring git/http field already states the correct behaviour.

**Expected:** The CRD doc, and the regenerated CRD description, say "plain HTTP only; TLS verification is never skipped".

**Actual:** The documentation promises a weaker behaviour than the code implements.

**Evidence:** [verification-operator.md#h-operator-10](verification-operator.md#h-operator-10)

### F-074

**Repro / observation**
1. Read `api/cmd/main.go:253` and `:676-700`: requestTimeout(60s) is applied to all routes except WebSocket and GET /events.
2. Read `api/internal/ws/dialer.go:283-286`: the upstream request uses req.Context(), streamed with io.Copy, so a context cancel mid-stream stops the copy.
3. Put a 2 GiB file in a server's data volume and download it through the dashboard at 10 Mbps rate. After 60 s, curl ends with "transfer closed with … bytes remaining".

**Expected:** download and upload proxies stream for as long as the transfer takes, not cut off at 60 s like other routes.

**Actual:** every non-WebSocket, non-SSE transfer is cut at 60 s. A truncated download that already sent 200 silently fails. An upload via direct API exposure fails with 504.

**Evidence:** [evidence/review-api/verification.md#c-api-01](evidence/review-api/verification.md#c-api-01)

### F-075

**Repro / observation**
1. Read `api/cmd/main.go:602-623`: bodyLimit wraps every request body except /servers/*/files/upload paths.
2. Read `api/internal/ws/dialer.go:87`: /mods/upload has a declared 512 MiB cap, and dialer.go:243-248 has a 64 MiB default, both dead code.
3. On direct API exposure, send POST /servers/<name>/mods/upload with a 3 MiB mod jar. Response is 502 "agent unreachable"; API log shows "http: request body too large".

**Expected:** /mods/upload accepts up to 512 MiB, and /files/write accepts up to 64 MiB, as their mounts declare.

**Actual:** both are capped at 1 MiB by the global body limit, and the caller gets 502 blaming the agent.

**Evidence:** [evidence/review-api/verification.md#c-api-02](evidence/review-api/verification.md#c-api-02)

### F-076

**Repro / observation**
1. Read `api/internal/handlers/config.go:179` and `:272`: both compare err.Error() != "sql: no rows" for a missing config row.
2. `database/sql.ErrNoRows.Error()` is "sql: no rows in result set" (different string), so the comparison never matches.
3. On a fresh install, call DELETE /admin/config/auth/role-mappings/admin. Classification as 500 "internal error".

**Expected:** resetRoleMapping is idempotent and returns 200 even when there is no override.

**Actual:** missing row returns 500 until the row exists.

**Evidence:** [evidence/review-api/verification.md#c-api-03](evidence/review-api/verification.md#c-api-03)

### F-077

**Repro / observation**
1. Read `api/internal/handlers/destinations.go:132,136,177` and `modules.go:176,184`: hand-written errors passed to httperr.Write.
2. Read `api/internal/httperr/httperr.go:63-100`: classify has no case for plain errors, so they map to 500 "internal error".
3. POST /backup-destinations with invalid input returns 500 with generic message instead of the hand-written 400/409.

**Expected:** 400 (409 for clash) with the caller-provided message, via httperr.WriteCode.

**Actual:** 500 "internal error".

**Evidence:** [evidence/review-api/verification.md#c-api-04](evidence/review-api/verification.md#c-api-04)

### F-078

**Repro / observation**
1. Read `api/internal/audit/audit.go:728-739`: WriteSync enqueues Event{…, Reason: reason} to webhook/S3 sinks.
2. Read `audit.go:221-229` and `s3.go:231-247`: both drop the reason field; CSV export header (handlers/audit.go:76) has no reason column.
3. Log in via OIDC with a role mapping as a new user. The OIDC role assignment reason carries only in the DB row and JSON export, not webhook/S3.

**Expected:** webhook, S3 and stdout sinks all carry reason, per api/specs.md:338.

**Actual:** only stdout and JSON export carry it. For role-assignment events, reason is the only field recording which role was granted, so external consumers can't see privilege grants.

**Evidence:** [evidence/review-api/verification.md#c-api-05](evidence/review-api/verification.md#c-api-05)

### F-079

**Repro / observation**
1. An SSO user logs in and creates users, oidc_links, bindings and preferences rows.
2. Admin deletes the user via DELETE /users/{id}. Handler deletes users and bindings only; oidc_links and user_preferences survive.
3. The person logs in through IdP again. The join finds no user, tries to insert into oidc_links, hits the (issuer, subject) primary key, and gets 500 "login failed".

**Expected:** a deleted SSO user can be provisioned again on next login. users.go deletes a user's rows explicitly.

**Actual:** that IdP subject is locked out until someone deletes the orphaned oidc_links row by hand.

**Evidence:** [evidence/review-api/verification.md#c-api-06](evidence/review-api/verification.md#c-api-06)

### F-080

**Repro / observation**
1. Read `api/cmd/main.go:228-231`: AttachAuditWriteSyncFunc is called only on oidcAuth (Helm flag provider).
2. Read `api/internal/auth/registry.go:308-342`: providers from the auth config row are built with no audit function attached.
3. Add a dashboard-managed OIDC provider with role mapping admin: [ops]. Log in as a new user in group ops. GET /admin/audit shows no role assignment row.

**Expected:** FR-014 requires role assignments from OIDC mappings to be recorded, regardless of provider.

**Actual:** only Helm-flag OIDC providers are audited.

**Evidence:** [evidence/review-api/verification.md#c-api-07](evidence/review-api/verification.md#c-api-07)

### F-081

**Repro / observation**
1. Enable clusterOps.enabled=true. POST /cluster/kubeconfig returns a file with server: https://<ClusterIP>.
2. POST /cluster/nodes:join returns a kubeadm command using that same ClusterIP.
3. Try to use the kubeconfig from a workstation outside the cluster. It times out. The ClusterIP is not routable.

**Expected:** kubeconfig and join command point at an API server address reachable from outside the cluster (configurable).

**Actual:** both use the in-cluster Service ClusterIP, unreachable from where the user would run them.

**Evidence:** [evidence/review-api/verification.md#c-api-08](evidence/review-api/verification.md#c-api-08)

### F-082

**Repro / observation**
1. Read `api/internal/db/shares.go:311-313`: RevokeShareLink returns a wrapped plain error for no-row match.
2. Read `api/internal/handlers/shares.go:264-267`: httperr.Write classifies it as 500.
3. Call DELETE /servers/<name>/shares/doesnotexist. Response is 500 "internal error".

**Expected:** 404.

**Actual:** 500.

**Evidence:** [evidence/review-api/verification.md#c-api-09](evidence/review-api/verification.md#c-api-09)

### F-083

**Repro / observation**
1. Read `api/internal/handlers/roles.go:151-152`: WriteHeader(201) is called before writeJSON sets Content-Type.
2. net/http snapshots headers at WriteHeader, so the body is sniffed. The seven routes are modules.go:203, module_sources.go:263, module_upload.go:138, modules_builder.go:417, clusters.go:184, users.go:772, and roles.go:151.
3. Curl -si a POST to any of these routes. Response has Content-Type: text/plain; charset=utf-8.

**Expected:** Content-Type: application/json on every JSON response.

**Actual:** text/plain; charset=utf-8 on the 201 responses of seven routes.

**Evidence:** [evidence/review-api/verification.md#c-api-10](evidence/review-api/verification.md#c-api-10)

### F-084

**Repro / observation**
1. Read `api/internal/handlers/tunnelcreds.go:136-141`: put merge-patches stringData, keeping the old provider's key.
2. Read `:225-231`: get ranges the tunnelProviderKeys map and returns the first match (random in Go).
3. Set frp credentials, then tailscale. The Secret now has both token and authKey. GET returns either one randomly.

**Expected:** switching provider doesn't leave the old provider's credential behind.

**Actual:** both keys stay in the Secret, and get returns random one.

**Evidence:** [evidence/review-api/verification.md#c-api-11](evidence/review-api/verification.md#c-api-11)

### F-085

**Repro / observation**
1. Read `api/internal/db/migrations/011_user_theme_preferences.sql:23` and `:28-31`: the backfill takes SQLite's datetime('now') default, which is YYYY-MM-DD HH:MM:SS.
2. Read `api/internal/db/preferences.go:46-50`: GetPreferences returns it verbatim.
3. On an upgraded install, GET /users/me/preferences for a backfilled user returns "updatedAt":"2026-09-24 08:00:00" (SQLite format, not RFC 3339).

**Expected:** api/specs.md:279 requires RFC 3339 written by Go.

**Actual:** backfilled users get SQLite format until their first save.

**Evidence:** [evidence/review-api/verification.md#c-api-12](evidence/review-api/verification.md#c-api-12)

### F-086

**Repro / observation**
1. Read `api/specs.md:92-146` and cross-check the routes listed with the actual handlers mounted.
2. `:124` lists `/module-sources` but code has `/modules/sources`.
3. `:128-129` and `:146` list `/cluster/actions` and `/admin/cluster/{op}` but no such routes exist.
4. Documentation only; no functional impact.

**Expected:** the "External interface" section lists the routes the API actually serves.

**Actual:** multiple mismatches as detailed in the evidence.

**Evidence:** [evidence/review-api/verification.md#c-api-14](evidence/review-api/verification.md#c-api-14)

### F-087

**Repro / observation**
1. Read `api/specs.md:110` and `:215`: contradictory claims about capture enable/disable (planned vs. implemented).
2. Read `:322` and `:543`: audit failure fatal vs. non-fatal; code is fatal.
3. Read `:205` and `:211` vs. `capture.go:1024-1030`: 409 for Pending/Running vs. every phase except Completed; code returns 409 for not-Completed.

**Expected:** one consistent description matching capture.go.

**Actual:** multiple contradictory statements.

**Evidence:** [evidence/review-api/verification.md#c-api-15](evidence/review-api/verification.md#c-api-15)

### F-088

**Repro / observation**
1. Read `api/specs.md:517`: sessions are described as memory store + DB; code uses DB-only with 12h TTL.
2. Read `:416`: per-IP + per-user on /auth/login + OIDC callback; code has only per-IP on callback.
3. Read `:374`: operator gets read/write modules; code grants only modules:read.

**Expected:** spec matches code.

**Actual:** multiple contradictory statements.

**Evidence:** [evidence/review-api/verification.md#c-api-16](evidence/review-api/verification.md#c-api-16)

### F-089

**Repro / observation**
1. Read `api/specs.md:79` and `:168`: five `--capture-*` flags listed; code has only two (enabled and default-max-duration).
2. Read `:29-52`: layout leaves out `internal/steam/`.
3. Read `:591`: nightly Postgres job claimed; no workflow mentions postgres or schedule: trigger.

**Expected:** specs.md lists dependencies and flags actually present.

**Actual:** multiple stale or missing entries.

**Evidence:** [evidence/review-api/verification.md#c-api-17](evidence/review-api/verification.md#c-api-17)

### F-090

**Repro / observation (by reading master `13a859ff`)**
1. `api/cmd/main.go:331` mounts `handlers.MountRegistry(…)` with the home cluster client.
2. `api/internal/handlers/registry.go:66-207`: the five MountRegistry routes (providers, search, versions, modpackDeps, installModpack) never call `rejectRemoteCluster`.
3. By contrast, `mod_ids.go:73` and `:93` call `rejectRemoteCluster` on sibling routes with the same home client.

**Expected:** every MountRegistry route answers 404 for `?cluster=<registered-non-local>`, or dispatches through the registry to that cluster.

**Actual:** no guard on any of the five routes.

**How a maintainer confirms the control holds:** add a handler test that calls each MountRegistry route with `?cluster=<registered-non-local>` and asserts 404 with no call to the Kubernetes client. Add a structural check that every handler with a bare `*kube.Client` under namespaced RBAC is wrapped by `rejectRemoteCluster`. After the fix, multicluster e2e should show that a user bound only to a remote cluster cannot change a home-cluster GameServer through `/servers/{name}/modpack`.

**Evidence:** [verification-api.md#h-api-01](verification-api.md#h-api-01)

### F-091

**Repro / observation (by reading master `13a859ff`)**
1. `client_ip.go:92-117`: ClientIPFromXFF walks the header right to left but never reads RemoteAddr.
2. `cmd/main.go:522-523`: the default trusted list is loopback, all of RFC 1918, link-local and ULA.
3. On a LAN install where the client IP is in RFC 1918, the result depends on the path in front of the API and either lacks identity or takes an identity the proxies didn't add.

**Expected:** the TCP peer is checked against the trusted set before any XFF entry is used.

**Actual:** the peer is not checked. Per-IP limiting and audit collapse for private-range clients.

**How a maintainer confirms the control holds:** unit tests on the middleware chain. (a) RemoteAddr outside the trusted set plus an XFF header: recorded client IP is the RemoteAddr host. (b) RemoteAddr is a trusted proxy and XFF ends in a private-range client address: recorded IP is that client address. (c) Two such clients get different limiter buckets. Update docs/security.md to match the fixed behaviour.

**Evidence:** [verification-api.md#h-api-02](verification-api.md#h-api-02)

### F-092

**Repro / observation (by reading master `13a859ff`)**
1. `oidc.go:447-454`: first-login role is resolved from merged policy (Helm + override).
2. `oidc.go:469`: syncRole is keyed to `o.policy != nil && o.policy.RoleMappings != nil` — the Helm base.
3. With no `--oidc-role-mapping-*` flags, RoleMappings is nil in the Helm base, so syncRole is false.
4. On a later login, the role is never re-evaluated, even if an override with role mappings exists.

**Expected:** re-evaluation runs whenever the effective, post-merge policy has role mappings.

**Actual:** re-evaluation is keyed on Helm mappings only.

**How a maintainer confirms the control holds:** a unit test with a Helm policy whose RoleMappings is nil and an override that maps group G to admin. First login in G gives admin. A second login without G must change the role (and emit the FR-014 audit event), except when the last-user-manager guard applies.

**Evidence:** [verification-api.md#h-api-03](verification-api.md#h-api-03)

### F-093

**Repro / observation (by reading master `13a859ff`)**
1. `rbac.go:243`: /events requires only servers:read.
2. `events.go:63-110`: the handler watches all entries of kube.GVRs (servers, templates, backups, schedules, restores) and streams each full object.
3. A custom role with servers:read but without backups:read receives all backups in the stream.

**Expected:** the stream includes only kinds the caller may read in the resolved namespace.

**Actual:** every kind is sent to any holder of servers:read.

**How a maintainer confirms the control holds:** an SSE handler test with a custom role that holds only servers:read asserts that no event with a kind other than servers arrives. Include a namespace-scoped binding case.

**Evidence:** [verification-api.md#h-api-04](verification-api.md#h-api-04)

### F-094

**Repro / observation (by reading master `13a859ff`)**
1. `handlers/shares.go:251-258`: ownership check is on {name}.
2. `db/shares.go:289-300`: revoke SQL matches `id` and `cluster` only, not the path's namespace or server name.

**Expected:** revocation matches cluster, namespace, server_name and id.

**Actual:** only id and cluster are matched. A mismatch affects no row; impact is small because ids are random and appear only in owner-only listings.

**How a maintainer confirms the control holds:** a store test where revoking with a mismatched namespace or server name returns a not-found sentinel and leaves the row untouched, and a handler test that expects 404 in that case.

**Evidence:** [verification-api.md#h-api-05](verification-api.md#h-api-05)

### F-095

**Repro / observation (by reading master `13a859ff`)**
1. `006_share_links.sql:20`: `created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE`.
2. `values.yaml:129`: the shipped DSN doesn't enable SQLite foreign keys, so the cascade never fires.
3. `users.go:464-471`: delete removes only the users row and bindings.
4. LookupShareLink checks revocation and expiry only, not whether the creator exists.

**Expected:** deleting a user revokes or deletes the share links that user created, on both SQLite and Postgres.

**Actual:** on SQLite the links stay valid indefinitely.

**How a maintainer confirms the control holds:** a handler test that creates a link as user U, deletes U, and asserts that the token now gets the uniform not-found response from `GET /shares/{token}`.

**Evidence:** [verification-api.md#h-api-06](verification-api.md#h-api-06)

### F-096

**Repro / observation (by reading master `13a859ff`)**
1. `bootstrap.go:165-170`: enableLocalLogin decodes into `struct{ Providers []map[string]any }`.
2. `:183-195`: it marshals that struct back, dropping any top-level key outside `providers`.
3. helmOverride is lost; the Helm-seeded role mappings apply again on next OIDC logins.

**Expected:** the command preserves everything else in the auth config row, per docs/security.md:58-59.

**Actual:** helmOverride is erased and unaudited.

**How a maintainer confirms the control holds:** a bootstrap test that seeds an auth row with providers and helmOverride.roleMappings, runs enableLocalLogin, and asserts that the override is unchanged and the local provider is enabled.

**Evidence:** [verification-api.md#h-api-07](verification-api.md#h-api-07)

### F-097

**Repro / observation (by reading master `13a859ff`)**
1. `bootstrap.go:114-124` (--force path): updates pw_hash and role, but leaves sessions alone.
2. Compare `handlers/users.go:651-658` (dashboard reset): evicts sessions explicitly.
3. Sessions last 12h, so the new credential takes up to 12h to take effect on existing sessions.

**Expected:** break-glass reset evicts the account's sessions like the dashboard reset does.

**Actual:** existing sessions stay valid for up to 12h.

**How a maintainer confirms the control holds:** a bootstrap test that inserts a session for the user, runs the --force path, and asserts that `SELECT COUNT(*) FROM sessions WHERE user_id = ?` is 0.

**Evidence:** [verification-api.md#h-api-08](verification-api.md#h-api-08)

### F-098

**Repro / observation (by reading master `13a859ff`)**
1. `capture.go:943`: resolves k from ?cluster=.
2. `:1065-1070`: builds `<name>-agent.<ns>.svc.cluster.local` and `:1086-1094` sends with home-cluster mTLS.
3. On a remote cluster, the downstream resolves the home cluster's sidecar.

**Expected:** the download either dispatches to the target cluster's sidecar or answers 404 for a non-local cluster, like the other home-only proxies.

**Actual:** a request for a remote cluster goes to the home cluster's same-named sidecar.

**How a maintainer confirms the control holds:** a handler test with `?cluster=<registered-non-local>` asserts either 404 before any upstream call, or an upstream host that belongs to the target cluster, and never the home-cluster service name.

**Evidence:** [verification-api.md#h-api-09](verification-api.md#h-api-09)

### F-099

**Repro / observation (by reading master `13a859ff`)**
1. `api/specs.md:430` and `:548`: netguard is described as the "SSRF dial-guard for … module registry fetches".
2. `registry.go:12-15`: package doc explains every host is a fixed provider hostname.
3. `registry.go:208-209`: NewSet uses `&http.Client{Timeout: 15s}` with no netguard dialer.

**Expected:** the spec and code agree on whether netguard guards registry fetches.

**Actual:** the spec claims a control that the code doesn't implement.

**How a maintainer confirms the control holds:** if netguard is wired in, a unit test that points a provider's base URL at a link-local address expects `netguard.ErrBlockedAddr`. Otherwise, the spec diff is the fix.

**Evidence:** [verification-api.md#h-api-10](verification-api.md#h-api-10)

### F-100

**Repro / observation (by reading master `13a859ff`)**
1. `module_upload.go:78-87`: the request body is capped at 900 KiB (compressed).
2. `:303`: the number of distinct member paths is capped at 256.
3. `:306-312`: each member is capped at 900 KiB.
4. No running total is kept, so decompressed set can reach about 256 × 900 KiB before parseUploadedBundle keeps only four files.

**Expected:** the extractor enforces a total decompressed budget while it extracts.

**Actual:** the total isn't bounded.

**How a maintainer confirms the control holds:** a unit test feeds extractUploadArchive an archive whose members together exceed the budget, while each stays under the per-member cap, and asserts an error before full expansion.

**Evidence:** [verification-api.md#h-api-11](verification-api.md#h-api-11)

### F-101

**Repro / observation (by reading master `13a859ff`)**
1. `watch.go:57-61`: DeleteFunc returns when obj isn't `*unstructured.Unstructured`, so `cache.DeletedFinalStateUnknown` is ignored.
2. `clusters.go:192-236`: DELETE /clusters/{name} deletes the CR and Secret but never calls `reg.Remove`.
3. Removal depends only on the watcher, which can miss the delete and deliver it as a tombstone.

**Expected:** a deleted Cluster registration always removes its client, including tombstones.

**Actual:** the tombstone path is ignored; the client stays registered until API restart.

**How a maintainer confirms the control holds:** a watcher unit test delivers `cache.DeletedFinalStateUnknown{Obj: <cluster>}` to the handler and asserts that the id is gone from `reg.IDs()`. Optionally, also call `reg.Remove` directly in the `DELETE /clusters/{name}` handler, so revocation doesn't depend on the watch at all.

**Evidence:** [verification-api.md#h-api-12](verification-api.md#h-api-12)

### F-102

**Repro / observation**
1. `agent/internal/files/files.go:202-224` (`write`: `os.Create` at `:213` truncates the target before the `io.Copy` at `:219`); `agent/internal/files/files.go:302-331` (`savePart`: `os.Create` at `:309`, then `os.Remove(dstPath)` in the deferred cleanup at `:315-319` on any error).
2. By reading master: `write` opens the target with `os.Create`, which truncates it, and only then copies the body. After `:213`, any error leaves the target truncated or partly rewritten.
3. `savePart` does the same for each upload part. Its deferred cleanup deletes `dstPath` on any error. If a file already existed at `dstPath`, the cleanup deletes the user's previous file.
4. Agent-only reproduction paths exist (test helpers); live paths require `web.enabled=false` or port-forward.
5. On a default install, remaining triggers are failures during the transfer: the data volume fills up, or the API or agent restarts while a file is being written.

**Expected:** A failed write or upload leaves the previous file untouched. Recommended: write to a temporary file in the same directory and rename it over the target only on success.

**Actual:** A failed `/files/write` leaves the target truncated or partly rewritten. A failed `/files/upload` deletes the file that was already there.

**Evidence:** [evidence/review-agent/verification.md#c-agent-01](evidence/review-agent/verification.md#c-agent-01)

### F-103

**Repro / observation**
1. `agent/cmd/main.go:160` (`r.Use(middleware.Timeout(30 * time.Second))` on the root router, so it applies to every route).
2. chi's `Timeout` replaces the request context with `context.WithTimeout(r.Context(), 30s)`. After the handler returns, it writes 504 if the deadline has passed.
3. `console.serve` reads with `wsjson.Read(ctx, …)` on that context, and coder/websocket arms an `AfterFunc` that closes the connection after 30 s.
4. Live: open the Console tab of a running RCON server and leave it open. About 30 s after the "connected" line, the terminal prints the disconnect line and reconnects. This repeats every 30 s.
5. The Logs tab's file source is cut the same way. `mods.install` passes the same context to downloads, limiting them to 30 s.

**Expected:** The WebSocket routes are exempt from the request deadline, as they are in the API. Mod URL installs are bounded by their own download timeout, not by the router's 30 s.

**Actual:** Console and log streams drop every 30 s, and mod URL installs fail after 30 s.

**Evidence:** [evidence/review-agent/verification.md#c-agent-02](evidence/review-agent/verification.md#c-agent-02)

### F-104

**Repro / observation**
1. `agent/internal/rcon/rcon.go:300` (`if size < 10 || size > 4096`), reached from `Exec` at `:160-175`.
2. `readPacket` rejects any packet whose size field is above 4096. The size field counts the id, the type, the body and the two trailing nulls, so any reply packet with a body of 4087 bytes or more fails.
3. Minecraft splits long replies into chunks of up to 4096 characters and writes each chunk's size as its UTF-8 byte length + 10. The first chunk of a long reply has a size field of 4106 or more.
4. Unit-level test coverage: `rcon_extra_test.go:65-81` covers only size 0.
5. Live: on a `minecraft-java` server with ~80 banned players, `/players/banned` returns 502.

**Expected:** Replies of up to at least 4106 bytes (a 4096-byte body + 10) are accepted. Non-ASCII text can make a Minecraft chunk larger, so the documented bound should allow for that too.

**Actual:** Any Minecraft reply of 4087 bytes or more fails, together with the RCON connection it was read on.

**Evidence:** [evidence/review-agent/verification.md#c-agent-03](evidence/review-agent/verification.md#c-agent-03)

### F-105

**Repro / observation**
1. `agent/internal/heartbeat/heartbeat.go:129` (`"gameVersion": cfg.Game`). `cfg.Game` comes from `--game` / `GAMEPLANE_GAME`, which the operator sets to `tmpl.Spec.Game`.
2. Start a server from `minecraft-java` and wait one heartbeat interval (20 s). `kubectl get gameserver <name> -o jsonpath='{.status.agent.gameVersion}'` prints `minecraft-java`.
3. The CRD documents the field as "the version string the running game reports". The dashboard shows it as the version in the page header and status card.
4. `heartbeat_test.go:60,75` feeds a test value the operator never sets, so the test passes.

**Expected:** The field holds the running game's version (or the selected version token), or it is left empty.

**Actual:** The field holds the template's game identifier (`minecraft-java`, `palworld` and so on), and the dashboard labels that as the version.

**Evidence:** [evidence/review-agent/verification.md#c-agent-04](evidence/review-agent/verification.md#c-agent-04)

### F-106

**Repro / observation**
1. RCON disabled: 11 modules declare `rcon.protocol: none` (7-days-to-die, arma-reforger, beammp, dont-starve-together, enshrouded, euro-truck-simulator-2, garrys-mod, mount-and-blade-2-bannerlord, terraria, tmodloader, valheim). For them the agent uses `rcon.Disabled`, and `/players` answers `online: -1, max: -1`.
2. No player list declared: 10 source-protocol modules declare no `capabilities.players` (ark-survival-ascended, ark-survival-evolved, cs2, hell-let-loose, left-4-dead-2, project-zomboid, squad, team-fortress-2, the-isle, v-rising). A reply that matches neither Minecraft format gives `online: 0, max: 0`.
3. Neither `agent/specs.md` nor `agent/openapi.yaml` defines a value for "unknown". The heartbeat patches `playersOnline: null` for the second case, so the status card shows "—" while the Players tab shows 0.

**Expected:** One documented representation of "unknown" (for example `online: null`) for both cases, rendered as "—".

**Actual:** Two different values, `-1/-1` and `0/0`, and the dashboard renders both as real counts.

**Evidence:** [evidence/review-agent/verification.md#c-agent-06](evidence/review-agent/verification.md#c-agent-06)

### F-107

**Repro / observation**
1. `docs/module-authoring.md:1109-1111` says: "any command error — or output matching `failurePattern` (case-insensitive) — aborts the backup and best-effort runs `unquiesce`.
2. `agent/internal/quiesce/quiesce.go:118-135` (the `if i > 0` at `:128`): runs `unquiesce` only when a later command fails. If the first command returns an error, the function returns without running `unquiesce`.
3. `TestDeclaredQuiescer_FirstCommandErrorSkipsRollback` pins this on purpose. The operator doesn't fill the gap; on any error other than `ErrUnsupported`, it fails the Backup without setting the `quiesce-attempted` annotation.
4. Not reproduced: the notes' runtime case.

**Expected:** The doc and the code agree. Either the doc says `unquiesce` runs only after an earlier command has succeeded, or the agent runs a best-effort `unquiesce` on any quiesce failure. The second option needs sign-off because it changes a pinned test.

**Actual:** The doc promises a rollback on every failure, and the code skips it when the first command fails.

**Evidence:** [evidence/review-agent/verification.md#c-agent-08](evidence/review-agent/verification.md#c-agent-08)

### F-108

**Repro / observation**
1. `agent/internal/files/files.go:71-75` (`resolve` returns the `EvalSymlinks` result for an existing path) and `:346-368` (`del` removes the resolved path).
2. Inside a server's data volume, create a symlink `latest.log -> logs/a.log`. The file API can't create a symlink directly.
3. `/files/list` shows `latest.log` with mode `L…` and `dir: false`. Delete `latest.log` in the Files tab.
4. `resolve` returns the symlink target's path, and `os.Remove` deletes it. `latest.log` remains and now dangles.
5. No existing test covers a link that stays inside the root. The symlink tests in `files_test.go` cover only links that escape it.

**Expected:** Delete acts on the entry that was named. For example: confine the parent directory, then remove the final component without following it.

**Actual:** The link's target is deleted, and the link is left behind.

**Evidence:** [evidence/review-agent/verification.md#c-agent-09](evidence/review-agent/verification.md#c-agent-09)

### F-109

**Repro / observation (by reading master)**
1. `agent/specs.md:137` documents `/actions/run` request `{ name, params }`, but code decodes `{ id, params }`.
2. `specs.md:138` documents `/status` response `{ metrics[] }`, but code writes a bare JSON array.
3. `specs.md:139` documents `GET /mods` response `{ mods[] }`, but code writes a bare array.
4. `specs.md:140` documents `/mods/install` request `{ name, version, ... }`, but code reads `{ url, name?, replaces?, meta? }` (no `version`).
5. `specs.md:141` documents `DELETE /mods` request `{ name }`, but code reads the `?name=` query parameter.
6. The table at `specs.md:110` omits 4 endpoints: `GET /logs/download`, `GET /players/whitelist`, `POST /players/whitelist/{add,remove}`.
7. The flag table (`specs.md:82-100`) omits `--cli-pipe` / `GAMEPLANE_CLI_PIPE`.
8. `specs.md:63` says the heartbeat patches `status.playersOnline`, `status.playersMax` and `status.gameVersion`. The code patches `status.agent.*`.
9. The `openapi.yaml` `Capabilities` schema lists `kick`, `ban` and `unban`. The agent also returns `whitelist`.

**Expected:** The spec and the OpenAPI file describe the mounted contract.

**Actual:** They differ from the code as listed.

**Evidence:** [evidence/review-agent/verification.md#c-agent-11](evidence/review-agent/verification.md#c-agent-11)

### F-110

**Repro / observation**
1. `agent/specs.md:170-179` lists dependencies: chi v5.1.0, coder/websocket v1.8.12, etc.
2. `agent/go.mod:18-23` has v5.3.2, v1.8.15, v0.37.0, v0.37.0, v1.24.1 and v0.48.0.
3. `specs.md:179` says "The agent is pinned to Kubernetes v0.31.1 … while other modules use v0.35.0 … This is intentional". All three modules (`agent`, `operator`, `api`) use k8s.io `v0.37.0`.

**Expected:** The table matches `go.mod`, and the pin note is removed or corrected.

**Actual:** Both are stale.

**Evidence:** [evidence/review-agent/verification.md#c-agent-12](evidence/review-agent/verification.md#c-agent-12)

### F-111

**Repro / observation (by reading master)**
1. `players.go:6-8` says moderation is dispatched "through a small commander strategy keyed off the agent's --game flag". `pickCommander` ignores the game and builds only from declared capabilities.
2. `specs.md:67` promises "game-specific `commander` implementations (Minecraft, Satisfactory, Palworld, etc.)". Only `templateCommander` and `unsupportedCommander` exist.
3. `rcon.go:14-16` says replies are assembled with "the Valve-documented 'empty-cmd sentinel' trick". The code deliberately doesn't use that trick.
4. `heartbeat.go:2-3` says the heartbeat reports "the agent's own cpu/memory/disk usage". In proc mode, the values are the game processes' usage, with the agent's own subtree excluded.
5. `satisfactory.go:64-66` says RCON clients "never leave the pod", but the package doc comment (`rcon.go:1-16`) says nothing on the subject.

**Expected:** The comments and the spec describe the current design.

**Actual:** The descriptions are stale, as listed.

**Evidence:** [evidence/review-agent/verification.md#c-agent-13](evidence/review-agent/verification.md#c-agent-13)

### F-112

**Repro / observation (defensive — confirms the control is in place; no payload)**
1. The WebRcon protocol takes the secret in the URL path. `ensureLocked` builds `ws://<host>:<port>/<url.PathEscape(secret)>`.
2. When the handshake request fails at the transport level, `http.Client.Do` returns a `*url.Error`. Its text is `Op "URL": err`, and its URL has passed only through `stripPassword`, which redacts userinfo and leaves the path.
3. `console.serve` sends `err.Error()` as the body of the `err` envelope. The players, status and actions handlers log it with `slog`.
4. The shipped module that uses this protocol is `rust` (`rcon.protocol: websocket`). Its console mode is `rcon`, so the Console tab is shown and talks to the agent.

**Control**: RCON error text never leaves the agent, because it can carry addresses or secrets. `players.go:131-132` and `actions.go:157-159` state this rule, and both handlers reply with a generic "upstream unavailable". The console handler does not follow the rule.

**Expected**: The RCON secret never appears in an error string. The WebRcon client redacts the URL path from dial errors, just as `mods.redactURLErr` strips query strings. The console handler returns a generic message for transport failures and logs detail only after redaction.

**Actual**: The dial error text includes the path-escaped secret. The console handler sends it to the client, and three handlers log it.

**How a maintainer confirms it holds**: Add a unit test in `agent/internal/rcon` that points `NewWebSocket` at a closed local port with a `PassFn` returning a unique sentinel, and assert that neither the sentinel nor its `url.PathEscape` form appears in the error text. Add a `console` test with a fake `Rcon` whose error contains a sentinel, and assert that the `err` envelope doesn't carry it.

**Evidence:** [verification-agent.md#h-agent-01](verification-agent.md#h-agent-01)

### F-113

**Repro / observation (defensive — confirms the control is in place; no payload)**
1. `upload` calls `resolve` on the directory from `?path=` and never on the final path.
2. `savePart` removes directory components with `filepath.Base` and rejects `.` and `..`. It then calls `os.Create(dir/name)`, which opens with O_CREAT|O_TRUNC and follows a symlink that already exists at that name.
3. The precondition is a symlink in the data volume that points outside the root. The file API has no way to create one. The mods archive handling rejects symlink entries. So the link would have to come from another writer of the volume.
4. The agent container runs with a read-only root filesystem, and its only other mounts limit the reach of such a write.

**Control**: Every file operation is confined to `--data-root`, and symlinks can't lead outside it. `agent/specs.md:14`, `:201` and `SECURITY_AUDIT.md:150` all state this. `/files/write` enforces it on the full path. The mods upload enforces it through `ConfinePath`.

**Expected**: The upload destination gets the same full-path confinement as `/files/write` (`resolve` or `ConfinePath` on `dir/name`), and/or the part is written to a temporary file and renamed into place.

**Actual**: The final upload path is never confined, and an existing symlink at that name is followed.

**How a maintainer confirms it holds**: Add a unit test in `agent/internal/files`. Create a root that contains a symlink to a file outside the root, then POST a multipart upload whose part filename matches the link's name. Assert a 4xx response, and that the outside file's content is unchanged.

**Evidence:** [verification-agent.md#h-agent-02](verification-agent.md#h-agent-02)

### F-114

**Repro / observation (defensive — confirms the control holds; each item gives the spec text, then what the code enforces)**
1. `specs.md:153`: WebRcon dials "using `netguard.IsPublic()` … cannot reach private/loopback addresses". The code uses `netguard.IsAllowed`, which permits loopback and private ranges. The code is right: the agent always dials the in-pod game at the `--rcon-host` default `127.0.0.1`. `SECURITY_AUDIT.md:155` says "`IsAllowed` (operator) and `IsPublic` (agent)". The agent uses both: `IsPublic` for mod downloads and `IsAllowed` for WebRcon.
2. `specs.md:112-114`, `:144` and `:205`: call `/healthz` and `/metrics` "Public (unauthenticated)". The operator always passes `--tls-cert/--tls-key/--tls-client-ca`. `auth.ServerTLS` then sets `ClientAuth: tls.RequireAndVerifyClientCert`, which requires a client certificate at the handshake for every path. This fails closed. The agent container has no probes that would depend on it.
3. `specs.md:201`: the `files` package "rejects … dotfile access". `resolve` has no dotfile rule, so list, read, write and delete all accept dot-prefixed names. That includes the mods ledger `.gameplane-mods.json`, whose comment says it is "out of reach of client-supplied names". That holds only for the mods API.
4. `specs.md:199`: private registries "are rejected unless explicitly whitelisted". Mod downloads dial through `netguard.IsPublic`, whatever `allowedHosts` contains. The code is stricter than the sentence. `docs/security.md:343-350` describes the real behaviour correctly.
5. `specs.md:92`: `--tls-cert` "if set, requires `--tls-key` and enables HTTPS + mTLS". TLS is enabled only when both flags are set. With `--tls-cert` alone, the listener starts on plain HTTP without an error. In mTLS mode this fails closed. Only hand-run agents are affected.
6. `specs.md:152`: `ConfinePath` is "the single point of validation for all filesystem operations on untrusted paths", including archive extraction. The `files` package uses its own `resolve`, and archive extraction uses `ConfineRelPath`.

**Control**: `agent/specs.md` and `SECURITY_AUDIT.md` are the documented description of the agent's security controls. Reviewers and operators rely on them.

**Expected**: `agent/specs.md` and `SECURITY_AUDIT.md` state what the code enforces. For items 3 and 5, the maintainer decides whether the spec wording is the intended contract and the code should change instead: rejecting dotfiles in `resolve`, or refusing to start when `--tls-cert` is given without `--tls-key`.

**Actual**: The six statements above don't match the code.

**How a maintainer confirms it holds**: Read the cited lines against the corrected text. For item 2: a TLS handshake to an agent pod without a client certificate is refused, and one with the API's client certificate succeeds. For item 3, if the dotfile rule is kept as the contract: a unit test that reads or writes `/.gameplane-mods.json` through `/files/*` expects 4xx. For item 5, if the spec wording is kept: a startup test that passes `--tls-cert` without `--tls-key` expects a non-zero exit.

**Evidence:** [verification-agent.md#h-agent-03](verification-agent.md#h-agent-03)

### F-115

**Repro / observation**
1. On a test server, open the Files tab at `/`, where `server.properties` exists and has content.
2. Click **New file**, type `server.properties`, and click **Create**.
3. The browser sends `POST /servers/<name>/files/write?path=/server.properties` with an empty body. The API forwards it unchanged, and the agent opens the path with `os.Create`, which truncates it.
4. The editor opens the file, now 0 bytes.

**Expected:** "New file" refuses a name that's already in the current listing, or asks before overwriting.

**Actual:** The existing file is silently replaced by an empty one. It can't be recovered without a backup.

**Evidence:** [evidence/review-web/verification.md#c-web-01](evidence/review-web/verification.md#c-web-01)

### F-116

**Repro / observation**
1. On a test install, add `team-a` to `GAMEPLANE_EXTRA_NAMESPACES` on the API Deployment and grant the API a matching RoleBinding. Create a GameServer `mc` in `team-a` owned by the admin. It appears under "Shared with you" on `/servers`, and its link opens `/servers/mc?ns=team-a`.
2. Mods tab: the list query passes `ns`, but Install, Upload, Update all, Remove and the version picker call handlers without it. The browser's network panel shows no `namespace=` query on those requests.
3. With no namespace, `scope.Resolve` returns `gameplane-games`. The requests 404, or hit `gameplane-games/mc` if a server by that name exists there.
4. Backups tab: **Back up now** POSTs `/backups` with `serverRef.name: mc` and no namespace. The requests use `gameplane-games`.
5. Logs, Quick actions, Game status, Settings, and after **Clone** all omit `ns` in the same way.

**Expected:** Every per-server call from the detail page carries the page's `ns`.

**Actual:** For a server outside `gameplane-games`, these features 404. If a same-named server exists in `gameplane-games`, they act on that server instead.

**Evidence:** [evidence/review-web/verification.md#c-web-02](evidence/review-web/verification.md#c-web-02)

### F-117

**Repro / observation**
1. Open a Running server's Settings → General and change the description. The form becomes dirty.
2. Click **Stop** in the page header. The API merge-patches `spec.suspend: true`, and the server stops.
3. Click **Save changes**. `save` GETs the latest object, sets `out.spec` to the draft's spec (with `suspend: false`), and PUTs it.
4. The PUT succeeds, and the server starts again. The same thing happens to any spec field changed elsewhere while the form was dirty.

**Expected:** Save applies only the fields the user changed, or it detects the concurrent change. The merge keeps unmodelled fields from being clobbered.

**Actual:** The whole draft `spec` overwrites the latest one, so concurrent spec changes are reverted silently, and the 409 conflict path never fires.

**Evidence:** [evidence/review-web/verification.md#c-web-03](evidence/review-web/verification.md#c-web-03)

### F-118

**Repro / observation**
1. In **Create server** → Network, tick **Enable tunnel**, choose frp, and enter a token, a server address and one port mapping. Click **Create server**.
2. `POST /servers` carries `spec.networking.tunnel.credentialsSecretRef.name: "<name>-tunnel-auth"`. That Secret doesn't exist yet.
3. `validateAndProtectGameServer` calls `isServerOwnedSecret`. The Secret is missing, so it returns false, and the API answers 403.
4. A body with the ref left out fails too: the CEL rule rejects it with 422.

**Expected:** A tunnel-enabled create succeeds and gets its credentials attached. One way is to create with the tunnel disabled, PUT tunnel credentials, then enable it.

**Actual:** Every wizard create with a tunnel enabled is refused, and the error blames the user's role.

**Evidence:** [evidence/review-web/verification.md#c-web-04](evidence/review-web/verification.md#c-web-04)

### F-119

**Repro / observation**
1. On a server with no tunnel, open Settings → Networking and switch on **Enable tunnel**. Fill in the frp address and one port mapping.
2. Enter a token and click **Save credential**. The API creates the Secret, then merge-patches `spec.networking.tunnel.credentialsSecretRef` onto the live object. The live object has no `tunnel.provider`, so the apiserver rejects the patch.
3. Instead, type the token and click **Save changes** without **Save credential**. Validity passes. The PUT carries no `credentialsSecretRef`, so the CEL rule rejects it.
4. If the live object already had a disabled tunnel block with a provider, step 2 succeeds. The draft still has no ref, though, so step 3's PUT is rejected.

**Expected:** The Networking section can turn a tunnel on, with provider config and credentials.

**Actual:** Every dashboard path ends in a validation error. Together with F-118, a tunnel can be set up only with kubectl.

**Evidence:** [evidence/review-web/verification.md#c-web-05](evidence/review-web/verification.md#c-web-05)

### F-120

**Repro / observation**
1. Create a can-start share link for a sleeping test server, where the pod needs more than about two minutes to become Running.
2. Open `/share/<token>` signed out and click **Start server**. The resolve and the start spend 2 of the 20 tokens.
3. The page polls `GET /shares/<token>` every 2 s. That spends 0.5 tokens/s against a refill of 0.33 tokens/s, so the bucket runs out after about 108 s.
4. The next poll gets 429, and the page switches to "Link not available" and stops polling.

**Expected:** The page stays on "waking up" for the whole wake. It could poll at or below the limiter's refill rate, or back off on 429 instead of treating it as invalid.

**Actual:** A wake that takes longer than about 110 s, or several viewers behind one IP, ends on the invalid-link message while the link is valid and the server is still starting.

**Evidence:** [evidence/review-web/verification.md#c-web-06](evidence/review-web/verification.md#c-web-06)

### F-121

**Repro / observation**
1. Create a new server. ServerDetail opens on the Logs tab while the pod-log stream is still refused, so `openWS` is waiting in backoff (up to 30 s).
2. Switch to Overview. `LogsTab` cleanup calls `sock.close()`. The current socket is already closed, and the scheduled timer is left in place.
3. When the timer fires, `connect()` opens a new WebSocket. The network panel shows it opening after the tab has gone. Once the pod is up it keeps streaming into the unmounted tab's callbacks.
4. Each tab switch during a backoff window adds one more connection. The retry loop on each leaked socket ends when that socket closes.

**Expected:** `close()` cancels any pending reconnect, and `connect()` does nothing after `close()`.

**Actual:** Orphaned log and console connections stay open until the server side closes them.

**Evidence:** [evidence/review-web/verification.md#c-web-07](evidence/review-web/verification.md#c-web-07)

### F-122

**Repro / observation**
1. Open Settings → General and change the description. `settingsDirty` becomes `true`, and polling of `["server", name, ns]` stops.
2. Click the **Overview** tab. SettingsTab unmounts, the edit is discarded, and no prompt appears.
3. `settingsDirty` stays `true`, so the header phase chip, the uptime, and the Overview cards stop updating. SSE watch events invalidate only `["servers"]`, which doesn't match.

**Expected:** Navigation away without save prompts user. Polling resumes once the form is gone.

**Actual:** The edits are lost without a prompt, and the server page shows stale state.

**Evidence:** [evidence/review-web/verification.md#c-web-08](evidence/review-web/verification.md#c-web-08)

### F-123

**Repro / observation**
1. Settings → Share links: create a link with **No expiry**, then **Revoke** it.
2. The list reloads.
3. The row is still listed with status "Active", and **Revoke** is still offered. The API returns the row with no revoked field.

**Expected:** The table shows "Status (Active/Expired/Revoked)".

**Actual:** "Revoked" never renders, so a revoked link looks active to its owner. The token is correctly refused on lookup.

**Evidence:** [evidence/review-web/verification.md#c-web-09](evidence/review-web/verification.md#c-web-09)

### F-124

**Repro / observation**
1. Run `git grep -n "gameplane.local/pinned\|gameplane.local/gpu"`. The only matches are `CreateServer.tsx` and its test.
2. In Create server → Configure, choose **Pin to node** or **GPU-enabled**, and create.
3. The GameServer gets `spec.nodeSelector`, which the operator copies onto the pod template. The pod gets stuck Pending.
4. Settings → Placement edits only tolerations and affinity, so the dashboard has no way to remove the selector.

**Expected:** The option either produces a working placement, or the docs name the label an admin must apply.

**Actual:** On a default cluster, the server never starts.

**Evidence:** [evidence/review-web/verification.md#c-web-10](evidence/review-web/verification.md#c-web-10)

### F-125

**Repro / observation**
1. Save custom CSS that visibly changes the layout, then load `/?safe-mode=1`. The banner shows, and custom CSS is disabled.
2. Click **Open Appearance Settings** on the banner. The app navigates to `/settings/theme`, and the query parameter is gone.
3. ThemeSettings' preview effect calls `applyThemePreferences(draft)`. Safe mode is now false, so custom CSS is injected again.

**Expected:** Safe mode "suspends the custom CSS overlay for the session". The URL parameter is "the guaranteed path".

**Actual:** Safe mode entered through the URL ends at the first in-app navigation, including the banner's own link to the page needed for the fix.

**Evidence:** [evidence/review-web/verification.md#c-web-11](evidence/review-web/verification.md#c-web-11)

### F-126

**Repro / observation**
1. As a viewer, click **Start** on a Servers row. The API returns 403, and the page shows nothing.
2. In Users & RBAC → Roles, create a role with a name that already exists. The API returns 409, the modal stays open, and no message appears.
3. Delete a capture while the capture sidecar is unreachable: the dialog stays open with no message.

**Expected:** Failures are shown, as the neighbouring flows do.

**Actual:** These failures are silent.

**Evidence:** [evidence/review-web/verification.md#c-web-12](evidence/review-web/verification.md#c-web-12)

### F-127

**Repro / observation**
1. Run `git grep -n -i defaultNamespace -- ':!*_test.go'`. It's read only by the validator, web types, and fixtures.
2. Set Admin Settings → General → Default namespace to an allowed extra namespace and save.
3. Create a server in the wizard. `POST /servers` carries no namespace, so the API uses `gameplane-games`.

**Expected:** The setting does what its hint says, or the field is removed or relabelled.

**Actual:** New servers always land in `gameplane-games`.

**Evidence:** [evidence/review-web/verification.md#c-web-13](evidence/review-web/verification.md#c-web-13)

### F-128

**Repro / observation**
1. On `/modules`, click **Manage sources**. The URL becomes `/admin#modules`.
2. The page opens on the General section, because `initialSection` reads only `?section=`.

**Expected:** The Module sources section opens.

**Actual:** The General section opens.

**Evidence:** [evidence/review-web/verification.md#c-web-14](evidence/review-web/verification.md#c-web-14)

### F-129

**Repro / observation**
1. Open the top-bar cluster selector and choose **Add cluster**. It navigates to `/cluster`.
2. That page has no cluster registration UI, and the web client has no call to register a cluster.

**Expected:** A registration flow exists, or the item is removed or relabelled.

**Actual:** The item leads to a page that can't add a cluster.

**Evidence:** [evidence/review-web/verification.md#c-web-15](evidence/review-web/verification.md#c-web-15)

### F-130

**Repro / observation**
1. With a server in an extra namespace, open `/servers`. Its row under "Shared with you" has an empty Actions cell: no Start, Stop or Restart.
2. The guard's comment says "detail route and lifecycle calls are namespace-blind". The detail route is no longer namespace-blind, and `ServerActionsMenu` also passes the namespace. The page's own `act` mutation, though, calls `Servers.lifecycle` with no `ns`.

**Expected:** Inline lifecycle buttons and an action menu on each row, with the calls carrying the row's namespace.

**Actual:** Those rows have no actions. The detail page header works as a workaround.

**Evidence:** [evidence/review-web/verification.md#c-web-16](evidence/review-web/verification.md#c-web-16)

### F-131

**Repro / observation**
1. Settings → Lifecycle: turn on idle auto-sleep and set "Sleep after" to `3`. The inline error appears, and the draft holds the invalid value.
2. **Save changes** stays enabled. Clicking it sends the PUT, the CRD rejects it, and the footer shows the API error.
3. An out-of-range grace period, invalid env name, or invalid storage size behave the same way.

**Expected:** Invalid sections disable Save, as Networking, Network capture and Placement do through `onValidityChange`.

**Actual:** Save stays enabled, and the request fails at the API.

**Evidence:** [evidence/review-web/verification.md#c-web-17](evidence/review-web/verification.md#c-web-17)

### F-132

**Repro / observation**
1. Settings → Placement: edit the affinity JSON, then click **Discard**. The draft goes back to the baseline.
2. The editor still shows the edited JSON.
3. Type one more character. The edited JSON is brought back.

**Expected:** After Discard, the editors show the draft.

**Actual:** They show stale text, and the next keystroke brings back the discarded edit.

**Evidence:** [evidence/review-web/verification.md#c-web-18](evidence/review-web/verification.md#c-web-18)

### F-133

**Repro / observation**
1. Users & RBAC → **Invite user**. Enter username `alice` and a password, then **Create user**. The user is created, and the dialog closes.
2. Click **Invite user** again. `alice` and the masked password are still filled in.
3. Submitting again gives a 409.

**Expected:** A fresh form each time the dialog opens.

**Actual:** The previous entry, including the password, is still there.

**Evidence:** [evidence/review-web/verification.md#c-web-19](evidence/review-web/verification.md#c-web-19)

### F-134

**Repro / observation**
1. Open Users & RBAC → Identity providers. It reads: "OIDC identity providers configured in Helm values appear here." No list follows.
2. Admin Settings → Authentication already adds, enables and deletes OIDC, Google and GitHub providers.

**Expected:** The tab lists providers or points to Admin Settings → Authentication.

**Actual:** The copy contradicts the shipped feature, and nothing is listed.

**Evidence:** [evidence/review-web/verification.md#c-web-20](evidence/review-web/verification.md#c-web-20)

### F-135

**Repro / observation**
1. Open Admin → System logs → API server.
2. Each line is slog JSON with `"time":…`. The parser reads only `ts`, so the timestamp column is empty, and `time=…` appears among the trailing structured fields.

**Expected:** Timestamps render for all components.

**Actual:** API server lines have no timestamp.

**Evidence:** [evidence/review-web/verification.md#c-web-21](evidence/review-web/verification.md#c-web-21)

### F-136

**Repro / observation**
1. Players → Ban a player → confirm.
2. While the request is pending, the button reads "Baning…".

**Expected:** "Banning…".

**Actual:** "Baning…".

**Evidence:** [evidence/review-web/verification.md#c-web-22](evidence/review-web/verification.md#c-web-22)

### F-137

**Repro / observation**
1. Get Settings into the conflict state, then make `GET /servers/<name>` fail. Click **Reload**.
2. The async `reload` rejects with no handler: the console shows "Uncaught (in promise)", and no message appears.
3. Other handlers also return promises that nothing handles. `no-misused-promises` isn't enabled.

**Expected:** CLAUDE.md rule 5: "Handle all promises: `await` or prefix with `void`".

**Actual:** Four handlers leave their promises unhandled. The Reload one fails silently.

**Evidence:** [evidence/review-web/verification.md#c-web-23](evidence/review-web/verification.md#c-web-23)

### F-138

**Repro / observation**
1. Run `grep -rn --exclude='*.test.*' "<symbol>" web/src` for several exported symbols and custom error types. Each has only its definition.
2. The audit-log label branch can't be reached because a more general route matches first.
3. Several guards are always true.

**Expected:** Unused code is removed or wired up, and reachable branches have guards that matter.

**Actual:** Dead code suggests features that don't exist, and audit events are mislabelled.

**Evidence:** [evidence/review-web/verification.md#c-web-24](evidence/review-web/verification.md#c-web-24)

### F-139

**Repro / observation**
1. `web/specs.md` says several UI features, utilities and pages are "NOT implemented". Reading the source finds all of them exist.
2. It says feature 017 expiry UI is "not yet implemented". `ShareLinks.tsx` has the six choices.
3. `Dashboard.tsx` is described as a loading skeleton with a TODO. It renders full content with no TODO.

**Expected:** specs.md describes what shipped, with historical notes marked as resolved.

**Actual:** It says implemented features don't exist and that route files are broken.

**Evidence:** [evidence/review-web/verification.md#c-web-25](evidence/review-web/verification.md#c-web-25)

### F-140

**Repro / observation**
1. `web/specs.md` lists components, helpers and UI features that don't exist in the shipped code: StatCard and FilterPopover on Backups, daily/weekly radios, a Clone button on Danger, node-selector builder on Placement, several lib helpers.
2. The description of ScheduleForm's structure doesn't match what shipped.

**Expected:** specs.md lists only what exists.

**Actual:** It describes UI and helpers the code doesn't have.

**Evidence:** [evidence/review-web/verification.md#c-web-26](evidence/review-web/verification.md#c-web-26)

### F-141

**Repro / observation**
1. `web/specs.md:5` says "Vite 5.4 + React 18.3 + TypeScript 5.6". `web/package.json` has `react ^19.3.0`, `typescript ^6.0.3`, `vite ^8.3.0`, and other newer versions.
2. `CLAUDE.md:78` and `:262` say "React 18". The F-030 fix doesn't touch this wording.
3. `web/specs.md` line references to `api.ts` and `ServerDetail.tsx` no longer match the file.
4. `:210` says "nine sub-views" and then lists 11. `:358` says "11 sections" but Settings has 12.

**Expected:** Current versions, counts and line references.

**Actual:** Stale versions, counts and line references.

**Evidence:** [evidence/review-web/verification.md#c-web-27](evidence/review-web/verification.md#c-web-27)

### F-142

**Location:** `web/src/routes/AdminSettings.tsx:307-315` (identity provider removal), `:974-982` (mod-registry key removal), `:1460-1468` (notification sink removal); `useSectionForm` at `:197-224`.

**Control:** The lifecycle of the API-managed Secrets behind dashboard-managed identity providers, keyed mod registries and notification sinks. A Secret and the config row that references it should change together.

**Repro / observation** (defensive: confirms whether the control holds):
1. Each trash button does two things: calls `deleteSecret` at once, and changes only the section's local draft.
2. `useSectionForm.save` is the only path that PUTs the section config. Leaving the page or a failed validation leaves the stored config unchanged.
3. The add forms work the other way round: they write the Secret before the row exists in the saved config.

**Expected:** A Secret is deleted only as part of, or after, a successful save of the config change that stops referencing it. A cancelled add doesn't leave a Secret.

**Actual:** Removing a row and then leaving without saving keeps the row in the stored config while its Secret is already gone. The dependent feature stops working until the secret is entered again.

**Evidence:** [verification-web.md#hc-web-01](verification-web.md#hc-web-01)

### F-143

**Location:** `web/src/routes/AdminSettings.tsx:197-199` (`useSectionForm` seeds its draft once), `:130` (`AuthSection` is not keyed), `:1748-1757` (`handleReset`); `api/internal/handlers/config.go:102-156` (`put`), `:159-250` (`resetRoleMapping`).

**Control:** The OIDC role-mapping overrides, which decide which IdP groups get admin, operator or viewer. "Reset to Helm default" should remove an override for good.

**Repro / observation** (defensive: confirms whether the control holds):
1. `handleReset` calls `DELETE /admin/config/auth/role-mappings/{role}`, and on success the API removes the override.
2. `useSectionForm` ignores new `initial` values (`useState(initial)`), so `f.draft.helmOverride` still holds the removed mapping.
3. Any later "Save changes" in the Authentication card PUTs `f.draft`, and `config.go:put` stores it, putting the override back. The reset only logs to console on failure.

**Expected:** After a reset, the card and the draft show the Helm value. No later save brings the override back.

**Actual:** The reset looks as if nothing happened, and the next save of the Authentication section re-applies the override the admin just reset, including an admin-group mapping. This is an unintended change to authorization config.

**Evidence:** [verification-web.md#hc-web-02](verification-web.md#hc-web-02)

### F-144

**Location:** `web/src/lib/verify.ts:48-51` (`verifyForEntry`, installed branch), `:22-24` (the contract comment); `web/src/components/modules/ModuleCard.tsx:249-265` (`VerifyBadge`); `operator/internal/controller/module_controller.go:100-107` (the early return for a converged module).

**Control:** The cosign signature-verification posture that the Modules catalog shows for installed modules. A solid "verified" chip should mean that the installed digest was signature-checked.

**Repro / observation** (defensive: confirms whether the control holds):
1. For an installed entry, `verifyForEntry` returns `enforced: mode !== "none"`, where `mode` comes from the `installedFrom` source's *current* `spec.verify`.
2. `VerifyBadge` renders the solid "verified" chip whenever `enforced` is true.
3. The Module reconciler returns early when the module is converged. A verify policy added to a source after install doesn't make the operator pull or verify the installed bundle again. Nothing on the Module status records whether a verification actually ran.

**Expected:** The solid "verified" state appears only when the operator has actually verified the installed digest, as recorded on the Module status. A policy added later shows as the softer "policy" state.

**Actual:** A module installed while its source had no verify policy shows "verified" as soon as a policy is added to that source.

**Evidence:** [verification-web.md#hc-web-03](verification-web.md#hc-web-03)

### F-145

**Location:** (a) `web/src/routes/ServerDetail.tsx:133-138` (the Capture tab is always visible), `web/src/components/CaptureWidget.tsx:180-203`. (b) `web/src/routes/tabs/Mods.tsx:68`, `web/src/lib/auth.ts:34-42`. For comparison, `web/src/routes/tabs/settings/NetworkCapture.tsx:53` and `web/src/components/server/ServerActionsMenu.tsx:37-39` pass a namespace.

**Control:** The dashboard's permission gating. The API is still the enforcer. The UI should show a control exactly when the API would accept the action, using the same permission and namespace.

**Repro / observation** (defensive: confirms whether the control holds):
1. (a) `CaptureWidget` renders "Enable Capture", "Start Capture" and "Delete" with no `can(me, "captures:manage", ns)` check. The Settings → Network capture section does gate on it.
2. (b) `can(me, perm)` with no namespace checks only `perms["*"]`. A user whose `servers:write` comes only from a binding in `gameplane-games` gets `false`, even for a server in `gameplane-games`, which the API authorizes against that namespace's binding.

**Expected:** Each UI gate uses the permission and namespace the API checks for that action: `captures:manage` in the server's namespace for capture controls, and `servers:write` in the server's namespace for mods and actions.

**Actual:** (a) shows capture controls to users the API will refuse; the refusal is displayed as an error. (b) disables mod and action controls for namespace-scoped operators whose actions the API would accept.

**Evidence:** [verification-web.md#hc-web-04](verification-web.md#hc-web-04)

### F-146

**Repro / observation**
1. Read `netguard/specs.md:8` and `netguard/netguard.go:6-21`: both state "Used by both the operator and the agent via two deliberately different policies".
2. Grep for all netguard call sites in Go code outside the module itself: operator git/http module sources use `IsAllowed`; agent mod downloads use `IsPublic`; agent WebSocket RCON uses `IsAllowed` with loopback target; API notification sinks use `IsAllowed`; API Steam resolver uses `IsPublic`.
3. Check `CLAUDE.md:56,250` and `docs/dependencies.md:333-338`: all describe the split as operator → `IsAllowed`, agent → `IsPublic`, but they omit the API's use of both and the agent's `IsAllowed` call site.

**Expected:** The netguard spec (Purpose and References), package doc, `CLAUDE.md:56,250` and `docs/dependencies.md` name every importer and which policy it uses:
- operator git/http module sources: `IsAllowed`
- agent mod downloads: `IsPublic`
- agent WebSocket RCON: `IsAllowed`
- API notification sinks: `IsAllowed`
- API Steam resolver: `IsPublic`

**Actual:** The docs describe a strict two-way split. The code has two more call-site groups, with the API importing both policies and the agent using both.

**Evidence:** [evidence/review-netguard/verification.md#c-netguard-01](evidence/review-netguard/verification.md#c-netguard-01)

### F-147

**Location:** `netguard/netguard.go:95-110` (`IsAllowed`); the promise is at `netguard/netguard.go:1-4`, `netguard/specs.md:8,76` and `docs/security.md:308-313`.

**Control:** `IsAllowed` is the permissive dial-time address policy for admin-configured git/http ModuleSource fetches, notification-sink delivery, and the agent's loopback WebSocket RCON. It is meant to refuse cloud instance-metadata endpoints while keeping RFC 1918, ULA, loopback and CGNAT reachable for self-hosted endpoints.

**Repro / observation** (defensive; confirms whether the control holds):
1. Read `netguard/netguard.go:99-108`. `IsAllowed` refuses unspecified, multicast, interface-local multicast, link-local unicast and link-local multicast addresses, plus `64:ff9b::/96` and `2002::/16`. Nothing else is refused.
2. `fd00:ec2::254` (AWS EC2 IPv6 instance-metadata address, inside ULA) and `100.100.100.200` (Alibaba Cloud ECS metadata address, inside CGNAT) both return `true` from `IsAllowed`.
3. `IsPublic` correctly refuses both addresses. Only the permissive policy is affected.

**Expected:** `IsAllowed` refuses known instance-metadata addresses by exact match: at least `169.254.169.254` (already refused via link-local), `fd00:ec2::254` and `100.100.100.200`. RFC 1918, ULA, loopback and the rest of CGNAT stay allowed. `SECURITY_AUDIT.md` should say only `IsPublic` refuses CGNAT.

**Actual:** Both addresses pass `IsAllowed`, so on those clouds the policy doesn't enforce its stated metadata refusal for admin-configured ModuleSources and notification sinks. Whether a given pod can reach those endpoints depends on the cloud's IMDS settings and the cluster's CNI.

**How a maintainer confirms the control holds:** Add `{"fd00:ec2::254", false}` and `{"100.100.100.200", false}` to the `TestIsAllowed` table (`netguard_test.go:14-35`) and let CI run it. Today both rows fail. The fix holds when they pass and the existing `10.0.0.1`, `fc00::1` and `127.0.0.1` → `true` rows still pass. Then read `SECURITY_AUDIT.md` and check that it no longer says `IsAllowed` refuses CGNAT.

**Evidence:** [verification-netguard.md#h-netguard-01](verification-netguard.md#h-netguard-01)

### F-148

**Location:** `netguard/netguard.go:117-132` (`IsPublic`), `:77-89` (`reservedBlocks`), `:56-61` (`normalize`), and `:65-68` (`blockedV6Prefixes`); the promise is at `netguard/specs.md:41-42`, `netguard/netguard.go:14-18` and `docs/security.md:344`.

**Control:** `IsPublic` is the strict dial-time policy for agent mod downloads (after the `allowedHosts` check) and the API's Steam resolver, documented to allow "only globally routable unicast addresses". It is built as a denylist.

**Repro / observation** (defensive; confirms whether the control holds):
1. Read `netguard/netguard.go:121-131`. Go's `IsUnspecified` matches only `0.0.0.0`, and `reservedBlocks` has no `0.0.0.0/8` entry, so `IsPublic(0.0.0.1)` returns `true`.
2. Read `netguard/netguard.go:56-61`. `normalize` relies on `To4()`, which unwraps only `::ffff:0:0/96`. IPv4-compatible addresses (`::7f00:1`, `::a9fe:a9fe`) stay 16 bytes and `IsPublic` returns `true`.
3. `reservedBlocks` (`:85`) and `blockedV6Prefixes` (`:66`) list only well-known NAT64 prefix `64:ff9b::/96`. The RFC 8215 local-use prefix `64:ff9b:1::/48` is not listed, so `IsPublic(64:ff9b:1::a9fe:a9fe)` returns `true`. Also `100::/64` (RFC 6666 discard-only) is not listed, so `IsPublic(100::1)` returns `true`.
4. Reachability: mod downloads must first pass `allowedHosts`, and each redirect is checked again. No path to an internal host was shown in a default Linux netns.

**Expected:** `IsPublic` refuses every IANA special-purpose block marked not globally reachable: at least `0.0.0.0/8`, `::/96`, `64:ff9b:1::/48` and `100::/64`. `IsAllowed` also gets `64:ff9b:1::/48` and an unwrap-and-recheck of `::/96`. Alternatively, narrow the "only globally routable" wording to the ranges that are actually refused.

**Actual:** Each listed address passes `IsPublic`, and the `::/96` and `64:ff9b:1::/48` forms also pass `IsAllowed`. No path to an internal host was shown in a default install, so this is a gap between the documented contract and the denylist.

**How a maintainer confirms the control holds:** Add `0.0.0.1`, `::7f00:1`, `::a9fe:a9fe`, `64:ff9b:1::a9fe:a9fe` and `100::1` to the `blocked` list in `TestIsPublic` (`netguard_test.go:54-66`). Add `{"64:ff9b:1::a9fe:a9fe", false}` and `{"::a9fe:a9fe", false}` to `TestIsAllowed` (`:14-35`). CI must pass with these rows. Existing public rows (`8.8.8.8`, `2606:4700:4700::1111`) must stay allowed, and `10.0.0.1`, `fc00::1` and `127.0.0.1` must stay allowed by `IsAllowed`.

**Evidence:** [verification-netguard.md#h-netguard-02](verification-netguard.md#h-netguard-02)

### F-149

**Repro / observation**
1. Read `gameaction/action.go:78-80`: parameter value length check is `len(val) > 512`, where Go's `len` on a string counts bytes, not characters.
2. Test with multi-byte UTF-8: `Resolve` with 300 × `é` (600 bytes) rejects with "too long (max 512)"; 171 × `中` (513 bytes) also rejects; 170 × `中` (510 bytes) passes.
3. Check `gameaction/action.go:29`, `gameaction/specs.md:20,81,113` and the error message: all say "512 characters" or "512-char cap".

**Expected:** Code and docs agree on the unit. Either the check counts characters (`utf8.RuneCountInString(val) > 512`), or specs and error message say "512 bytes".

**Actual:** The docs say 512 characters, but the code enforces 512 bytes. A non-ASCII value of 257–512 two-byte characters, or 171–512 three-byte characters, is rejected when it should pass.

**Evidence:** [evidence/review-gameaction/verification.md#c-gameaction-01](evidence/review-gameaction/verification.md#c-gameaction-01)

### F-150

**Repro / observation**
1. Read `gameaction/action.go:34-40`: the default is substituted before the required check, so `if !ok || val == "" { val = p.Default }` runs at line 35, and the required check is at line 38.
2. Test with `Resolve([]Param{{Name: "m", Type: "string", Required: true, Default: "hello"}}, map[string]string{"m": ""})`: returns `map[m:hello]` with no error. With `"  "` (whitespace only) it returns an error.
3. Check `gameaction/specs.md:117`: says "A param with `Required: true` rejects empty or whitespace-only values, even with a default", but the last sentence of the same paragraph describes the actual behaviour ("a missing value is filled with the default").

**Expected** (`specs.md:117`): Code and spec agree. Either the spec is reworded to match the code (empty value → default, whitespace-only → rejected), or the code is fixed to reject both.

**Actual:** An empty value is silently replaced by the default and accepted. Only whitespace-only is rejected. The paragraph contradicts itself.

**Evidence:** [evidence/review-gameaction/verification.md#c-gameaction-02](evidence/review-gameaction/verification.md#c-gameaction-02)

### F-151

**Repro / observation**
1. Read `gameaction/specs.md:121`: "rendering always produces the same output (modulo non-determinism in the template itself, e.g., `rand`)."
2. Read `gameaction/action.go:104-110`: the template engine has no function map registered. Go's `text/template` builtins include no random function.
3. Test `gameaction.Compile("x", "say {{rand}}")`: returns error "function "rand" not defined".

**Expected:** Invariant 7 (determinism) holds because no non-deterministic function is available.

**Actual:** The spec names a function that doesn't exist and implies module authors can use one.

**Evidence:** [evidence/review-gameaction/verification.md#c-gameaction-03](evidence/review-gameaction/verification.md#c-gameaction-03)

### F-152

**Repro / observation**
1. Read `gameaction/specs.md:5`: "**Dependencies:** stdlib only (Go 1.25+)".
2. Read `gameaction/go.mod:3`: `go 1.26.0`.
3. All 15 modules in the workspace declare `go 1.26.0`.

**Expected:** The spec header states the minimum Go version from `go.mod`, which is 1.26.

**Actual:** The header says 1.25+. Related stale version claims also in `gameproto/specs.md:5`, `test/e2e/internal/specs.md:5,262` and `audit-syslog-bridge/specs.md:55`.

**Evidence:** [evidence/review-gameaction/verification.md#c-gameaction-04](evidence/review-gameaction/verification.md#c-gameaction-04)

### F-153

**Repro / observation**
1. Read `gameproto/minecraft.go:69-76`: a packet ID other than 0x00 returns an error via `Classify`, which wraps it as `(nil, err)`.
2. Read `gameproto/terraria.go:87-91`: a ConnectRequest parse error returns `(nil, err)`.
3. Test `mc.Classify([]byte{0x01, 0x02})` and `tc.Classify([]byte{0x03, 0x00, 0x01})`: both return error, not an Unknown result.
4. Check `gameproto/specs.md:295` and `:314`: both say "returns Unknown" for error cases.

**Expected** (spec text): Parse errors return Unknown (a non-nil result with Consumed).

**Actual:** Both cases return error with nil result. Only non-ConnectRequest Terraria types or Minecraft with non-login next_state return non-error Unknown.

**Evidence:** [evidence/review-gameproto/verification.md#c-gameproto-03](evidence/review-gameproto/verification.md#c-gameproto-03)

### F-154

**Repro / observation**
1. Read `gameproto/terraria.go:152-173` (`readTerrariaString`): bounds a string only by the remaining payload (max 65,532 bytes).
2. Test with a Terraria frame whose version string is 40,000 bytes: `Classify` accepts it and returns result with 40,000-byte version.
3. Check `gameproto/specs.md:348`: states "32KB max (derived from the 7-bit-encoded int max for a single message)".

**Expected** (`specs.md:348`): Version string is bounded to 32 KB or states the actual bound (remaining frame payload).

**Actual:** There is no 32 KB cap. Terraria string is bounded only by remaining payload. The derivation is also wrong: a 7-bit-encoded int can represent up to 2^31-1.

**Evidence:** [evidence/review-gameproto/verification.md#c-gameproto-04](evidence/review-gameproto/verification.md#c-gameproto-04)

### F-155

**Repro / observation**
1. Read `gameproto/classifier.go:43-45` and `gameproto/specs.md:101-103`: contract says error if payload "is malformed or oversized".
2. Read `gameproto/minecraft.go:122-135`: checks only the length, nothing checks JSON validity.
3. Test `mc.BuildStatusResponse("{")`: returns 4 bytes and nil error. The only production caller is `sentinel/main.go:523`, which passes a constant valid JSON string.

**Expected:** Implementation rejects malformed payload (e.g., with `json.Valid`), or the contract says it only rejects oversized payload.

**Actual:** Malformed payload is framed and returned without error. No effect today.

**Evidence:** [evidence/review-gameproto/verification.md#c-gameproto-05](evidence/review-gameproto/verification.md#c-gameproto-05)

### F-156

**Repro / observation**
1. Read `gameproto/gameproto.go:27-38` (package doc): the example calls `registry.Lookup("minecraft")`.
2. There is no `registry` package; `Lookup` is in `gameproto` package at `gameproto/registry.go:22`, returning `(Classifier, bool)`.
3. The example doesn't assign the ok value and refers to a nonexistent package.

**Expected:** Example reads `classifier, ok := gameproto.Lookup("minecraft")` with an ok check, and ideally the package doc names the sentinel as the consumer.

**Actual:** The example is invalid Go code.

**Evidence:** [evidence/review-gameproto/verification.md#c-gameproto-06](evidence/review-gameproto/verification.md#c-gameproto-06)

### F-157

**Repro / observation**
1. Read `gameproto/specs.md:43,379`: describe `gameproto_test.go` as integration tests; the file has 28 lines and one test (`TestKindString`).
2. The layout tree (`:31-47`) omits `classifier_golden_test.go` (559 lines) and `demo_test.go` (143 lines).
3. Dependency list (`:406-414`) omits `sort` (in `registry.go:3`).
4. References cite `test/e2e/tests/bot_*.go` (directory doesn't exist); actual tests are `test/e2e/minecraft_bot_e2e_test.go`, `terraria_bot_e2e_test.go`, `wake_on_connect_e2e_test.go`.
5. Line 5 says "Go 1.25+" but `go.mod:3` says `go 1.26.0`.

**Expected:** Layout, testing, dependencies and references sections describe the files that exist. Header states Go 1.26.

**Actual:** Five mismatches: test file description, layout tree, stdlib list, path references, and Go version.

**Evidence:** [evidence/review-gameproto/verification.md#c-gameproto-07](evidence/review-gameproto/verification.md#c-gameproto-07)

### F-158

**Repro / observation**
1. Read `gameproto/specs.md:277`: "**That's it.** No changes to sentinel/main.go, gameproto.go, or any shared code."
2. Read `operator/api/v1alpha1/gametemplate_types.go:886`: `WakeProtocol` has `// +kubebuilder:validation:Enum=minecraft;terraria;generic;none`.
3. Apply a GameTemplate with `wakeProtocol: demo`: rejected with `Unsupported value: "demo"` error, even though the sentinel accepts `demo` as a registered classifier.

**Expected:** Either "Adding a New Protocol" section includes extending the `WakeProtocol` enum and running `make generate && make manifests`, or the section states plainly that listed steps only register the sentinel classifier, and the CRD still gates which protocols a template can select.

**Actual:** The spec says no other change is needed. A maintainer following it gets a classifier that no GameTemplate can select.

**Evidence:** [evidence/review-gameproto/verification.md#c-gameproto-08](evidence/review-gameproto/verification.md#c-gameproto-08)

### F-159

**Repro / observation**
1. `cd gp-module && go build -o /tmp/gp-module ./cmd/gp-module`, then from the repo root run `/tmp/gp-module validate modules/terraria modules/7-days-to-die`. You get 7 errors of the form `ERROR [invalid-config-type] template.yaml:211:7: configSchema enum field "AUTOCREATE" must specify a non-empty options list`.
2. `modules/terraria/template.yaml:211-216` declares that field as `type: enum` with `enum: ["1", "2", "3"]` and `default: "2"`.
3. The CRD field is `Enum []string json:"enum,omitempty"` (`operator/api/v1alpha1/gametemplate_types.go:1083`). In the generated `operator/config/crd/gameplane.local_gametemplates.yaml`, `spec.configSchema.items.properties` has `enum` and no `options`.

**Expected:** The validator checks the CRD's `enum:` list, as FR-008 ("against … `GameTemplate` CRD schemas") and FR-012 ("enum lists") require.

**Actual:** CRD-correct enum fields fail validation (12 errors across 5 shipped modules), and the only spelling that passes produces a template whose enum field rejects every value.

**Evidence:** [evidence/review-gp-module/verification.md#c-gp-module-01](evidence/review-gp-module/verification.md#c-gp-module-01)

### F-160

**Repro / observation**
1. Scaffold a scratch module (`/tmp/gp-module init tbool --archetype generic -y` in a scratch directory). Append a `configSchema` entry `{name: HARDCORE, type: boolean, default: "true"}` to `spec`.
2. `/tmp/gp-module validate modules/tbool` prints `OK (no findings)`.
3. The CRD restricts `spec.configSchema[].type` to `string;int;bool;enum;password` (`operator/api/v1alpha1/gametemplate_types.go:1073`). The Module reconciler creates the GameTemplate from the typed struct, and the API server rejects `type: boolean` with `Unsupported value`.

**Expected:** The accepted set equals the CRD enum. The message lists `bool`, the only boolean spelling the CRD accepts.

**Actual:** `boolean` passes `validate` and the builder export gate, and the install then fails.

**Evidence:** [evidence/review-gp-module/verification.md#c-gp-module-02](evidence/review-gp-module/verification.md#c-gp-module-02)

### F-161

**Repro / observation**
1. In a scratch copy of a scaffolded generic module, remove `name: game` from the only port. `validate` prints `OK (no findings)`.
2. In another copy, remove `spec.displayName` and `spec.version`. `validate` prints `OK (no findings)`.
3. The generated CRD requires `spec.ports[].name` and `spec: [displayName, game, image, version]`. The API server rejects both templates when the Module reconciler creates the GameTemplate.

**Expected:** The validator detects 100% of schema violations against `gametemplate.schema.json`. The CRD-required fields are checked.

**Actual:** CRD-required fields can be missing and the module still validates clean. The first report comes from the API server at install.

**Evidence:** [evidence/review-gp-module/verification.md#c-gp-module-03](evidence/review-gp-module/verification.md#c-gp-module-03)

### F-162

**Repro / observation**
1. `/tmp/gp-module preview modules/minecraft-java --json` reports `versionId: "default"` with image `itzg/minecraft-server:java21@sha256:f7155587…`, and `effectiveEnv` has no `TYPE` and no `VERSION`.
2. `modules/minecraft-java/template.yaml:121-126` marks `1.21.4-paper` as `default: true`.
3. The operator's `resolveVersion` picks the entry marked default, and the game container gets that version's image and env.
4. `/tmp/gp-module preview modules/minecraft-java --version-id 1.21.4-paper --json` shows `TYPE: PAPER` and `VERSION: 1.21.4`.

**Expected:** Preview simulates "the Gameplane operator's config materialization and environment variable synthesis". With no version choice, it applies the version the operator would apply.

**Actual:** A preview with no version choice leaves out the version layer that every real server receives. The dashboard builder's preview always shows that incomplete result.

**Evidence:** [evidence/review-gp-module/verification.md#c-gp-module-04](evidence/review-gp-module/verification.md#c-gp-module-04)

### F-163

**Repro / observation**
1. `docs/module-authoring.md:139-141` says "`template.yaml` is the same `GameTemplate` you would write today, with one difference: omit `metadata.name`".
2. `specs/done_010-easy-module-building/contracts/archetypes-contract.md` §3 says: "The offline validator (`gp-module validate`) requires `metadata.name` to be present and non-empty".
3. Copy a scaffolded module and delete `metadata.name` from `template.yaml`. `validate` reports `ERROR [template-schema-violation] template.yaml:5: metadata.name is required in template.yaml`.

**Expected:** The authoring guide and the spec agree. Under the spec 010 contract, the guide says to include `metadata.name`.

**Actual:** An author who follows the guide gets a validation error.

**Evidence:** [evidence/review-gp-module/verification.md#c-gp-module-06](evidence/review-gp-module/verification.md#c-gp-module-06)

### F-164

**Repro / observation**
1. In a scratch directory, run the documented `gp-module init cs2-match --archetype=steamcmd … --ports="game:27015/udp:adv"`. It exits 1 with `error: invalid port "game:27015/udp:adv": invalid port number "game:27015"`.
2. `make -n module-validate MODULE=modules/cs2-match` prints `bin/gp-module validate modules/modules/cs2-match`, because the target already prefixes `modules/`.
3. `make -n module-preview MODULE=modules/minecraft-java MEMORY=8Gi` prints `bin/gp-module preview modules/modules/minecraft-java --memory 8Gi`. `module-package` has the same doubled prefix.

**Expected:** The quickstart commands run as written. That means a port example in `PORT/PROTOCOL` form and `MODULE=<name>` in the make examples.

**Actual:** The `init` example exits 1, and the three make examples point at `modules/modules/<name>`, which doesn't exist.

**Evidence:** [evidence/review-gp-module/verification.md#c-gp-module-07](evidence/review-gp-module/verification.md#c-gp-module-07)

### F-165

**Repro / observation**
1. Validate any module whose `spec.image` has no digest. The remediation says "Pin image digest using 'gp-module pin'".
2. `/tmp/gp-module pin` prints `error: unknown command "pin"` and exits 2. `cmd/gp-module/main.go` has only `init`, `validate`, `preview` and `package`.
3. `make module-pin` runs `python3 modules/validate.py --pin` and rewrites every module's `template.yaml`, not just the module being authored.

**Expected:** The remediation names a command that exists and matches the diagnostics contract.

**Actual:** It names a subcommand that doesn't exist, and a make target that re-pins the whole catalog.

**Evidence:** [evidence/review-gp-module/verification.md#c-gp-module-08](evidence/review-gp-module/verification.md#c-gp-module-08)

### F-166

**Repro / observation**
1. `cli-contract.md:121` lists `--format <type>` with values `yaml`, `json` and `text`. `/tmp/gp-module preview modules/minecraft-java --format json` exits 2 with "flag provided but not defined". Only `--json` exists.
2. `cli-contract.md:92-99` shows findings as `ERROR [template.yaml:45] [invalid-port-number] …`. The code prints `ERROR [invalid-port-number] template.yaml:45:7: …`.
3. `cli-contract.md:66` says interactive mode is the "default when attached to a TTY and missing arguments". The code has no TTY check.

**Expected:** The CLI matches `cli-contract.md`, or the contract is updated to match the CLI.

**Actual:** There is no `--format`, the human output format differs from the contract, and there is no TTY detection.

**Evidence:** [evidence/review-gp-module/verification.md#c-gp-module-09](evidence/review-gp-module/verification.md#c-gp-module-09)

### F-167

**Repro / observation**
1. `specs.md:5` lists `github.com/ValgulNecron/gameplane/operator/api/v1alpha1` as a dependency. `gp-module/go.mod` doesn't require it.
2. `specs.md:53-55` lists `internal/archetypes/steamcmd.go`, `java.go` and `generic.go`. `ls gp-module/internal/archetypes/` shows only `archetypes.go` and `archetypes_test.go`.
3. `pkg/archetypes/archetypes.go:22` says "PlaceholderIconBytes returns default 128x128 PNG icon bytes." The implementation draws a 256x256 image.

**Expected:** The spec's dependency list, file layout and package doc match the module.

**Actual:** The spec lists one dependency and three files that don't exist, and the package doc gives the wrong icon size.

**Evidence:** [evidence/review-gp-module/verification.md#c-gp-module-10](evidence/review-gp-module/verification.md#c-gp-module-10)

### F-168

**Repro / observation**
1. Copy a scaffolded module and append `gameplaneMinVersion: 9.0.0` to `module.yaml`. `validate` prints `OK (no findings)`.
2. `gp-module --version` prints `1.0.0`. `specs/done_010-easy-module-building/spec.md:102` (Edge Cases): "If a module references a `gameplaneMinVersion` higher than the current tooling version, validation must notify the author".

**Expected:** A warning when `gameplaneMinVersion` is above the version the tool represents.

**Actual:** No notification.

**Evidence:** [evidence/review-gp-module/verification.md#c-gp-module-12](evidence/review-gp-module/verification.md#c-gp-module-12)

### F-169

**Location:** `gp-module/internal/archetypes/archetypes.go:121-122` (the `steamcmd` preset's `Description` and `DefaultImage`); `gp-module/internal/scaffold/scaffold.go` (the rendered `spec` never includes `security`).

**Control:** Privilege defaults for scaffolded modules. The `steamcmd` preset is documented as producing a template that runs the game as a non-root user.

**Repro / observation (defensive; confirms whether the control holds):**
1. Read `archetypes.go:121`: the description ends "(Valve UDP ports, save volume, non-root user)". `specs/done_010-easy-module-building/contracts/archetypes-contract.md:13` lists "non-root security defaults" as a primary characteristic of `steamcmd`.
2. Read `archetypes.go:122`: `DefaultImage` is `cm2network/steamcmd:root@sha256:4d830b…`. The tag names the upstream image's root variant.
3. In a scratch directory, run `gp-module init x --archetype steamcmd -y` and read the generated `template.yaml`. It uses that image and has no `spec.security` block.
4. Read `operator/internal/controller/gameserver_controller.go:1981-1990`. With no `spec.security`, `gameContainerSecurityContext` returns nil, so the pod runs the game as whatever user the image declares.

**Expected:** The `steamcmd` preset produces a template whose game container does not run as root. Either the default image is a non-root variant, or the template carries a `spec.security` block with a non-root uid and gid. Then the preset description and the spec contract are true.

**Actual:** The preset's default is the root image variant and it emits no `spec.security` block, so the documented non-root default is not delivered.

**Evidence:** [verification-gp-module.md#h-gp-module-01](verification-gp-module.md#h-gp-module-01)

### F-170

**Repro / observation**
1. `svcutil/specs.md:101` lists consumer binaries that import svcutil. `grep -rln 'gameplane/svcutil' --include='*.go' .` returns no files.
2. `grep -rn 'svcutil\.[A-Z]' --include='*.go' . | grep -v '^./svcutil/'` returns one line, a comment. `capture-sidecar/go.mod:11-13` has a `replace` directive for svcutil with no matching `require`.
3. The named consumers keep their own copies: `api/cmd/main.go:533`, `agent/cmd/main.go:248`, and others define their own `envOr`/`parseLogLevel` helpers.

**Expected:** The docs match the tree. Either the listed binaries import svcutil, or the docs say the module has no active consumers.

**Actual:** No binary imports svcutil. Four binaries keep their own copies of the helpers, and three documents describe an adoption that never happened.

**Evidence:** [evidence/review-svcutil/verification.md#c-svcutil-01](evidence/review-svcutil/verification.md#c-svcutil-01)

### F-171

**Repro / observation**
1. `specs.md:102` cites `docs/architecture.md` § "svcutil". `grep -n -i svcutil docs/architecture.md` returns nothing.
2. `specs.md:31` says `env_test.go` has "13 subtests". The three tables run their cases through `t.Run`: `TestOr` has 3 cases, `TestOrInt` 6 and `TestParseLogLevel` 12, so 21 total.
3. `specs.md:83` says `TestRunHTTPShutdownTimeout` shows that the "shutdown timeout mechanism bounds `RunHTTP`'s wait time". The test never sends a request, so `srv.Shutdown` returns at once whatever the timeout is.

**Expected:** The spec's cross-reference, test count and test descriptions match the tree.

**Actual:** A dead section reference, wrong subtest count, and a test described as covering behaviour it never exercises.

**Evidence:** [evidence/review-svcutil/verification.md#c-svcutil-02](evidence/review-svcutil/verification.md#c-svcutil-02)

### F-172

**Repro / observation**
1. Copy `exponentialBackoff.next()` (`tunnel/main.go:558-576`) into a scratch `main` package and call it 70 times: retries 1-9 give 1s…4m16s, retries 10-63 give 5m0s, and retries 64-70 give `0s`.
2. `run` creates the backoff once and only ever increments it. A relay that later runs healthily for days still inherits the count.
3. The mechanism: on 64-bit `int`, `1 << 63` is `math.MinInt64`, which is not `> 300`, so the cap is skipped, and `time.Duration(MinInt64) * time.Second` wraps to 0.

**Expected:** Delays follow `min(2^(N-1), 300)` seconds, so no delay is ever below 1s.

**Actual:** From the 64th lifetime restart on, every delay is `0s` until the pod is recreated. The supervisor respawns the relay in a busy loop.

**Evidence:** [evidence/review-tunnel/verification.md#c-tunnel-01](evidence/review-tunnel/verification.md#c-tunnel-01)

### F-173

**Repro / observation**
1. `BACKING_SERVICE_DNS` and `BACKING_SERVICE_PORTS` are only checked for presence (`main.go:171-172`). For `TUNNEL_TYPE=tailscale`, neither value reaches tailscaled.
2. `renderTailscaleConfig` writes `{"version":"alpha0","authKey":…,"hostname":…}` with no serve or forwarding configuration. `buildCommand` runs `tailscaled --tun=userspace-networking --state=… --config=…` and nothing else.
3. The operator runs the tunnel as its own Deployment `<gs>-tunnel`, not as a sidecar in the game pod. So the tunnel pod's loopback has no game listener.

**Expected:** The tunnel pod "becomes a device reachable by all users on your tailnet" and "becomes a relay that forwards traffic to the game server". Connections to the advertised endpoint reach the game Service.

**Actual:** The device registers under the hostname, but nothing bridges tailnet traffic to `<gs>.<ns>.svc`, so the advertised endpoint has no game behind it.

**Evidence:** [evidence/review-tunnel/verification.md#c-tunnel-05](evidence/review-tunnel/verification.md#c-tunnel-05)

### F-174

**Repro / observation**
1. `tunnel/main.go:282-285` is a TODO: "The exact mechanism (interface, callback, status update) is TBD."
2. `tunnel/main.go` contains no Kubernetes client, no read of playitd's output, and no status write.
3. The operator creates a ServiceAccount and RoleBinding so the tunnel pod can patch the GameServer's status subresource. No process in the pod uses them.

**Expected:** The address "appears in `status.endpoints` once the tunnel pod reports it back", per `docs/tunnels.md:66-67`.

**Actual:** No playit address ever reaches `status.endpoints`. A user following the docs waits indefinitely.

**Evidence:** [evidence/review-tunnel/verification.md#c-tunnel-06](evidence/review-tunnel/verification.md#c-tunnel-06)

### F-175

**Repro / observation**
1. `docs/tunnels.md:304`: `kubectl -n gameplane-games get pods -l gameplane.local/tunnel=<server-name>`. `grep -rn 'gameplane.local/tunnel'` finds only this doc line. The operator labels tunnel pods `app.kubernetes.io/name=gameplane-tunnel` and `app.kubernetes.io/instance=<gs>`.
2. `docs/tunnels.md:325-328`: "Planned: Gameplane will automatically create a NetworkPolicy rule". The operator already creates `<gs>-tunnel-egress` in `reconcileTunnelNetworkPolicy`.

**Expected:** The troubleshooting steps use the correct label selector and describe the NetworkPolicy the operator creates.

**Actual:** The first troubleshooting command finds nothing, and the NetworkPolicy step tells users to hand-write a policy that already exists.

**Evidence:** [evidence/review-tunnel/verification.md#c-tunnel-08](evidence/review-tunnel/verification.md#c-tunnel-08)

### F-176

**Repro / observation**
1. `tunnel/specs.md:245` cites `docs/architecture.md` § "tunnel". `grep -n -i tunnel docs/architecture.md` returns nothing.
2. Each Dockerfile says "gameaction is an in-repo module tunnel depends on via a local replace" and runs `COPY gameaction/ ./gameaction/`. `tunnel/go.mod` contains only the `module` line and has no gameaction requirement.
3. `tunnel/specs.md:89`: "Files are cleaned up automatically when the relay process exits". `/tmp/tailscale.state` is never removed.

**Expected:** The spec's cross-reference and cleanup statement, and the Dockerfile comments, match the module.

**Actual:** A dead section reference, a stale gameaction dependency copied into all three image builds, and a cleanup claim the code doesn't implement.

**Evidence:** [evidence/review-tunnel/verification.md#c-tunnel-09](evidence/review-tunnel/verification.md#c-tunnel-09)

### F-177

**Location:** `tunnel/main.go:304-309` (`serverAddr = "%s"` gets `cfg.FrpServerAddr` unescaped); `tunnel/main.go:325-332` (`name = "%s"` gets the proxy name unescaped); `tunnel/main.go:424-432` (`escapeTomlString`, applied only to the token); CRD markers `operator/api/v1alpha1/gameserver_types.go:406-409,426-429` (length-only).

**Control:** The frpc config renderer must produce a file that holds exactly the keys the supervisor intends, whatever the GameServer spec contains.

**Repro / observation (defensive; confirms whether the control holds, no payload):**
1. Read `tunnel/main.go:304-309`. Of the three string values in the header, only `auth.token` goes through `escapeTomlString`. `serverAddr` is inserted as is.
2. Read `tunnel/main.go:313-332`. Each `BACKING_SERVICE_PORT` entry's name part is inserted into `name = "%s"` unescaped.
3. Read `operator/api/v1alpha1/gameserver_types.go:404-432`. `ServerAddr` and `RemotePortMapping.Name` carry only length markers, with no `Pattern`.
4. Read `api/internal/handlers/resources.go:544-554`. Only `credentialsSecretRef` is validated. `loadConfig` (`tunnel/main.go:141-162`) checks only that the values are non-empty.
5. So a `serverAddr` or `remotePorts[].name` containing a double quote and a line break passes every layer, and the rendered file can gain keys the operator never generated.

**Expected:** Every value written into the frpc TOML is either escaped or rejected before rendering. `serverAddr` parses as a hostname or IP, and proxy names are DNS labels, enforced by a CRD `Pattern` and/or `loadConfig`. The rendered file then holds exactly the generated keys.

**Actual:** Only the token is escaped. The server address and proxy names reach the TOML file unescaped and unvalidated beyond their length.

**Evidence:** [verification-tunnel.md#h-tunnel-01](verification-tunnel.md#h-tunnel-01)

### F-178

**Location:** `tunnel/main.go:169` (`TAILSCALE_TAGS` read), `tunnel/main.go:291` (only hostname and auth key passed), `tunnel/main.go:378-397` (config holds only `version`, `authKey` and `hostname`), `tunnel/main.go:478-482` (no tag flag); `docs/tunnels.md:183-184`; `operator/api/v1alpha1/gameserver_types.go:447`.

**Control:** Tailnet authorization of the tunnel device. The docs and the CRD tell admins that `spec.networking.tunnel.tailscale.tags` become the device's ACL tags at registration, so tailnet ACL rules written against those tags govern who can reach the device.

**Repro / observation (defensive; confirms whether the control holds):**
1. `operator/internal/controller/gameserver_tunnel.go:260-282` joins `spec.networking.tunnel.tailscale.tags` into `TAILSCALE_TAGS`.
2. `tunnel/main.go:169` stores it in `cfg.TailscaleTags`. `grep -n TailscaleTags tunnel/main.go` finds only the struct field and that assignment.
3. `renderTailscaleConfig` writes only `version`, `authKey` and `hostname`, and `buildCommand` passes no tag argument. So tailscaled registers with whatever identity the auth key carries.
4. `tunnel/specs.md:63` and `:119` list this as a known gap. `docs/tunnels.md:183-184` and the CRD field doc do not.

**Expected:** Either the configured tags reach tailscaled at registration and the device shows them, or `docs/tunnels.md:183-184` and the CRD field doc say the tags are not applied yet and tell admins to put the tags on the auth key instead.

**Actual:** The tags are accepted by the CRD and passed to the pod, then dropped. The device carries only the auth key's identity, while the guide and the CRD doc say it carries the configured tags.

**Evidence:** [verification-tunnel.md#h-tunnel-02](verification-tunnel.md#h-tunnel-02)

### F-179

**Repro / observation**
1. On a cluster with the chart installed, create a GameServer from the `terraria` module (`tmodloader` and `minecraft-java` behave the same) with `spec.networking.expose: ClusterIP`, `spec.idle.enabled: true` and `spec.idle.wakeOnConnect: true`. NodePort and LoadBalancer behave the same as ClusterIP. Let the server go to sleep, then confirm that `<gs>-waker` is Ready and that the game Service selects `app.kubernetes.io/name=gameplane-waker`.
2. Connect a Terraria client, or the e2e Terraria bot, to the server address. The sentinel classifies the connection as a Join, patches `gameplane.local/idle-wake-requested` and starts polling `<gs>-game-direct`.
3. `<gs>-game-direct` publishes not-ready addresses, so the dial succeeds as soon as the game process binds its port. If that happens within the 25 s hold, the sentinel replays the handshake and the client enters the world through the proxy.
4. Run `kubectl get pods,deploy -n <ns> -w`. Readiness cannot pass until 30 s after the game container starts. When it passes, the operator deletes `<gs>-waker`. `kubectl logs` for the sentinel pod ends with `received signal terminated, shutting down`, and the client is disconnected at that moment.
5. To check by reading on master: `planSentinel` returns `sentinelPlan{}` when `gameReady` (`gameserver_sentinel.go:132-133`). `reconcileSentinel(want=false)` calls `deleteSentinel` (`:168-169`). The sentinel's signal handler calls `cancel()`, and `proxyBidirectional` takes the `ctx.Done()` branch and closes both conns without draining.

**Expected:** A connection that the sentinel hands through survives the switch back to the game pod. The design says "Connections already held in the proxy survive the flip-back" (`docs/superpowers/specs/2026-07-24-wake-on-connect-design.md:80`). The operator comment says the connection is "handed straight through instead of being dropped mid-wake" (`gameserver_sentinel.go:90-94`). `sentinel/specs.md:233` (invariant 3) says the proxy waits for both directions to finish.

**Actual:** A handed-through session lasts only until the game pod reports Ready. The sentinel pod is then deleted, and the player who woke the server is disconnected and must reconnect.

**Evidence:** [evidence/review-sentinel/verification.md#c-sentinel-01](evidence/review-sentinel/verification.md#c-sentinel-01)

### F-180

**Repro / observation**
1. Read `run` on master. After it starts the listeners, `select` takes the first error from `errCh` and calls `wg.Wait()` before it returns (`:419-421`).
2. Read `serveTCP` (`:435-466`) and `serveUDP` (`:705-760`). A healthy listener goroutine returns only when ctx is cancelled or its own socket fails. `run` has no cancel function of its own to stop the other listeners.
3. Take `PORTS_CONFIG=7777:TCP:generic,<P>:UDP:generic`, where the sentinel cannot bind UDP `<P>`. `serveUDP` sends `listen udp :<P>: …` and returns. `run` then blocks in `wg.Wait()` on the TCP goroutine until SIGTERM, and nothing is logged.
4. Live trigger: a custom GameTemplate that advertises a TCP port of 1024 or higher and a UDP port below 1024, on a node whose container runtime leaves `net.ipv4.ip_unprivileged_port_start` at 1024. If the TCP port were privileged too, the synchronous TCP listen would fail first and take the C-sentinel-03 path instead. The sentinel runs as UID 65532 with all capabilities dropped. Its pod stays Running and Ready, and the game Service stays routed to it. The log shows only the start line, and UDP traffic on that port never wakes the server. No shipped module triggers this, because all advertised ports are 2001 or higher.

**Expected:** `run` "blocks until ctx is cancelled or a listener reports a fatal error" (`:383-385`). A fatal listener error is returned or at least logged, so the dead port is visible.

**Actual:** While any other listener is healthy, the error is held silently until shutdown. The failed port is not served, and nothing is logged.

**Evidence:** [evidence/review-sentinel/verification.md#c-sentinel-02](evidence/review-sentinel/verification.md#c-sentinel-02)

### F-181

**Repro / observation**
1. Read `main` on master. The error from `run(ctx, cfg, w)` goes only to `log.Printf("sentinel exiting: %v", err)`. `main` then returns, so the exit status is 0.
2. Live: use a custom GameTemplate that advertises a TCP port the sentinel cannot bind, with the same privileged-port trigger as C-sentinel-02. Put the server to sleep with wake-on-connect armed. `kubectl get pod -l app.kubernetes.io/name=gameplane-waker -o jsonpath='{.items[0].status.containerStatuses[0].lastState.terminated}'` shows `exitCode: 0` and `reason: Completed`, and the log shows `sentinel exiting: listen tcp :N: …`.

**Expected:** A fatal startup error exits non-zero, as the `config:` and `kubernetes client:` paths do (`:90`, `:95`), so the container reports `Error`.

**Actual:** The process exits 0, and the pod shows `Completed` while it restart-loops.

**Evidence:** [evidence/review-sentinel/verification.md#c-sentinel-03](evidence/review-sentinel/verification.md#c-sentinel-03)

### F-182

**Repro / observation**
1. Read `handleJoin` and `handleTCPConnection` on master. After `proxyBidirectional` returns, both conns are already closed (`:688-689`). The deferred `upstream.Close()` (`:605`) and `conn.Close()` (`:479`) then return `net.ErrClosed`, and each error is logged.
2. Live: wake a sleeping Minecraft or Terraria server with a join that is handed through (see C-sentinel-01), and let the session end. `kubectl logs deploy/<gs>-waker` shows `close upstream connection: close tcp …: use of closed network connection` and `close connection: close tcp …: use of closed network connection`.

**Expected:** A close error is logged only when a close really fails.

**Actual:** Every proxied session, including one cut by shutdown, logs two error lines.

**Evidence:** [evidence/review-sentinel/verification.md#c-sentinel-04](evidence/review-sentinel/verification.md#c-sentinel-04)

### F-183

**Repro / observation**
1. Read `specs.md:143`: Hostport "Works as above". The hold-and-poll and proxy path it refers to is described at `:122-134`.
2. Read `gameserver_sentinel.go:95-101` ("There is no hold-open window in this mode") and `:120-123`. With `Expose == "Hostport"`, `planSentinel` returns an empty plan as soon as the server is awake, so the sentinel is deleted when the wake is decided.
3. On SIGTERM, `waitForUpstream` returns the ctx error, and `handleJoin` bounces the client (`main.go:597-602`).

**Expected:** `specs.md` describes the Hostport asymmetry, as `docs/roadmap.md:158-160` does: on wake, held joins are bounced and never proxied.

**Actual:** `specs.md` says Hostport behaves like the Service-backed modes.

**Evidence:** [evidence/review-sentinel/verification.md#c-sentinel-05](evidence/review-sentinel/verification.md#c-sentinel-05)

### F-184

**Repro / observation**
1. `main.go:754` passes `addr.String()` to `shouldWake`. For a `*net.UDPAddr` that string is `ip:port`, so two sockets on one host count as two sources.
2. `main.go:816-822` returns `false` during the cooldown before the packet is appended, and `:838` set `st.packets` to nil when the wake fired.
3. Compare with `specs.md:19` ("Track UDP sources by IP"), `:116` ("extract the remote IP address"), `:149` ("per source IP"), `:162` ("distinct IPs") and `:158` ("Additional packets … within the cooldown are counted").

**Expected:** `specs.md` matches how sources are keyed and what happens in the cooldown.

**Actual:** Sources are keyed by `ip:port`, and packets are not counted during the cooldown.

**Evidence:** [evidence/review-sentinel/verification.md#c-sentinel-06](evidence/review-sentinel/verification.md#c-sentinel-06)

### F-185

**Repro / observation**
1. `specs.md:51` marks `PORTS_CONFIG` "(required)". `parsePortsConfig("")` returns `nil, nil` (`main.go:232-235`), `loadConfig` accepts it, and `TestParsePortsConfig` pins the empty case as a success (`main_test.go:32`).
2. `specs.md:82` says "exponential backoff / polling". `waitForUpstream` waits a fixed `time.After(pollInterval)` (`main.go:635-639`), and `specs.md:125` itself says "fixed interval".
3. The example JSON at `specs.md:94` has four `{` and three `}`, and it nests `description` inside `players`. The code sends `…"players":{"max":0,"online":0,"sample":[]},"description":{…}}` (`main.go:66`).
4. `specs.md:235` says generic "doesn't half-close". Generic TCP goes through `handleJoin` → `proxyBidirectional` (`main.go:560-569`), which calls `closeWrite` on both sides (`:668`, `:673`).

**Expected:** `specs.md` states what the code does.

**Actual:** All four statements contradict the code.

**Evidence:** [evidence/review-sentinel/verification.md#c-sentinel-07](evidence/review-sentinel/verification.md#c-sentinel-07)

### F-186

**Repro / observation**
1. `sentinel/go.mod` requires only `gameproto`, `k8s.io/apimachinery` and `k8s.io/client-go`, with no controller-runtime. `main.go:293-296` says the dynamic client is used so the module "doesn't need controller-runtime".
2. `grep -n '^func Test' sentinel/main_test.go` lists no `TestHandleRegistryProtocol*`, `TestParsePortsConfigUnknownProtocol` or `TestParsePortsConfigValidRegisteredProtocols`. The registry tests are `TestRegistryProtocolDispatch*`, `TestParsePortsConfigRejectsUnknownProtocol` and `TestRegistryProtocolReplayConsumedBytes` (`main_test.go:1327-1529`).
3. `ls test/e2e/tests` fails. The bot tests are `test/e2e/*_bot_e2e_test.go`, and the wake tests are in `test/e2e/wake_on_connect_e2e_test.go`.
4. `grep -n -i 'wake-on-connect' CLAUDE.md` matches only the repo-map line and two table rows (`:73`, `:147`, `:259`). There is no section with that name.

**Expected:** Every dependency, test and reference named in `specs.md` exists.

**Actual:** None of these four references resolves.

**Evidence:** [evidence/review-sentinel/verification.md#c-sentinel-08](evidence/review-sentinel/verification.md#c-sentinel-08)

### F-187

**Repro / observation**
1. By reading on master: the `captures` volume is `EmptyDir{SizeLimit: 1Gi}` with no `Medium`, so it is disk-backed (`gameserver_controller.go:1432-1437`). The chart default and API ceiling for one capture is 900 MiB (`values.yaml:545`; the API rejects larger values at `capture.go:143-149`), and the API allows durations up to 3600 s (`capture.go:134`).
2. `HandleStart` checks only `maxSizeBytes > 0` (`handlers.go:365-368`). It never looks at the space that earlier capture files already use on the volume.
3. A finished file is removed only by `expireCapture` → `DeleteCaptureFile` (`networkcapture_controller.go:693`), after the retention window, which is 24 h by default (`values.yaml:531`). Deleting a capture through the API removes only the CR (`capture.go:840-848`), so that file stays until the pod is recreated.
4. Live: install with `capture.enabled: true` and opt a busy GameServer into capture. Run capture A with the default `maxSizeBytes` (943718400), a broad filter such as `tcp or udp` and a duration long enough to reach the size limit. When it ends with `max_size_reached`, run capture B the same way. Several smaller captures in one day add up the same way, and so does deleting captures through the dashboard and then capturing again.
5. Watch `kubectl get events -n <ns> --field-selector involvedObject.name=<gs>-0`. Once the volume passes 1 GiB, the kubelet's eviction manager evicts the pod with "Usage of EmptyDir volume "captures" exceeds the limit "1Gi"". The StatefulSet recreates the pod, so the game server restarts, capture B is failed with reason `PodRestarted` instead of `disk_full`, and every capture file on the old emptyDir is lost. On the live run, also check whether the kubelet skips the game's graceful stop for a local-storage eviction. If it does, game progress since the last autosave is lost too.

**Expected:** `specs.md:24` says the sidecar gracefully stops a capture when the volume fills. `specs.md:241` says the `sizeLimit` is a hard bound that the sidecar respects. The spec 003 edge case (`spec.md:107`) says a disk-full capture fails with a clear error "and the server remains playable". In total, the retained captures never push the pod past its emptyDir limit.

**Actual:** The ENOSPC path never fires for the `sizeLimit` case. When retained captures pass 1 GiB in total, the result is a pod eviction: the game server restarts, and all capture files are lost.

**Evidence:** [evidence/review-capture-sidecar/verification.md#c-capture-sidecar-02](evidence/review-capture-sidecar/verification.md#c-capture-sidecar-02)

### F-188

**Repro / observation**
1. `grep -rn 'Getenv\|LookupEnv' capture-sidecar/ --include='*.go'` returns nothing.
2. `main.go:37-39` defines the flags `-tls-cert`, `-tls-key` and `-tls-client-ca`, with defaults `/etc/tls/tls.crt`, `/etc/tls/tls.key` and `/etc/tls/ca.crt`. The ephemeral container is started with no args.
3. `buildCaptureEphemeralContainer` sets `TLS_CERT_FILE`, `TLS_KEY_FILE` and `TLS_CA_FILE` to the same three paths (`gameserver_controller.go:1628-1630`). On a live pod, `kubectl get pod <gs>-0 -o jsonpath='{.spec.ephemeralContainers[?(@.name=="capture")].env}'` shows them.
4. Change one env value, for example to point at a different mount path. The sidecar still reads `/etc/tls/...`.

**Expected:** The sidecar reads the env vars that `specs.md` documents as required and that the operator passes. The other option is that the operator passes flags and `specs.md` documents flags.

**Actual:** The env vars have no effect. The sidecar works only because the flag defaults equal the env values.

**Evidence:** [evidence/review-capture-sidecar/verification.md#c-capture-sidecar-03](evidence/review-capture-sidecar/verification.md#c-capture-sidecar-03)

### F-189

**Repro / observation**
1. `handlers.go:192-197` registers `GET /healthz`, `POST /captures/{id}/start`, `POST /captures/{id}/stop`, `GET /captures/{id}/status`, `GET /captures/{id}/file` and `DELETE /captures/{id}`. The comment at `:184-186` says the `{id}:start` form is an invalid ServeMux pattern. The contract agrees (`specs/done_003-network-capture-sidecar/contracts/capture-sidecar.md:216`, `:234`, `:304`, `:499`).
2. `specs.md:21`, `:53`, `:75` and the table at `:112-117` list four operations, using `/captures/{id}:start` and `:stop`.
3. The `captureDelete` doc comment at `api/internal/handlers/capture.go:840-848` says "there is no sidecar endpoint to delete an individual capture file (capture-sidecar/specs.md's endpoint table lists only :start/:stop/status/file)". But the operator calls that endpoint in `DeleteCaptureFile` (`operator/internal/agent/sidecar_capture.go:227-268`).

**Expected:** `specs.md` lists the real paths and all six routes, and the API comment reflects that the delete endpoint exists.

**Actual:** `specs.md` gives colon paths and only four routes, and the API comment repeats that stale table.

**Evidence:** [evidence/review-capture-sidecar/verification.md#c-capture-sidecar-04](evidence/review-capture-sidecar/verification.md#c-capture-sidecar-04)

### F-190

**Repro / observation**
1. `:11` and `:253` say TTL expiry and a dashboard UI are future work, and that "no auto-deletion reconciliation runs yet". But `expireCapture` (`operator/internal/controller/networkcapture_controller.go:680-716`) deletes files and CRs, and `web/src/components/CaptureWidget.tsx` and `web/src/routes/tabs/settings/NetworkCapture.tsx` exist.
2. `:73` says the writer "stops gracefully, deleting partial files". `:211` and the code keep the file (`handlers.go:489-499`, `finish`). The disclaimer at `:199` names only "Packet Processing", "Shutdown" and "Security Considerations #5", not `:73`.
3. `:183` (invariant 2) says there is "no 'one more packet' tolerance". `:148`, `:203` and `writer.go:262-269` write the packet that crosses `maxSizeBytes`.
4. `:280` lists the test cases "empty filter fallback, default port-based filter". But `CompileFilter("")` returns an error (`filter.go:70-72`), and so does an empty request filter (`handlers.go:378-381`). The sidecar has no fallback.

**Expected:** `specs.md` describes one behaviour: the implemented one.

**Actual:** It contradicts itself and the code in these four places.

**Evidence:** [evidence/review-capture-sidecar/verification.md#c-capture-sidecar-05](evidence/review-capture-sidecar/verification.md#c-capture-sidecar-05)

### F-191

**Repro / observation**
1. `go.mod:6` requires `github.com/gopacket/gopacket v1.7.2`, and `go.sum` lists only that version.
2. `specs.md:5` and `:85` name `github.com/google/gopacket v1.1.19`, which is a different module path and version. `:127` names `github.com/google/gopacket/pcapgo.NgWriter`.
3. `specs.md:207`, `afpacket.go:79` and `:181`, and `writer.go:55` and `:157` say the behaviour was "verified against" `github.com/gopacket/gopacket@v1.6.1`, not the v1.7.2 the module builds with.
4. The svcutil part of the original candidate is not repeated here. C-svcutil-01 already covers `specs.md:58` and `:88`, the orphan `replace` at `go.mod:11-13` and `Dockerfile:4-7`.

**Expected:** `specs.md` and the code comments name the gopacket module and version that `go.mod` actually uses.

**Actual:** They name an unrelated upstream module at v1.1.19, and verification notes against v1.6.1.

**Evidence:** [evidence/review-capture-sidecar/verification.md#c-capture-sidecar-06](evidence/review-capture-sidecar/verification.md#c-capture-sidecar-06)

### F-192

**Repro / observation**
1. By reading on master: `HandleStart` returns `409 capture '<request id>' already in progress` whenever any capture is current, whatever its id (`:391-394`).
2. The operator can start a second capture while the first is still running on the sidecar. A single `GetCaptureStatus` error during Running goes to `r.fail(..., "sidecar unreachable: …")` (`:473-475`). `failWithReason` clears `status.capture.activeCapture` (`:797-808`) and never calls `StopCapture`, so `cap-aaa` keeps running on the sidecar until its own limit.
3. The user starts `cap-bbb`. The sidecar answers `409 capture 'cap-bbb' already in progress`. `IsTransientError` treats a 4xx as permanent (`sidecar_capture.go:52-60`), so the operator fails `cap-bbb` with `failed to start capture on sidecar: … capture 'cap-bbb' already in progress` (`:389`).
4. Unit-level check by reading: with a stub packet source, `POST /captures/cap-aaa/start` followed by `POST /captures/cap-bbb/start` on one `Server` returns a 409 whose body names `cap-bbb`.

**Expected:** For a conflict with a different id, the message names the capture that is actually running (`s.currentCapture.id`). FR-012 (`specs/done_003-network-capture-sidecar/spec.md:133`) requires "a clear error message stating that a capture is already in progress".

**Actual:** The message names the rejected capture, so the user sees a capture that never started described as "in progress".

**Evidence:** [evidence/review-capture-sidecar/verification.md#c-capture-sidecar-07](evidence/review-capture-sidecar/verification.md#c-capture-sidecar-07)

### F-193

**Repro / observation**
1. `main.go:59-60` says "/healthz is the only unauthenticated route", and `HandleHealthz`'s doc says "unauthenticated liveness check" (`handlers.go:201-202`). `handlers.go:192` registers the route without the middleware.
2. The same `http.Server` uses the `TLSConfig` from `auth.ServerTLS`, which sets `ClientAuth: tls.RequireAndVerifyClientCert` (`tls.go:37`). A client without a client certificate signed by the CA fails the handshake before any route runs.
3. The sidecar is an ephemeral container, and the Kubernetes API does not allow probes on ephemeral containers. `grep -rn healthz operator/internal api/internal charts/gameplane/templates` finds no caller on port 9091.

**Expected:** The comments describe the route as it behaves (mTLS-only, like every other route), or the unused route is removed.

**Actual:** The comments describe an unauthenticated liveness endpoint. The route in fact requires mTLS, and nothing calls it.

**Evidence:** [evidence/review-capture-sidecar/verification.md#c-capture-sidecar-08](evidence/review-capture-sidecar/verification.md#c-capture-sidecar-08)

### F-194

**Repro / observation**
1. Build the bridge: `cd audit-syslog-bridge && go build -o /tmp/bridge .`.
2. Start a TCP listener on `127.0.0.1:25614`. On its first connection it reads one frame and then closes the connection. After that it keeps accepting new connections and prints every frame.
3. Run `LISTEN_ADDR=127.0.0.1:28614 SYSLOG_ADDR=127.0.0.1:25614 /tmp/bridge`. TCP is the default network.
4. Once `/healthz` answers, POST events about 0.5 s apart: `curl -s -o /dev/null -w '%{http_code}\n' -X POST --data '{"event":N}' http://127.0.0.1:28614/`.
5. Observed: the first delivered event arrived on connection 1, and the listener then closed that connection. The next POST returned `204`, but its frame never arrived anywhere. The POST after that also returned `204` and arrived on connection 2. The bridge logged nothing about the lost record or the reconnect.

**Expected:** README.md:32-35 says "TCP surfaces a dead collector as a `502` (→ API `failed`), which is the signal you want". A record sent on a connection the peer has already closed should either reach a fresh connection or get a `502`, so the API counts it as `failed`.

**Actual:** The first write after the peer closes succeeds locally, and the peer answers with RST. One record is lost every time the collector closes the connection, and both the bridge (`204`) and the API (`sent`) report it as delivered. Audit events are infrequent, so long idle gaps are normal, and the idle-reap case the comment is written for is exactly the one that loses a record.

**Evidence:** [evidence/review-audit-syslog-bridge/verification.md#c-audit-syslog-bridge-01](evidence/review-audit-syslog-bridge/verification.md#c-audit-syslog-bridge-01)

### F-195

**Repro / observation**
1. `sed -n 75p audit-syslog-bridge/specs.md` prints: "API audit webhook sink (`api/internal/handlers/audit.go`, `api/internal/notify/`) — calls `POST http://syslog-bridge-svc:8514/` with audit events".
2. `grep -n "^func " api/internal/handlers/audit.go` lists only `MountAudit` and `parseStreamFilter`. Nothing in that file POSTs to a webhook.
3. The component that POSTs to the bridge is `WebhookSink` in `api/internal/audit/audit.go:96-216`. `api/cmd/main.go:197` builds it from `--audit-webhook-url`.
4. `git grep -n "syslog-bridge-svc"` matches only `specs.md:75`. The chart's Service is `gameplane-audit-syslog-bridge`, and the chart gives the API `http://gameplane-audit-syslog-bridge.<namespace>.svc:8514/`.

**Expected:** The References entry names `api/internal/audit/audit.go` and the real Service DNS name.

**Actual:** It names two wrong files and a Service that doesn't exist.

**Evidence:** [evidence/review-audit-syslog-bridge/verification.md#c-audit-syslog-bridge-02](evidence/review-audit-syslog-bridge/verification.md#c-audit-syslog-bridge-02)

### F-196

**Repro / observation**
1. `specs.md:26` says `bridge_test.go` covers "... TCP/UDP forwarding, auth, reconnection".
2. `specs.md:68` says: "Tests cover ... connection reuse, write deadline enforcement, forward-failure 502, ...".
3. `grep -n "func Test" bridge_test.go` lists 15 tests. `grep -n -i "deadline\|reconnect\|redial" bridge_test.go` finds nothing.
4. `TestForwarder_ReusesConnection` (`:265-316`) sends two frames over one healthy connection. `TestHandle_ForwardFailureIs502` (`:254-263`) targets a closed port, so the first dial fails. Neither reaches the reconnect-and-retry branch or makes the `SetWriteDeadline` fire.

**Expected:** The spec lists only behaviour the tests exercise, or there are tests for the reconnect path and the write deadline.

**Actual:** Two of the test areas the spec lists have no test. (see held item F-194, OD-019)

**Evidence:** [evidence/review-audit-syslog-bridge/verification.md#c-audit-syslog-bridge-03](evidence/review-audit-syslog-bridge/verification.md#c-audit-syslog-bridge-03)

### F-197

**Repro / observation**
1. `sed -n 55p audit-syslog-bridge/specs.md` prints: "**Go 1.25** (workspace-linked to `go.work` ...)".
2. `sed -n 3p audit-syslog-bridge/go.mod` prints `go 1.26.0`. `audit-syslog-bridge/Dockerfile:1` builds with `golang:1.27-alpine`.

**Expected:** The spec gives the Go version the module requires, 1.26.

**Actual:** It says 1.25.

**Evidence:** [evidence/review-audit-syslog-bridge/verification.md#c-audit-syslog-bridge-04](evidence/review-audit-syslog-bridge/verification.md#c-audit-syslog-bridge-04)

### F-198

**Repro / observation**
1. Build the bridge (see held item F-194, OD-019), and start a TCP listener that prints what it receives on `127.0.0.1:25714`.
2. Run `APP_NAME="gameplane audit" LISTEN_ADDR=127.0.0.1:28714 SYSLOG_ADDR=127.0.0.1:25714 /tmp/bridge`. On a cluster, the same value comes from `--set api.audit.webhook.syslogBridge.appName="gameplane audit"`.
3. The bridge starts without error and logs `app="gameplane audit"`.
4. POST `{"event":1}`. The response is `204`, and the listener receives `69 <134>1 2026-09-24T14:44:09.718Z dev gameplane audit - - - {"event":1}`.
5. RFC 5424 §6 defines APP-NAME as 1*48 PRINTUSASCII, which excludes SP. A collector that splits the header on SP reads APP-NAME=`gameplane`, PROCID=`audit`, MSGID=`-` and STRUCTURED-DATA=`-`, and the MSG starts with a stray `- `.

**Expected:** `specs.md:51` promises RFC 5424 output. An `APP_NAME` or `SYSLOG_HOSTNAME` that can't be a valid RFC 5424 field is rejected at startup, the way an unknown FACILITY or SEVERITY already is, or it is sanitised.

**Actual:** The bridge accepts the value without complaint, and every record it sends has shifted header fields.

**Evidence:** [evidence/review-audit-syslog-bridge/verification.md#c-audit-syslog-bridge-05](evidence/review-audit-syslog-bridge/verification.md#c-audit-syslog-bridge-05)

### F-199

**Location:** `audit-syslog-bridge/main.go:283-287` (the `http.Server` literal in `serve`) and `main.go:148` (`io.ReadAll(http.MaxBytesReader(...))` in `handle`), measured against `audit-syslog-bridge/specs.md:64`.

**Control:** the DoS bound on the bridge's HTTP intake. The spec promises three limits: a 64 KiB body cap, a read deadline and a write deadline.

**Repro / observation (how to confirm the control holds):**
1. Read `serve` in `main.go:283-287`. The server sets `ReadHeaderTimeout: 10 * time.Second` and nothing else: there is no `ReadTimeout` and no `IdleTimeout`.
2. Read `handle` in `main.go:138-168`. The body is read with `io.ReadAll` on a `MaxBytesReader`, which bounds size but not time, and nothing sets a per-request read deadline before the read. By contrast, the collector write sets a deadline (`main.go:263`).
3. Start the handler under `httptest.NewServer` with the production `http.Server` settings, open a raw connection, send complete headers with a non-zero `Content-Length`, then send no body. Assert that the server closes the connection within the configured read bound. On the current tree the connection stays open past the 10 s header timeout.

**Expected:** Per `specs.md:64`, a request whose body is not fully received within a bounded time is closed, for example by `http.Server.ReadTimeout` or by `http.NewResponseController(w).SetReadDeadline` before the body is read. That bounds the number and lifetime of intake goroutines.

**Actual:** Only the header phase is time-bounded. The spec's "read deadlines" have no matching code for the body phase. Exposure is limited by the chart's API-only NetworkPolicy when `networkPolicies.enabled=true`.

**Evidence:** [verification-audit-syslog-bridge.md#h-audit-syslog-bridge-01](verification-audit-syslog-bridge.md#h-audit-syslog-bridge-01)

### F-200

**Repro / observation**
1. `sed -n 5p telemetry-receiver/specs.md` prints: "**Go version:** 1.25".
2. `sed -n 3p telemetry-receiver/go.mod` prints `go 1.26.0`. `telemetry-receiver/Dockerfile:1` builds with `golang:1.27-alpine`.

**Expected:** The spec gives the module's actual minimum Go version, 1.26.

**Actual:** It says 1.25. `audit-syslog-bridge/specs.md:55` has the same drift (F-197), and both can be fixed in one PR.

**Evidence:** [evidence/review-telemetry-receiver/verification.md#c-telemetry-receiver-02](evidence/review-telemetry-receiver/verification.md#c-telemetry-receiver-02)

### F-201

**Repro / observation**
1. `sed -n 105p telemetry-receiver/specs.md` prints: "**HTTP method guards**: non-POST to `/ingest`, non-GET to `/metrics`/`/healthz` return 405".
2. `grep -n "http.Method" telemetry-receiver/main_test.go` shows that the only request with a disallowed method is `GET /ingest` in `TestIngestMethodNotAllowed`. The `/metrics` helper (`:38`) and `TestHealthz` (`:158`) send only `GET`.
3. The behaviour is correct: run `LISTEN_ADDR=127.0.0.1:28180 ./telemetry-receiver`, then send `curl -s -o /dev/null -w '%{http_code}' -X PUT http://127.0.0.1:28180/metrics`, and the same with `POST` and `DELETE` on `/metrics` and `/healthz`. Each prints `405`.

**Expected:** The Testing section lists only what the suite exercises, or a test covers the `/metrics` and `/healthz` guards.

**Actual:** Two of the three guards the spec lists as tested have no test. A regression that registered those routes without a method, for example `mux.Handle("/metrics", ...)`, would still pass the suite.

**Evidence:** [evidence/review-telemetry-receiver/verification.md#c-telemetry-receiver-03](evidence/review-telemetry-receiver/verification.md#c-telemetry-receiver-03)

### F-202

**Location:** `telemetry-receiver/main.go:125-140` (decode, then check only for negative counts), measured against `telemetry-receiver/specs.md:55-57`.

**Control:** input validation on `POST /ingest`. The spec says only a payload with exactly the three required fields is accepted and counted.

**Repro / observation (how to confirm the control holds):**
1. Read `main.go:125-140`. `json.Decoder` with `DisallowUnknownFields` fills a `payload` struct of plain `string`/`int` fields, and the only check after that is `Servers < 0 || Templates < 0`. The code never distinguishes an absent field from its zero value. A top-level `null` decodes into the zero struct without error.
2. POST bodies with one or more required fields missing, and a body that is the JSON literal `null`. On the current tree each gets `204`, and `/metrics` counts it.

**Expected:** A payload that lacks any of the three fields, or isn't a JSON object, is rejected with `400` and not counted, as `specs.md:45` says for malformed payloads and `specs.md:55-57` says for required fields.

**Actual:** Such payloads get `204` and are counted, which skews the fleet-size histograms towards `0`.

**Evidence:** [verification-telemetry-receiver.md#h-telemetry-receiver-01](verification-telemetry-receiver.md#h-telemetry-receiver-01)

### F-203

**Location:** `telemetry-receiver/main.go:125-136` (a single `dec.Decode`, with no end-of-input check), measured against `telemetry-receiver/specs.md:14`, `:45` and `telemetry-receiver/README.md:21`.

**Control:** input validation and the 16 KiB body-size limit on `POST /ingest`.

**Repro / observation (how to confirm the control holds):**
1. Read `main.go:125-136`. `json.Decoder.Decode` returns as soon as the first complete JSON value has been read. `http.MaxBytesReader` raises its error only when a read goes past the limit, and for a small leading object the decoder never reads that far. Nothing after `Decode` checks that the body is exhausted.
2. POST a valid report followed by non-whitespace bytes, and a valid report followed by padding that takes the body over 16 KiB. On the current tree both get `204` and both are counted. A body whose oversized content sits inside the JSON value still gets `413`.

**Expected:** After decoding, the handler confirms the body holds nothing more, for example with a second `Decode` that must return `io.EOF`, reading far enough that `MaxBytesError` still maps to `413`. Trailing content gets `400`, and any body over 16 KiB gets `413`, as the spec and README say.

**Actual:** Only the first JSON value is validated. Trailing content, including content past the 16 KiB cap, is accepted with `204`.

**Evidence:** [verification-telemetry-receiver.md#h-telemetry-receiver-02](verification-telemetry-receiver.md#h-telemetry-receiver-02)

### F-204

**Repro / observation**
1. Pick a container whose last 5000 log lines exceed 256 KiB. On kubelab, `kubectl logs -n gameplane-games soak-pool-west-0 -c game --tail=5000 | wc -lc` prints `5000 510000`.
2. Reproduce what `PodLogs` returns: `kubectl logs ... --tail=5000 | head -c 262144 | wc -l` prints `2570`. The last complete line has log time `127248.515`, and `kubectl logs ... --tail=1` shows `145468.515`.
3. Through the MCP server, call `get_pod_logs` with `namespace: gameplane-games`, `pod: soak-pool-west-0`, `container: game` and `tailLines: 5000`. The text returned ends at the same line as in step 2 and doesn't say it was truncated.

**Expected:** When the byte cap applies, the newest bytes are kept, or fewer lines are returned, and the result says it was truncated. The newest lines matter most here, because the CrashLoop advice in `propose_fix` sends users to this tool.

**Actual:** When the output is over 256 KiB, the result is the oldest part of the requested tail. The most recent output, which is the part closest to a crash, is dropped without any notice.

**Evidence:** [evidence/review-mcp-server/verification.md#c-mcp-server-01](evidence/review-mcp-server/verification.md#c-mcp-server-01)

### F-205

**Repro / observation**
1. `git grep -n "gameplane.local/server=\|gameplane.local/backup="` matches only `mcp-server/tools.go:149` and `mcp-server/fixadvice.go:74`. Nothing in `operator/` or `api/` sets either key.
2. The operator labels game pods with `app.kubernetes.io/name=gameplane-game`, `app.kubernetes.io/instance=<server>` and `gameplane.local/template=<template>`, on the StatefulSet pod template only. Backup Jobs are created as `Name: b.Name` with no labels.
3. On kubelab, `kubectl get pods -A -l gameplane.local/server` prints "No resources found". `kubectl get gameservers.gameplane.local -A --show-labels` lists five GameServers with labels `<none>`. `kubectl get gameservers.gameplane.local -A -l gameplane.local/template=minecraft-java` prints "No resources found".

**Expected:** The schema examples and suggested commands use selectors that match objects Gameplane creates.

**Actual:** All three examples use label keys, or label-object pairings, that never occur. An assistant that follows the server's own guidance gets empty results and may conclude the resources don't exist.

**Evidence:** [evidence/review-mcp-server/verification.md#c-mcp-server-02](evidence/review-mcp-server/verification.md#c-mcp-server-02)

### F-206

**Repro / observation**
1. Read `mcp-server/tools.go:219-222`: `sel, err := labels.Parse(selector); if err != nil { return list }`. The error is dropped, and the full, unfiltered list is returned.
2. `labels.Parse("app in (")`, run against apimachinery v0.37.0, returns `unable to parse requirement: found '', expected: ',', ')' or identifier`.
3. So calling `list_events` with `namespace: "gameplane-games"` and `labelSelector: "app in ("` returns every event in the namespace, as a success.
4. For comparison, `list_pods` passes the same selector to the API server, and the error comes back as a tool error.

**Expected:** A malformed `labelSelector` gets a tool error, as it does in the sibling tools.

**Actual:** The parse error is dropped, and the caller gets unfiltered data that looks like a successful filtered result.

**Evidence:** [evidence/review-mcp-server/verification.md#c-mcp-server-03](evidence/review-mcp-server/verification.md#c-mcp-server-03)

### F-207

**Repro / observation**
1. `grep -n "+kubebuilder:resource" operator/api/v1alpha1/*_types.go` lists 9 kinds: Backup, BackupSchedule, Cluster, GameServer, GameTemplate, Module, ModuleSource, NetworkCapture and Restore.
2. `CRDKinds` (`client.go:60-68`) and the ClusterRole (`charts/gameplane/templates/mcp-server.yaml:29`) cover 7 of them. `Cluster` and `NetworkCapture` are missing.
3. `mcp-server/README.md:5` says the server reads "the 7 Gameplane CRDs". `main.go:130-131` tells every MCP client "list/get the 7 Gameplane CRDs". `values.yaml:395` uses the same phrase. `get_gameplane_resource` with `kind: "NetworkCapture"` returns `unknown Gameplane resource kind`.

**Expected:** The docs and the instructions sent to clients describe the real scope, for example "7 of the 9 Gameplane CRDs (not Cluster or NetworkCapture)".

**Actual:** "The 7 Gameplane CRDs" tells human readers and the AI client that these are all of Gameplane's CRDs.

**Evidence:** [evidence/review-mcp-server/verification.md#c-mcp-server-04](evidence/review-mcp-server/verification.md#c-mcp-server-04)

### F-208

**Repro / observation**
1. `sed -n 135,143p mcp-server/specs.md` shows "From `go.mod` (verified):" followed by go-sdk `v0.8.0`, `k8s.io/api|apimachinery|client-go` `v0.35.0` and controller-runtime `v0.23.3`.
2. `sed -n 5,11p mcp-server/go.mod` shows go-sdk `v1.8.0`, `k8s.io/*` `v0.37.0` and controller-runtime `v0.25.1`. `operator/go.mod:25-29` has the same k8s and controller-runtime versions.

**Expected:** The table matches `go.mod`, or it drops the version column and the "(verified)" label.

**Actual:** All five versions are stale, and go-sdk is a major version off.

**Evidence:** [evidence/review-mcp-server/verification.md#c-mcp-server-05](evidence/review-mcp-server/verification.md#c-mcp-server-05)

### F-209

**Repro / observation**
1. `sed -n 16,21p charts/gameplane/templates/mcp-server.yaml` shows "... (Client in mcp-server/client.go exposes no mutating method, ...)".
2. `ls mcp-server/client.go` fails. The type is defined at `mcp-server/internal/kube/client.go:116`.

**Expected:** The comment names `mcp-server/internal/kube/client.go`.

**Actual:** The comment names a file that doesn't exist.

**Evidence:** [evidence/review-mcp-server/verification.md#c-mcp-server-06](evidence/review-mcp-server/verification.md#c-mcp-server-06)

### F-210

**Repro / observation**
1. Use a Linux host with Docker and a kubeconfig in the usual mode 0600, owned by your user (`ls -l ~/.kube/config`).
2. Run the README command as written: `KUBECONFIG=~/.kube/config docker run --rm -i -v ~/.kube/config:/kubeconfig:ro -e KUBECONFIG=/kubeconfig ghcr.io/valgulnecron/gameplane/mcp-server:edge serve`.
3. The process runs as UID 65532, which can't read the mounted file, so `serve` exits with `load kubeconfig: ...`.
4. With a readable kubeconfig for the repo's dev cluster (`make dev-up`, server `https://127.0.0.1:6443`), `serve` starts, because `kube.New` doesn't contact the server. Every tool call then fails with connection refused, because 127.0.0.1 inside the container is the container itself and the command has no `--network host`.

**Expected:** The standalone example works for local use against `KUBECONFIG`/`~/.kube/config`, or the README says what it needs: `--user "$(id -u):$(id -g)"` or a readable copy of the kubeconfig, plus `--network host` for a loopback API server.

**Actual:** Copied as written, the example fails on a typical Linux host. Against the repo's own kind cluster, it also can't reach the API server.

**Evidence:** [evidence/review-mcp-server/verification.md#c-mcp-server-07](evidence/review-mcp-server/verification.md#c-mcp-server-07)

### F-211

**Location:** `mcp-server/main.go:8-17` (the package doc, layer 1), `mcp-server/specs.md:77-91` (the "Package boundary enforcement" bullet at `:91`) and `mcp-server/README.md:12-25`. The code that bounds the claim is `main.go:106-115` (`runServe` holds the `*rest.Config`) and `internal/kube/client.go:145-147` (the exported `NewFrom`).

**Control:** the MCP server's read-only guarantee. It has two layers: (1) tool handlers receive only a `*kube.Client`, whose exported methods are all List/Get-shaped; (2) a ClusterRole that grants only `get`/`list`/`watch`, plus `get` on `pods/log`.

**Repro / observation (how to confirm the control holds):**
1. RBAC, the authoritative layer: `kubectl auth can-i --list --as=system:serviceaccount:<release-ns>:gameplane-mcp-server` shows only `get`/`list`/`watch` on the seven `gameplane.local` resources and on core `pods` and `events`, and `get` on `pods/log`.
2. Code: a grep of `mcp-server/` for `.Create(`, `.Update(`, `.Patch(`, `.Delete(`, `.Apply(`, `UpdateStatus` and `DeleteCollection` finds nothing. `kube.Client` exports only `ListCRD`, `GetCRD`, `ListPods`, `GetPod`, `ListEvents` and `PodLogs`.
3. Scope of layer 1: read `main.go:106-115`. `runServe` itself calls `ctrl.GetConfig()`, so `package main` holds a full `*rest.Config`. Layer 1 therefore depends on the convention that handlers receive only the `*kube.Client` built from it. The unexported fields are not the only route to a clientset.

**Expected:** The layer-1 description says what the package boundary actually enforces, for example "tool handlers receive only a `*kube.Client`, which exposes no mutating method". RBAC is described as the only layer that stops mutation by any code in the process. `docs/architecture.md:306-316` already phrases it that way.

**Actual:** `main.go:13-15`, `specs.md:91` and `README.md:19-22` describe layer 1 as making mutation impossible for `package main` "even by mistake". That is broader than the boundary enforces. The guarantee holds today because of RBAC and the absence of mutating call sites, not because of the package boundary alone.

**Evidence:** [verification-mcp-server.md#h-mcp-server-01](verification-mcp-server.md#h-mcp-server-01)

### F-212

**Repro / observation**
1. `helm template gameplane charts/gameplane -n gameplane-system` renders `kind: Namespace` `gameplane-games` with labels but no annotations, and no `helm.sh/resource-policy: keep`.
2. `grep -rn resource-policy charts/gameplane/templates/` finds only the API PVC (`api.yaml:187`).
3. Live on a scratch cluster: `make dev-up`, create a GameServer, write data to its volume, then `helm uninstall gameplane -n gameplane-system`. The namespace goes to `Terminating` and then NotFound, taking all GameServers and PVCs with it.
4. Docs (`install.md:544-546`, `values.yaml:30-31`) promise servers and data survive uninstall.

**Expected:** As documented, `helm uninstall` leaves GameServers and their data in place. The games Namespace carries `helm.sh/resource-policy: keep`, or docs warn plainly that uninstall destroys every game server and volume.

**Actual:** Uninstall deletes the games namespace and every GameServer, StatefulSet, data PVC and per-server Secret in it.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-01](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-01)

### F-213

**Repro / observation**
1. `helm template gp charts/gameplane -n gpns` renders API Deployment `gameplane-api`, but the hook Role has `resourceNames: ["gp-api"]`.
2. The pre-upgrade hook's `migrate-api-strategy` initContainer patches `gp-api`, which doesn't exist.
3. Live: `helm install gp charts/gameplane -n gpns --create-namespace`, then `helm upgrade gp charts/gameplane -n gpns` fails at the pre-upgrade hook.

**Expected:** The hook patches the Deployment the chart renders (`gameplane-api`), or docs say the release must be named `gameplane`.

**Actual:** A release with any other name installs but can never be upgraded while the defaults are on.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-02](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-02)

### F-214

**Repro / observation**
1. New keys in the chart (`capture.*`, `api.oidc.roleMappings`, `operator.gameDataStorage`) have no nil-safe reads.
2. `helm upgrade ... --reuse-values` uses the old release's chart values, leaving new keys nil.
3. Live: simulate with `git show v0.2.0-beta.8:charts/gameplane/values.yaml`, then `helm template` the new chart with those old values. Renders fail with nil-pointer errors at `operator.yaml:283`, `api.yaml:259` and more.

**Expected:** Templates read new keys nil-safely, or `install.md` names `--reset-then-reuse-values` as the upgrade command.

**Actual:** `helm upgrade ... --reuse-values` from 0.2.0-beta.8 fails to render.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-03](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-03)

### F-215

**Repro / observation**
1. Backup and Restore Jobs render with no pod labels except the Job controller's.
2. The chart's default NetworkPolicy `default-deny-egress` selects `podSelector: {}` and permits DNS to kube-system only.
3. Live: take a restic backup to a network repository. The Job pod can't reach the repository host; the Job fails after `backoffLimit: 2`.
4. `test/e2e/fixtures/restic-server.yaml:61-85` adds a policy to make backups work. No docs mention this.

**Expected:** A restic backup to a configured destination works on a default install. Either the Job pods get an egress allowance, or docs name the policy needed.

**Actual:** On a default install, restic backups can't reach any repository.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-04](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-04)

### F-216

**Repro / observation**
1. The operator always passes `--tls-cert/--tls-key/--tls-client-ca` to the agent, so the agent serves `/metrics` through TLS with `RequireAndVerifyClientCert`.
2. The PodMonitor has no scheme or TLS config, so Prometheus scrapes over plain HTTP.
3. Live: with `serviceMonitors.enabled=true`, the agent scrape targets show `down` in Prometheus with "client sent an HTTP request to an HTTPS server".

**Expected:** `install.md:239-241` says the PodMonitor scrapes agent metrics. They reach Prometheus, or docs say they don't and explain why.

**Actual:** On every chart install, every agent scrape target is down.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-05](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-05)

### F-217

**Repro / observation**
1. The telemetry-receiver NetworkPolicy admits ingress on port 8080 only from `gameplane-api` pods.
2. `/metrics` is on the same listener as `/ingest`, but `servicemonitors.yaml` has no monitor for the receiver.
3. Live: from a Prometheus pod in the monitoring namespace, `curl http://gameplane-telemetry-receiver:8080/metrics` times out.

**Expected:** A Prometheus can scrape the receiver's metrics through a scrape object and a policy rule for the monitoring namespace, or docs name the extra policy needed.

**Actual:** With default `networkPolicies.enabled: true`, no in-cluster Prometheus can reach the receiver's metrics.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-06](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-06)

### F-218

**Repro / observation**
1. The pre-upgrade hook is hook `pre-upgrade` only, so it doesn't run on install.
2. Helm's `crds/` install skips any CRD that already exists, and uninstall leaves CRDs behind.
3. On upgrade from beta.8 to this tree, the old GameServer and GameTemplate CRDs stay; the new `networkcaptures` CRD is installed; but existing CRDs aren't updated until the next `helm upgrade`.

**Expected:** On fresh install, CRDs come from Helm's `crds/` and are current. On reinstall, they're updated by a hook, or docs say to apply them by hand.

**Actual:** Uninstall followed by install of a newer chart keeps the old CRD schemas until the first upgrade.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-07](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-07)

### F-219

**Repro / observation**
1. The default module catalog lists 16 names in `values.yaml:480-496`.
2. `modules/` has 30 directories.
3. Live: `kubectl get modulesource default -o jsonpath='{.status.modules[*].name}'` shows 16 names.

**Expected:** Per spec 015 US1, an administrator selects any module "via the Gameplane catalog". The default catalog lists every published module.

**Actual:** 14 spec-015 modules don't appear in the default catalog.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-08](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-08)

### F-220

**Repro / observation**
1. `install.md:595-600` says CRDs are installed once and not updated on upgrade; a manual `kubectl apply` is needed.
2. `install.md:528-540` says the pre-upgrade hook applies CRDs automatically; "No manual `kubectl apply` step is needed."
3. Both are on the same page; one contradicts the other.

**Expected:** One statement, matching `crd-apply-hook.yaml`. Any manual command uses `--server-side`.

**Actual:** Two different procedures are described on the same page.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-09](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-09)

### F-221

**Repro / observation**
1. `install.md:228-229` documents the capture buffer default as 5 GiB.
2. `values.yaml:545` is 943718400 bytes (900 MiB), and `:544` notes it's kept under the 1 GiB emptyDir limit.
3. An admin who follows the docs sets 5 GiB, which exceeds the 1 GiB volume limit, and the pod is evicted.

**Expected:** The docs give 943718400 (900 MiB) and explain the 1 GiB volume limit.

**Actual:** The docs give 5 GiB.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-10](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-10)

### F-222

**Repro / observation**
1. `install.md:176` documents `git.ref` default as `main`.
2. `values.yaml:473` pins `v0.2.0-beta.6`.
3. The comment at `:464-472` explains why tracking `main` orphans installed Modules.

**Expected:** The docs describe the real default (the pinned, tested tag).

**Actual:** The docs say `main`, the behaviour the pin was added to avoid.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-11](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-11)

### F-223

**Repro / observation**
1. `docs/install.md:127-128` and `docs/oidc.md` walkthroughs reference `api.oidc.displayName`.
2. `charts/gameplane/values.yaml:239-275` has no such key.
3. `charts/gameplane/templates/api.yaml:249-268` never passes `--oidc-display-name` to the API.
4. `helm template --set api.oidc.displayName=Keycloak` renders nothing for it.

**Expected:** The chart wires `api.oidc.displayName` to `--oidc-display-name`, or docs drop the key.

**Actual:** The documented key is silently ignored.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-12](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-12)

### F-224

**Repro / observation**
1. `values.yaml:205` and `install.md:357-358` document empty region as "path-style requests".
2. `api/internal/audit/s3.go:67-70` sets empty region to `us-east-1`.
3. `api/cmd/main.go:497` (flag help) correctly states "defaults to us-east-1 if empty".

**Expected:** The chart docs say an empty region means `us-east-1`.

**Actual:** The chart docs describe an addressing behaviour the code doesn't have.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-13](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-13)

### F-225

**Repro / observation**
1. `security.md:290-293` says setting `podSecurity.enforceRestricted=false` disables `restricted` "cluster-wide" with a "per-pod opt-in".
2. `namespaces.yaml:7-10` only adds/omits the enforce label on the games Namespace.
3. Pod Security Admission has no per-pod opt-in label.

**Expected:** The docs describe the real scope: disabling the label from the games namespace, which is already the default.

**Actual:** The docs claim a cluster-wide scope and a per-pod mechanism that don't exist.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-14](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-14)

### F-226

**Repro / observation**
1. `install.md:23-25` lists the images `appVersion` pins: `{operator,api,agent}`.
2. `_helpers.tpl:11-81` derives 12 images from `image.registry`/`appVersion`: operator, api, agent, web, audit-syslog-bridge, telemetry-receiver, sentinel, tunnel-frp, tunnel-tailscale, tunnel-playit, mcp-server and capture-sidecar.
3. `web.enabled` is `true` by default.

**Expected:** `install.md` lists every image the chart can pull under `image.registry`, including `web`.

**Actual:** The only image list in `install.md` names 3 of 12 images and leaves out one that a default install runs.

**Evidence:** [evidence/review-charts-gameplane/verification.md#c-charts-gameplane-15](evidence/review-charts-gameplane/verification.md#c-charts-gameplane-15)

### F-227

**Repro / observation (defensive; confirms whether the control holds)**
1. Read `api/cmd/main.go:260` and `web/nginx.conf.template:94-115`. `/metrics` sits on the root router with no auth, and nginx proxies every non-HTML request to it.
2. On a default install, request `/metrics` on the ingress host with no session. The response is Prometheus text format, not 401/403/404.

**Expected:** The public host does not serve Prometheus metrics to unauthenticated clients. The control is either an nginx rule (`location = /metrics { return 404; }`) or a separate metrics listener the ingress doesn't expose.

**Actual:** Nothing in the chart or nginx config keeps `/metrics` off the public host, so it is proxied to the API.

**Evidence:** [verification-charts-gameplane.md#c-charts-gameplane-h01](verification-charts-gameplane.md#c-charts-gameplane-h01)

### F-228

**Repro / observation (by reading code)**
1. `api/internal/audit/s3.go:71-75` sets `Secure: !cfg.Insecure` with no custom transport.
2. In minio-go v7.3.0, `getEndpointURL` picks `http` when `secure` is false.
3. `api/internal/audit/s3_test.go` tests only with `Insecure: true` against plain-HTTP servers.
4. Docs say the option "skips certificate verification"; the code means "plain HTTP".

**Expected:** The option does what the docs say (keep TLS, skip verification) or the docs say plainly "plain HTTP only".

**Actual:** The option disables TLS entirely, while docs describe it as skipping certificate verification only.

**Evidence:** [verification-charts-gameplane.md#c-charts-gameplane-h02](verification-charts-gameplane.md#c-charts-gameplane-h02)

### F-229

**Repro / observation (by reading code)**
1. `install.md:131-134` and `:147-148` say empty `groupsClaim` disables group mapping.
2. `api/internal/auth/oidc.go:110-116` shows empty claim becomes `"groups"`, so mapping is active whenever mappings are configured.
3. `values.yaml:247-248` and `security.md:699-700` state the real behaviour.

**Expected:** All docs say the same thing: empty `groupsClaim` defaults to reading the `groups` claim, and mapping is active when mappings exist.

**Actual:** `install.md` alone says empty `groupsClaim` disables mapping.

**Evidence:** [verification-charts-gameplane.md#c-charts-gameplane-h03](verification-charts-gameplane.md#c-charts-gameplane-h03)

### F-230

**Repro / observation (by reading code)**
1. `mtls.yaml` has no `helm.sh/hook` annotation; both Secrets are normal release resources.
2. Both Secrets always render under the fixed names `gameplane-agent-ca` and `gameplane-agent-client`, whatever `api.agentMTLS.*.name` says.
3. `grep -n agentMTLS charts/gameplane/templates/*.yaml` finds only the `.name` fields. The `.key` fields are never read.
4. Docs promise a bring-your-own path that fails at `helm install` with an ownership error.

**Expected:** The chart skips generating Secrets whose names were overridden, or docs describe the Helm ownership metadata needed.

**Actual:** The documented bring-your-own path fails at install, and four of six `api.agentMTLS` fields do nothing.

**Evidence:** [verification-charts-gameplane.md#c-charts-gameplane-h04](verification-charts-gameplane.md#c-charts-gameplane-h04)

### F-231

**Repro / observation (by rendering)**
1. `allow-agent-to-apiserver` (the default) admits RFC1918 ranges on TCP 443/6443 for `gameplane-game` pods.
2. `allow-game-public-egress` excludes private ranges from public egress.
3. For TCP 443/6443, the private-range exception is cancelled; game containers reach any private/link-local address on those ports.

**Expected:** Game pods reach only the apiserver on private addresses 443/6443. Either `apiServerCIDRs` gets a narrower default (e.g., resolved from the `kubernetes` Endpoints), or docs plainly say the default allows game containers to reach any private address on those ports.

**Actual:** With defaults, the anti-SSRF exception doesn't hold for TCP 443/6443.

**Evidence:** [verification-charts-gameplane.md#c-charts-gameplane-h05](verification-charts-gameplane.md#c-charts-gameplane-h05)

### F-232

**Repro / observation**
1. When the kind cluster already exists, `up.sh:154-159` never selects `kind-${CLUSTER}`.
2. Later `kubectl` calls use the current context: ConfigMap at `:178`, ingress-nginx at `:194-201`, MetalLB at `:203-204`, namespace at `:210`.
3. `make dev-install` then Helm-installs against the same current context.

**Expected:** Every `kubectl` and `helm` call targets `kind-${CLUSTER}` through `--context`/`--kube-context`, or the header describes what a re-run actually does.

**Actual:** A re-run changes whichever cluster is current. Installing MetalLB into a cluster that already has a load-balancer implementation can disrupt that cluster's Services.

**Evidence:** [evidence/review-deploy/verification.md#c-deploy-01](evidence/review-deploy/verification.md#c-deploy-01)

### F-233

**Repro / observation**
1. `e2e.sh:11` says it "Loads pre-built gameplane/{operator,api,agent}:<tag> images".
2. `:165` prints `loading gameplane/{operator,api,agent,sentinel,capture-sidecar}:${TAG}`.
3. The loop builds `gameplane-test/` images when missing, and loads `gameplane-test/gameprobe` and `gameplane-test/fakeoidc`.

**Expected:** The header and the message name the `gameplane-test/` prefix and the full set of images.

**Actual:** Both name the wrong prefix, and the header lists incomplete image counts.

**Evidence:** [evidence/review-deploy/verification.md#c-deploy-03](evidence/review-deploy/verification.md#c-deploy-03)

### F-234

**Repro / observation**
1. `up.sh:147` checks if the `kind-registry` container exists and is running.
2. If it's stopped but not removed, `docker run --name kind-registry` fails with a name-conflict error.
3. `set -euo pipefail` (`:9`) aborts the bootstrap.

**Expected:** A stopped registry is restarted (`docker start`), and a new one is created only when none exists.

**Actual:** The bootstrap aborts on the raw Docker name-conflict error.

**Evidence:** [evidence/review-deploy/verification.md#c-deploy-04](evidence/review-deploy/verification.md#c-deploy-04)

### F-235

**Repro / observation (by reading code)**
1. `up.sh:196` applies `https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml`.
2. `up.sh:28-31` forbids floating refs for MetalLB and pins `v0.14.9`.
3. CI is not affected (e2e.sh doesn't install ingress-nginx).

**Expected:** The ingress-nginx manifest URL names an immutable release tag (e.g. `controller-vX.Y.Z`), held in a variable next to `METALLB_VERSION`.

**Actual:** Each fresh `make dev-up` applies whatever upstream `main` holds at that moment.

**Evidence:** [verification-deploy.md#c-deploy-h01](verification-deploy.md#c-deploy-h01)

### F-236

**Repro / observation**
1. Read `hack/check-links.sh:166`. The strip is `LC_ALL=C sed … -e 's/[[:punct:]]//g'`. In the C locale `[[:punct:]]` matches only the 32 ASCII punctuation bytes, so it never matches the UTF-8 bytes of `→` (E2 86 92) or `—` (E2 80 94).
2. Copy lines 151-183 of the script into a scratch file, `source` it, and run `github_slug "$(strip_heading_marker '### Config schema → wizard')"`. It prints `config-schema-→-wizard`. `## API → Agent` gives `api-→-agent`. `# Signing key rotation — Ed25519 → ECDSA P-256 (2026-07)` gives `signing-key-rotation-—-ed25519-→-ecdsa-p-256-2026-07`.
3. For comparison, `## Beta Status & Limitations` gives `beta-status--limitations`, which matches the worked example at `:60-67`. So the ASCII path is correct, and only non-ASCII punctuation and symbols are affected.
4. GitHub builds the anchor by removing every character that is not a letter, digit, space, hyphen or underscore, which is the rule the header states. The first heading's GitHub anchor is therefore `#config-schema--wizard`.
5. Consequence: a link `[wizard](module-authoring.md#config-schema--wizard)` in any audited doc works on GitHub but makes `make check-links` report `missing anchor`. A link to `#config-schema-→-wizard` passes the checker but is dead on GitHub.
6. `grep -rnoE '\]\([^)]*#(config-schema|api-|signing-key-rotation|sqlite-database-adoption|trust-chain)[^)]*\)'` over the audited files finds nothing, so no current link hits this.

**Expected:** Rule 4 as documented at `check-links.sh:48-49`: remove "any character that is not a letter (including Unicode letters), digit, space, hyphen, or underscore". Non-letter Unicode characters such as `→` and `—` are dropped, and Unicode letters are kept.

**Actual:** Only ASCII punctuation is dropped, and Unicode punctuation and symbols stay in the slug. The defect is latent until someone links to one of the five headings, and then it gives a false failure or a false pass.

**Evidence:** [evidence/review-hack/verification.md#c-hack-01](evidence/review-hack/verification.md#c-hack-01)

### F-237

**Repro / observation**
1. Read `CLAUDE.md:104`: "`make dev-load` # Rebuild and reload local images into Kind".
2. Read `Makefile:367-371`. `dev-load` has no prerequisites. Its recipe is `kind load docker-image $(REGISTRY)/{operator,api,web,agent}:$(TAG)`, and its own help string is "Load local images into kind cluster (kind only)".
3. On a running `make dev-up` cluster, edit a file under `api/` and run `make dev-load`. kind loads the `ghcr.io/valgulnecron/gameplane/api:dev` image that is already in the local Docker daemon, which was built before the edit. The edit does not reach the cluster until `make images TAG=dev` (or `make image-api`) runs first.

**Expected:** `CLAUDE.md` says what `dev-load` does, "load the already-built local images into kind", and names the build step, matching the target's help string.

**Actual:** `CLAUDE.md` says the target rebuilds. An agent following it after a code change deploys a stale image.

**Supporting observation for C-deploy-02 (not counted here):** On 2026-09-24, anonymous GHCR manifest requests for `valgulnecron/gameplane/sentinel:dev` and `valgulnecron/gameplane/agent:dev` both returned 404, and the same requests for `:edge` returned 200. `dev-install` points a `make dev-up` kind cluster at `ghcr.io/valgulnecron/gameplane/<component>:dev` (`Makefile:386-387`). For the 8 images `dev-load` skips (sentinel, capture-sidecar, the three tunnels, mcp-server, telemetry-receiver and audit-syslog-bridge), neither kind nor GHCR can supply that ref, because `deploy/kind/up.sh` loads no images and GHCR has no `:dev` tag.

**Evidence:** [evidence/review-hack/verification.md#c-hack-04](evidence/review-hack/verification.md#c-hack-04)

### F-238

**Repro / observation**
1. Read `.github/workflows/ci.yaml:1139-1159`. `report` needs 18 jobs, one of them `capture-sidecar-setcap-proof`.
2. Read `:1247-1252`. `NEEDS_ORDER` lists 17 keys and leaves out `capture-sidecar-setcap-proof`. Read `:1257-1275`: no matcher recognises "capture-sidecar CAP_NET_RAW survival".
3. Read `:1277-1299`. `passed`, `failed`, `skipped`, `cancelled` and `failingRows` are computed only from `NEEDS_ORDER`. The headline (`:1437`) prints `failed`, and the "Failing" table (`:1439-1445`) is rendered only when `failed > 0`.
4. Take a PR with `go=true` where every job passes except the setcap proof. For example, a `capture-sidecar/Dockerfile` change that loses the file capability makes the proof `exit 1` at `:362-373`. On that PR the job summary and the sticky comment say `0 failed`, with no Failing table, while the workflow run is red.

**Expected:** Spec 008 FR-016 says the reporter consolidates status across all jobs. `contracts/permissions-matrix.md:58-60` says a new job needs "the `needs:` list, the `NEEDS_ORDER` array, and the `JOB_MATCHERS` map. Miss any one and the new job is silently absent from the PR comment."

**Actual:** Only the `needs:` edit was made, so the setcap proof is missing from the tally and from the failing-jobs table.

**Evidence:** [evidence/review-github-workflows/verification.md#c-github-workflows-01](evidence/review-github-workflows/verification.md#c-github-workflows-01)

### F-239

**Repro / observation**
1. Read `publish-edge.yaml:17-29`. The listed paths are `operator/`, `api/`, `web/`, `agent/`, `netguard/`, `audit-syslog-bridge/`, `telemetry-receiver/`, `mcp-server/`, `go.work`, `go.work.sum`, `**/Dockerfile` and the workflow file. `**/Dockerfile` matches `sentinel/Dockerfile` and `capture-sidecar/Dockerfile` only when that file itself changes, and it never matches `tunnel/Dockerfile.{frp,tailscale,playit}`.
2. Compare the `COPY` lines: `sentinel/Dockerfile:7-13` (`gameproto/`, `sentinel/`), `capture-sidecar/Dockerfile:7-13` (`svcutil/`, `capture-sidecar/`), `tunnel/Dockerfile.*:8-14` (`gameaction/`, `tunnel/`), `api/Dockerfile:7-15` (`netguard/`, `gameaction/`, `gp-module/`, `api/`) and `agent/Dockerfile:7-14` (`netguard/`, `gameaction/`, `agent/`). The paths that land in an image but are not listed are `sentinel/**`, `capture-sidecar/**`, `tunnel/**`, `gameaction/**`, `gameproto/**`, `svcutil/**` and `gp-module/**`. They feed 7 images: sentinel, capture-sidecar, the three tunnels, api and agent.
3. Live check: `gh run list --workflow publish-edge.yaml` has no run for master merge `9e1c7bc3` (#399, `capture-sidecar/go.mod` bump, 2026-09-21 23:28 +02:00) or for `b21684ed` (#402, tunnel Dockerfile base bump, 23:30). The next run is for `26e7d444` (#410, a web change, 2026-09-22 00:01).
4. Each run rebuilds all 12 matrix images, so a change under an unlisted path reaches `:edge` only when a later push touches a listed path, or when someone dispatches the workflow by hand. A chart installed with `image.tag=edge` runs the older build until then.

**Expected:** `publish-edge.yaml:15-16` says "Only rebuild when something that lands in an image changes", and `:3` says "on every push to master". `docs/install.md:29` says "Every push to `main` publishes rolling `:edge` images."

**Actual:** A push that changes only an unlisted input publishes nothing. There is also wording drift. The header comment (`:8`) names 6 of the 12 images. `docs/install.md:29` and `values.yaml:19` call the branch `main`, but it is `master` (`publish-edge.yaml:14`).

**Evidence:** [evidence/review-github-workflows/verification.md#c-github-workflows-02](evidence/review-github-workflows/verification.md#c-github-workflows-02)

### F-240

**Repro / observation**
1. Case A (appVersion bump): a PR changes only `charts/gameplane/Chart.yaml` `appVersion` (for example to `0.3.0-rc.1`). The workflow starts, but only the `charts` filter matches, so the combine step (`:186-209`) emits `go=false`, `specs=false` and `docs=false`. `lint` is skipped, and `make check-doc-versions` does not run. The next PR that touches one of the 18 audited docs runs `lint` and fails on every unmarked `0.2.0-beta.8` in those docs.
2. Case B1 (renamed link target): a PR renames `docs/agent-architecture.md`, which `docs/architecture.md:7` links to. `docs/**` starts the workflow, but the `docs` filter lists only the 18 audited files and the two scripts, so `lint` is skipped and the broken link merges green.
3. Case B2 (archived spec): a PR that only `git mv`s `specs/012-docs-refresh-and-outreach` to `specs/done_012-…` (CLAUDE.md rule 16) and misses the link at `docs/contributing.md:155` changes only `.md` files under `specs/`. Those are excluded by `!**.md` (`:18`, `:33`), so CI doesn't start.
4. Open PR #420 adds `hack/test-check-doc-versions.sh` and its testdata to the `docs` filter but not `Chart.yaml`, so Case A stays open after it merges.

**Expected:** The two doc gates run whenever an input they read changes: `charts/gameplane/Chart.yaml`, and the files the audited docs link to (`docs/**`, `CHANGELOG.md`, `CLAUDE.md`, `LICENSE`, `cosign*.pub`, `specs/012-*/outreach.md`).

**Actual:** They run only when one of the 18 audited docs, or one of the two checker scripts, changes. A stale version or broken link then fails a later, unrelated PR.

**Evidence:** [evidence/review-github-workflows/verification.md#c-github-workflows-03](evidence/review-github-workflows/verification.md#c-github-workflows-03)

### F-241

**Repro / observation**
1. Open a PR that edits only `.golangci.yml`, for example to enable a linter that flags existing code. The file is not `.md`, so the workflow starts.
2. The `go`, `ci`, `specs` and `docs` filters (`:101-173`) don't list the file, so the combine step emits `go=false`, `specs=false` and `docs=false`. All 15 `lint (…)` legs and all `go (…)` legs are skipped, and the PR merges green.
3. The lint steps run `golangci-lint-action` with `working-directory: <module>` (`:432-460`). golangci-lint finds the root config by walking up parent directories, so the change first takes effect on the next Go PR, which then fails lint on code it didn't change.

**Expected:** A lint-config change forces the lint job to run, the same way the `ci` filter (`:138-144`) forces everything on for `Makefile`, `go.work` and the workflow itself.

**Actual:** `.golangci.yml` is in no filter.

**Evidence:** [evidence/review-github-workflows/verification.md#c-github-workflows-04](evidence/review-github-workflows/verification.md#c-github-workflows-04)

### F-242

**Repro / observation**
1. Read `:812-816` and `:860-870`. Every `api-auth` leg (amd64 and arm64) runs `buckets.sh regex ratelimit` as a last step, after its own bucket passes.
2. `test/e2e/buckets.sh:302` lists `ratelimit` as a bucket in its own right.
3. Read `:1410-1420`. `bucketSet` adds names only from job names (`^e2e (operator|api-auth|api-roles|api-rbac|api-agent|api-mods) \/`, multicluster and upgrade), plus `bot-fast` from `needs['e2e-game-bot']`. Nothing adds `ratelimit`.
4. On any green run where the `api-auth` legs ran, the report lists `api-auth` but not `ratelimit`.

**Expected:** `ci.yaml:1127-1128`: the report shows "which e2e buckets actually ran".

**Actual:** `ratelimit` never appears. A correct fix needs step-level information, not just the job name, because the tail is skipped when the `api-auth` bucket step fails.

**Evidence:** [evidence/review-github-workflows/verification.md#c-github-workflows-06](evidence/review-github-workflows/verification.md#c-github-workflows-06)

### F-243

**Repro / observation**
1. Read `:567`. `npm run test:cover` runs vitest with `thresholds` (`web/vitest.config.ts:37-42`) and the `json-summary` reporter (`:23`).
2. Read `:568-584`. The step runs `if: always()`. It exits early only if `coverage/coverage-summary.json` is missing, and otherwise POSTs `"state":"success"`, a literal, for context `coverage/web`.
3. When a threshold fails, vitest writes its reports and then sets a failing exit code, so the summary exists and the step posts `success`. I did not reproduce this with a run (Rule 8), and `web/node_modules` is not installed here, so the order comes from vitest's coverage-provider behaviour, not from reading the installed source. Whatever the order, the posted state never depends on the gate result.
4. `gh api repos/ValgulNecron/Gameplane/rules/branches/master` returns only `deletion`, `non_fast_forward` and `pull_request` rules, so `coverage/web` is not a required check.

**Expected:** The status state reflects the gate (`failure` below threshold). `docs/security.md:448-449` says the `web` job's `statuses: write` is there "to mark PR checks".

**Actual:** A green `coverage/web` check appears next to a red `web` job. It can't let a PR through, but it misreports the coverage gate.

**Evidence:** [evidence/review-github-workflows/verification.md#c-github-workflows-07](evidence/review-github-workflows/verification.md#c-github-workflows-07)

### F-244

**Repro / observation**
1. `grep -n 'directory' .github/dependabot.yml` gives `gomod` directories `/agent`, `/api`, `/audit-syslog-bridge`, `/capture-sidecar`, `/gameaction`, `/gameproto`, `/mcp-server`, `/netguard`, `/operator`, `/sentinel`, `/svcutil`, `/telemetry-receiver`, `/test/e2e` and `/tunnel`. There is no `/gp-module`.
2. `go.work` lists `./gp-module`. `gp-module/go.mod` requires `gopkg.in/yaml.v3 v3.0.1` and `k8s.io/apimachinery v0.37.0`.
3. Spec 008 `data-model.md:125-133` states the invariant `count(gomod entries) == count(module lines in go.work)`, which is now 14 against 15. Its "COVERAGE GAP" note says a 15th module added without an entry won't be caught automatically, and that is what happened.

**Expected:** Spec 008 SC-003 ("Dependabot monitors all … Go submodules … without omitting any repository component") and FR-017: every `go.work` module has a `gomod` entry.

**Actual:** `gp-module` has no entry, so Dependabot opens no version-update PRs for its `go.mod`. Shipped images are not affected, because the api image builds with api's own required versions.

**Evidence:** [evidence/review-github-workflows/verification.md#c-github-workflows-09](evidence/review-github-workflows/verification.md#c-github-workflows-09)

### F-245

**Repro / observation**
1. Read `docs/contributing.md:50`: `actionlint .github/workflows/*.yml`.
2. `ls .github/workflows/` gives `ci.yaml`, `images.yaml`, `publish-edge.yaml`, `release.yaml`, `republish-modules.yaml`, `screenshot-refresh.yaml` and `visual-diff.yaml`. None ends in `.yml`.
3. In zsh the glob fails with "no matches found" before actionlint starts. In bash the literal `.github/workflows/*.yml` is passed, and actionlint fails to open it. Either way, nothing is linted.
4. CI runs `actionlint` with no arguments from the checkout root (`ci.yaml:711-715`) and pins v1.7.9 by sha256 (`:705-709`). Line 49 of the doc installs `@latest`.

**Expected:** A command that lints the workflows, such as `actionlint` with no arguments (as CI does) or `.github/workflows/*.yaml`, ideally with the version CI pins.

**Actual:** The documented pre-push check fails without linting anything.

**Evidence:** [evidence/review-github-workflows/verification.md#c-github-workflows-10](evidence/review-github-workflows/verification.md#c-github-workflows-10)

### F-246

**Location:**
- `.github/workflows/release.yaml:57-68` (build-push with `push: true` and every metadata tag), `:70-77` (key-presence check) and `:90-107` (keyed sign with 3 attempts). `:19-36` is the images `strategy`, which has no `fail-fast: false`.
- The same push-then-check order appears in `.github/workflows/images.yaml:61-75` and `:76-84` (common base, sign at `:94`), in `images.yaml:224-240` and `:242-250` (game images, sign at `:260`), and in `.github/workflows/publish-edge.yaml:87-99` and `:104-111` (sign at `:144`).

**Control:** Official tags point only at signed artifacts. It is stated at `release.yaml:68-69` ("Signing is mandatory for an official release: fail rather than publish unsigned images") and at `docs/contributing.md:146-148` ("Signing is **mandatory and fail-closed**").

**Repro / observation** (how to confirm the control holds):
1. Read `release.yaml:46-68`. The build step pushes every tag `docker/metadata-action` produced (`:54-56`). For a final tag such as `v0.3.0` that is `v0.3.0`, `0.3.0`, `0.3` and `latest`. The push happens before the "require signing key" step (`:70`) and the "sign image" step (`:90`) run.
2. Read `release.yaml:19-20`. The `strategy` block sets only `matrix`, so GitHub's default `fail-fast: true` applies: when one leg fails, the in-progress legs are cancelled, whatever step they are on.
3. From steps 1 and 2: a run that stops between push and sign leaves the tags on a digest with no signature until the job is re-run. That happens when signing exhausts its retries (`:97-107`) or when a sibling leg fails. The job still fails, so the gap is not silent, but publication comes before the check.
4. After every release run, including failed or cancelled ones, confirm with `cosign verify --key cosign.pub ghcr.io/valgulnecron/gameplane/<component>:<tag>` for all 12 components (`release.yaml:21-36`) and every tag that run created. Also confirm `cosign verify --key cosign.pub ghcr.io/valgulnecron/charts/gameplane:<version>` for the chart. Do the same for `:edge` after a failed `publish-edge` run.
5. After a fix, confirm by reading the order: the key check runs before any push step, and tags are attached only to a digest that has already been signed.

**Expected:** An official tag only ever resolves to a digest whose signature already exists. One approach is to push by digest, sign, and then attach tags. At minimum, the key check should run before any push, and the matrix should use `fail-fast: false` with a documented re-run policy. `docs/contributing.md:146-148` then holds at every point of a run, not just at its end.

**Actual:** Every image job fails closed, but only after it has published. The unsigned window lasts until someone re-runs the job. I did not observe this live, since that needs a failing release run. The digest in the window is still the project's own CI build, and a consumer who verifies gets a verification failure, not a false pass.

**Evidence:** [verification-github-workflows.md#h-github-workflows-01](verification-github-workflows.md#h-github-workflows-01)

### F-247

**Location:**
- `.github/workflows/publish-edge.yaml:30` (`workflow_dispatch: {}`), `.github/workflows/republish-modules.yaml:10-11` (`on: workflow_dispatch`) and `.github/workflows/images.yaml:4` (`workflow_dispatch:`). None of their jobs has an `environment:` or an `if: github.ref == …` guard (grep over `.github/workflows/`).
- Repository settings: Actions secrets `COSIGN_PRIVATE_KEY` and `COSIGN_PASSWORD` are repository-scoped, the only environment is `copilot`, and the only ruleset is `18692396` ("protect main", target `branch`).

**Control:** Signing-secret confinement. Spec 008 `spec.md:176` (Assumptions) says: "Production signing secrets (`COSIGN_PRIVATE_KEY`) remain strictly restricted to master branch and release tag workflows (`publish-edge.yaml`, `release.yaml`, `images.yaml`, `republish-modules.yaml`)". This complements the protected-master ruleset (one human approval, no self-approval).

**Repro / observation** (how to confirm the control holds):
1. Read the `on:` blocks of the three files. Each accepts `workflow_dispatch`, and GitHub lets the person dispatching choose the ref.
2. Run `grep -n 'environment:\|github.ref ==' .github/workflows/*.yaml`. It finds no job-level ref guard or environment on any signing job.
3. `gh api repos/ValgulNecron/Gameplane/actions/secrets --jq '.secrets[].name'` lists `COSIGN_PASSWORD` and `COSIGN_PRIVATE_KEY` at repository scope. `gh api repos/ValgulNecron/Gameplane/environments` lists only `copilot`. Repository-scoped secrets are not tied to a ref, so adding a ref guard to these three files is necessary but not sufficient. The setting that actually confines the key is environment scoping with a deployment policy.
4. `gh api repos/ValgulNecron/Gameplane/rulesets` returns a single branch-target ruleset. Nothing restricts who can create `v*` tags, and `release.yaml` signs whatever commit such a tag points at.
5. After a fix, confirm the following:
   - The `COSIGN_*` secrets are gone from the repository list and exist only in an environment (for example `release-signing`) whose deployment policy allows `refs/heads/master` and `refs/tags/v*` only (Settings → Environments).
   - Every signing job declares that environment.
   - A tag ruleset limits who can create `v*` tags.

**Expected:** The signing key is available only to runs on `master` or on a `v*` release tag, and this is enforced by repository configuration, as spec 008's assumption states.

**Actual:** Confinement relies on convention. Any account with write access can run the signing workflows against a ref other than master, and those runs get the key. Fork pull requests are unaffected, because they receive no secrets.

**Evidence:** [verification-github-workflows.md#h-github-workflows-02](verification-github-workflows.md#h-github-workflows-02)

### F-248

**Location:** `api/internal/handlers/clusters.go:192-236` (`delete`): the Secret name comes from the Cluster CR at `:213-220`, and the delete at `:230-233` has no label check. Create path for comparison: `clusters.go:130-145`, which sets `gameplane.local/cluster-kubeconfig=true`. Load-path guard: `api/internal/kube/loader.go:38-41`. Stated rule: `charts/gameplane/templates/api.yaml:129-144`, where the API Role grants `delete` on every Secret in the control-plane namespace and the comment names code as the real boundary. Documentation: `docs/security.md` ("Kubeconfig Secret handling", label-guard bullet, around `:609-620`), and `docs/install.md:442-492` (Path 1, kubectl apply with a user-chosen Secret name).

**Control:** the API's code-level boundary on control-plane Secrets. RBAC can't filter by label, so the API may delete only Secrets it is entitled to manage. A Cluster CR's reference must not make the API delete an arbitrary control-plane Secret: for example an auth-provider client secret, a notification-sink credential or a mod-registry key.

**Repro / observation** (defensive: confirms whether the control holds; by reading `213bdaa7`):
1. Read `clusters.go:203-220`. The handler loads the Cluster CR and takes `spec.kubeconfigSecret.name` as it is.
2. Read `clusters.go:230-233`. It deletes that name in `h.namespace` (the control-plane namespace) and ignores the result. It never reads the Secret's labels.
3. Read `kube/loader.go:38-41`. Loading the same reference refuses a Secret without `gameplane.local/cluster-kubeconfig=true`, with a comment saying the check exists "to prevent loading arbitrary secrets".
4. Read `charts/gameplane/templates/api.yaml:129-141`. For the other Secret-managing features, deletes also require `gameplane.local/managed-by=gameplane-api`, "so kubectl-/GitOps-created Secrets are never deleted over HTTP". The cluster create path (`clusters.go:133-139`) sets only the kubeconfig label, not `managed-by`.
5. How to confirm on a test install, as a `cluster:manage` holder, without touching real credentials. Create a throwaway Secret `audit018-unrelated` without the kubeconfig label in the control-plane namespace. Create a Cluster CR `audit018-probe` whose `spec.kubeconfigSecret.name` is `audit018-unrelated`. Call `DELETE /clusters/audit018-probe`. With the control in place, `audit018-unrelated` still exists afterwards. Clean up both objects.

**Expected:** `DELETE /clusters/{name}` deletes the referenced Secret only when it carries `gameplane.local/cluster-kubeconfig=true`, the same check the load path applies. Otherwise the CR is removed and the Secret is left in place. The maintainer decides whether a kubeconfig Secret created with kubectl or GitOps (Path 1) should be deleted over HTTP at all. If not, create should also set `managed-by=gameplane-api`, and delete should require it, as the other API-managed Secrets do.

**Actual:** the handler deletes whichever control-plane Secret the Cluster CR names, labelled or not, and discards any error.

**Evidence:** [verification-followup.md#followup-3](verification-followup.md#followup-3)

### F-249

**Location:** `api/internal/handlers/roles.go:160-238` (`update`). Only the built-in `admin` role is refused (`:171-176`). The permission set is replaced at `:205-220` with no user-management check. The guards this path doesn't apply are in `api/internal/handlers/users.go`: self-demotion `:517-524`, last user manager on role change `:525-542`, last user manager on delete `:449-463`. Counting: `api/internal/db/rbac.go:63-106` (`RoleGrantsUserManagement`, `UserManagesUsers` and `UserManagerCount` all join a user's primary role to `role_permissions`). Route gate: `api/internal/rbac/rbac.go:176-177` (`roles:manage`). Documentation: `docs/security.md:121-122` ("The API refuses to demote or delete the last user who can manage users, and refuses self-demotion below `users:manage`"). The OIDC re-evaluation path applies the same guard (`docs/security.md:712-718`).

**Control:** the user-administration lockout guards. No API action may leave the install with zero users who hold `users:manage`, and a caller may not remove their own `users:manage` access.

**Repro / observation** (defensive: confirms whether the control holds; by reading `213bdaa7`):
1. `users.go:511-542` checks, on a primary-role change, whether the new role still grants user management. It refuses self-demotion, and it refuses when the target is the last user manager.
2. `roles.go:183-220` validates only that each permission is in the catalog and isn't `*`. It then deletes and re-inserts the role's permissions. It never calls `RoleGrantsUserManagement`, `UserManagesUsers` or `UserManagerCount`.
3. `db/rbac.go:95-106` counts user managers through their primary role's current permissions. Removing `users:manage` from a role therefore removes every user whose primary role it is from the count at once. The `admin` role (`*`) can't be edited, so the count reaches zero only when no user has `admin` as their primary role. `users.go:517-524` allows that state, because a user may switch to a custom role that still grants `users:manage`.
4. How to confirm on a test install with throwaway objects only. Create a custom role `audit018-um` with `users:manage` and `roles:manage`, and a throwaway user `audit018-um-user` with that primary role. As that user, `PUT /roles/audit018-um` with a permission list that drops `users:manage`. With the control in place, the API answers 400, the same as the self-demotion refusal. The same request is also expected to fail whenever it would leave `UserManagerCount` at zero. Delete the throwaway user and role afterwards.

**Expected:** `roles.update` (and any future bulk role edit) applies the same lockout guards as `users.update`. It refuses a permission change that removes `users:manage` from the caller's own primary role. It also refuses one that would leave no user whose primary role grants `users:manage`.

**Actual:** a `roles:manage` holder can remove `users:manage` from any editable role, including their own primary role, and the change is applied even when it leaves no user who can manage users. Recovery then needs the `bootstrap-admin` break-glass command.

**Evidence:** [verification-followup.md#followup-4](verification-followup.md#followup-4)

### F-250

**Location:** `docs/security.md:271-274` (Network Capture Security Exception: "The game container retains its unprivileged posture: `runAsNonRoot: true`, `allowPrivilegeEscalation: false`, and no elevated capabilities. Only the capture sidecar holds `CAP_NET_RAW`; exploit of game code cannot grant packet-capture ability."). Code: `operator/internal/controller/gameserver_controller.go:1967` (game container `SecurityContext`), `:1971-1990` (`gameContainerSecurityContext` and its rationale), `:1503` and `:2074-2084` (`gamePodSecurityContext`, `fsGroup` only). For comparison: the agent container's fixed hardening at `:2183-2188`, and the capture sidecar's at `:1580-1611`. Related text: `docs/security.md:226-238` (managed pods are hardened; game pods are shaped per template; `podSecurity.enforceRestricted=true` is recommended for untrusted modules).

**Control:** the privilege posture of the game container, and the capture feature's documented isolation claim that only the sidecar can capture packets in a game pod. Operators rely on this text when they decide whether to enable capture and whether untrusted modules need extra admission policy.

**Repro / observation** (defensive: confirms whether the control holds; by reading `213bdaa7`):
1. Read `gameserver_controller.go:1981-1990`. With no `spec.security`, or one without `runAsUser`/`runAsGroup`, the game container gets no `securityContext`. With one, it gets only `runAsUser` and `runAsGroup`. Neither case sets `runAsNonRoot`, `allowPrivilegeEscalation: false` or a capability drop.
2. Read `:2078-2084`. The pod-level context carries only `fsGroup`, so no pod-level `runAsNonRoot` fills the gap.
3. Read `:1971-1980`. Leaving these fields unset is deliberate, so that arbitrary third-party game images keep working.
4. The shipped templates without a `spec.security` block (`dayz`, `dont-starve-together`, `enshrouded`, `garrys-mod`, `minecraft-java`, `rust`, `valheim`, `v-rising`) run the game as the image's own user.
5. How to confirm on a test install. For a throwaway GameServer from one of those templates, read the game container's `securityContext` with `kubectl get pod <name>-0 -o jsonpath` and expect it to be empty today. Then read `/proc/1/status` (`Uid`, `CapEff`, `CapBnd`, `NoNewPrivs`) in the game container. After the fix, either the docs match what these show, or the values show a non-root uid, `NoNewPrivs: 1` and no `NET_RAW` in the bounding set.
6. Related check for the maintainer, not rated here. The `restricted` Pod Security profile that `docs/security.md:236-238` recommends requires `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`, `runAsNonRoot: true` and a seccomp profile on every container. Confirm on a test install whether game pods are admitted with `podSecurity.enforceRestricted=true`.

**Expected:** `docs/security.md:271-274` states what the operator actually sets. The game container runs as the template's `spec.security` uid/gid, or else as the image's default user. The operator adds no `runAsNonRoot`, no `allowPrivilegeEscalation: false` and no capability drop. The packet-capture isolation claim is limited to what that posture supports. Alternatively, if the maintainer wants the sentence to be the contract, the operator sets at least `allowPrivilegeEscalation: false` and drops `NET_RAW` on the game container, and the docs name which templates run non-root.

**Actual:** the docs claim `runAsNonRoot: true`, `allowPrivilegeEscalation: false` and no elevated capabilities for every game container. The operator sets none of these, and templates without `spec.security` run as whatever user the image declares.

**Evidence:** [verification-followup.md#followup-7](verification-followup.md#followup-7)

### F-251

**Location:** `web/nginx.conf.template` (no `client_max_body_size` directive anywhere in the file — checked the full 133 lines); `web/Dockerfile:36-37` (bakes the template into the image, no runtime override point); `charts/gameplane/templates/ingress.yaml:16-24` (all paths, including uploads, route to `gameplane-web` when `web.enabled`, the default); `charts/gameplane/values.yaml:307` (ingress-level `proxy-body-size: "64m"`); `api/cmd/main.go:254,602-611,619-623` (API's own `bodyLimit` middleware exempts `/servers/{name}/files/upload`); `api/internal/ws/dialer.go:244-247` (default proxy cap `64<<20` = 64 MiB for the file/mod upload proxy paths).

**Repro / observation:**
1. `grep -c client_max_body_size web/nginx.conf.template` → `0`.
2. nginx's compiled-in default for `client_max_body_size` (unset) is `1m`, and nothing in `web/nginx.conf.template`'s `server {}` or `location {}` blocks sets it.
3. `web/Dockerfile:37` copies the template verbatim into the image at build time (`COPY web/nginx.conf.template /etc/nginx/templates/gameplane.conf.template`); the only substitutions applied at container start are `${API_UPSTREAM}` and `${NGINX_RESOLVER}` (per the file's own header comment), so `client_max_body_size` cannot be raised via env var, ConfigMap, or Helm value — only by editing the source file and rebuilding the image.
4. A dashboard user uploads a file or mod archive larger than 1 MiB via Files.tsx or the mod-upload UI. The browser's request reaches the ingress (which allows up to 64 MiB per `nginx.ingress.kubernetes.io/proxy-body-size: "64m"`), then the `gameplane-web` pod's own nginx, which rejects it with `413 Request Entity Too Large` before the request is proxied to `gameplane-api` at all.
5. The API itself would have accepted it: `bodyLimit(1<<20)` at `api/cmd/main.go:254` is explicitly bypassed for `isUploadPath` (`/servers/{name}/files/upload`), and the agent-proxy path caps uploads at 64 MiB (`dialer.go:247`, raised further for mod uploads per the same file's `httpProxyLimit` callers) — the API is never given the chance to apply its own, larger limit.

**Expected:** `web/nginx.conf.template` sets `client_max_body_size` to at least the ingress's 64m (or the API's model-specific per-route caps), so a body the ingress and API are willing to accept is not rejected by the pod sitting between them.

**Actual:** The web pod's nginx silently falls back to the 1 MiB compiled-in default, so any dashboard upload over 1 MiB gets `413` from the web pod regardless of what the ingress or API would allow, and there is no config knob to raise it without rebuilding the `web` image.

**Evidence:** [evidence/review-followup/verification.md#followup-pub-1](evidence/review-followup/verification.md#followup-pub-1)

### F-252

**Location:** `docs/oidc.md:165` (Okta example), `:229` (Azure AD example), `:266` and `:282` (Keycloak CLI + values example); `charts/gameplane/values.yaml:239-244` (schema); `charts/gameplane/templates/api.yaml:334-340` (consumer).

**Repro / observation:**
1. `grep -n clientSecretRef docs/oidc.md` returns four hits, all of the shape `clientSecretRef: "<name>-oidc-secret"` (YAML example blocks) or `--set api.oidc.clientSecretRef="keycloak-oidc-secret"` (Helm CLI example), i.e. a bare string in every case.
2. `charts/gameplane/values.yaml:239-244` declares the real schema: `clientSecretRef: { name: gameplane-oidc, key: ... }`, an object with `name` and `key`.
3. `charts/gameplane/templates/api.yaml:337-340` dereferences it as an object: `secretKeyRef: { name: {{ .Values.api.oidc.clientSecretRef.name }}, key: {{ .Values.api.oidc.clientSecretRef.key }} }`, gated on `.Values.api.oidc.enabled`.
4. Reproduced live: `helm template gameplane charts/gameplane -f <values with api.oidc.enabled: true, clientSecretRef: "keycloak-oidc-secret">` (i.e., the Keycloak values-file example from the doc, plus turning `enabled: true` on since the doc's own example never does) prints `coalesce.go:298: warning: cannot overwrite table with non table for gameplane.api.oidc.clientSecretRef (map[key:clientSecret name:gameplane-oidc])` and then fails outright: `Error: template: gameplane/templates/api.yaml:339:34: executing "gameplane/templates/api.yaml" at <.Values.api.oidc.clientSecretRef.name>: can't evaluate field name in type interface {}`.
5. Separately: `grep -n "oidc.enabled\|enabled: true" docs/oidc.md` matches nothing — none of the doc's own example blocks actually sets `api.oidc.enabled: true`, so copy-pasting an example as given renders no OIDC env vars at all (the whole block is gated on that flag) until a reader also adds `enabled: true` themselves, at which point they hit the type error above.

**Expected:** The doc's Helm examples use the real schema (`clientSecretRef: { name: ..., key: ... }` in YAML, or `--set api.oidc.clientSecretRef.name=... --set api.oidc.clientSecretRef.key=...` on the CLI) and include `api.oidc.enabled: true`, so a reader who copies an example gets a working install.

**Actual:** Every example gives `clientSecretRef` the wrong shape and omits `enabled: true`; following any of them as literally written either renders nothing OIDC-related, or — once a reader also flips `enabled` on, as the surrounding prose clearly intends — fails `helm template`/`helm install` outright with a Go-template type error.

**Evidence:** [evidence/review-followup/verification.md#followup-pub-2](evidence/review-followup/verification.md#followup-pub-2)

### F-253

**Location:** `README.md:230`; `specs/done_018-v0-3-release-readiness/plan.md:20`; `go.work:1`; all 15 workspace `go.mod` files (`agent`, `api`, `audit-syslog-bridge`, `capture-sidecar`, `gameaction`, `gameproto`, `gp-module`, `mcp-server`, `netguard`, `operator`, `sentinel`, `svcutil`, `telemetry-receiver`, `tunnel`, `test/e2e`), each at line 3 (`go.work` at line 1).

**Repro / observation:**
1. `sed -n 230p README.md`: "Requires: Go 1.25+, Node 20+, Docker, kind, kubectl, helm, ...".
2. `sed -n 20p specs/done_018-v0-3-release-readiness/plan.md`: "**Language/Version**: ... Fixes land in the existing stack: Go 1.25 (`go.work`, 15 modules including `gp-module` and `test/e2e`), ...".
3. `grep '^go ' go.work` → `go 1.26.0`.
4. `grep -h '^go ' */go.mod` (all 15 modules, including `gp-module` and `test/e2e`) → `go 1.26.0` in every one, no exceptions.

**Expected:** The quickstart requirement and the plan's technical-context line state the real minimum, Go 1.26 (matching `go.work` and every module's `go.mod`).

**Actual:** Both say "Go 1.25", one minor version behind what `go.work` and all 15 `go.mod` files actually require. Same pattern already tracked per-module for `gameaction`, `audit-syslog-bridge` and `telemetry-receiver` specs.md files (F-152, F-197, F-200), but `README.md` and this feature's own `plan.md` are not covered by any existing finding.

**Evidence:** [evidence/review-followup/verification.md#followup-pub-3](evidence/review-followup/verification.md#followup-pub-3)

### F-254

**Location:** `sentinel/` (no `.gitignore` file present); `.gitignore:1-15` (root, Go section); `mcp-server/.gitignore:1`, `telemetry-receiver/.gitignore:1`, `audit-syslog-bridge/.gitignore:1` (sibling single-binary modules' own ignore files).

**Repro / observation:**
1. `ls sentinel/` shows `Dockerfile, go.mod, go.sum, main.go, main_test.go, sentinel, specs.md, .testcoverage.yml` — `sentinel` is a compiled ELF binary (16,823,897 bytes), and there is no `sentinel/.gitignore`.
2. `git status --porcelain sentinel/` → `?? sentinel/sentinel` (untracked).
3. `git check-ignore -v sentinel/sentinel` exits `1` (no pattern matches; nothing is printed) — confirming the root `.gitignore`'s Go section (`/bin/`, `/dist/`, `*.test`, `*.out`, `*.prof`, `coverage.*`, `coverage/`, `vendor/`, `operator/bin/`, `test/e2e/.kube/`) does not cover a bare `sentinel/sentinel` path.
4. `cat mcp-server/.gitignore` → `/mcp-server`; `cat telemetry-receiver/.gitignore` → `/telemetry-receiver`; `cat audit-syslog-bridge/.gitignore` → `/audit-syslog-bridge`. Each of these three modules, structured the same way as `sentinel/` (a `main.go` directly in the module root, no `cmd/` subdirectory, so `go build ./...` from the module root drops a same-named binary there), carries exactly the one-line `.gitignore` that `sentinel/` lacks.

**Expected:** `sentinel/` has a `.gitignore` (or a root-level pattern) matching its sibling modules, so `go build ./...` inside it can't leave a committable binary.

**Actual:** No such pattern exists for `sentinel/`; the binary is untracked but not ignored, so a routine `git add -A` (or `git add sentinel/`) commits a 16 MB compiled binary to the repository by accident.

**Evidence:** [evidence/review-followup/verification.md#followup-pub-4](evidence/review-followup/verification.md#followup-pub-4)

### F-255

**Location:** `Makefile:260` (`IMAGES` list), `:263` (`images` target), `:353-365` (`dev-up`), `:367-371` (`dev-load`), `:384-391` (`dev-install`); `charts/gameplane/values.yaml` (`mcpServer.enabled: false` at `:400`, `capture.enabled: false` at `:527`); `evidence/review-deploy/verification.md#c-deploy-02`; `evidence/review-hack/verification.md#c-hack-04`; `findings.md:201` (F-237).

**Repro / observation:**
1. `Makefile:260`: `IMAGES := operator api web agent telemetry-receiver sentinel mcp-server capture-sidecar` (8 names). `Makefile:263`: `images: $(addprefix image-,$(IMAGES)) image-audit-syslog image-tunnel-frp image-tunnel-tailscale image-tunnel-playit` — 8 + 4 = 12 images total, confirming the "12 images" premise.
2. `Makefile:367-371` (`dev-load`): four `kind load docker-image` lines, exactly `operator`, `api`, `web`, `agent` — 4 of the 12.
3. `evidence/review-deploy/verification.md`, `C-deploy-02` row: "rejected (duplicate) ... same finding as C-hack-04 ... If the hack-chunk verifier drops C-hack-04, reinstate this one at S3." No `### C-deploy-02` subsection was written (only `C-deploy-01`, `-03`, `-04` have one) — the substantive image-count defect has no kept, evidenced section in this file.
4. `evidence/review-hack/verification.md`, `C-hack-04` row: "kept (narrowed) ... The other half of the candidate says dev-load loads 4 of the 12 built images ... That is the same defect as the deploy chunk's C-deploy-02 ... so it is left there and counted once." The `### C-hack-04` subsection's **Repro/Expected/Actual** cover only the "Rebuild" wording claim; the image-count half appears solely as a **Supporting observation for C-deploy-02 (not counted here)** paragraph beneath it.
5. Net effect: each chunk explicitly defers the substantive image-loading gap to the other chunk, and neither actually files a kept, evidenced `###` section for it. `findings.md:201` (F-237) is titled "CLAUDE.md says dev-load rebuilds when it only loads" — the wording claim only, not the count.
6. Independently confirmed the gap itself is real: `mcpServer.enabled` defaults `false` (`values.yaml:400`) and `capture.enabled` defaults `false` (`values.yaml:527`), so those two of the 8 unloaded images are opt-in. `sentinel` and the three `tunnel-*` images are per-GameServer images the operator references for quiesce/wake-on-connect and relay features rather than always-running `gameplane-system` deployments, so a bare `make dev-up` does not hit an `ImagePullBackOff` from this gap — but enabling mcp-server, capture, quiesce/sentinel, a tunnel relay, telemetry-receiver, or the bundled audit-syslog bridge on a `make dev-up` Kind cluster does, since neither the local `kind` registry (only 4 loaded) nor GHCR (`:dev` tag confirmed absent for `sentinel`, per `C-hack-04`'s supporting observation) can supply the image.

**Expected:** Either `dev-load` (and its `dev-up` caller) loads all images `make images` builds, so every optional component works out of the box on a Kind dev cluster, or `CLAUDE.md`/the target's help text says which images it loads and that the rest need a manual `kind load docker-image` — and the gap has one kept, evidenced item in the audit trail.

**Actual:** `dev-load` loads 4 of 12; the other 8 back optional/on-demand components that silently fail to pull their image on a Kind cluster if exercised, and — because each component chunk deferred the substantive defect to the other — it has no kept `###` section or table row in either `evidence/review-deploy/verification.md`, `evidence/review-hack/verification.md`, or `findings.md` (F-237 covers only the wording half).

**Evidence:** [evidence/review-followup/verification.md#followup-pub-5](evidence/review-followup/verification.md#followup-pub-5)

### F-256

**Location:** `api/internal/rbac/rbac.go:109-144`. The owner/collaborator fallback, with its owner-only check at `:128-136`, runs only when the namespace permission check at `:109` fails. A caller who holds the permission is served at `:144` without any ownership check. Related: the package comment at `:11-15`, and the rule at `:214` (`servers:write` for every non-GET `/servers` path). Role seed: `api/internal/db/migrations/003_roles.sql:40,48`, where the built-in `operator` role holds `servers:write`; no later migration changes that. Handlers that do no ownership check of their own: `api/internal/handlers/ownership.go:79-120` (`transfer`), `:125-230` (`setCollaborators`), `api/internal/handlers/lifecycle.go:57-92` (`wipeDataHandler`) and `api/internal/handlers/resources.go:291-322` (`deleteHandler`). For comparison, the share-link handlers do check ownership themselves (`isServerOwner`, `api/internal/handlers/shares.go:106,205,259,555-572`). Text that currently grants the permission: `docs/security.md:124-135` ("Only the owner and users holding the namespace `servers:write` permission can perform owner-only operations", `:133-134`), `api/internal/rbac/catalog.go:27` (`servers:write`: "Create, edit, delete, and control servers"), `api/specs.md:404-407`, and the dashboard hint "Requires owner or operator role" (`web/src/components/server/ServerActionsMenu.tsx:35-39,62-85`).

**Control:** per-server ownership. Four operations are owner-only: transferring ownership, editing the collaborator list, wiping data and deleting the server. The maintainer's HQ-001 answer says only the server's owner or an admin may perform them. Collaborators keep the access they have today, which excludes these four operations. Holding the namespace `servers:write` permission, through the built-in operator role or a custom role, is not enough on its own.

**Repro / observation** (defensive: confirms whether the control holds; by reading `c44cb179`, the RC-TAG-1 target, where these files are unchanged since `13a859ff`):
1. Read `rbac.go:109-143`. The owner-only check (`isOwnerOnly`, `:129-130`) sits inside `if !allow(...)`, so it runs only when the caller lacks the rule's permission.
2. Read `rbac.go:213-214` and `003_roles.sql:47-49`. `:transfer`, `:collaborators`, `:wipe-data` and `DELETE /servers/{name}` all require `servers:write`. The built-in operator role holds it cluster-wide by default: the migration backfills a `*` binding, and `POST /users` mirrors the primary role as a `*` binding.
3. Read the four handlers. Each one resolves the cluster and namespace, then patches or deletes the GameServer. None reads `gameplane.local/owner-id` or checks the caller's role. `setCollaborators` does fetch the server (`ownership.go:142-148`), but only to drop the owner's ID from the new list.
4. So a caller with namespace `servers:write` passes the middleware at `:109` and reaches these handlers for any server in that namespace, whether or not they own it.
5. To confirm on a test install, use throwaway objects only. Never use `audit018-rbac-test`: these calls are destructive when the control is missing.
   1. As `audit018-admin`, create a throwaway GameServer `audit018-hq001`. The admin is stamped as its owner.
   2. As `audit018-operator`, which is neither owner nor collaborator, make these calls in order:
      - `PUT /servers/audit018-hq001:collaborators` with its own user ID
      - `POST /servers/audit018-hq001:transfer` with its own user ID
      - `POST /servers/audit018-hq001:wipe-data` with `{"confirm":"audit018-hq001"}`
      - `DELETE /servers/audit018-hq001`
   3. With the control in place, each call answers 403, and the server's `gameplane.local/owner-id` and `gameplane.local/collaborators` annotations are unchanged. The same calls made as `audit018-admin` succeed.
   4. Delete the throwaway server afterwards.
6. Related check for the maintainer, not rated here (D18 in `fix-plan-held.md`): other namespace-gated writes can also reach a server the caller doesn't own. These include `PUT /servers/{name}`, lifecycle verbs, console and files, and a Restore onto the server with `backups:restore`. `docs/security.md:135` says backups and restores stay namespace-gated in this release.

**Expected:** `:transfer`, `:collaborators`, `:wipe-data` and `DELETE /servers/{name}` succeed only for the server's owner or an admin, whatever namespace permission the caller holds. A server with no owner annotation, for example one created with kubectl or GitOps, can then be managed this way only by an admin. `docs/security.md:124-135`, `api/specs.md:404-407`, the `servers:write` catalog label and the dashboard gates and hints all state that rule. D18 settles who counts as an admin, and whether the rule should cover more than these four operations.

**Actual:** any caller with namespace `servers:write` can transfer, change the collaborators of, wipe or delete any server in that namespace, whether or not they own it. This includes every holder of the built-in operator role. The docs, the catalog label and the dashboard currently describe this as allowed.

**Evidence:** [questions.md#hq-001-server-ownership-vs-the-operator-role-2026-09-24](questions.md#hq-001-server-ownership-vs-the-operator-role-2026-09-24)

### F-257

**Repro / observation**
1. Push tag `v0.3.0-rc.1`; `release.yaml` runs (run 36051057887).
2. The `images` matrix builds each component for `linux/amd64,linux/arm64` (`release.yaml:62`) under `timeout-minutes: 30` (`release.yaml:15`).
3. Every Go Dockerfile's build stage (`FROM golang:1.27-alpine AS build`, then `GOOS=linux go build`) runs under QEMU for the arm64 platform, because nothing sets `--platform=$BUILDPLATFORM` or `GOARCH`.
4. The `operator` build (the largest dependency tree) passes 30 minutes and the job is cancelled. A re-run of the failed jobs is cancelled the same way. `chart` (`needs: images`) and the GitHub-release job are skipped.

**Expected:** A tag push publishes every component image, the signed chart and the GitHub release within the job timeouts.

**Actual:** rc.1 has images for every component except the operator, no chart and no GitHub release, so the RC can't be installed from the registry.

**Evidence:** release run 36051057887 (both attempts cancelled at the `images / operator` timeout); `release.yaml:15`, `:62`; `operator/Dockerfile:1`, `:18`.

### F-258

**Repro / observation**
1. Put a Module into `Failed` (for example a digest pin that doesn't match, reason `DigestMismatch`).
2. Each reconcile of a Failed Module calls `markPullingTransition` (phase `Failed` → `Pulling`, `Pulling` condition flipped to `True` with a new `LastTransitionTime`) and then `markFailed` (phase back to `Failed`): two status writes.
3. The controller watches `For(&Module{})` without a generation-changed predicate, so each status write queues another reconcile, and the loop repeats for as long as the Module stays Failed.
4. Seen in envtest: a test that does Get+Update on the Module's spec while it is Failed lost all five `retry.DefaultRetry` attempts with "the object has been modified".

**Expected:** A Module that stays Failed for the same generation settles: no status write when nothing changed, or retries paced by a requeue delay instead of its own watch events.

**Actual:** Continuous status churn on every Failed Module: apiserver write load, and conflicts for any client that updates the Module (the dashboard or kubectl) while it is Failed.

**Evidence:** CI job 107809566821 (`TestModule_DigestPinCheckedOnReadyModule`, conflict at `module_verify_envtest_test.go:186`); `operator/internal/controller/module_controller.go` (`markPullingTransition`, `markFailed`).

### F-259

**Repro / observation**
1. Start a capture, then `POST /servers/{name}:capture-stop`. It returns 200, and the NetworkCapture reads `phase=Completed` at once, because `StopNetworkCapture` (`api/internal/kube/capture.go`) patches the phase directly.
2. Straight away, `GET /servers/{name}:capture-file?id=…` returns 409. The download handler checks only the phase, so it proxies to the sidecar. The sidecar still holds the capture as running (`capture-sidecar/internal/httpserver/handlers.go`), because the operator's reconcile that tells the sidecar to stop and sets `SidecarStopped=True` (`operator/internal/controller/networkcapture_controller.go`) hasn't run yet.
3. Seen in CI as a sporadic failure: `TestGameServer_NetworkCaptureStartStopDownload` on arm64 kind (run 36074674471, job 107886876010), on a PR that didn't touch capture code.

**Expected:** Completed means the file is downloadable.

**Actual:** The download fails with 409 until the operator reconciles; a retry succeeds.

**Evidence:** CI job 107886876010 (`gameserver_e2e_test.go:630`, `download capture file: status=409 Conflict`).

**Note:** an earlier amd64 failure, `TestGameServer_NetworkCaptureEphemeralContainer` (run 36067327578, job 107864038508, "status.capture.ready still false" after 90s), is a different symptom: the ephemeral sidecar was never reported running. Its cause wasn't found, because the pod was gone before the dump ran. Watch for a repeat.

### F-260

**Repro / observation**
1. `GameServerPhase` (`operator/api/v1alpha1/gameserver_types.go:8-22`)
   declares `Pending;Starting;Running;Stopping;Stopped;Suspended;Failed`.
   `derivePhase` (`operator/internal/controller/gameserver_status.go:268-294`),
   the only function that assigns `gs.Status.Phase`, returns `Pending`,
   `Starting`, `Stopping`, `Suspended`, or `Running` — never `Stopped`.
2. `GameServerPhaseStopped` is still referenced downstream as if it were
   reachable: `gameserver_status.go:251` clears `StartedAt` on it,
   `metrics.go:23` includes it in the phase-count gauge's label set (so
   that series is permanently zero), and `restore_controller.go:127`
   accepts it as a valid pre-restore precondition that can never actually
   be observed.
3. `RestorePhase` (`operator/api/v1alpha1/restore_types.go:8-16`) declares
   `Pending;Suspending;Running;Resuming;Succeeded;Failed`.
   `restore_controller.go` only ever assigns `Pending` (:70), `Running`
   (:83, :130), `Suspending` (:85), `Succeeded` (:161), or `Failed` (:200)
   — `RestorePhaseResuming` is never written.
4. `inventory-CRD.md` row `INV-CRD-020` ("Restore Running → Resuming →
   Succeeded") documents the never-observed `Resuming` transition as if it
   happens.

**Expected:** Every declared enum value is either reachable by some code
path, or removed from the CRD/type if the transition it names was never
implemented (e.g. a suspend-then-resume restore, or an explicit stopped-
vs-suspended GameServer distinction).

**Actual:** `GameServerPhase.Stopped` and `RestorePhase.Resuming` are both
permanently dead values — present in the OpenAPI schema (`+kubebuilder:
validation:Enum` on both types), documented, and even branched on
elsewhere in the operator, but no reconciler ever sets either one.

**Evidence:** `operator/api/v1alpha1/gameserver_types.go:10,19`;
`operator/api/v1alpha1/restore_types.go:8,16`;
`operator/internal/controller/gameserver_status.go:251,268-294`;
`operator/internal/controller/metrics.go:23`;
`operator/internal/controller/restore_controller.go:70,83,85,127,130,161,200`;
`inventory-CRD.md` (018 branch) row `INV-CRD-020`.

### F-261

**Repro / observation**
1. #483 (F-187) makes capture-sidecar `HandleStart` refuse a start with 507 when retained capture bytes plus the new capture's `maxSizeBytes` exceed the volume budget (`CAPTURE_VOLUME_BUDGET_BYTES`, derived from the capture emptyDir `SizeLimit`).
2. Deleting a capture through the API removes the NetworkCapture record but not the capture file in the sidecar directory; `capture-sidecar/specs.md` §8a documents this as a known limitation.
3. The file keeps counting as retained until the pod restarts. With default values one leftover default-size capture blocks every further default-size capture on that pod, and the 507 text ("wait for retained captures to expire") is misleading.

**Expected:** Deleting a capture frees its bytes from the budget (the sidecar deletes the file, or the budget ignores files whose capture no longer exists).

**Actual:** Deleted captures occupy the budget until the pod restarts.

**Evidence:** opus review of #483 (spec-018 wave 3); `capture-sidecar/specs.md` §8a.

### F-262

**Repro / observation**
1. `operator/internal/controller/gameserver_tunnel.go` `reconcileTunnelNetworkPolicy` adds relay egress ports per provider: frp gets `ServerPort` (default 7000/TCP), tailscale gets 443/TCP and 41641/UDP.
2. The `case "playit":` branch adds no ports; its comment says all ports are permitted.
3. With the tunnel NetworkPolicy in place, a playit tunnel pod may be unable to reach the playit relay.

**Expected:** The playit branch admits the egress playit needs (or genuinely allows all egress, as the comment says), and a test covers it.

**Actual:** Comment and code disagree; playit egress depends on other policies.

**Evidence:** opus review of #468 (spec-018 group 47); `docs/tunnels.md` now documents the actual behaviour.

### F-263

**Repro / observation**
1. On a test install, add `team-a` to `GAMEPLANE_EXTRA_NAMESPACES` on the API Deployment and grant the API a matching RoleBinding. Create a GameServer `mc` in `team-a` that the admin does not own.
2. Open `/servers` as admin. `Servers.list()` (`web/src/lib/endpoints.ts:109`) calls `GET /servers` with no `namespace` query, so `scope.Resolve` (`api/internal/scope/scope.go:48-57`) uses `gameplane-games` and `listHandler` (`api/internal/handlers/resources.go:120-146`) lists only that namespace.
3. The namespace filter is built from that list alone (`distinctNamespaces`, `web/src/routes/Servers.tsx:110-116`), so it only ever offers `gameplane-games`.
4. `mc` is not shown. A server in `team-a` appears only under "Shared with you", and only when the viewer owns or collaborates on it (`sharedServers`, `Servers.tsx:88-96`).

**Expected:** The Servers page lists servers in every allowed namespace the viewer may read, and the namespace filter offers each of them.

**Actual:** Servers outside `gameplane-games` are hidden from the main list, and the namespace filter cannot select another namespace.

**Evidence:** code reading during the OD-021 item 14 procedure fix (2026-09-27); `procedures/web.md` `servers-filter-by-namespace` is blocked on it.

### F-264

**Repro / observation**
1. `grep -n 'package-ecosystem: "github-actions"' -A2 .github/dependabot.yml` gives one entry, `directory: "/"`.
2. For that ecosystem, Dependabot treats `/` as `.github/workflows/` plus a root `action.yml`/`action.yaml`. Composite actions in subdirectories are scanned only when they are listed (`directories:` or one entry per path).
3. `grep -rn 'setup-buildx-action\|upload-artifact' .github/` shows the drift. `build-e2e-images/action.yml:18` has `setup-buildx-action@d7f5e7f5… # v4.1.0`, and the workflows have `@f87e5991… # v4.4.1`. `build-e2e-images/action.yml:59` has `upload-artifact@bbbca2dd… # v7.0.0`, and the workflows have `@043fb46d… # v7.0.1`.
4. The workflow pins moved through Dependabot group bumps (for example `54b7a0d0`), and the composite-action pins did not.

**Expected:** Spec 008 US3 (`spec.md:55-59`) puts GitHub Actions under Dependabot because "stale actions expose the project to known CVEs", and SC-003 asks for no repository component to be left out. Every `uses:` in `.github/actions/*/action.yml` should get the same version-update PRs as the workflows.

**Actual:** The pins in the four composite actions are never bumped, and they already trail the workflows.

**Evidence:** [evidence/review-github-actions/verification.md#c-github-actions-01](evidence/review-github-actions/verification.md#c-github-actions-01)

### F-265

**Repro / observation**
1. Read `docker-bake.hcl:1`: "Bake definition for the e2e images (used by .github/actions/e2e-images)."
2. `grep -rn 'docker-bake.hcl\|bake-action' .github/` finds the bake calls only in `build-e2e-images/action.yml`.
3. `e2e-images/action.yml` has no bake step. It runs `download-artifact` and `docker load`.

**Expected:** The header names `.github/actions/build-e2e-images`.

**Actual:** It names the loader action, so a reader looking for the builder opens the wrong file.

**Evidence:** [evidence/review-github-actions/verification.md#c-github-actions-06](evidence/review-github-actions/verification.md#c-github-actions-06)

### F-266

**Repro / observation**
1. Start a pod once so the game installs, and `/data/NuclearOptionServer.x86_64` exists on the volume.
2. Restart it with the default `UPDATE_ON_BOOT=true`. `entrypoint.sh:32-33` passes `STEAM_SKIP_IF_INSTALLED=false`, so `steam-install.sh:47` doesn't skip.
3. Attempt 1 runs steamcmd. Suppose it fails partway (a CDN error, or the volume fills up) and exits non-zero. `:83-87` stores `steamcmd_exit`, which nothing reads unless the last attempt fails.
4. `:90-92` finds the sentinel left by the earlier install, prints "success — sentinel file … found" and exits 0. Attempts 2 and 3 never run, and the server starts on the old or partly updated files.

**Expected:** `README.md:110-112` says the script verifies success and retries on transient failure. When the sentinel existed before the run, a non-zero steamcmd exit should count as a failed attempt.

**Actual:** After the first install the sentinel check is always true, so update failures are reported as success and are never retried.

**Evidence:** [evidence/review-images/verification.md#c-images-01](evidence/review-images/verification.md#c-images-01)

### F-267

**Repro / observation**
1. Follow "Adding a new game" (`README.md:37-48`) and copy the Dockerfile template.
2. `FROM ${STEAMCMD_BASE_IMAGE}` inherits `USER gameserver:gameserver` from the base (`steamcmd/Dockerfile:117`).
3. `COPY entrypoint.sh /entrypoint.sh` creates the file owned by `root:root`.
4. `RUN chmod +x /entrypoint.sh` runs as `gameserver`. Only the owner or root can `chmod`, so it fails with `Operation not permitted`, and `docker build` stops.

**Expected:** The template builds. For example `COPY --chmod=0755 entrypoint.sh /entrypoint.sh`, as `nuclear-option/Dockerfile:27` does.

**Actual:** The documented template fails to build on top of the documented base.

**Evidence:** [evidence/review-images/verification.md#c-images-02](evidence/review-images/verification.md#c-images-02)

### F-268

**Repro / observation**
1. Build both images as documented, then run `docker run -it -v game-data:/data gameplane-nuclear-option:latest` with a new named volume.
2. `/data` doesn't exist in the image, so Docker creates the mount point and the volume root is `root:root 0755`.
3. The entrypoint runs as UID 10000 (`steamcmd/Dockerfile:117`). `mkdir -p /data` (`steam-install.sh:53`) succeeds, but steamcmd can't write the install into `/data`. Every attempt leaves no sentinel, and the script exits 1 after three tries.
4. In Kubernetes, `fsGroup: 10000` (`README.md:157`) makes the PVC group-writable, so the problem doesn't show there.

**Expected:** The documented local run works. For example, the base creates `/data` owned by `gameserver` (so Docker seeds a new named volume with that ownership), or the README says to pre-create the volume with the right owner.

**Actual:** The documented local `docker run` fails to install the game.

**Evidence:** [evidence/review-images/verification.md#c-images-03](evidence/review-images/verification.md#c-images-03)

### F-269

**Repro / observation**
1. Read `README.md:111`: "up to `STEAM_RETRY_COUNT` times (default 3) with exponential backoff."
2. Read `steam-install.sh:103-104`: `sleep "$STEAM_RETRY_DELAY"`. The delay is the same fixed value (default 5 s, `:28`) between every pair of attempts.

**Expected:** The README describes a fixed delay, as its own variable table does (`:126`), or the script implements backoff.

**Actual:** The README promises exponential backoff, and the script uses a fixed delay.

**Evidence:** [evidence/review-images/verification.md#c-images-04](evidence/review-images/verification.md#c-images-04)

### F-270

**Repro / observation**
1. Read `images.yaml:3-14`. The triggers are `workflow_dispatch`, `pull_request` on `images/**` and the workflow file, and `push` to `master` on the same paths.
2. Read `README.md:222-226`. The list has `workflow_dispatch` and two `push` lines, with no `pull_request` and no `master` limit.
3. From the repo root, run `docker build -t gameplane-steamcmd-base:latest -f Dockerfile images/common/steamcmd` (`README.md:190`). Docker resolves `-f Dockerfile` against the current directory, there is no `./Dockerfile`, and the build fails before it starts. `:193` has the same form.

**Expected:** The trigger list matches `images.yaml`. The manual commands either drop `-f` (the context's `Dockerfile` is the default) or pass `-f images/common/steamcmd/Dockerfile`.

**Actual:** The list leaves out the PR build, and the copy-paste build commands fail from the repo root.

**Evidence:** [evidence/review-images/verification.md#c-images-07](evidence/review-images/verification.md#c-images-07)

### F-271

**Repro / observation (defensive; confirms whether the control holds)**
1. Read `.github/actions/dump-cluster-state/action.yml` at master before #545: every step pasted the same `redact()` filter, which redacted only values after the key names password, passwd, token, secret, api_key, bearer and authorization.
2. Pipe sample lines with other credential key names (for example `passphrase:`, `client_secret=`, `AWS_ACCESS_KEY_ID=`, `Cookie:`) or a URL with userinfo through that filter; they come out unchanged.

**Expected:** Diagnostic output written to public CI logs redacts common credential forms, not only a short key list.

**Actual (before #545):** Values under other key names and URL userinfo passed through unredacted. #545 moved the filter into one shared `redact.sed`, widened the key list (passphrase, credentials, private/client/access/secret keys, x-api-key, auth config, cookies, session), added URL userinfo and bare `Bearer`/`Basic` rules, and kept the earlier quoted/JSON handling.

**Evidence:** [evidence/review-github-actions/verification.md](evidence/review-github-actions/verification.md); fix #545.

### F-272

**Repro / observation (defensive; confirms whether the control holds)**
1. Read `.github/actions/dump-cluster-state/action.yml` at master before #545: `${{ inputs.context }}`, `${{ inputs.namespaces }}` and `${{ inputs.include-previous-logs }}` were expanded inside `run:` bodies.

**Expected:** Composite-action inputs reach step scripts through `env:` and are referenced as quoted shell variables.

**Actual (before #545):** Inputs were expanded into the script text. Every caller in `ci.yaml` passed a literal or a job `env` value, so nothing was reachable from untrusted input; this is hardening. #545 passes them as `DUMP_*` env vars.

**Evidence:** [evidence/review-github-actions/verification.md](evidence/review-github-actions/verification.md); fix #545.

### F-273

**Repro / observation**
1. Loading the dashboard on a cluster with 30 GameTemplates fired about 30 `GET /templates` (176 KB each) in about 5 s and filled the bell with "9+" fake `added template ...` notifications.
2. Source: the description of [#548](https://github.com/ValgulNecron/Gameplane/pull/548); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** The events stream starts from the collection's current state and reports only new changes.

**Actual:** The API server replayed every existing object as ADDED on each connect. #548 watches from the list's current `resourceVersion` (`Limit: 1` list) and coalesces SSE-driven invalidations per query key (500 ms).

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/548](https://github.com/ValgulNecron/Gameplane/pull/548) (PR description and commit list).

### F-274

**Repro / observation**
1. After a pod restart or recreation the header still showed the server's age ("up 13d").
2. Source: the description of [#548](https://github.com/ValgulNecron/Gameplane/pull/548); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** `status.startedAt` follows the current start of the `game` container.

**Actual:** `startedAt` was set only the first time a server reached Running. #548 follows the `game` container's `Running.StartedAt` while Running, falling back to set-once when the pod can't be read.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/548](https://github.com/ValgulNecron/Gameplane/pull/548) (PR description and commit list).

### F-275

**Repro / observation**
1. Recent activity on the dashboard rendered every login attempt as "anonymous created".
2. Source: the description of [#548](https://github.com/ValgulNecron/Gameplane/pull/548); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Successful logins are attributed to the user; failed attempts stay anonymous so attempted usernames are not recorded.

**Actual:** Successful logins were audited as `anonymous`. #548 records the user through `auth.SetActor` and the dashboard says "<user> signed in" / "<actor> failed to sign in".

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/548](https://github.com/ValgulNecron/Gameplane/pull/548) (PR description and commit list).

### F-276

**Repro / observation**
1. The Servers page's Stopped tab lists Failed servers.
2. Source: the description of [#548](https://github.com/ValgulNecron/Gameplane/pull/548); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** As documented in `web/src/lib/servers.ts:15-16`.

**Actual:** Matches the documented, tested behaviour. #548 did not change it.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/548](https://github.com/ValgulNecron/Gameplane/pull/548) (PR description and commit list).

**Justification:** #548 ("Not changed") states the Stopped tab including Failed servers is documented and tested as intentional in `web/src/lib/servers.ts:15-16` (`countByState`: "stopped" folds in Suspended and Failed).

### F-277

**Repro / observation**
1. Files and mods uploaded through the agent could not be read by the game container.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Uploads and mods are group-readable by the game container.

**Actual:** Temp files were written 0600 and renamed into place. #549 chmods them to the target mode before the rename; a follow-up keeps the target's mode when an upload replaces a symlink.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-278

**Repro / observation**
1. Opening the Logs tab did not replay recent log history.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** The tab replays recent history, then follows live.

**Actual:** #549: the tab requests `?tail=500`; the API forwards only the allow-listed `from` and `tail` keys; the agent validates `tail`, caps it at 20000, bounds replay by bytes and does not replay again after a rotation.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-279

**Repro / observation**
1. restic backup/restore Jobs did not run with the GameTemplate fsGroup (the PR text gives no further symptom), and a Job stuck (for example `CreateContainerConfigError` after its destination Secret was deleted) kept a Backup in flight indefinitely.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Jobs run with the GameTemplate fsGroup, retries are bounded, and only a real failure is permanent.

**Actual:** #549: restic Jobs use the template fsGroup (fallback 65532), get `BackoffLimit: 2` and `ActiveDeadlineSeconds: 86400`, and only `Failed=True` counts as permanent failure. The limits are provisional ([OD-027](../OPEN-DECISIONS.md)).

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-280

**Repro / observation**
1. Deleting a BackupSchedule removed the Backups it had created, through the ownerReference.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Backups survive their schedule.

**Actual:** #549 drops the ownerReference; the `backup-schedule` label still tracks the backups.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-281

**Repro / observation**
1. After a world wipe the server did not come back.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** The server restarts once the wipe finishes.

**Actual:** #549: the wipe waits for the StatefulSet to report 0 replicas (and for the game Pod to be gone) and `ackWipe` sets `spec.suspend=false`.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-282

**Repro / observation**
1. Module finalize on an already-gone object and StatefulSet update conflicts were logged as errors.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Expected races are ignored or requeued quietly.

**Actual:** #549: module finalize ignores NotFound; StatefulSet update conflicts requeue quietly. The new `IsConflict` branch in `gameserver_controller.go` has no test (stated in the PR).

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-283

**Repro / observation**
1. A burst of logins OOM-killed the 256 MiB API pod, and user timestamps came back in a non-RFC 3339 form.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Hashing concurrency is bounded and timestamps are RFC 3339.

**Actual:** #549: a 2-slot weighted semaphore bounds argon2id hashing (honours the request context, keeps timing parity) and user timestamps are normalized to RFC 3339 (`TestUsers_FetchByIDReturnsRFC3339`).

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-284

**Repro / observation**
1. Revoked share links were not labelled Revoked.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Revoked links show the Revoked chip, as in design `xCJlu`.

**Actual:** #549 shows the Revoked chip.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-285

**Repro / observation**
1. The Cluster page had no per-node pod counts.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Per-node pod usage is reported.

**Actual:** #549 reports per-node pod usage (paged and cached in a follow-up commit), with the RBAC widening described in the note.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-286

**Repro / observation**
1. An empty `instanceName` was rejected.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** `instanceName` is optional.

**Actual:** #549 makes it optional (`config_validators_test.go` updated: empty is now valid). The hint text in AdminSettings is deferred (see the design-first finding below).

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-287

**Repro / observation**
1. With many dashboard tabs open, EventSource connections used up the per-origin limit of 6.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Hidden tabs release their stream.

**Actual:** #549: hidden tabs close their EventSource and reconnect when visible.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-288

**Repro / observation**
1. Several small display defects seen in the sweep (proxy HTML error pages shown raw; issues with the game version, transfer audit label, next-backup time, backup repo Secret key and image placeholder).
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Each shows the correct value.

**Actual:** #549 (`fix(web): small dashboard fixes from the live sweep`): proxy HTML error pages show their short status; the other items are corrected. The PR text does not describe each wrong value.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-289

**Repro / observation**
1. Raised in the live sweep as B3; the PR text gives no further symptom.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Terraria and tModLoader templates declare a TCP readiness probe.

**Actual:** Not fixed; deferred by #549 to a `gameplane-module` PR and an e2e update.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-290

**Repro / observation**
1. Deferred by #549 as a design-first item.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Distinct "starting up" and "waking up" copy on the public share page.

**Actual:** Not changed; waiting on a design PR.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-291

**Repro / observation**
1. Deferred by #549 as a design-first item.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** AdminSettings shows a hint that `instanceName` is optional.

**Actual:** Not changed; waiting on a design PR.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-292

**Repro / observation**
1. Deferred by #549 as a design-first item.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Folder selection and delete in the Files tab, as designed.

**Actual:** Not changed; waiting on a design PR.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).

### F-293

**Repro / observation**
1. Deferred by #549 as a design-first item.
2. Source: the description of [#549](https://github.com/ValgulNecron/Gameplane/pull/549); the sweep did not record per-row evidence (see [rounds.md](rounds.md#live-sweep-2026-10-04)).

**Expected:** Console history, as designed.

**Actual:** Not changed; waiting on a design PR.

**Evidence:** [https://github.com/ValgulNecron/Gameplane/pull/549](https://github.com/ValgulNecron/Gameplane/pull/549) (PR description and commit list).
