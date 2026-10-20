#!/usr/bin/env bash
# check-coverage-gaps_test.sh: Proof for hack/check-coverage-gaps.sh (spec 013,
# contracts/coverage-gap-record.md).
#
# Runs the validator against hack/testdata/coverage-gaps/:
#   pass/*.md must exit 0; fail/*.md must exit non-zero.
# Fails if either glob matches nothing. If the real coverage-gaps.md exists,
# it is validated too.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

VALIDATOR="$SCRIPT_DIR/check-coverage-gaps.sh"
FIXTURES="$SCRIPT_DIR/testdata/coverage-gaps"
REAL="specs/done_013-expand-test-coverage/coverage-gaps.md"

shopt -s nullglob
pass_files=("$FIXTURES"/pass/*.md)
fail_files=("$FIXTURES"/fail/*.md)
shopt -u nullglob

if [[ ${#pass_files[@]} -eq 0 ]]; then
  echo "✗ no pass fixtures found under $FIXTURES/pass" >&2
  exit 1
fi
if [[ ${#fail_files[@]} -eq 0 ]]; then
  echo "✗ no fail fixtures found under $FIXTURES/fail" >&2
  exit 1
fi

total=0
ok=0

# run_case FILE EXPECT(0|nonzero) LABEL
run_case() {
  local file="$1" expect="$2" label="$3" rc=0 out=""
  total=$((total + 1))
  out=$(COVERAGE_GAPS_FILE="$file" "$VALIDATOR" 2>&1) || rc=$?
  if [[ "$expect" == "0" && $rc -eq 0 ]] || [[ "$expect" == "nonzero" && $rc -ne 0 ]]; then
    ok=$((ok + 1))
    echo "✓ $label (exit $rc)"
  else
    echo "✗ $label (exit $rc, expected $expect)"
    [[ -n "$out" ]] && printf '%s\n' "$out" | sed 's/^/    /'
  fi
}

for f in "${pass_files[@]}"; do
  run_case "$f" 0 "pass/$(basename "$f")"
done
for f in "${fail_files[@]}"; do
  run_case "$f" nonzero "fail/$(basename "$f")"
done
if [[ -f "$REAL" ]]; then
  run_case "$REAL" 0 "$REAL"
fi

echo "$ok/$total cases as expected"
if [[ $ok -ne $total ]]; then
  exit 1
fi
