# Quickstart: Validating Feature 012 (Documentation Refresh & Outreach)

This guide walks through validating that the documentation refresh implementation is complete and correct. It covers the comparison table, version strings, internal links, optional component labels, unshipped features, screenshots, outreach tracking, and the audit log.

All validation commands below are read-only grep, file inspection, and test operations. No cluster or test suites are run locally (per CLAUDE.md rule 8 and constitution Principle VI).

## Prerequisites

- Checked out on branch `012-docs-refresh-and-outreach`
- `README.md` has been updated with comparison table (intro paragraph, status line, table)
- `docs/comparison-sources.md` has been created with source documentation (explicit HTML anchors per the maintainer-signed contract correction; GitHub-flavored Markdown does not support `{#id}` heading attributes)
- `docs/` audited files have been refreshed for version strings (with `<!-- doc-versions: historical -->` markers per OD-14), links, and labels
- `docs/img/` contains six existing and at least five new screenshots (1920×1080 JPEG per OD-3a, captured via CI workflow_dispatch per OD-15)
- `specs/done_012-docs-refresh-and-outreach/outreach.md` has been created
- `docs/contributing.md` links to the outreach tracker
- `specs/done_012-docs-refresh-and-outreach/audit-log.md` has been created with all findings
- `hack/check-doc-versions.sh` and `hack/check-links.sh` exist (once implemented per OD-1 and OD-2)
- `.github/workflows/screenshot-refresh.yaml` has been created with `workflow_dispatch` trigger (OD-15)

## Scenario 1: Comparison Table Present and Sourced

**Validation**: The comparison table exists in README.md, positioned correctly, with all nine rows, four columns, status line, and source markers linking to docs/comparison-sources.md.

**How to verify**:

```bash
# Check table position: must be after "## Why Gameplane?" and before "## Features"
grep -n "^## Why Gameplane?" README.md
grep -n "^## Features" README.md

# Confirm status line appears before the table
grep -n "Status: \*\*beta\*\*" README.md

# Count comparison table rows (should be 9 data rows + 1 header)
grep -c "^| " README.md | head -20
awk '/Status: \*\*beta\*\*/{flag=1} /^## Features/{flag=0} flag' README.md | grep "^| " | wc -l

# Verify source markers exist for Gameplane column ([G-a] through [G-i])
grep "\[G-[a-i]\]" README.md | wc -l

# Verify competitor source markers exist for at least one row per product
grep -E "\[P-[a-i]\]|\[C-[a-i]\]|\[A-[a-i]\]" README.md | wc -l

# Check that docs/comparison-sources.md exists and has product sections
test -f docs/comparison-sources.md && echo "✓ docs/comparison-sources.md exists" || echo "✗ missing"
grep -c "^## " docs/comparison-sources.md
```

**Expected outcome**:

- Status line is at a line number between the two grep results for "## Why Gameplane?" and "## Features"
- Comparison table appears immediately after status line, with an intro paragraph above it explaining scope (Pterodactyl/CubeCoders are control panels; Agones is a Kubernetes operator library)
- Table has 9 data rows (one for each dimension a–i)
- Gameplane column has 9 source markers ([G-a] through [G-i])
- At least 27 competitor source markers present (minimum 3 per dimension for competitors)
- `docs/comparison-sources.md` exists and contains four top-level sections (Gameplane, Pterodactyl, CubeCoders AMP, Agones)
- Each row heading in docs/comparison-sources.md is preceded by an explicit HTML anchor line `<a id="<product>-row-<letter>"></a>` (grep -c '<a id="' docs/comparison-sources.md >= 36)

**SC-001, SC-002, SC-003**: Confirms comparison table presence, Gameplane column traceability, and competitor sourcing per contracts/comparison-table.md §1–5.

---

## Scenario 2: Version Strings Match Current Release

**Validation**: All version strings in the 17 audited files match v0.2.0-beta.8 or are explicitly marked as example versions.

**How to verify**:

```bash
# Primary: Run the version checker script
hack/check-doc-versions.sh
# Expect exit code 0 on success

# Fallback (if script unavailable): manual version string check
CURRENT_VERSION="0.2.0-beta.8"
echo "Current version: $CURRENT_VERSION"

# Check appVersion in Helm chart (truth source)
grep "appVersion:" charts/gameplane/Chart.yaml

# Scan all 17 audited files for version patterns (stale or current)
FILES="README.md docs/architecture.md docs/contributing.md docs/dependencies.md docs/game-coverage.md docs/install.md docs/key-rotation.md docs/module-authoring.md docs/networking.md docs/notifications.md docs/oidc.md docs/roadmap.md docs/security.md docs/tunnels.md audit-syslog-bridge/README.md mcp-server/README.md telemetry-receiver/README.md"

echo "=== Version string scan ==="
for file in $FILES; do
  if grep -E "v?0\.[0-9]\.[0-9]-beta\.[0-9]" "$file" > /dev/null; then
    echo "✓ $file contains version patterns:"
    grep -n "v?0\.[0-9]\.[0-9]-beta\.[0-9]" "$file" | head -5
  fi
done
```

**Expected outcome**:

- `charts/gameplane/Chart.yaml:6` shows `appVersion: 0.2.0-beta.8`
- All version strings in audited files are either:
  - `0.2.0-beta.8` (current), or
  - Marked as `(example)`, `(example version)`, or in a code block labeled as example, or
  - Marked as `<!-- doc-versions: historical -->` inline on the same line (OD-14)
- No stale versions like `0.2.0-beta.7`, `0.2.0-beta.6`, etc. in product documentation
- Examples, placeholders, and historical references are clearly marked

**SC-005, SC-008a**: Confirms zero stale version strings per contracts/docs-audit.md §FR-010 (Version Strings).

---

## Scenario 3: Internal Links Resolve to Existing Targets

**Validation**: All internal doc links in README.md and docs/ resolve to existing files and valid markdown headings.

**How to verify**:

```bash
# Primary: Run the link checker script
hack/check-links.sh
# Expect exit code 0 on success

# Fallback (if script unavailable): manual link check per contracts/docs-audit.md §Link Checking

# Example: verify [reference](docs/install.md#helm-values)
# Step 1: Check file exists
test -f docs/install.md && echo "✓ File exists" || echo "✗ File missing"

# Step 2: Look for the heading that would generate the anchor
# GitHub converts headings to anchors: spaces→hyphens, lowercase, special chars removed
grep -n "^## Helm Values\|^### Helm Values\|^#### Helm Values" docs/install.md

# Step 3: Verify cross-references in networking.md are correct
# (Known issue from R2: some links use "docs/install.md" instead of "install.md")
echo "=== Checking for path resolution errors in docs/ ==="
grep -rn "\[.*\](docs/" docs/ README.md 2>/dev/null | head -10

# Sample verification: check that key cross-reference files exist
echo "=== Key cross-reference files ==="
for target in architecture.md install.md module-authoring.md security.md key-rotation.md; do
  test -f "docs/$target" && echo "✓ docs/$target" || echo "✗ docs/$target missing"
done
```

**Expected outcome**:

- All file-only links resolve to existing files (e.g., `docs/install.md`, `key-rotation.md`)
- Anchor links (e.g., `#helm-values`) correspond to valid headings in target files
- No double-nesting of directory paths (e.g., `docs/docs/file.md`)
- No broken cross-references between audit files

**SC-006, SC-008b**: Confirms zero broken internal links per contracts/docs-audit.md §FR-010 (Internal Links).

---

## Scenario 4: Optional/Experimental/Beta Components Labelled Consistently

**Validation**: Every optional, experimental, or beta component is labelled at first mention in each audited file with D-C vocabulary tags.

**How to verify**:

```bash
# Component label registry from contracts/docs-audit.md §Standard FR-010
COMPONENTS=(
  "sentinel"
  "capture-sidecar\|capture"
  "mcp-server\|MCP server"
  "audit-syslog-bridge\|audit-syslog"
  "telemetry-receiver\|telemetry"
  "tunnel"
  "postgres"
)

echo "=== Scanning for optional/beta component labels ==="

# Sample check: look for sentinel in docs and verify [optional] tag nearby
echo "Sentinel references:"
grep -n "sentinel" docs/*.md README.md 2>/dev/null | head -5

echo ""
echo "Checking for label tags on optional components:"
grep -rn "\[optional\]\|\[experimental\]\|\[BETA\]\|\[disabled by default\]" docs/ README.md | wc -l

# Verify Helm chart values.yaml has correct defaults for optional components
echo ""
echo "=== Verifying component defaults in Helm chart ==="
grep -n "enabled: false\|enabled: null" charts/gameplane/values.yaml | head -10
```

**Expected outcome**:

- Each optional component (sentinel, capture-sidecar, mcp-server, audit-syslog-bridge, telemetry-receiver, tunnel) appears with `[optional]` on first mention in each file where it appears
- Postgres driver appears with `[experimental]` on first mention in docs/install.md
- Beta features appear with `[BETA]` where applicable (e.g., multi-cluster console/log streaming)
- No contradictory labels for the same component across files
- Helm chart `values.yaml` confirms `enabled: false` or `enabled: null` for all optional components

**SC-007, SC-008c**: Confirms consistent labelling per contracts/docs-audit.md §Standard FR-010 (Optional/Experimental/Beta Labels).

---

## Scenario 5: No Unshipped Features Announced as Available

**Validation**: Features listed in CHANGELOG.md Unreleased section are not presented in audited files as available (except in docs/roadmap.md or with explicit "(unreleased)" qualification).

**How to verify**:

```bash
# Extract Unreleased section from CHANGELOG.md
echo "=== Features in CHANGELOG.md Unreleased section ==="
awk '/^## Unreleased/{flag=1} /^## [0-9]/{flag=0} flag' CHANGELOG.md | head -20

# Check if those features are documented as available in non-roadmap files
# Example: if CHANGELOG mentions "OIDC role mappings", search for it in docs/oidc.md
echo ""
echo "=== Sample: Check OIDC role mappings status ==="
grep -n "OIDC role\|role mappings" docs/oidc.md | head -3

# Verify every item in roadmap.md has shipped/planned marker (OD-7)
echo ""
echo "=== Checking roadmap.md for version markers ==="
grep -vE '^\s*#|^\s*$|^\s*\[' docs/roadmap.md | grep -vE '\(shipped v|^\(planned\)' | head -10

# Count items with proper markers
echo ""
echo "=== Roadmap items with markers ==="
grep -c "(shipped v\|(planned)" docs/roadmap.md
```

**Expected outcome**:

- Features in CHANGELOG.md Unreleased section are either:
  - NOT mentioned in audited files (most common), or
  - Qualified with "(unreleased; ships in the next release)" on first mention in docs, or
  - Documented in docs/roadmap.md in a clearly marked "Planned" or "Post-v1" section
- No unqualified claims like "Feature X is available" for unreleased features
- docs/roadmap.md contains clear section headers distinguishing shipped vs. planned work
- Every roadmap item carries an explicit "(shipped vX.Y.Z)" or "(planned)" marker per OD-7

**SC-004, FR-014**: Confirms no unshipped features announced as available per contracts/docs-audit.md §Standard FR-013 (Multi-Cluster Scope) and §Standard FR-014 (No Unshipped Features).

---

## Scenario 6: Screenshots Present and Compliant

**Validation**: At least 11 total screenshot files exist in docs/img/ (six existing + at least five new), all are JPEG 1920×1080, linked in README.md with alt text, and contain no forbidden patterns. README.md screenshot gallery includes one disclosure sentence above it stating that screenshots are captured against mocked data.

**How to verify**:

```bash
# Inventory screenshot files
echo "=== Screenshot file inventory ==="
ls -lh docs/img/*.jpg 2>/dev/null

# Verify format and dimensions (requires 'identify' from ImageMagick or 'file')
echo ""
echo "=== File format and dimensions ==="
file docs/img/*.jpg | grep -c JPEG

identify docs/img/*.jpg 2>/dev/null | grep "1920x1080" | wc -l

# Check that README.md has at least 11 image references
echo ""
echo "=== Image references in README.md ==="
grep -c "docs/img/.*\.jpg" README.md

# Scan for forbidden patterns in visible alt text (heuristic)
echo ""
echo "=== Checking alt text for forbidden patterns ==="
# Look for IPs, real hostnames, production names, real emails
grep -E '\[.*\]\(docs/img' README.md | grep -iE 'prod|production|customer|example\.com|[0-9]{1,3}\.[0-9]{1,3}\.|@[a-z]+\.(com|net|org)' | wc -l
# Expect 0 matches

# Sample: check that new screenshots use dummy data naming
echo ""
echo "=== Checking for test/demo data patterns ==="
grep "test-server\|demo-\|test-cluster\|test-user" README.md | wc -l
```

**Expected outcome**:

- `docs/img/` contains six original filenames (dashboard.jpg, servers-list.jpg, server-overview.jpg, mods-registry-browse.jpg, server-console.jpg, admin-mod-registries.jpg)
- At least five new screenshot files are present (login.jpg, create-server-template-select.jpg, server-detail-events.jpg, admin-settings-general.jpg, cluster-nodes.jpg, plus optionally server-detail-logs.jpg)
- All files are JPEG format
- All files are exactly 1920×1080 pixels (per OD-3a)
- README.md screenshot gallery includes one disclosure sentence above it stating "Screenshots are captured against mocked data" (per OD-3d)
- README.md contains at least 11 image links in the Screenshots section
- Each image has non-empty alt text describing purpose and key UI elements
- Alt text scan shows zero forbidden patterns (no real IPs, hostnames, emails, production names)
- Server/cluster/user names in alt text follow dummy-data naming scheme (test-*, demo-*, etc.)
- Individual alt texts do NOT mention mocking (per OD-3d)

**SC-009, SC-010, SC-011, FR-015, FR-016, FR-017, FR-018, FR-019**: Confirms screenshot presence, format, content, and data safety per contracts/screenshot-set.md §Scope and §Dummy Data Rule.

---

## Scenario 7: Outreach Tracking To-Do List Created and Linked

**Validation**: The outreach.md file exists in the spec folder, contains all three target directories with proper status vocabulary, and is linked from docs/contributing.md.

**How to verify**:

```bash
# Check that outreach.md exists
test -f specs/done_012-docs-refresh-and-outreach/outreach.md && echo "✓ outreach.md exists" || echo "✗ missing"

# Verify table structure: Target | Submission URL | Status | Submitted Reference | Notes
echo ""
echo "=== Outreach table content ==="
grep -A 10 "^| Target" specs/done_012-docs-refresh-and-outreach/outreach.md

# Verify all three target directories are listed
echo ""
echo "=== Checking for all three targets ==="
grep -c "AlternativeTo\|Awesome-Selfhosted\|Awesome-Kubernetes" specs/done_012-docs-refresh-and-outreach/outreach.md

# Verify status vocabulary (pending | in-progress | submitted | rejected | deferred)
echo ""
echo "=== Status values used ==="
grep -oE "(pending|in-progress|submitted|rejected|deferred)" specs/done_012-docs-refresh-and-outreach/outreach.md | sort | uniq -c

# Check that docs/contributing.md links to outreach.md
echo ""
echo "=== Link from docs/contributing.md ==="
grep -n "outreach\|outreach.md" docs/contributing.md

# Verify link path resolves
test -f docs/contributing.md && grep -q "outreach.md" docs/contributing.md && echo "✓ Link exists in contributing.md" || echo "✗ Link missing"
```

**Expected outcome**:

- `specs/done_012-docs-refresh-and-outreach/outreach.md` exists and is a markdown file
- Outreach table has exactly three rows (one per target: AlternativeTo, Awesome-Selfhosted, Awesome-Kubernetes)
- Each row has Status field with one of the allowed values: `pending`, `in-progress [YYYY-MM-DD]`, `submitted [YYYY-MM-DD]`, `deferred [YYYY-MM-DD, reason]`, or `rejected [YYYY-MM-DD, reason]`
- Per OD-6a, Awesome-Selfhosted status is: `deferred [2026-09-02, first release 2026-06-22 is under the 4-month minimum; eligible from 2026-10-22]`
- Per OD-6b, Awesome-Kubernetes status is: `deferred [2026-09-02, 25-star / 3-contributor eligibility rule not verified; revisit in a later release]`
- Per OD-6c, AlternativeTo status is: `pending` (or `submitted [YYYY-MM-DD]` if maintainer has already submitted per OD-5)
- Each row has a "Submitted Reference" field with appropriate content (pending/in-progress: "pending" or context; submitted: URL/PR/email date; deferred/rejected: "N/A" with reason in Notes)
- `docs/contributing.md` contains a link to `../specs/done_012-docs-refresh-and-outreach/outreach.md` (or similar relative path)
- Link resolves when docs/ is read

**SC-012, SC-013, SC-014, FR-020, FR-021, FR-022, FR-025**: Confirms outreach tracking structure, vocabulary, and linkage per contracts/outreach-todo.md §Table Schema and §Status Vocabulary.

---

## Scenario 8: Audit Log Complete with All Findings

**Validation**: The audit-log.md file exists and contains one row per correction, with file, line(s), finding, evidence, resolution, and commit information.

**How to verify**:

```bash
# Check that audit-log.md exists and is a markdown table
test -f specs/done_012-docs-refresh-and-outreach/audit-log.md && echo "✓ audit-log.md exists" || echo "✗ missing"

# Verify table structure (headers: File | Line(s) | Finding | Evidence Checked | Resolution | Commit)
echo ""
echo "=== Audit log headers ==="
head -5 specs/done_012-docs-refresh-and-outreach/audit-log.md

# Count audit log entries (number of data rows, skipping header row only)
echo ""
echo "=== Number of findings recorded ==="
grep "^| " specs/done_012-docs-refresh-and-outreach/audit-log.md | tail -n +2 | wc -l

# Verify each row has evidence citations (path:line format)
echo ""
echo "=== Evidence citation examples ==="
grep -oE "[a-z_/.-]+\.(md|yaml|go):?[0-9]*" specs/done_012-docs-refresh-and-outreach/audit-log.md | head -10
```

**Expected outcome**:

- `specs/done_012-docs-refresh-and-outreach/audit-log.md` exists and is a markdown table
- Table header row lists: File | Line(s) | Finding | Evidence Checked | Resolution | Commit
- Each correction identified during implementation is recorded as a separate row
- Evidence citations are in format `path:line` (e.g., `docs/install.md:14`, `charts/gameplane/Chart.yaml:6`)
- Resolutions describe the change made (e.g., "Update version string to 0.2.0-beta.8")
- Commit column either contains a git hash (during/after implementation) or is empty (during planning)

**D-F, FR-011**: Confirms audit log schema and completeness per contracts/docs-audit.md §Audit Evidence Log.

---

## Local Execution Note

Running the verification commands above (grep, file, test, identify) is permitted as a compile-check exception per maintainer ruling D6 and CLAUDE.md rule 8. These commands perform read-only static file inspection only; they are equivalent to `ls`, `cat`, or `grep` and do not run the test or lint suites. The full `make lint`, `make test`, or `make test-integration` targets remain CI-only.

---

## Done When

All scenarios above pass without error, confirming:

1. ✓ Comparison table is present, correctly positioned, and sourced (SC-001, SC-002, SC-003)
2. ✓ Version strings are current or explicitly marked as examples (SC-005, SC-008a)
3. ✓ Internal links resolve to existing files and anchors (SC-006, SC-008b)
4. ✓ Optional/experimental/beta components are consistently labelled (SC-007, SC-008c)
5. ✓ No unshipped features are announced as available (SC-004, FR-014)
6. ✓ Screenshots are present, properly formatted, and contain no forbidden patterns (SC-009, SC-010, SC-011)
7. ✓ Outreach tracking exists and is linked (SC-012, SC-013, SC-014)
8. ✓ Audit log is complete with all findings and evidence (D-F, FR-011)

---

## Cleanup

After validation, no branch cleanup is required. The feature branch remains active until the corresponding PR is approved and merged, at which point the branch is deleted per CLAUDE.md rule 12.

Once the feature is merged and the branch deleted, the spec folder is renamed from `specs/done_012-docs-refresh-and-outreach/` to `specs/done_012-docs-refresh-and-outreach/` per CLAUDE.md rule 16 and constitution Principle IV. In that same commit, update the link in `docs/contributing.md` to point to the new path.

---

## References

- **Feature Specification**: `specs/done_012-docs-refresh-and-outreach/spec.md` (all FR and SC requirements)
- **Contracts**: `specs/done_012-docs-refresh-and-outreach/contracts/` (comparison-table.md, docs-audit.md, outreach-todo.md, screenshot-set.md)
- **Orchestrator Decisions**: Feature spec Assumptions and Orchestrator Decisions (D-A through D-I, OD-1 through OD-15)
- **Research Findings**: synthesized into `specs/done_012-docs-refresh-and-outreach/research.md` (R1-R8 detailed findings were produced during planning)
- **Audit Log Schema**: `specs/done_012-docs-refresh-and-outreach/audit-log.md`
- **Style Reference**: `specs/done_011-add-missing-module-specs/quickstart.md` (matching tone and structure)
- **Project Guidance**: `CLAUDE.md` (rules 8, 11, 12, 16); `.specify/memory/constitution.md` (Principle IV, VI)
