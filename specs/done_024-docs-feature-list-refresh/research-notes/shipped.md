# Shipped features and done specs (research notes for 024)

Sources: `CHANGELOG.md` (`[Unreleased]` at line 8, `[0.3.0] — 2026-10-05` header at line 181), and `specs/done_*/spec.md` plus `specs/done_*/tasks.md`.

## [Unreleased] (no release date; CHANGELOG.md:8)

### Added
- Independent central panel: Docker Compose runs API/dashboard without host Kubernetes; standalone Helm profile runs without central operator or workloads (CHANGELOG.md:89).
- Credential maintenance: standalone key custody, offline `rotate-panel-key`, `PUT /clusters/{name}/kubeconfig`, `PUT`/`DELETE /clusters/{name}/gateway` (CHANGELOG.md:94).
- Extended telemetry tier: second, separately consented tier with random install ID and fixed-set categories; signed reports (CHANGELOG.md:100).
- Telemetry receiver dashboard and optional public `GET /v1/summary` (CHANGELOG.md:108).
- Proof-of-work on telemetry ingestion, off by default (CHANGELOG.md:118).
- Telemetry provider runbook at `docs/telemetry-provider.md` (CHANGELOG.md:133).
- Optional Steam display names on player lists via a Steam Web API key (CHANGELOG.md:136).
- Nuclear Option player lists are structured (`steamId`, `faction`) (CHANGELOG.md:146).

### Changed
- New optional `GameTemplate` field `spec.rcon.authentication` (default `true`) allows consoles with no RCON Secret (CHANGELOG.md:152).

### Removed
- Hell Let Loose module withdrawn from the official catalog (CHANGELOG.md:161).

### Security
- Standalone credential protection: keys kept outside database volumes, transactional rotation (CHANGELOG.md:168).
- Remote workload boundaries: verified TLS, dial-time unsafe-destination rejection, bounded responses; `GAMEPLANE_REMOTE_ALLOWED_CIDRS` / `api.remoteAllowedCIDRs` (CHANGELOG.md:173).

### Upgrade-note behaviour changes (user-visible)
- Telemetry on by default for new installs after a first-login notice (CHANGELOG.md:29).
- Installs with a saved basic toggle start sending to the default receiver after upgrade (CHANGELOG.md:46).
- Empty `api.telemetry.endpoint` means the project default receiver (CHANGELOG.md:56).
- Receiver `/metrics` moved to `:8081` and needs the dashboard token (CHANGELOG.md:62).
- Bundled receiver stores data on a PVC by default (CHANGELOG.md:71).
- Receiver `DASHBOARD_TOKEN` must be at least 32 characters (CHANGELOG.md:77).
- Breaking: selected-cluster module/template grants need a cluster-wide grant for remote catalogs (CHANGELOG.md:20).

## [0.3.0] — 2026-10-05 (CHANGELOG.md:181)

### Added
- Configurable backup/restore Job retry limit and deadline Helm values (CHANGELOG.md:231).
- Multicluster gateways and unified server management across clusters (CHANGELOG.md:232).
- User theme customization: presets, custom colors with WCAG AA check, custom CSS overlay, safe mode (CHANGELOG.md:238).
- Network capture sidecar: admin-only, on-demand PCAPNG capture from running game pods (CHANGELOG.md:247).
- Module authoring toolkit `gp-module` (scaffold, validate, preview, package) and dashboard Modules builder (CHANGELOG.md:256).
- 13 new top Steam game modules (CHANGELOG.md:264).
- Nuclear Option dedicated server support (CHANGELOG.md:272).
- Install-time config: Helm-seeded OIDC role mappings and default StorageClass for game-data PVCs (CHANGELOG.md:278).
- Share link expiry options (presets, custom date, no expiry) replacing the 90-day cap (CHANGELOG.md:290).
- Console WebSocket robustness on non-default ports and input queueing (CHANGELOG.md:296).
- Existing PVC for SQLite via `api.storage.existingClaim` (CHANGELOG.md:303).
- REST and CLI console protocols for games with HTTP admin APIs or PTY-only consoles (CHANGELOG.md:306).
- Mod archive path confinement (zip-slip and symlink guards) (CHANGELOG.md:317).
- Design and visual polish: HeroUI foundation and brand re-theme (CHANGELOG.md:326).

### Changed
- Owner-only server operations (transfer, collaborators, wipe, delete) need the owner or an admin (CHANGELOG.md:211).
- Playit tunnel NetworkPolicy adds unrestricted egress (CHANGELOG.md:217).
- Every game pod gets an empty capture emptyDir; existing GameServers roll once on upgrade (CHANGELOG.md:191).
- Helm reinstall over leftover CRDs runs the CRD-apply hook (CHANGELOG.md:222).
- Dependency updates (Node 26, Go 1.27, nginx 1.31, restic 0.19 and others) (CHANGELOG.md:478).

### Fixed (user-visible, selected)
- SQLite DSN gets `busy_timeout(5000)` by default (CHANGELOG.md:333).
- Agent metrics on a separate unauthenticated `:9090` listener; PodMonitor scrapes it (CHANGELOG.md:334).
- Experimental PostgreSQL backend works end to end (CHANGELOG.md:347).
- Servers page lists servers from every namespace the viewer may read; `GET /namespaces` (CHANGELOG.md:353).
- Catalog tag filters use AND logic (CHANGELOG.md:357).
- Helm upgrade works for releases not named `gameplane` and with `--reuse-values` from beta.8 (CHANGELOG.md:367).
- Data wipe failures now fail the Job and set `DataWipe=False` (CHANGELOG.md:385).
- Backup/restore quiesce lifecycle fixed; restore removes files created after the snapshot (CHANGELOG.md:388).
- Server detail page passes the server namespace; "Shared with you" rows regain Start/Stop/Restart (CHANGELOG.md:399).
- Deleting a capture frees its volume budget immediately (CHANGELOG.md:436).
- Dashboard nginx accepts request bodies up to 64 MiB (CHANGELOG.md:445).
- `clusterOps.externalAddress` for node-routable join commands and kubeconfig (CHANGELOG.md:455).
- Admin Logs shows timestamps for `time`-field log lines (CHANGELOG.md:462).

### Security hardening (selected)
- Multi-cluster request scoping, OIDC role re-evaluation, bootstrap-admin reset, cluster registration removal, mod bundle integrity, tunnel credential handling (CHANGELOG.md:496-509 and following).

## Done specs (specs/done_*)

Outcome is taken from `tasks.md` counts (`[X]` done, `[ ]` open, "withdrawn" mentions) and the spec.md `Status` line (spec.md:7). Several spec.md Status lines were never updated from "Draft" even though their tasks are complete, so the task counts are the more reliable signal.

- 001 — Game Protocol E2E Coverage (specs/done_001-gameprotocol-e2e-coverage/spec.md:1). Outcome: complete, 32 tasks [X], 0 open. Status line: Draft.
- 003 — Network Capture Sidecar for Game Servers (specs/done_003-network-capture-sidecar/spec.md:1). Outcome: complete, 91 tasks [X], 0 open. Status line: Draft, amended 2026-08-23.
- 004 — Lint Backlog Wave 2: Static Analysis Gate for api, agent, test/e2e (specs/done_004-lint-backlog-wave2/spec.md:1). Outcome: complete, 90 tasks [X], 0 open. Status line: Draft.
- 005 — Protocol Classifier Registry Refactor (specs/done_005-gameproto-classifier-registry/spec.md:1). Outcome: complete, 85 tasks [X], 0 open; has completion-summary.md and verification.md. Status line: Draft.
- 006 — Install-Time Configuration (Storage Class and OIDC Role Mapping) (specs/done_006-install-time-config/spec.md:1). Outcome: complete, 62 tasks [X], 0 open. Status line: Draft.
- 007 — Capture Overhead CI Regression Guard (specs/done_007-capture-overhead-guard/spec.md:1). Outcome: complete, 14 tasks [X], 0 open. Status line: Planned (no research.md or checklists folder).
- 008 — Hardened GitHub Actions CI/CD, AI Automation and Multi-Module Dependabot (specs/done_008-hardened-github-actions/spec.md:1). Outcome: complete, 31 tasks [X], 0 open, 1 withdrawn. Status line: Draft.
- 010 — Easy Module Building and Authoring Toolkit (specs/done_010-easy-module-building/spec.md:1). Outcome: complete, 38 tasks [X], 0 open. Status line: Draft.
- 011 — Complete Module Specifications and Compliance Verification (specs/done_011-add-missing-module-specs/spec.md:1). Outcome: complete, 18 tasks [X], 0 open. Status line: Draft.
- 012 — Documentation Refresh, Comparison Table and External Outreach (specs/done_012-docs-refresh-and-outreach/spec.md:1). Outcome: complete, 65 tasks [X], 0 open, 2 withdrawn. Status line: Draft.
- 014 — Rebuild the dashboard on the HeroUI design system (specs/done_014-heroui-web-rebuild/spec.md:1). Outcome: complete, 215 tasks [X], 0 open, 1 withdrawn. Status line: Draft.
- 015 — Dedicated Server Modules for Top Steam Games (specs/done_015-top-steam-game-modules/spec.md:1). Outcome: complete, 147 tasks [X], 0 open, 5 withdrawn. Status line: Draft.
- 016 — User Theme Customization (specs/done_016-user-theme-customization/spec.md:1). Outcome: complete, 46 tasks [X], 0 open, 1 withdrawn. Status line: Ready for Planning.
- 017 — Configurable Share Link Expiry (specs/done_017-share-link-expiry/spec.md:1). Outcome: complete, 33 tasks [X], 0 open. Status line: Draft.
- 018 — v0.3 Release Readiness Audit (specs/done_018-v0-3-release-readiness/spec.md:1). Outcome: complete, 51 tasks [X], 0 open, 27 withdrawn. Status line: Draft.
- 020 — Forget Restic Snapshot on Backup Delete (specs/done_020-backup-snapshot-forget/spec.md:1). Outcome: implemented, merged in PR #567 on 2026-10-05, 18 tasks [X], 0 open. Status line: Implemented.
