#!/usr/bin/env bash
# check-doc-versions.sh: Verify that all audited documentation files have version strings that match the current appVersion.
#
# Purpose:
#   Ensures compliance with SC-005 (Version String Audit Standard) from feature 012
#   (docs-refresh-and-outreach). Validates that every product documentation claim about
#   Gameplane version matches the current appVersion from charts/gameplane/Chart.yaml,
#   or is explicitly marked as an example version or historical reference.
#
# Audited Files (19):
#   README.md, docs/architecture.md, docs/contributing.md, docs/dependencies.md,
#   docs/game-coverage.md, docs/install.md, docs/key-rotation.md,
#   docs/module-authoring.md, docs/networking.md, docs/notifications.md,
#   docs/oidc.md, docs/roadmap.md, docs/security.md, docs/tunnels.md,
#   audit-syslog-bridge/README.md, mcp-server/README.md,
#   telemetry-receiver/README.md, docs/comparison-sources.md,
#   FEATURES.md
#
# Allowlist Rules (OD-1, OD-11, OD-14):
#   A version string v?0\.[0-9]+\.[0-9]+(-(beta|rc)\.[0-9]+)? passes if:
#   1. It matches the current appVersion (with or without leading 'v'), OR
#   2. The line contains "(example version)", "(example)", or "(placeholder)", OR
#   3. The line or ±2 lines contain "# Example:" or "<!-- Example -->", OR
#   4. The line contains "<!-- doc-versions: historical -->" (OD-14), OR
#   5. The line contains "<!-- doc-versions: dependency -->" (OD-11): the version belongs to a dependency, not to Gameplane.
#
#   Both bare v?0.X.Y and -(beta|rc).N pre-release versions are now matched (OD-011).
#   Note: A candidate preceded by a digit, letter, or '.', or followed by a fourth numeric
#   component (IP addresses such as 10.0.0.1 or 0.0.0.0), is not a version and is ignored.
#
# Test overrides:
#   DOC_VERSIONS_CHART: path to test Chart.yaml (default: charts/gameplane/Chart.yaml)
#   DOC_VERSIONS_FILES: space-separated list of files to audit (default: 19 files below)
#
# Exit codes:
#   0 = all version strings match current appVersion or are allowlisted
#   1 = one or more stale version strings found, or audited files missing

set -euo pipefail

# Navigate to repo root (one level up from script's directory)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$REPO_ROOT"

# Test overrides for Chart.yaml path and audited files list
chart_file="${DOC_VERSIONS_CHART:-charts/gameplane/Chart.yaml}"

# Check that Chart.yaml exists and is readable
if [[ ! -f "$chart_file" ]] || [[ ! -r "$chart_file" ]]; then
    echo "✗ Error: $chart_file not found or unreadable"
    exit 1
fi

# Extract appVersion from Chart.yaml (line 6: appVersion: "X.Y.Z" or "X.Y.Z-(beta|rc).N")
# The regex extracts the quoted or unquoted version string
current_version=$(sed -n '6p' "$chart_file" | sed 's/^appVersion:[[:space:]]*"\?//;s/"[[:space:]]*$//')

if [[ -z "$current_version" ]]; then
    echo "✗ Error: Could not extract appVersion from $chart_file:6"
    exit 1
fi

# Normalize: appVersion in chart is unquoted (e.g. 0.2.0-beta.8 or 0.3.0)
# We'll accept matches with or without leading 'v'
current_version_pattern="(v)?${current_version//./\\.}"

# List of 19 audited files
if [[ -n "${DOC_VERSIONS_FILES:-}" ]]; then
    read -r -a audited_files <<< "$DOC_VERSIONS_FILES"
else
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
fi

# Validate files and scan version strings in a single pass: a missing audited
# file is itself a failure, but does not stop the rest of the audit from
# running (mirrors hack/check-links.sh's main loop).
failed_files=()
version_errors=()

for file in "${audited_files[@]}"; do
    if [[ ! -f "$file" ]]; then
        echo "✗ $file: missing audited file"
        failed_files+=("$file")
        continue
    fi

    if [[ ! -r "$file" ]]; then
        continue
    fi

    # Grep for version pattern v?0\.[0-9]+\.[0-9]+(-(beta|rc)\.[0-9]+)? and bare versions
    # -n includes line numbers; extract with boundary check and filter out 4-part numbers (IPs)
    while IFS= read -r line_data; do
        # Parse line_data format: "line_number:content"
        line_num="${line_data%%:*}"
        line_content="${line_data#*:}"

        # Extract all version matches on this line: bare v?0.X.Y or -(beta|rc).N pre-release
        # grep captures a boundary char and possibly a trailing 4th component; sed drops the
        # boundary char, and grep -v drops any 4-part numbers (like IPs: 10.0.0.1)
        version_matches=$(echo "$line_content" \
            | grep -oE '(^|[^0-9A-Za-z.])v?0\.[0-9]+\.[0-9]+(-(beta|rc)\.[0-9]+)?(\.[0-9]+)?' \
            | sed -E 's/^[^v0]//' \
            | grep -vE '^v?0\.[0-9]+\.[0-9]+\.[0-9]+' || true)

        if [[ -z "$version_matches" ]]; then
            continue
        fi

        # For each match on this line
        while IFS= read -r match; do
            [[ -z "$match" ]] && continue

            # Check if match equals current version (with or without leading 'v')
            match_normalized="${match#v}"
            current_normalized="${current_version#v}"

            if [[ "$match_normalized" == "$current_normalized" ]]; then
                # Match is current version; pass
                continue
            fi

            # Match is not current version; check for allowlist markers
            is_allowlisted=0

            # Check current line for allowlist markers
            if echo "$line_content" | grep -q '(example version)\|(example)\|(placeholder)\|# Example:\|<!-- Example -->\|<!-- doc-versions: historical -->\|<!-- doc-versions: dependency -->'; then
                is_allowlisted=1
            fi

            # Check ±2 lines for example markers (not historical marker, which is line-specific)
            if [[ $is_allowlisted -eq 0 ]]; then
                # Check lines above and below
                for offset in -2 -1 1 2; do
                    check_line=$((line_num + offset))
                    if [[ $check_line -gt 0 ]]; then
                        # Use sed to get the Nth line
                        context_line=$(sed -n "${check_line}p" "$file" 2>/dev/null || true)
                        if echo "$context_line" | grep -q '# Example:\|<!-- Example -->'; then
                            is_allowlisted=1
                            break
                        fi
                    fi
                done
            fi

            # If not allowlisted, record as error
            if [[ $is_allowlisted -eq 0 ]]; then
                version_errors+=("$file:$line_num: $match (current appVersion is $current_version)")
            fi
        done <<< "$version_matches"
    done < <(grep -nE '(^|[^0-9A-Za-z.])v?0\.[0-9]+\.[0-9]+' "$file" || true)
done

# Report results. Missing-file failures were already echoed as they were
# found above; version-string failures are echoed here. Both are decided
# together, only after every audited file has had a chance to be scanned.
file_count=${#audited_files[@]}
if [[ ${#failed_files[@]} -eq 0 && ${#version_errors[@]} -eq 0 ]]; then
    echo "✓ Checked $file_count files: all Gameplane version strings match appVersion $current_version or are allowlisted"
    exit 0
else
    # Report each version-string error
    for error in "${version_errors[@]}"; do
        echo "✗ $error"
    done

    missing_count=${#failed_files[@]}
    error_count=${#version_errors[@]}

    if [[ $missing_count -gt 0 ]]; then
        if [[ $missing_count -eq 1 ]]; then
            echo "✗ 1 audited file is missing"
        else
            echo "✗ $missing_count audited files are missing"
        fi
    fi

    if [[ $error_count -gt 0 ]]; then
        # Count unique files with version-string errors
        unique_files=$(printf '%s\n' "${version_errors[@]}" | cut -d: -f1 | sort -u | wc -l)

        if [[ $error_count -eq 1 ]]; then
            echo "✗ 1 stale version string across 1 file"
        else
            echo "✗ $error_count stale version string(s) across $unique_files file(s)"
        fi
    fi

    exit 1
fi
