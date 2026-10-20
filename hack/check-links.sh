#!/usr/bin/env bash
# check-links.sh: Validate internal markdown links and anchors across the
# documentation audit's 19 files.
#
# Purpose:
#   Ensures compliance with SC-006 (Internal Link Audit Standard) from feature 012
#   (docs-refresh-and-outreach), contracts/docs-audit.md "Standard FR-010: Internal
#   Links" and "Link Checking (OD-2)". Validates that every markdown link and image
#   target `[text](target)` / `![alt](target)` in the 19 audited files resolves to
#   an existing file, and that any `#anchor` fragment resolves to an existing
#   heading slug or explicit HTML anchor in the target file.
#
# Audited Files (19) — same fixed list as hack/check-doc-versions.sh:
#   README.md, docs/architecture.md, docs/contributing.md, docs/dependencies.md,
#   docs/game-coverage.md, docs/install.md, docs/key-rotation.md,
#   docs/module-authoring.md, docs/networking.md, docs/notifications.md,
#   docs/oidc.md, docs/roadmap.md, docs/security.md, docs/tunnels.md,
#   audit-syslog-bridge/README.md, mcp-server/README.md,
#   telemetry-receiver/README.md, docs/comparison-sources.md,
#   FEATURES.md
#
# Scope (contracts/docs-audit.md "Standard FR-010: Internal Links"):
#   - Markdown link targets `[text](target)` and image targets `![alt](target)`,
#     extracted per line via the `\]\([^)]*\)` pattern, then the surrounding
#     parens and any trailing ` "title"` are stripped.
#   - Targets starting with http://, https://, or mailto: are skipped (external
#     links are out of scope per OD-2 — no network access, air-gap friendly).
#   - A pure `#anchor` target (no path) is a same-file anchor reference and IS
#     checked against the source file's own anchor set.
#   - Lines inside fenced code blocks (``` ... ```) are not linted, so code
#     samples containing link-like text are ignored.
#
# Path resolution:
#   - `path#anchor` or bare `path` is resolved relative to the *source* file's
#     directory, unless it starts with `/`, in which case it is resolved
#     relative to the repository root.
#   - Resolution is a pure lexical normalization (collapsing `.` and `..`
#     segments) — no external `realpath`/`readlink` dependency, and the target
#     need not exist yet for the normalization step itself.
#
# Anchor slug rules (GitHub-flavored Markdown heading-to-anchor conversion):
#   1. Take the heading text; drop the leading `#`-`#` marker (1-6 hashes plus
#      whitespace, up to 3 leading spaces per GFM) and any trailing closing
#      `#`s (e.g. `## Title ##` -> `Title`).
#   2. Remove HTML comments `<!-- ... -->` and HTML tags `<...>` (markdown may
#      embed HTML for complex layouts; GitHub strips them from anchor slugs).
#      Trim any trailing whitespace that results from removal.
#   3. Replace markdown links `[text](url)` inside the heading with just `text`.
#   4. Remove backticks and any character that is not a letter (including
#      Unicode letters), digit, space, hyphen, or underscore — hyphen and
#      underscore are protected before stripping ASCII punctuation
#      ([:punct:], evaluated in the C locale so multi-byte UTF-8 sequences for
#      Unicode letters are left untouched) and restored afterward.
#   5. Lowercase the ASCII letters.
#   6. Convert every space to a hyphen — NOT collapsed: N consecutive spaces
#      become N consecutive hyphens.
#   7. If the resulting slug was already produced earlier in the same document
#      (in document order), append -1, -2, ... to disambiguate, matching
#      GitHub's own de-duplication behavior.
#
#   Worked example: "## Pre-v1 Status & Limitations"
#     -> strip marker:           "Pre-v1 Status & Limitations"
#     -> no markdown link, no backticks
#     -> remove '&' (its surrounding spaces are untouched by the removal,
#        since only the '&' character itself is deleted):
#                                 "Pre-v1 Status  Limitations"   (two spaces)
#     -> lowercase:               "pre-v1 status  limitations"
#     -> spaces -> hyphens:       "pre-v1-status--limitations"   (two hyphens)
#
#   Explicit HTML anchors `<a id="x">` / `<a name="x">` (case-insensitive tag
#   and attribute name; single or double quoted value) are also valid anchor
#   targets — GitHub-flavored Markdown does not support `{#id}` heading
#   attributes, so docs/comparison-sources.md (contracts/comparison-table.md
#   section 9) uses explicit `<a id="...">` anchors instead.
#
# Exit codes:
#   0 = all 19 audited files exist and every internal link/anchor resolves
#   1 = an audited file is missing, or one or more links/anchors are broken
#
# No network access. Pure bash + grep/sed/tr, plus perl (present on Ubuntu
# runners) for Unicode-aware anchor-slug punctuation stripping when
# available; falls back to an ASCII-only strip otherwise.

set -euo pipefail

# Navigate to repo root (one level up from script's directory)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$REPO_ROOT"

# Same fixed list of 19 audited files as hack/check-doc-versions.sh.
audited_files=(
    "README.md"
    "docs/architecture.md"
    "docs/contributing.md"
    "docs/dependencies.md"
    "docs/game-coverage.md"
    "docs/install.md"
    "docs/key-rotation.md"
    "docs/module-authoring.md"
    "docs/networking.md"
    "docs/notifications.md"
    "docs/oidc.md"
    "docs/roadmap.md"
    "docs/security.md"
    "docs/tunnels.md"
    "audit-syslog-bridge/README.md"
    "mcp-server/README.md"
    "telemetry-receiver/README.md"
    "docs/comparison-sources.md"
    "FEATURES.md"
)

total_issues=0
declare -A files_with_issues=()
declare -A anchor_cache=()

record_issue() {
    # $1 = source file the issue was found in (for the per-file tally)
    files_with_issues["$1"]=1
    total_issues=$((total_issues + 1))
}

# ---------------------------------------------------------------------------
# normalize_rel_path: lexically collapse "." and ".." segments in a path
# that is expressed relative to REPO_ROOT. Pure bash — no realpath/readlink,
# and the path need not exist. Empty/"." segments (incl. from "//" or a
# leading "./") are dropped.
# ---------------------------------------------------------------------------
normalize_rel_path() {
    local input="$1"
    local -a parts=() segs
    IFS='/' read -ra segs <<< "$input"
    for seg in "${segs[@]}"; do
        case "$seg" in
            "" | ".") continue ;;
            "..")
                if [[ ${#parts[@]} -gt 0 ]]; then
                    unset 'parts[-1]'
                    parts=("${parts[@]}")
                fi
                ;;
            *) parts+=("$seg") ;;
        esac
    done
    local IFS='/'
    printf '%s' "${parts[*]}"
}

# ---------------------------------------------------------------------------
# github_slug: convert a heading's text (marker already stripped) into a
# GitHub-flavored anchor slug. Does NOT de-duplicate — the caller tracks
# repeated slugs in document order.
# ---------------------------------------------------------------------------
github_slug() {
    local text="$1"
    # Remove HTML comments <!-- ... --> and HTML tags <...>.
    text=$(printf '%s' "$text" | sed 's/<!--.*-->//g; s/<[^>]*>//g')
    # Trim trailing whitespace that may result from HTML removal.
    text="${text%"${text##*[![:space:]]}"}"
    # Replace [text](url) with just text.
    text=$(printf '%s' "$text" | sed -E 's/\[([^]]*)\]\([^)]*\)/\1/g')
    # Remove backticks.
    text=${text//\`/}
    # Remove any character that is not a letter (including Unicode letters),
    # digit, space, hyphen, or underscore — GitHub's actual rule. `[[:punct:]]`
    # in the C locale only matches the 32 ASCII punctuation bytes, so it never
    # touches multi-byte UTF-8 punctuation/symbols (e.g. "→", "—"); use Perl's
    # Unicode-aware \p{L}/\p{N} properties instead, decoding/encoding the
    # stream as UTF-8 (-CSD) so multi-byte sequences are matched as single
    # characters. Falls back to the ASCII-only C-locale strip if perl is
    # unavailable, matching prior (ASCII-correct) behavior.
    if command -v perl >/dev/null 2>&1; then
        text=$(printf '%s' "$text" | LC_ALL=C.UTF-8 perl -CSD -pe 's/[^\p{L}\p{N} _-]//g')
    else
        text=$(printf '%s' "$text" | LC_ALL=C sed -e 's/-/\x01/g' -e 's/_/\x02/g' -e 's/[[:punct:]]//g' -e 's/\x01/-/g' -e 's/\x02/_/g')
    fi
    # Lowercase ASCII letters.
    text=$(printf '%s' "$text" | LC_ALL=C tr '[:upper:]' '[:lower:]')
    # Spaces -> hyphens, one-for-one (not collapsed).
    text=${text// /-}
    printf '%s' "$text"
}

# ---------------------------------------------------------------------------
# self_test: exercises github_slug against known-good GitHub anchor slugs,
# including headings with Unicode punctuation/symbols (→, —) that the C
# locale's [[:punct:]] strip used to leave in the slug (F-236). Prints PASS
# or a diff-style FAIL per case; returns non-zero if any case fails.
# Invoke via: hack/check-links.sh --self-test
# ---------------------------------------------------------------------------
self_test() {
    local failures=0
    local desc heading expected actual

    _assert_slug() {
        desc="$1" heading="$2" expected="$3"
        actual=$(github_slug "$heading")
        if [[ "$actual" == "$expected" ]]; then
            echo "PASS: $desc"
        else
            echo "FAIL: $desc"
            echo "  heading:  $heading"
            echo "  expected: $expected"
            echo "  actual:   $actual"
            failures=$((failures + 1))
        fi
    }

    # Regression: existing ASCII behavior (worked example in the header
    # comment) must not change.
    _assert_slug "ASCII punctuation (regression)" \
        "Pre-v1 Status & Limitations" "pre-v1-status--limitations"

    # Unicode arrow: only the arrow itself is dropped, surrounding spaces
    # are untouched (so two spaces -> two hyphens), matching GitHub.
    _assert_slug "Unicode arrow (→)" \
        "Config schema → wizard" "config-schema--wizard"

    # Unicode em dash, combined with an arrow later in the same heading.
    _assert_slug "Unicode em dash and arrow (—, →)" \
        "Signing key rotation — Ed25519 → ECDSA P-256 (2026-07)" \
        "signing-key-rotation--ed25519--ecdsa-p-256-2026-07"

    # Unicode letters (accented) are kept, not stripped as "punctuation".
    _assert_slug "Unicode letters kept" \
        "Café Déploiement" "café-déploiement"

    unset -f _assert_slug

    if [[ $failures -eq 0 ]]; then
        echo "self-test: all cases passed"
        return 0
    fi
    echo "self-test: $failures case(s) failed"
    return 1
}

# ---------------------------------------------------------------------------
# strip_heading_marker: given a raw ATX heading line ("## Title ##"), return
# just the heading text ("Title").
# ---------------------------------------------------------------------------
strip_heading_marker() {
    local line="$1"
    line=$(printf '%s' "$line" | sed -E 's/^[[:space:]]{0,3}#{1,6}[[:space:]]+//')
    line=$(printf '%s' "$line" | sed -E 's/[[:space:]]+#+[[:space:]]*$//')
    printf '%s' "$line"
}

# ---------------------------------------------------------------------------
# extract_html_anchor_ids: print (one per line) the id/name values of every
# explicit HTML anchor (`<a id="x">` / `<a name='x'>`, case-insensitive tag
# and attribute) found on the given line.
# ---------------------------------------------------------------------------
extract_html_anchor_ids() {
    local line="$1"
    printf '%s\n' "$line" | grep -oiE '<a[[:space:]]+(id|name)[[:space:]]*=[[:space:]]*"[^"]*"' \
        | sed -E 's/^<a[[:space:]]+[a-zA-Z]+[[:space:]]*=[[:space:]]*"//; s/"$//' || true
    printf '%s\n' "$line" | grep -oiE "<a[[:space:]]+(id|name)[[:space:]]*=[[:space:]]*'[^']*'" \
        | sed -E "s/^<a[[:space:]]+[a-zA-Z]+[[:space:]]*=[[:space:]]*'//; s/'\$//" || true
}

# ---------------------------------------------------------------------------
# build_anchor_set: print (one per line) every valid anchor slug/id for the
# given file — heading slugs (GitHub-flavored, de-duplicated in document
# order) plus explicit HTML `<a id>`/`<a name>` anchors. Fenced code blocks
# are skipped, same as the link scan below.
# ---------------------------------------------------------------------------
build_anchor_set() {
    local file="$1"
    local in_fence=false
    local line
    declare -A slug_seen=()
    while IFS= read -r line || [[ -n "$line" ]]; do
        if [[ "$line" =~ ^[[:space:]]{0,3}'```' ]]; then
            if $in_fence; then in_fence=false; else in_fence=true; fi
            continue
        fi
        if $in_fence; then
            continue
        fi

        if [[ "$line" =~ ^[[:space:]]{0,3}#{1,6}[[:space:]] ]]; then
            local heading_text base_slug
            heading_text=$(strip_heading_marker "$line")
            base_slug=$(github_slug "$heading_text")
            if [[ -z "${slug_seen[$base_slug]+x}" ]]; then
                slug_seen[$base_slug]=0
                printf '%s\n' "$base_slug"
            else
                slug_seen[$base_slug]=$(( slug_seen[$base_slug] + 1 ))
                printf '%s\n' "${base_slug}-${slug_seen[$base_slug]}"
            fi
        fi

        while IFS= read -r anchor_id; do
            [[ -n "$anchor_id" ]] && printf '%s\n' "$anchor_id"
        done < <(extract_html_anchor_ids "$line")
    done < "$file"
}

# ---------------------------------------------------------------------------
# has_anchor: true if $2 is in the (cached) anchor set of file $1.
# ---------------------------------------------------------------------------
has_anchor() {
    local file="$1" anchor="$2"
    if [[ -z "${anchor_cache[$file]+x}" ]]; then
        anchor_cache["$file"]=$'\n'"$(build_anchor_set "$file")"$'\n'
    fi
    local set="${anchor_cache[$file]}"
    case "$set" in
        *$'\n'"$anchor"$'\n'*) return 0 ;;
        *) return 1 ;;
    esac
}

# ---------------------------------------------------------------------------
# process_file: scan one audited file for `](target)` occurrences and
# validate each one (file existence, then anchor existence if present).
# ---------------------------------------------------------------------------
process_file() {
    local src="$1"
    local src_dir
    src_dir=$(dirname "$src")
    local in_fence=false
    local line_num=0
    local line

    while IFS= read -r line || [[ -n "$line" ]]; do
        line_num=$((line_num + 1))

        if [[ "$line" =~ ^[[:space:]]{0,3}'```' ]]; then
            if $in_fence; then in_fence=false; else in_fence=true; fi
            continue
        fi
        if $in_fence; then
            continue
        fi

        local match
        while IFS= read -r match; do
            [[ -z "$match" ]] && continue

            local target
            target=$(printf '%s' "$match" | sed -E 's/^\]\(//; s/\)$//')
            # Strip a trailing double-quoted title: (target "title")
            target=$(printf '%s' "$target" | sed -E 's/[[:space:]]+"[^"]*"[[:space:]]*$//')
            # Trim surrounding whitespace.
            target="${target#"${target%%[![:space:]]*}"}"
            target="${target%"${target##*[![:space:]]}"}"
            [[ -z "$target" ]] && continue

            case "$target" in
                http://* | https://* | mailto:*) continue ;;
            esac

            local path anchor
            if [[ "$target" == *"#"* ]]; then
                path="${target%%#*}"
                anchor="${target#*#}"
            else
                path="$target"
                anchor=""
            fi

            local resolved_raw resolved
            if [[ -z "$path" ]]; then
                resolved_raw="$src"
            elif [[ "$path" == /* ]]; then
                resolved_raw="${path#/}"
            else
                resolved_raw="$src_dir/$path"
            fi
            resolved=$(normalize_rel_path "$resolved_raw")

            if [[ ! -f "$resolved" ]]; then
                echo "✗ $src:$line_num: $target -> missing file $resolved"
                record_issue "$src"
                continue
            fi

            if [[ -n "$anchor" ]]; then
                if ! has_anchor "$resolved" "$anchor"; then
                    echo "✗ $src:$line_num: $target -> missing anchor #$anchor in $resolved"
                    record_issue "$src"
                fi
            fi
        done < <(printf '%s\n' "$line" | grep -oE '\]\([^)]*\)' || true)
    done < "$src"
}

# ---------------------------------------------------------------------------
# --self-test: run the github_slug unit cases (see self_test above) and exit,
# skipping the full audited-file link scan. Wired into nothing else — run it
# directly (`hack/check-links.sh --self-test`) or via hack/check-links_test.sh.
# ---------------------------------------------------------------------------
if [[ "${1:-}" == "--self-test" ]]; then
    self_test
    exit $?
fi

# ---------------------------------------------------------------------------
# Main: verify all 19 audited files exist, then link-check every one that
# does (a missing file is itself a failure, but does not stop the rest of
# the audit from running).
# ---------------------------------------------------------------------------
for file in "${audited_files[@]}"; do
    if [[ ! -f "$file" ]]; then
        echo "✗ $file: missing audited file"
        record_issue "$file"
        continue
    fi
    process_file "$file"
done

file_count=${#audited_files[@]}
if [[ $total_issues -eq 0 ]]; then
    echo "✓ Checked $file_count files: all internal links and anchors resolve"
    exit 0
else
    file_fail_count=${#files_with_issues[@]}
    echo "✗ $total_issues broken link(s) across $file_fail_count file(s)"
    exit 1
fi
