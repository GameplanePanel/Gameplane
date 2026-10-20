# Documentation Audit Contract

**Status**: Specification (Ruled 2026-09-02 — Ready for Implementation)  
**Feature**: 012 — Documentation Refresh, Comparison Table, and External Outreach  
**Authored**: 2026-09-01  
**Type**: Audit Standard (FR-009 to FR-014, SC-004 to SC-008)

---

## Audited Files

The documentation audit covers exactly **17 files** as specified in FR-009:

### Primary Files (14)

| Path | Purpose |
|------|---------|
| `README.md` | Project README with comparison table, feature summary, and quick-start |
| `docs/architecture.md` | System architecture, data flow, threat model boundaries |
| `docs/contributing.md` | Development workflow, commit conventions, test tiers |
| `docs/dependencies.md` | Dependency catalog, version pins, and version justifications |
| `docs/game-coverage.md` | Game protocol support matrix and handshake parsing status |
| `docs/install.md` | Helm chart values reference, first-time setup, OIDC walkthroughs |
| `docs/key-rotation.md` | Cosign key rotation, trust continuity, and signature verification |
| `docs/module-authoring.md` | Game template OCI bundle format, schema, and development workflow |
| `docs/networking.md` | Network policies, expose modes, and address-pool managers |
| `docs/notifications.md` | Event sink configuration (Discord, Slack, webhook, SMTP) |
| `docs/oidc.md` | OIDC provider setup walkthroughs (Keycloak, Authentik, Google) |
| `docs/roadmap.md` | v1 GA blockers, shipped features, and post-v1 aspirations |
| `docs/security.md` | Authentication, RBAC, threat model, pre-auth privacy, pod security |
| `docs/tunnels.md` | Relay client setup, frp/Tailscale/playit integration |

### Component READMEs (3)

| Path | Purpose |
|------|---------|
| `audit-syslog-bridge/README.md` | HTTP-JSON → RFC 5424 syslog relay, config, and deployment |
| `mcp-server/README.md` | Read-only MCP server, tools, stdio transport, and RBAC bounds |
| `telemetry-receiver/README.md` | Telemetry collector, Prometheus metrics, and configuration |

### Exclusions

Per project rule 2 and architecture, the following are **not** audited:

- **`CHANGELOG.md`** (historical record; version strings in CHANGELOG are release notes, not product claims)
- **`docs/superpowers/**` (dated historical design records)
- **`website/` and `modules/` submodules** (external repos with their own CI)
- **Pencil-generated exports** (`design-export/`)
- All other directories

---

## Truth Sources

Canonical sources for audit checks:

| Check | Truth Source | Location | Reference |
|-------|-------------|----------|-----------|
| Current Gameplane version | Helm chart metadata | `charts/gameplane/Chart.yaml:6` appVersion field | D-B |
| Feature defaults (optional/experimental) | Helm chart values | `charts/gameplane/values.yaml` keys with `enabled: false` or `enabled: null` | D-C, SC-007 |
| CRD fields | Operator Go types | `operator/api/v1alpha1/*_types.go` struct tags and fields | Principle IV (specs.md-driven) |
| Helm values reference | Deployment values | `charts/gameplane/values.yaml` key paths | SC-007 |
| Make targets | Build automation | `Makefile` all targets | Rule 8 (no-local-execution) |
| Beta/optional status | Code comments + docs | `CLAUDE.md` repo map + `values.yaml` defaults | D-C |

---

## Standard FR-010: Version Strings

**Requirement** (FR-010a, SC-005): All version strings match the current release or are explicitly marked as example versions.

### Current Version

**Value**: `0.2.0-beta.8` (from `charts/gameplane/Chart.yaml:6`)  
**Release Date**: 2026-08-22 (from `CHANGELOG.md:47`)

### Check Procedure

1. **Scan each audited file** for patterns matching the regex:
   ```
   v?0\.[0-9]\.[0-9]-beta\.[0-9]
   ```
   (Case-insensitive; permits `v` prefix)

2. **For each match**, inspect the surrounding context (±2 lines):
   - If the context includes the phrase `(example version)`, `(example)`, `(placeholder)`, or is inside a code block marked `# Example:` or `<!-- Example -->`, **pass**: it is an explicitly marked example.
   - If the same line contains an HTML comment `<!-- doc-versions: historical -->`, **pass**: it is a legitimate historical reference (OD-14, ruled 2026-09-02).
   - Otherwise, the match is a **version string claim** in product documentation.

3. **Version string claims** MUST match `v?0\.2\.0-beta\.8` exactly.

4. **Non-version regex matches** that look like version patterns but don't match the full regex (e.g., `v0.2.0-alpha.1`, `v0.1.0`) are product claims and checked as feature descriptions (FR-010b).

### Pass Condition

- **Zero stale version strings** in product documentation (SC-008a).
- All version strings in examples are explicitly marked.
- Historical version references are marked with `<!-- doc-versions: historical -->` inline (OD-14).
- Count: Zero failures across all 17 files.

### Evidence Citation Format

Per FR-011, each correction cites the evidence checked:

```
File: docs/install.md
Lines: 14
Finding: Version string "v0.2.0-beta.7" is stale
Evidence Checked: charts/gameplane/Chart.yaml:6 (appVersion = 0.2.0-beta.8)
Resolution: Update to v0.2.0-beta.8
Commit: (filled during implementation; D-F)
```

---

## Standard FR-010: Feature Descriptions

**Requirement** (FR-010b, SC-004): Feature descriptions in docs match the implementation in operator/, api/, agent/, and are qualified with status (beta/optional) as appropriate.

### Check Procedure

1. **Identify feature claims** in each file:
   - Claims use phrases like "Gameplane supports X", "Feature X is available", "Enable X by setting Y", "The operator reconciles X".
   - Code examples showing a feature's usage.
   - Helm values described as enabling/disabling a feature.

2. **For each feature claim**, verify against the corresponding codebase source:

   | Feature Type | Verify Against | Example |
   |---|---|---|
   | CRD field/status | `operator/api/v1alpha1/*_types.go` struct and field tags | `spec.idle.wakeOnConnect` in docs/install.md:191 checked against `gameserver_types.go:137-195` |
   | Reconciler behavior | `operator/internal/controller/*_controller.go` reconcile logic | "idle sleep timer" in docs/architecture.md checked against `gameserver_controller.go` idle reconciliation |
   | API endpoint | `api/internal/handlers/*.go` handler functions and their RBAC gates | "console stream endpoint" in docs/security.md checked against `api/internal/handlers/console.go` |
   | Agent capability | `agent/internal/*/*.go` modules | "file upload via agent" in docs/architecture.md checked against `agent/internal/files/` |
   | Helm value | `charts/gameplane/values.yaml` key path and schema | "operator.sentinelImage" in docs/install.md:191 checked against `values.yaml:58` |

3. **For each claim**, note whether the implementation exists, is beta/optional, or is explicitly limited (e.g., "Minecraft/Terraria only").

4. **Mark inconsistencies**: If a claim overstates capability (e.g., "fully supported" when code shows partial support) or omits a required qualifier, flag it.

### Qualifiers (D-C Label Vocabulary)

When a feature claim requires qualification, apply the appropriate tag from this vocabulary:

- **`[optional]`** — Feature is disabled by default in `values.yaml` and requires explicit user enablement. Source: `values.yaml` key has `enabled: false` or explicit opt-in value.
- **`[experimental]`** — Feature is functional but not stable; may change in future releases. Source: CLAUDE.md wording (e.g., "experimental, work-in-progress") or explicit beta caveat in roadmap.md.
- **`[disabled by default]`** — Equivalent to `[optional]`; use when emphasizing that the feature is off at install time.
- **`[BETA]`** — Feature is complete but stabilizing; may have known limitations or API instability. Source: README.md status line or explicit feature-level caveat.
- **`[local cluster only]`** — Feature is scoped to the control-plane cluster; not available in remote clusters. Source: router/tree.tsx, explicit router guards, or code comments in handlers/ws.go. Applies specifically to FR-013 (multi-cluster console/log streaming).

### Pass Condition

- **Zero feature description mismatches** in product documentation (SC-008b, SC-004).
- Every optional/experimental/beta feature is qualified with the appropriate tag at its first mention in each file.
- Every claim about multi-cluster scope includes "[local cluster only]" if applicable per FR-013.

---

## Standard FR-010: Internal Links

**Requirement** (FR-010c, SC-006): All internal doc links in README.md and docs/ resolve to existing targets.

### Scope

Links to check:

- **File-only links**: `[text](docs/install.md)`, `[text](../other-dir/file.md)` (relative paths in markdown)
- **Anchor links**: `[text](docs/install.md#section-name)` (with anchor targeting a markdown heading)
- **Same-file anchors**: `[text](#section-name)` within the same document

Links to **exclude**:

- External links (http://, https://, ftp://) — out of scope; hack/check-links.sh (OD-2) validates internal links and anchors only, per maintainer ruling 2026-09-02.
- Links to external websites or GitHub repos (not owned by Gameplane)

### Check Procedure

1. **Parse each audited file** for markdown link syntax: `[text](target)`.

2. **For each internal link** (file-only or relative path):
   - Resolve the path relative to the audited file's directory.
   - Verify the target file exists in the repository.
   - If the link includes an anchor (e.g., `#section-name`):
     - Extract the anchor slug.
     - Verify the target file contains a heading that would generate that anchor slug (account for GitHub-flavored markdown heading-to-anchor conversion: spaces → hyphens, special chars removed/escaped, lowercase).
     - Explicit HTML anchors (`<a id="...">` / `<a name="...">`) count as valid anchor targets in addition to heading slugs (needed for docs/comparison-sources.md row anchors).
   - If no anchor is present, pass as long as the file exists.

3. **Record failures**: File paths that don't exist or anchors that don't match any heading.

4. **Manual fallback** (when automated link-check tooling is unavailable per OD-2):
   ```bash
   # For a link [text](docs/install.md#helm-values):
   # 1. Verify file exists:
   test -f docs/install.md
   # 2. Extract heading text from the target file:
   grep -n "^## Helm Values" docs/install.md
   # 3. Confirm the heading text matches the expected anchor slug
   #    (## Helm Values → #helm-values)
   ```

### Pass Condition

- **Zero broken internal links** across all 17 files (SC-008c, SC-006).
- All anchors resolve to valid headings or explicit HTML anchors in the target file.
- Count: Zero failures.

### Evidence Citation Format

```
File: docs/networking.md
Line: 13
Finding: Broken internal link
Broken Link: [reference](docs/install.md#network-setup)
Issue: Relative path resolution doubled: docs/docs/install.md (incorrect)
Evidence Checked: grep "#network-setup" docs/install.md (no match found)
Resolution: Change to [reference](install.md#network-setup)
Commit: (filled during implementation; D-F)
```

---

## Standard FR-010: Optional/Experimental/Beta Labels

**Requirement** (FR-010d, SC-007): Features marked as optional, experimental, or beta in code/charts are labelled consistently in all docs.

### Component Label Registry (R3 Findings)

Seven components have D-C tags and must be labelled consistently at first mention in each audited file:

| Component | Tag(s) | Default State | Source Evidence |
|-----------|--------|---------------|-----------------|
| sentinel (wake-on-connect) | `[optional]` | `sentinelImage: ""` (empty string) | `values.yaml:58` |
| capture-sidecar (AF_PACKET) | `[optional]` | `capture.enabled: false` | `values.yaml:522` |
| mcp-server | `[optional]` | `mcpServer.enabled: false` | `values.yaml:395` |
| audit-syslog-bridge | `[optional]` | Not in default chart; deployed separately | Disabled by default per `values.yaml:178` |
| telemetry-receiver | `[optional]` | `api.telemetry.receiver.enabled: false` | `values.yaml:230` |
| tunnel (relay supervisor) | `[optional]` | Not in chart; external component | `CLAUDE.md:372` ("optional relay client supervisor") |
| postgres persistence driver | `[experimental]` | `api.db.driver: sqlite` (default); postgres via build tag | `CLAUDE.md:378` ("experimental, work-in-progress") |

### First-Mention Rule (FR-012)

For each audited file where a component is mentioned:

1. **On the first mention** of the component, the appropriate tag from the registry above MUST appear.
   - Example: "The **sentinel** component [optional] watches advertised ports…"
   - Example: "The **postgres persistence driver** [experimental, work-in-progress] is available…"

2. **Subsequent mentions** in the same file may omit the tag if context makes the status clear (e.g., within a section marked "[Optional Components]").

3. **If a file never mentions the component**, no tag is needed.

### Components with Zero Mentions

Six audited files (`game-coverage.md`, `key-rotation.md`, `module-authoring.md`, `networking.md`, `notifications.md`, `oidc.md`) contain zero mentions of optional components. This is acceptable per scope — they document features that don't depend on optional components.

### Pass Condition

- **Zero inconsistent labels** across all mentions of a component within and across audited files (SC-007, SC-008d).
- Every component in the registry above that appears in an audited file carries the appropriate tag at first mention.
- Gameplane-specific components use tags from the vocabulary above, not invented qualifiers.
- Count: Consistent application across all 17 files; zero contradictory labels.

### Evidence Citation Format

```
File: docs/install.md
Line: 191
Finding: Optional component missing label
Claim: "The sentinel component watches advertised ports…"
Expected Label: [optional]
Evidence: values.yaml:58 (operator.sentinelImage: "" → disabled by default)
          CLAUDE.md:372 ("optional wake-on-connect component")
Resolution: Change to "The **sentinel** component [optional] watches…"
Commit: (filled during implementation; D-F)
```

---

## Standard FR-013: Multi-Cluster Console/Log Streaming Scope

**Requirement** (FR-013, FR-008, SC-002): Multi-cluster console and log streaming are explicitly limited to the local control-plane cluster.

### Rule Text

Any mention of console or log streaming in the context of multi-cluster deployments MUST include the qualifier **`[local cluster only]`**.

### Evidence (R5 + R8 Findings)

From codebase:
- `README.md:35` conveys the same limitation: "You can register and manage multiple clusters from a single dashboard, but WebSocket console/log streaming is currently scoped to the local control-plane cluster."
- `roadmap.md:18-22` clarifies: "Currently scoped to local control-plane cluster; remote cluster event feeds coming in v1.1"
- Web routing in `web/src/router/tree.tsx` and handlers in `api/internal/handlers/ws.go` guard WebSocket console/log endpoints with cluster-local checks.

### Scope

This rule applies to:
- Claims about "multi-cluster console access"
- Claims about "multi-cluster log streaming"
- Statements like "GameServers in any cluster can stream logs"

Does **not** apply to:
- Multi-cluster monitoring (event listing, status queries)
- Multi-cluster game server lifecycle management (create/stop/restart)

### Pass Condition

- **Zero unqualified multi-cluster console/log claims** in audited files (FR-013, SC-008).
- Every such claim includes `[local cluster only]` or explicit scope limitation.
- Count: Zero violations; evidence citable from code + docs.

---

## Standard FR-014: No Unshipped Features Announced as Available

**Requirement** (FR-014, SC-004): README.md and docs MUST NOT present features announced but not yet shipped as if they are available.

### Rule Text

Documentation MUST NOT claim "Gameplane supports X" or "Feature X is available" for any capability that:
1. Is listed in `CHANGELOG.md` under the "Unreleased" section (not yet released in any stable/beta version), **and**
2. Is not present in the latest released version (`v0.2.0-beta.8`).

### Roadmap Exemption (OD-7)

Roadmap.md is explicitly a forward-looking document. Entries in `docs/roadmap.md` may describe unshipped work as long as every item carries an explicit status marker:
- **`(shipped vX.Y.Z)`** — feature shipped in this release
- **`(planned)`** — feature planned for a future release

Every roadmap entry MUST have one of these markers so the document is self-explanatory. Roadmap.md becomes MODIFIED (not only audited) to ensure all entries carry status markers.

All other audited files MUST treat the roadmap as reference only and NOT advertise roadmap work as available.

### Unreleased Features (OD-8)

For every feature listed in `CHANGELOG.md` Unreleased section that documentation describes:

**Verification procedure:**
1. Check whether the feature is actually in v0.2.0-beta.8:
   - Inspect operator CRDs (`operator/api/v1alpha1/`)
   - Check API handlers (`api/internal/handlers/`)
   - Check Helm values (`charts/gameplane/values.yaml`)
   - Check web routes (`web/src/router/tree.tsx`)

2. **If shipped in v0.2.0-beta.8:** Move the CHANGELOG.md entry from "Unreleased" to the "v0.2.0-beta.8" section (this is a documentation fix, not a feature fix; CHANGELOG.md becomes MODIFIED).

3. **If not shipped:** Add the label **`(unreleased; ships in the next release)`** on first mention in the audited file.

### Check Procedure

1. **Extract the "Unreleased" section** from `CHANGELOG.md` (typically the topmost section before the first release heading).
2. **List all feature items** in "Unreleased" (e.g., "OIDC role mappings", "Default StorageClass feature").
3. **For each item**, scan all audited files for claims that present that feature as available.
4. **Verify** whether the feature is actually in v0.2.0-beta.8:
   - Check operator CRDs (`operator/api/v1alpha1/`)
   - Check API handlers (`api/internal/handlers/`)
   - Check Helm values (`charts/gameplane/values.yaml`)
   - Check web routes (`web/src/router/tree.tsx`)
5. **If not in beta.8 release**, flag the claim and either:
   - Qualify it per OD-8, or
   - Remove it and defer to roadmap.md

### Pass Condition

- **Zero unqualified claims** about unreleased features in non-roadmap audited files (SC-004, FR-014).
- `docs/roadmap.md` is MODIFIED to carry explicit `(shipped vX.Y.Z)` or `(planned)` markers on every entry (OD-7).
- All forward-looking language in other audited files is either:
  - References to `docs/roadmap.md` (exempt per OD-7), or
  - Qualified with `(unreleased; ships in next release)` per OD-8 if actually in CHANGELOG.md Unreleased and verified unshipped, or
  - Absent (features presented only after they ship).

### Evidence Citation Format

```
File: docs/oidc.md
Line: 318
Finding: Unshipped feature presented as available
Claim: "Helm-seeded OIDC role mappings are configured via…"
Evidence: CHANGELOG.md:38-45 lists "OIDC role mappings" under Unreleased
          operator/api/v1alpha1/cluster_types.go shows no spec.oidc.roleMappings field
          (Feature not in v0.2.0-beta.8)
Resolution: Add label "(unreleased; ships in the next release)"
            OR defer to roadmap.md
Commit: (filled during implementation; D-F)
```

---

## Audit Evidence Log (D-F)

All corrections identified during the audit MUST be recorded in `/home/user/Gameplane/specs/done_012-docs-refresh-and-outreach/audit-log.md` with the following schema:

### Schema

| Column | Description | Format | Example |
|--------|-------------|--------|---------|
| File | Audited file path (relative) | `path/to/file.md` | `docs/install.md` |
| Line(s) | Starting line number(s) of the finding | `N` or `N-M` | `14` or `191-198` |
| Finding | Concise description of the issue | One sentence | "Version string is stale" |
| Evidence Checked | Path:line citations of the truth source(s) consulted | `path:line` or comma-separated | `charts/gameplane/Chart.yaml:6, CHANGELOG.md:47` |
| Resolution | Correction applied | Brief description | "Update to v0.2.0-beta.8" |
| Commit | Git commit hash of the change | `<hash>` or empty during planning | (filled during implementation) |

### Worked Example (R1 Finding)

| File | Line(s) | Finding | Evidence Checked | Resolution | Commit |
|------|---------|---------|------------------|-----------|--------|
| telemetry-receiver/README.md | 9, 28 | Version string outdated: example shows v0.2.0-beta.7, current is v0.2.0-beta.8 | charts/gameplane/Chart.yaml:6 (appVersion = 0.2.0-beta.8), CHANGELOG.md:47 (release date 2026-08-22) | Update example payloads to version 0.2.0-beta.8 | (filled during impl.) |

### Markdown Table Format

The audit log is a standard GitHub-flavored markdown table with one row per finding. Rows are added during implementation as corrections are identified and applied.

---

## Link Checking (OD-2)

### Implementation: hack/check-links.sh

A read-only POSIX shell script `hack/check-links.sh` performs internal link validation following the done_011 precedent (ruling D6).

**Inputs**:
- All 17 audited files (listed above)
- Internal link targets (relative file paths and markdown anchors)

**Processing**:
- Resolve each internal link relative to its source file
- Verify target file exists in repository
- Verify anchor exists in target file using GitHub-flavored markdown slug rules (if anchor is present)
- Track which link categories failed (missing files, broken anchors)

**Exit Semantics**:
- **Exit 0 (success)**: All internal links resolve; zero failures detected
- **Exit 1 (failure)**: One or more internal links are broken (missing file or invalid anchor)

**Output**:
- Detailed failure log with file, link, and reason for each broken link
- Summary line with count of failures, or success message

**Usage**:
- Locally as pre-flight check: `hack/check-links.sh` (per D6 precedent)
- In CI: runs as a step in the `lint` job, same pattern as `hack/check-specs.sh`
- No external dependencies; stdlib POSIX shell only

---

## Version String Checking (OD-1)

### Implementation: hack/check-doc-versions.sh

A read-only POSIX shell script `hack/check-doc-versions.sh` performs version string drift detection.

**Inputs**:
- Chart version from `charts/gameplane/Chart.yaml:6` (appVersion field)
- All 17 audited files

**Processing**:
- Read the current Gameplane appVersion from the chart
- Scan each audited file for version literals (regex: `v?0\.[0-9]\.[0-9]-beta\.[0-9]`)
- For each match, check context (±2 lines) for allowlist markers:
  - `(example version)`, `(example)`, `(placeholder)`
  - Code blocks marked `# Example:` or `<!-- Example -->`
  - Historical references (e.g. `docs/install.md:54` "Pre-rotation releases (v0.2.0-beta.7 and earlier)") are allowlisted by an inline HTML comment on the same line: `<!-- doc-versions: historical -->` (OD-14, ruled 2026-09-02)
- If matched as allowlisted example, pass; otherwise treat as a product documentation claim
- Product claims MUST match the current appVersion exactly

**Exit Semantics**:
- **Exit 0 (success)**: All version strings match current appVersion or are allowlisted examples
- **Exit 1 (failure)**: One or more product version strings drift from current appVersion

**Output**:
- Detailed failure log with file, line, found version, and current appVersion
- Summary line with count of failures, or success message

**Usage**:
- Locally as pre-flight check: `hack/check-doc-versions.sh` (per D6 precedent)
- In CI: runs as a step in the `lint` job, same pattern as `hack/check-specs.sh`
- No external dependencies; stdlib POSIX shell only

---

## Done When (SC-004 to SC-008)

The documentation audit is **complete** when all of the following are true:

### Version String Audit (SC-005)

- [ ] **Every version string in product documentation matches `0.2.0-beta.8`** or is explicitly marked as an example version.
  - Scan: regex `v?0\.[0-9]\.[0-9]-beta\.[0-9]`
  - Evidence: `charts/gameplane/Chart.yaml:6`
  - Pass: Zero stale version strings across all 17 files.

### Feature Description Audit (SC-004)

- [ ] **Every feature claim in the 17 audited files is verified against the codebase** and matches the implementation.
  - Scope: CRD fields, API endpoints, agent capabilities, Helm values, reconciler behavior.
  - Evidence: path:line from `operator/api/v1alpha1/`, `api/internal/handlers/`, `agent/internal/`, `charts/gameplane/values.yaml`.
  - Pass: Zero feature description mismatches; all claims traceable to code.

### Internal Link Audit (SC-006)

- [ ] **All internal links in the 17 audited files resolve to existing targets.**
  - Scope: File-only links and anchor links (relative paths in markdown).
  - Exclude: External links (http/https) — covered by OD-2.
  - Evidence: Manual verification via file existence checks and heading grep.
  - Pass: Zero broken internal links; all anchors match valid headings.

### Optional/Experimental/Beta Label Audit (SC-007)

- [ ] **Every component in the registry (sentinel, capture-sidecar, mcp-server, audit-syslog-bridge, telemetry-receiver, postgres, and tunnel) that appears in an audited file is labelled with the appropriate tag at first mention.**
  - Tags: `[optional]`, `[experimental]`, `[disabled by default]`, or `[BETA]` per D-C vocabulary.
  - Evidence: `values.yaml` defaults and `CLAUDE.md` wording.
  - Pass: Consistent labelling across all 17 files; zero contradictions.

### Multi-Cluster Scope Audit (FR-013)

- [ ] **All claims about multi-cluster console or log streaming include the qualifier `[local cluster only]`.**
  - Evidence: `README.md:35`, `roadmap.md:18-22`, `web/src/router/tree.tsx`, `api/internal/handlers/ws.go`.
  - Pass: Zero unqualified multi-cluster console/log claims.

### Roadmap Status Markers (OD-7)

- [ ] **Every entry in `docs/roadmap.md` carries an explicit status marker.**
  - Markers: `(shipped vX.Y.Z)` for released features, `(planned)` for future features.
  - Evidence: visual inspection of roadmap.md; no entry is unmarked.
  - Note: `docs/roadmap.md` becomes MODIFIED to ensure all entries carry markers.
  - Pass: 100% of roadmap entries carry status markers.

### Unshipped Features Audit (FR-014 + OD-8)

- [ ] **No feature listed in `CHANGELOG.md` Unreleased section is presented in non-roadmap audited files as if it is available.**
  - For each Unreleased entry documented in the audited files:
    - Verify whether the feature is in v0.2.0-beta.8 release (check operator/api/agent/web/chart code).
    - If shipped: move the CHANGELOG entry to v0.2.0-beta.8 section (`CHANGELOG.md` becomes MODIFIED).
    - If unshipped: add "(unreleased; ships in next release)" label on first mention in each file.
  - Features in `docs/roadmap.md` are exempt (covered by OD-7 audit above).
  - Evidence: `CHANGELOG.md` Unreleased section vs. actual feature presence in operator/api/agent/web/chart.
  - Pass: Zero unqualified unshipped feature claims in non-roadmap files.

### Audit Evidence Log Complete (D-F)

- [ ] **Every correction identified is recorded in `audit-log.md`** with file, line(s), finding, evidence, resolution, and commit hash (or pending during implementation).
  - Schema: GitHub-flavored markdown table per D-F.
  - Pass: Audit log fully populated with all findings.

### Summary (SC-008 + OD-1 through OD-8)

**Core audit standards (SC-004 to SC-008):**
- [ ] **(a) Zero version-string mismatches** against v0.2.0-beta.8 across all 17 files (SC-005, verified by hack/check-doc-versions.sh).
- [ ] **(b) Zero feature description mismatches** in product documentation (SC-004).
- [ ] **(c) Zero broken internal links** across all 17 files (SC-006, verified by hack/check-links.sh).
- [ ] **(d) Consistent labelling of optional/experimental/beta features** across all mentions (SC-007).
- [ ] **(e) Multi-cluster console/log streaming qualified with [local cluster only]** (FR-013).

**Tooling and policies (OD-1 through OD-8):**
- [ ] **(f) hack/check-links.sh** integrated as CI lint step (OD-2).
- [ ] **(g) hack/check-doc-versions.sh** integrated as CI lint step (OD-1).
- [ ] **(h) Roadmap markers** — every docs/roadmap.md entry carries `(shipped vX.Y.Z)` or `(planned)` (OD-7).
- [ ] **(i) Unreleased features handled per OD-8** — CHANGELOG.md moved entries or audited files carry `(unreleased; ships in next release)` label.
- [ ] **(j) Audit evidence log complete** with all findings recorded in audit-log.md (D-F).

**Combined pass rate**: All categories (a through j) show zero unresolved findings.

---

## References

- **Feature Specification**: `specs/done_012-docs-refresh-and-outreach/spec.md` (FR-009 to FR-014, SC-004 to SC-008)
- **Constitution**: `.specify/memory/constitution.md` (Principle IV: Spec-Driven Development)
- **Project Guidance**: `CLAUDE.md` (rules 8, 11, 13, 15, 16; no-local-execution, commit-early, delegation)
- **Research Findings**: `specs/done_012-docs-refresh-and-outreach/research.md` (synthesis of version/link/label/screenshot/gameplane-facts/outreach/competitor-sources/unshipped audits; detailed research summaries R1–R8 are in the scratchpad during planning)
- **Orchestrator Decisions**: `specs/done_012-docs-refresh-and-outreach/OPEN-DECISIONS.md` (D-A through D-I, OD-1 through OD-15)
- **Style Reference**: `specs/done_011-add-missing-module-specs/contracts/check-specs.md` (tone, heading discipline, evidence density)
