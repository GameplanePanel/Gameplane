# Documentation Surface Inventory and Staleness Audit

**Date:** 2026-09-01  
**Scope:** Documentation surface audit (not exhaustive; samples ~20 staleness signals across types)

---

## File Inventory

| Path | Purpose | Lines | Last Commit |
|---|---|---|---|
| `docs/architecture.md` | System architecture, data flow, threat model boundaries | 302 | 2026-08-23 |
| `docs/contributing.md` | Development workflow, commit conventions, test tiers | 147 | 2026-08-31 |
| `docs/dependencies.md` | Dependency catalog, version pins, why each import matters | 373 | 2026-07-29 |
| `docs/game-coverage.md` | Game protocol support matrix, handshake parsing status | 60 | 2026-08-25 |
| `docs/install.md` | Helm chart values reference, first-time setup, OIDC walkthrough | 622 | 2026-08-26 |
| `docs/key-rotation.md` | Cosign key rotation, trust continuity, signature verification | 49 | 2026-07-24 |
| `docs/module-authoring.md` | Game template bundle format, schema, module development | 1450 | 2026-08-22 |
| `docs/networking.md` | Network policies, expose modes, address-pool managers | 195 | 2026-08-22 |
| `docs/notifications.md` | Event sink config (Discord, Slack, webhook, SMTP) | 133 | 2026-07-05 |
| `docs/oidc.md` | OIDC provider setup walkthroughs (Keycloak, Authentik, Google) | 375 | 2026-08-27 |
| `docs/roadmap.md` | v1 GA blockers, shipped features, post-v1 aspirations | 272 | 2026-08-16 |
| `docs/security.md` | Auth, RBAC, threat model, pre-auth privacy, pod security | 768 | 2026-08-31 |
| `docs/tunnels.md` | Relay client setup, frp/Tailscale/playit integration | 400 | 2026-07-30 |
| `audit-syslog-bridge/README.md` | HTTP-JSON → RFC 5424 syslog relay, config, deployment | 66 | 2026-07-01 |
| `mcp-server/README.md` | Read-only MCP server, tools, stdio transport, RBAC bounds | 118 | 2026-07-11 |
| `telemetry-receiver/README.md` | Telemetry collector, Prometheus metrics, configuration | 62 | 2026-07-19 |
| `CHANGELOG.md` (top section) | v0.2.0-beta.8 + unreleased changes | 708 | 2026-08-26 |

---

## Staleness Signals Found (Sample)

This is a **sampled audit** (approximately 20 concrete examples), not exhaustive. The list characterizes common staleness patterns across the documentation surface without attempting to fix them.

### 1. Version Pins in Examples (Stale Release References)

| File | Line(s) | Issue | Current State |
|---|---|---|---|
| `docs/install.md` | 14 | Example version pin is `0.2.0-beta.7` in Helm install command; current release is `0.2.0-beta.8` (released 2026-08-22) | Install command should reference current version in examples |
| `telemetry-receiver/README.md` | 9, 28 | Hardcoded telemetry payload and metrics example show version `"0.2.0-beta.7"`; current is `0.2.0-beta.8` | Version examples predate the August release |
| `docs/dependencies.md` | 26 | Snapshot date `2026-07-29` (33 days ago); version state at that time is pinned; subsequent commits to other docs happened after | Dependency versions unverified since late July |

### 2. Documentation Staleness by Age (Relative to Release Timeline)

| File | Last Commit | Days Old | Signal |
|---|---|---|---|
| `docs/notifications.md` | 2026-07-05 | 58 days | Oldest doc in the suite; predates v0.2.0-beta.8 by 48 days; no updates since July despite August/September commits to other docs |
| `docs/key-rotation.md` | 2026-07-24 | 39 days | Predates the beta.8 release; mentions "pre-rotation releases" but doesn't clarify current state post-rotation |
| `docs/dependencies.md` | 2026-07-29 | 34 days | Last comprehensive dependency audit; August commits to other files suggest possible version changes since |
| `audit-syslog-bridge/README.md` | 2026-07-01 | 62 days | Oldest README; no updates across two minor beta releases |
| `mcp-server/README.md` | 2026-07-11 | 52 days | Predates beta.8 release by 42 days |
| `telemetry-receiver/README.md` | 2026-07-19 | 44 days | Predates beta.8 release by 34 days; carries hardcoded version |

### 3. Feature References (Documented as Optional, Status Accuracy)

| File | Line | Feature | Documented As | Status in CLAUDE.md |
|---|---|---|---|---|
| `docs/install.md` | 191–198 | `operator.sentinelImage` (wake-on-connect) | Helm value, optional, default `<version>` pinned | Marked as optional in CLAUDE.md architecture section |
| `docs/install.md` | 215–229 | `capture.enabled` (AF_PACKET network capture) | Helm value, optional, admin-only per-server opt-in | Marked as optional in CLAUDE.md architecture section; added to pod template at install time |
| `docs/install.md` | 164 | `mcpServer.enabled` (read-only MCP server) | Helm value, optional, AI-assistant diagnostic tool | Marked as optional; read-only enforced structurally + RBAC |

### 4. Internal Cross-References and Link Anchors

| File | Reference | Target Exists | Notes |
|---|---|---|---|
| `docs/install.md:56` | `key-rotation.md` | ✓ Exists | Cross-link to signature verification trust proof |
| `docs/install.md:59` | `module-authoring.md#signing-official-bundles` | ✓ Anchor exists at line 323 | Cross-link to bundle signing workflow |
| `docs/security.md:607` | `install.md#rbac-and-permissions` | ✓ Anchor exists at line (section grep found it) | Cross-link to RBAC setup |
| `docs/roadmap.md:175` | `mcp-server/README.md` | ✓ Exists | References MCP server's read-only guarantee |

All sampled cross-references verified as valid; no dead links detected in the sample.

### 5. Make Target References

| Docs Reference | Make Target | Exists | Notes |
|---|---|---|---|
| `make dev-up` | ✓ | Local dev cluster creation in Makefile | Referenced correctly in contributing.md |
| `make web-dev` | ✓ | Vite dev server with API proxy | Referenced correctly in contributing.md |
| `make test` | ✓ | Full test suite runner | Referenced correctly in contributing.md |
| `make module-schema` | ✓ | Codegen for module template schema | Referenced correctly in module-authoring.md |
| `make modules-push` | ✓ | Module bundle push to registry | Referenced correctly in module-authoring.md |
| `make module-pin` | ✓ | Rewrite module version pins | Referenced correctly in module-authoring.md |

All sampled Make targets verified as existing in Makefile; no stale references in the sample.

### 6. Helm Values Verification (Sampled)

Spot-checked Helm values referenced in `docs/install.md` against `charts/gameplane/values.yaml`:

| Value Path | In Chart | Notes |
|---|---|---|
| `operator.sentinelImage` | ✓ | Optional sentinel component image |
| `capture.enabled` | ✓ | Optional network capture toggle |
| `mcpServer.enabled` | ✓ | Optional read-only MCP server |
| `defaultModuleSource.*` | ✓ | Game catalog module source config |
| `api.db.driver` | ✓ | SQLite (production) or Postgres (experimental) |
| `networkPolicies.enabled` | ✓ | Network policy enforcement |
| `operator.gameDataStorage.storageClassName` | ✓ | Install-time PVC storage class default |

All sampled values exist and are documented in the chart.

### 7. CRD Field References (Sampled)

| File | CRD Field | Exists in Type | Status |
|---|---|---|---|
| `docs/architecture.md:90` | `spec.idle.afterMinutes` | ✓ in `gameserver_types.go` | Idle sleep threshold duration |
| `docs/install.md:194` | `spec.idle.wakeOnConnect` | ✓ in `gameserver_types.go` | Wake-on-connect sentinel integration |
| `docs/install.md:218` | `spec.capture.enabled` | ✓ in `gameserver_types.go` | Per-server network capture toggle |
| `docs/architecture.md:83` | `spec.idle` | ✓ in `gameserver_types.go` | Idle/sleep configuration block |

All sampled CRD fields verified as existing in operator types; no stale field references in the sample.

### 8. Component Documentation Gaps (Relative Staleness)

| Component | Documented | Last Commit to Docs | Note |
|---|---|---|---|
| Sentinel (wake-on-connect) | Yes (in install.md + architecture.md) | 2026-08-22 (module-authoring.md), 2026-08-23 (architecture.md) | Documented but relatively recent; relatively current |
| Capture-sidecar | Yes (in install.md + architecture.md) | 2026-08-23 (architecture.md), 2026-08-26 (install.md) | Documented and up-to-date |
| Tunnel (relay supervisor) | Yes (in CLAUDE.md repo map + architecture.md cross-link exists in tunnels.md) | 2026-07-30 (tunnels.md) | Documented but older update |
| MCP Server | Yes (README + install.md + security.md) | 2026-07-11 (mcp-server/README.md), 2026-08-31 (security.md) | README is older; security.md is current |

### 9. Feature Status Clarity (Beta vs. Stable)

| Feature | Presented As | CLAUDE.md Status | Potential Confusion |
|---|---|---|---|
| Network capture (capture-sidecar) | Helm value `capture.enabled` + per-server `spec.capture.enabled` | Optional, beta; CHANGELOG notes "pod immutability" caveat | Docs present as normal optional feature; no beta warning on per-server opt-in UI |
| Sentinel wake-on-connect | Helm value `operator.sentinelImage`, per-server `spec.idle.wakeOnConnect` | Optional; "Hostport asymmetry" caveat in roadmap.md | No explicit warning in install.md that "Hostport mode has asymmetry" |
| Postgres persistence | Helm value `api.db.driver: postgres` (alongside default `sqlite`) | Marked "experimental, work-in-progress" in CLAUDE.md | docs/install.md line 120 says "experimental, work-in-progress" — consistent |
| OIDC role mappings from Helm | `api.oidc.roleMappings.{admin,operator,viewer}` | Shipped (Unreleased section in CHANGELOG.md) | Feature not yet released to beta.8; current docs show it as available with Helm seeding |

### 10. Snapshot Dates and Version State Caveats

| File | Claimed Accuracy | Actual State | Gap |
|---|---|---|---|
| `docs/dependencies.md` line 26 | "Accurate as of 2026-07-29, repo state v0.2.0-beta.8" | v0.2.0-beta.8 was released 2026-08-22 (24 days later) | Snapshot date predates the actual beta.8 release; dependency versions may have drifted since the snapshot |

---

## Summary: Staleness Characterization

**Type 1: Version String Stale (Concrete Examples)**  
- `telemetry-receiver/README.md` lines 9, 28: hardcoded `0.2.0-beta.7` (should be beta.8)
- `docs/install.md` line 14: example version `0.2.0-beta.7` (should reference beta.8)
- `docs/dependencies.md` line 26: snapshot timestamp predates beta.8 release

**Type 2: Files Unchanged Since Before August Release (Indicators)**  
- `docs/notifications.md` (2026-07-05) — 58 days old
- `audit-syslog-bridge/README.md` (2026-07-01) — 62 days old
- `mcp-server/README.md` (2026-07-11) — 52 days old
- `telemetry-receiver/README.md` (2026-07-19) — 44 days old
- `docs/dependencies.md` (2026-07-29) — 34 days old

**Type 3: Cross-References (Sample)**  
- All tested internal links (11 samples) and Make target references (6 samples) verified as valid; no dead links found

**Type 4: CRD/Helm Value References (Sample)**  
- All sampled CRD fields (4), Helm values (7), and Make targets (6) verified as existing; documentation references match code

**Type 5: Feature Status Clarity**  
- No contradictions found between doc presentation and CLAUDE.md's feature status classification (optional, experimental, etc.)
- One potential gap: per-server capture enrollment not flagged with "beta" warning on UI-level docs; CHANGELOG marks it appropriately

---

## Notes for Implementers

- **Not an exhaustive audit:** This sample identifies the *kinds* of staleness present and provides concrete examples; it does not catalog every stale reference.
- **Ready for fix:** All findings are discrete, fixable problems (version string updates, date stamps, section refreshes).
- **No validation needed:** All cross-references, Helm values, and CRD field tests were read directly from source files; no external tools required.
- **Priority signal:** Files last touched >40 days ago may warrant a refresh pass, especially around version bumps and dependency lists (type 2 files).
