#!/usr/bin/env bash
# Proves the Dockerfile lint gate can fail (and passes clean input), spec 013 G3.
# The fail fixture must make hadolint exit non-zero; the pass fixture must exit 0.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

HADOLINT="${HADOLINT:-hadolint}"
status=0

echo "== hadolint fail fixture (must exit non-zero)"
set +e
fail_out="$("$HADOLINT" --config .hadolint.yaml hack/testdata/hadolint/fail/Dockerfile 2>&1)"
fail_rc=$?
set -e
printf '%s\n' "$fail_out"
if [ "$fail_rc" -ne 0 ]; then
  echo "✓ fail fixture rejected (exit $fail_rc)"
else
  echo "✗ fail fixture was accepted (exit 0); gate cannot fail"
  status=1
fi

echo "== hadolint pass fixture (must exit 0)"
set +e
pass_out="$("$HADOLINT" --config .hadolint.yaml hack/testdata/hadolint/pass/Dockerfile 2>&1)"
pass_rc=$?
set -e
printf '%s\n' "$pass_out"
if [ "$pass_rc" -eq 0 ]; then
  echo "✓ pass fixture accepted"
else
  echo "✗ pass fixture rejected (exit $pass_rc)"
  status=1
fi

exit "$status"
