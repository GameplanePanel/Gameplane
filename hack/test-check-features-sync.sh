#!/usr/bin/env bash
# test-check-features-sync.sh: Fixture tests for the features sync checker (spec 024 FR-015).
#
# Runs hack/check-features-sync.sh against each fixture under hack/testdata/check-features-sync/:
#   pass/*/  must exit 0.
#   fail/*/  must exit non-zero, and its combined stdout+stderr must equal expected.txt.
#
# Paths are passed relative to the repo root, so messages carry the paths exactly as
# written in each expected.txt.
#
# See: specs/done_024-docs-feature-list-refresh/contracts/features-sync-check.md (FR-015, SC-005)

set -euo pipefail

# Navigate to repo root (one level up from script's directory)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$REPO_ROOT"

CHECK="hack/check-features-sync.sh"
FIX="hack/testdata/check-features-sync"

passed=0
failed=0

# Test pass/* fixtures
for dir in "$FIX"/pass/*/; do
    [[ ! -d "$dir" ]] && continue
    dir="${dir%/}"
    name="$(basename "$dir")"

    if out=$(FEATURES_MD="$dir/FEATURES.md" FEATURES_MDX="$dir/features.mdx" "$CHECK" 2>&1); then
        echo "✓ pass: $name"
        passed=$((passed+1))
    else
        echo "✗ expected pass: pass/$name"
        echo "$out"
        failed=$((failed+1))
    fi
done

# Test fail/* fixtures
for dir in "$FIX"/fail/*/; do
    [[ ! -d "$dir" ]] && continue
    dir="${dir%/}"
    name="$(basename "$dir")"
    expected="$(cat "$dir/expected.txt")"

    rc=0
    out=$(FEATURES_MD="$dir/FEATURES.md" FEATURES_MDX="$dir/features.mdx" "$CHECK" 2>&1) || rc=$?

    if [[ $rc -eq 0 ]]; then
        echo "✗ expected failure: fail/$name exited 0"
        failed=$((failed+1))
    elif [[ "$out" == "$expected" ]]; then
        echo "✓ fail: $name"
        passed=$((passed+1))
    else
        echo "✗ fail/$name output mismatch"
        echo "Expected:"
        echo "$expected"
        echo "Got:"
        echo "$out"
        failed=$((failed+1))
    fi
done

# Report results
total=$((passed + failed))
if [[ $failed -eq 0 ]]; then
    echo "✓ $total fixtures behaved as expected"
    exit 0
fi
echo "✗ $failed fixture(s) misbehaved"
exit 1
