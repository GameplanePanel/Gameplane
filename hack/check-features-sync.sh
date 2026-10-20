#!/usr/bin/env bash
# check-features-sync.sh: Verify that FEATURES.md and the website features page carry the same capability catalog.
#
# Purpose:
#   Implements spec 024 FR-015 (docs-feature-list-refresh). FEATURES.md (repo root) is the catalog
#   source; website/src/content/docs/features.mdx must list the same areas, capabilities, maturity and
#   default values in the same order. Description and Docs cells may differ.
#   Grammar:  specs/done_024-docs-feature-list-refresh/contracts/features-catalog.md
#   Behaviour: specs/done_024-docs-feature-list-refresh/contracts/features-sync-check.md
#
# Parsing rules:
#   - Area = level-2 heading ("## ") that owns a catalog table. Level-1 and level-3+ headings are ignored.
#   - Catalog table = header row exactly "| Capability | Maturity | Default | Description | Docs |",
#     then its separator row. Tables with any other header are ignored.
#   - Lines inside fenced code blocks (``` or ~~~) are skipped.
#   - Maturity: stable|beta|experimental|planned|in progress. Default: on|off|n/a.
#     A stable row must have default "on".
#
# Env overrides (fixtures use these):
#   FEATURES_MD   repo catalog path (default: FEATURES.md)
#   FEATURES_MDX  website page path (default: website/src/content/docs/features.mdx)
#
# Exit codes:
#   0 = both files parse, validate and match
#   1 = validation problems or differences found
#   2 = one or more input files missing

set -euo pipefail

# Navigate to repo root (one level up from script's directory)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$REPO_ROOT"

features_md="${FEATURES_MD:-FEATURES.md}"
features_mdx="${FEATURES_MDX:-website/src/content/docs/features.mdx}"

missing=0
for f in "$features_md" "$features_mdx"; do
    if [[ ! -f "$f" ]]; then
        echo "features-sync: missing $f"
        missing=1
    fi
done
if [[ $missing -ne 0 ]]; then
    exit 2
fi

# Single awk pass over both files. Side "R" is the first file argument (FEATURES.md),
# side "W" is the second (features.mdx). Validation messages print during the parse;
# comparison messages print from END, in this order: area mismatch, only-in rows,
# field differences, order differences.
if awk '
BEGIN {
    VM["stable"] = 1; VM["beta"] = 1; VM["experimental"] = 1; VM["planned"] = 1; VM["in progress"] = 1
    VD["on"] = 1; VD["off"] = 1; VD["n/a"] = 1
    HDR = "| Capability | Maturity | Default | Description | Docs |"
    N["R"] = 0; N["W"] = 0; AN["R"] = 0; AN["W"] = 0
    problems = 0
}
function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
FNR == 1 {
    side = (FILENAME == ARGV[1]) ? "R" : "W"
    area = ""; area_recorded = 0; in_fence = 0; in_table = 0; expect_sep = 0
}
{ hdr = $0; sub(/[ \t\r]+$/, "", hdr) }
/^[ \t]*(```|~~~)/ { in_fence = !in_fence; next }
in_fence { next }
/^## / { area = trim(substr($0, 4)); area_recorded = 0; in_table = 0; next }
hdr == HDR {
    in_table = 1; expect_sep = 1
    if (!area_recorded) { area_recorded = 1; AN[side]++; AL[side, AN[side]] = area }
    next
}
in_table && /^\|/ {
    if (expect_sep) { expect_sep = 0; next }
    split($0, f, "|")
    cap = trim(f[2]); mat = trim(f[3]); def = trim(f[4])
    if (!(mat in VM)) { printf "features-sync: %s:%d: invalid maturity \"%s\"\n", FILENAME, FNR, mat; problems++ }
    if (!(def in VD)) { printf "features-sync: %s:%d: invalid default \"%s\"\n", FILENAME, FNR, def; problems++ }
    if (mat == "stable" && def != "on") { printf "features-sync: %s:%d: stable row must have default \"on\"\n", FILENAME, FNR; problems++ }
    N[side]++; i = N[side]
    AREA[side, i] = area; CAP[side, i] = cap; MAT[side, i] = mat; DEF[side, i] = def
    next
}
in_table { in_table = 0 }
END {
    for (i = 1; i <= N["R"]; i++) {
        k = AREA["R", i] SUBSEP CAP["R", i]
        if (k in RIDX) { printf "features-sync: duplicate row in FEATURES.md: %s / %s\n", AREA["R", i], CAP["R", i]; problems++ } else RIDX[k] = i
    }
    for (j = 1; j <= N["W"]; j++) {
        k = AREA["W", j] SUBSEP CAP["W", j]
        if (k in WIDX) { printf "features-sync: duplicate row in features.mdx: %s / %s\n", AREA["W", j], CAP["W", j]; problems++ } else WIDX[k] = j
    }

    n = (AN["R"] > AN["W"]) ? AN["R"] : AN["W"]
    for (p = 1; p <= n; p++) {
        ra = (p <= AN["R"]) ? AL["R", p] : ""
        wa = (p <= AN["W"]) ? AL["W", p] : ""
        if (ra != wa) { printf "features-sync: area mismatch at position %d: \"%s\" vs \"%s\"\n", p, ra, wa; problems++ }
    }

    for (i = 1; i <= N["R"]; i++) {
        k = AREA["R", i] SUBSEP CAP["R", i]
        if (!(k in WIDX)) { printf "features-sync: only in FEATURES.md: %s / %s\n", AREA["R", i], CAP["R", i]; problems++ }
    }
    for (j = 1; j <= N["W"]; j++) {
        k = AREA["W", j] SUBSEP CAP["W", j]
        if (!(k in RIDX)) { printf "features-sync: only in features.mdx: %s / %s\n", AREA["W", j], CAP["W", j]; problems++ }
    }

    for (i = 1; i <= N["R"]; i++) {
        k = AREA["R", i] SUBSEP CAP["R", i]
        if (!(k in WIDX)) continue
        j = WIDX[k]
        if (MAT["R", i] != MAT["W", j]) { printf "features-sync: %s / %s: maturity \"%s\" vs \"%s\"\n", AREA["R", i], CAP["R", i], MAT["R", i], MAT["W", j]; problems++ }
        if (DEF["R", i] != DEF["W", j]) { printf "features-sync: %s / %s: default \"%s\" vs \"%s\"\n", AREA["R", i], CAP["R", i], DEF["R", i], DEF["W", j]; problems++ }
    }

    rc = 0
    for (i = 1; i <= N["R"]; i++) { if ((AREA["R", i] SUBSEP CAP["R", i]) in WIDX) RPOS[++rc] = i }
    wc = 0
    for (j = 1; j <= N["W"]; j++) { if ((AREA["W", j] SUBSEP CAP["W", j]) in RIDX) WPOS[++wc] = j }
    m_max = (rc < wc) ? rc : wc
    for (m = 1; m <= m_max; m++) {
        i = RPOS[m]; j = WPOS[m]
        if (AREA["R", i] != AREA["W", j] || CAP["R", i] != CAP["W", j]) { printf "features-sync: %s / %s: order differs\n", AREA["R", i], CAP["R", i]; problems++ }
    }

    if (problems > 0) exit 1
    exit 0
}
' "$features_md" "$features_mdx"; then
    exit 0
fi
exit 1
