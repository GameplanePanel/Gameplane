#!/usr/bin/env bash
# Integration test for the lint-gate-verify verifier.
#
# This script runs the verifier against all fixtures and asserts:
# 1. Each fixture exits with code 1 (hard failure)
# 2. The error message matches the expected pattern for that check
# 3. The verifier exits 0 against the real repository root
#
# Exit codes:
#   0: All tests passed
#   1: One or more tests failed
#
# Usage:
#   test/e2e/lint-gate-verify_test.sh

set -uo pipefail
# Deliberately NOT `set -e`: this is a test harness that must keep running
# after a fixture fails, so it can report every failure in one pass.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
FIXTURES_DIR="$SCRIPT_DIR/testdata/lint-gate"
VERIFIER="$SCRIPT_DIR/lint-gate-verify.sh"

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test counter
TESTS_PASSED=0
TESTS_FAILED=0

# Helper to run a test
run_test() {
	local fixture="$1"
	local rule="$2"
	local expected_pattern="$3"

	local fixture_path="$FIXTURES_DIR/$fixture"

	if [ ! -d "$fixture_path" ]; then
		echo -e "${RED}✗${NC} Fixture not found: $fixture"
		TESTS_FAILED=$((TESTS_FAILED + 1))
		return 1
	fi

	# CodeQL default setup auto-detects any go.work as a Go workspace and cannot
	# be given a paths-ignore. Fixtures are stored as go.work.fixture and staged
	# in a temp dir at test time, renamed to go.work for verifier execution.
	local tmp
	tmp=$(mktemp -d) || {
		echo -e "${RED}✗${NC} $fixture ($rule): Failed to create temp dir"
		TESTS_FAILED=$((TESTS_FAILED + 1))
		return 1
	}
	trap "rm -rf '$tmp'" RETURN

	cp -R "$fixture_path"/. "$tmp"/ || {
		echo -e "${RED}✗${NC} $fixture ($rule): Failed to copy fixture"
		TESTS_FAILED=$((TESTS_FAILED + 1))
		return 1
	}
	[ -f "$tmp/go.work.fixture" ] && mv "$tmp/go.work.fixture" "$tmp/go.work"

	# Run the verifier against the staged fixture
	local output
	local exit_code=0
	output=$("$VERIFIER" verify --root "$tmp" 2>&1) || exit_code=$?

	# Check exit code is 1
	if [ "$exit_code" -ne 1 ]; then
		echo -e "${RED}✗${NC} $fixture ($rule): Expected exit code 1, got $exit_code"
		echo "  Output: $output"
		TESTS_FAILED=$((TESTS_FAILED + 1))
		return 1
	fi

	# Check error message contains expected pattern
	if ! printf '%s\n' "$output" | grep -q "$expected_pattern"; then
		echo -e "${RED}✗${NC} $fixture ($rule): Expected pattern not found"
		echo "  Expected pattern: $expected_pattern"
		echo "  Got output: $output"
		TESTS_FAILED=$((TESTS_FAILED + 1))
		return 1
	fi

	echo -e "${GREEN}✓${NC} $fixture ($rule)"
	TESTS_PASSED=$((TESTS_PASSED + 1))
	return 0
}

# Helper to run a test that expects the verifier to SUCCEED (exit 0) against
# a fixture. run_test above only ever asserts a hard failure — this is the
# positive-path counterpart, needed to prove a valid config (e.g. the OR'd
# `if:` condition shape) is accepted, not merely that some invalid shape is
# rejected.
run_pass_test() {
	local fixture="$1"
	local description="$2"

	local fixture_path="$FIXTURES_DIR/$fixture"

	if [ ! -d "$fixture_path" ]; then
		echo -e "${RED}✗${NC} Fixture not found: $fixture"
		TESTS_FAILED=$((TESTS_FAILED + 1))
		return 1
	fi

	# CodeQL default setup auto-detects any go.work as a Go workspace and cannot
	# be given a paths-ignore. Fixtures are stored as go.work.fixture and staged
	# in a temp dir at test time, renamed to go.work for verifier execution.
	local tmp
	tmp=$(mktemp -d) || {
		echo -e "${RED}✗${NC} $fixture ($description): Failed to create temp dir"
		TESTS_FAILED=$((TESTS_FAILED + 1))
		return 1
	}
	trap "rm -rf '$tmp'" RETURN

	cp -R "$fixture_path"/. "$tmp"/ || {
		echo -e "${RED}✗${NC} $fixture ($description): Failed to copy fixture"
		TESTS_FAILED=$((TESTS_FAILED + 1))
		return 1
	}
	[ -f "$tmp/go.work.fixture" ] && mv "$tmp/go.work.fixture" "$tmp/go.work"

	# Run the verifier against the staged fixture
	local output
	local exit_code=0
	output=$("$VERIFIER" verify --root "$tmp" 2>&1) || exit_code=$?

	# Check exit code is 0
	if [ "$exit_code" -ne 0 ]; then
		echo -e "${RED}✗${NC} $fixture ($description): Expected exit code 0, got $exit_code"
		echo "  Output: $output"
		TESTS_FAILED=$((TESTS_FAILED + 1))
		return 1
	fi

	echo -e "${GREEN}✓${NC} $fixture ($description)"
	TESTS_PASSED=$((TESTS_PASSED + 1))
	return 0
}

# Main test suite
echo "Running lint-gate-verify integration tests..."
echo ""

# R-001: Missing module
run_test "case-missing-module" "R-001" \
	"missing from lint job matrix"

# R-001: Extra module
run_test "case-extra-module" "R-001" \
	"not in go.work"

# R-001: Duplicate module
run_test "case-duplicate-module" "R-001" \
	"Duplicate module"

# R-004: continue-on-error: true
run_test "case-continue-on-error" "R-004" \
	"continue-on-error: true"

# R-004: || true in lint steps
run_test "case-pipe-true" "R-004" \
	"|| true"

# R-006: Deferral comment (in a step name)
run_test "case-deferral-comment" "R-006" \
	"deferral comment"

# R-006: Deferral comment (a pure "#"-comment line, not inside a step name —
# this is the shape R-006 actually targets: a commented-out/deferred matrix
# entry, not a name that happens to contain the word).
run_test "case-deferral-comment-only" "R-006" \
	"deferral comment"

# R-002: Missing build tag for operator
run_test "case-missing-operator-tag" "R-002" \
	"operator.*missing conditional step"

# R-002: Missing build tag for api
run_test "case-missing-api-tag" "R-002" \
	"api.*missing conditional step"

# R-002: Missing build tag for test/e2e
run_test "case-missing-e2e-tag" "R-002" \
	"test/e2e.*missing conditional step"

# R-008: Missing working-directory
run_test "case-missing-working-directory" "R-008" \
	"missing working-directory"

# R-009: Unpinned version
run_test "case-unpinned-version" "R-009" \
	"not pinned"

# R-009: One fixed but incompatible version must fail even when other
# steps use the required version.
run_test "case-mismatched-version" "R-009" \
	"R-009:.*found: v2[.]13[.]2"

# R-003: Missing fail-fast: false (strategy omits it entirely)
run_test "case-missing-fail-fast" "R-003" \
	"missing 'fail-fast: false'"

# R-010: No root .golangci.yml
run_test "case-no-golangci-yml" "R-010" \
	"no .golangci.yml found"

# Combined OR'd if: condition — matrix.module == 'operator' || matrix.module
# == 'api' — the exact shape the real ci.yaml uses and that broke earlier
# line_has_module_equals implementations. This is the positive path: the
# fixture is a fully valid config, so the verifier must exit 0 against it,
# proving the OR-parsing path is accepted rather than just proving some
# other shape is rejected.
run_pass_test "case-combined-condition" \
	"OR'd if: condition parses and verifier passes"

# Bonus: Verify the verifier passes against the real repo root
echo ""
echo "Verifying verifier passes against real repository root..."
if "$VERIFIER" verify 2>&1 >/dev/null; then
	echo -e "${GREEN}✓${NC} Verifier passes on real repo root"
	TESTS_PASSED=$((TESTS_PASSED + 1))
else
	echo -e "${RED}✗${NC} Verifier failed on real repo root (should pass)"
	echo "  Run: $VERIFIER verify"
	TESTS_FAILED=$((TESTS_FAILED + 1))
fi

# Summary
echo ""
echo "====================================="
echo "Tests passed: $TESTS_PASSED"
echo "Tests failed: $TESTS_FAILED"
echo "====================================="

if [ "$TESTS_FAILED" -ne 0 ]; then
	exit 1
fi

exit 0
