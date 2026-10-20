#!/usr/bin/env bash
# check-image-scan_test.sh: Proof for hack/trivy-scan-images.sh (spec 013 G2,
# contracts/static-gates.md).
#
#   1. An end-of-life alpine pinned by digest must FAIL. alpine:3.17.0
#      (November 2022) ships libssl3/libcrypto3 3.0.7-r0, which has fixed HIGH
#      CVEs CVE-2023-0286, CVE-2022-4450 and CVE-2023-0215. The scan output
#      must name CVE-2023-0286, so a failure for any other reason (a pull
#      error, a DB download error) does not count as proof.
#   2. gameplane-test/telemetry-receiver:e2e must PASS.
#
# Env: CLEAN_IMAGE - override the image expected to pass.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCAN="$SCRIPT_DIR/trivy-scan-images.sh"

# alpine:3.17.0 image index digest.
EOL_IMAGE="alpine@sha256:8914eb54f968791faf6a8638949e480fef81e697984fba772b3976835194c6d4"
EXPECTED_CVE="CVE-2023-0286"
CLEAN_IMAGE="${CLEAN_IMAGE:-gameplane-test/telemetry-receiver:e2e}"

fail=0

rc=0
out=$("$SCAN" "$EOL_IMAGE" 2>&1) || rc=$?
if [[ $rc -eq 0 ]]; then
  echo "✗ ${EOL_IMAGE} accepted; expected the gate to reject it"
  fail=1
elif ! grep -q "$EXPECTED_CVE" <<<"$out"; then
  echo "✗ ${EOL_IMAGE} rejected (exit $rc) but ${EXPECTED_CVE} was not reported"
  printf '%s\n' "$out" | tail -n 40 | sed 's/^/    /'
  fail=1
else
  echo "✓ ${EOL_IMAGE} rejected (exit $rc, ${EXPECTED_CVE} reported)"
fi

rc=0
out=$("$SCAN" "$CLEAN_IMAGE" 2>&1) || rc=$?
if [[ $rc -ne 0 ]]; then
  echo "✗ ${CLEAN_IMAGE} rejected (exit $rc); expected it to pass"
  printf '%s\n' "$out" | tail -n 60 | sed 's/^/    /'
  fail=1
else
  echo "✓ ${CLEAN_IMAGE} accepted"
fi

exit "$fail"
