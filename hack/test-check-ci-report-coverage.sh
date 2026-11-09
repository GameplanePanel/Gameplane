#!/usr/bin/env bash
# test-check-ci-report-coverage.sh: Fixture tests for hack/check-ci-report-coverage.sh (F-238).
#
# Runs pass.yaml (needs: fully covered by NEEDS_ORDER + JOB_MATCHERS) and the
# fail-*.yaml fixtures (a needs: job missing from one or both structures)
# through the checker, and against the real .github/workflows/ci.yaml. Also
# exercises that workflow's actual executed-bucket collector with synthetic jobs.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$REPO_ROOT"

FIX="hack/testdata/check-ci-report-coverage"
passed=0
failed=0

check() {
	local desc="$1" workflow="$2" want_status="$3"
	local status=0
	CI_REPORT_WORKFLOW="$workflow" hack/check-ci-report-coverage.sh >/tmp/check-ci-report-coverage.out 2>&1 || status=$?
	if [[ "$status" -eq "$want_status" ]]; then
		echo "✓ $desc"
		passed=$((passed + 1))
	else
		echo "✗ $desc (expected exit $want_status, got $status)"
		cat /tmp/check-ci-report-coverage.out
		failed=$((failed + 1))
	fi
}

check "pass fixture accepted" "$FIX/pass.yaml" 0
check "fixture missing from NEEDS_ORDER and JOB_MATCHERS rejected" "$FIX/fail-missing-both.yaml" 1
check "fixture missing only from JOB_MATCHERS rejected" "$FIX/fail-missing-matcher.yaml" 1
check "real ci.yaml is currently covered" ".github/workflows/ci.yaml" 0

if node <<'NODE'
const assert = require('node:assert/strict');
const fs = require('node:fs');
const workflow = fs.readFileSync('.github/workflows/ci.yaml', 'utf8');
const startMarker = 'const bucketSet = new Set();';
const endMarker = 'const buckets = [...bucketSet].sort();';
const start = workflow.indexOf(startMarker);
const end = workflow.indexOf(endMarker, start);
assert.ok(start !== -1 && end > start, 'workflow bucket collector must exist');
const collect = new Function('apiJobs', 'needs',
  workflow.slice(start, end + endMarker.length) + '\nreturn buckets;');
const job = (bucket, steps) => ({ name: `e2e ${bucket} / amd64 (parallel 1)`, steps });
for (const bucket of ['wake-hostport', 'recovery']) {
  const stepName = `run go e2e (${bucket})`;
  const cases = [
    ['success', [{ name: stepName, conclusion: 'success' }], [bucket]],
    ['failure', [{ name: stepName, conclusion: 'failure' }], [bucket]],
    ['skipped', [{ name: stepName, conclusion: 'skipped' }], []],
    ['cancelled', [{ name: stepName, conclusion: 'cancelled' }], []],
    ['missing steps', undefined, []],
    ['missing step', [], []],
    ['wrong step', [{ name: 'run go e2e (operator)', conclusion: 'success' }], []],
  ];
  for (const [label, steps, expected] of cases) {
    assert.deepEqual(collect([job(bucket, steps)], {}), expected, `${bucket}: ${label}`);
  }
}
assert.deepEqual(collect([
  job('wake-hostport', [{ name: 'run go e2e (wake-hostport)', conclusion: 'success' }]),
  job('wake-hostport', [{ name: 'run go e2e (wake-hostport)', conclusion: 'failure' }]),
  job('recovery', [{ name: 'run go e2e (recovery)', conclusion: 'success' }]),
  job('recovery', [{ name: 'run go e2e (recovery)', conclusion: 'failure' }]),
  job('operator'),
  job('api-auth', [{ name: 'run go e2e (api-auth)', conclusion: 'skipped' }]),
], {}), ['api-auth', 'operator', 'recovery', 'wake-hostport'],
'collector deduplicates completed drills and preserves ordinary bucket reporting');
NODE
then
	echo "✓ executed-bucket collector reports completed drills only"
	passed=$((passed + 1))
else
	echo "✗ executed-bucket collector reported unexpected buckets"
	failed=$((failed + 1))
fi

total=$((passed + failed))
if [[ $failed -eq 0 ]]; then
	echo "✓ $total fixtures behaved as expected"
	exit 0
else
	echo "✗ $failed fixture(s) misbehaved"
	exit 1
fi
