#!/usr/bin/env bash
# trivy-scan-images.sh: Spec 013 G2 (contracts/static-gates.md). Scans each
# image argument with Trivy and fails if any image has a HIGH or CRITICAL
# vulnerability that has a fix. Every image is scanned before the exit status
# is decided, so one run lists every finding.
#
# Fix findings with a base-image digest bump or a Go/npm dependency bump in
# the image's module, never an ignore file (FR-008).
#
# Usage: hack/trivy-scan-images.sh IMAGE [IMAGE...]
# Env:   TRIVY - Trivy binary to run (default: trivy)
#        TRIVY_PLATFORMS - comma-separated os/arch list (e.g.
#          linux/amd64,linux/arm64); each image is scanned once per platform.
#          Unset scans Trivy's default platform only, which for a multi-arch
#          index is linux/amd64.
#
# Exit codes: 0 = every image clean; 1 = at least one image failed;
#             2 = no image given
set -euo pipefail

if [[ $# -eq 0 ]]; then
  echo "usage: $0 IMAGE [IMAGE...]" >&2
  exit 2
fi

trivy_bin="${TRIVY:-trivy}"
platforms=("")
if [[ -n "${TRIVY_PLATFORMS:-}" ]]; then
  IFS=',' read -r -a platforms <<<"$TRIVY_PLATFORMS"
fi
failed=()
scans=0

for img in "$@"; do
  for platform in "${platforms[@]}"; do
    label="$img"
    platform_args=()
    if [[ -n "$platform" ]]; then
      label="${img} (${platform})"
      platform_args=(--platform "$platform")
    fi
    scans=$((scans + 1))
    echo "::group::trivy ${label}"
    if "$trivy_bin" image "${platform_args[@]}" --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 --no-progress "$img"; then
      echo "✓ ${label}"
    else
      failed+=("$label")
    fi
    echo "::endgroup::"
  done
done

if [[ ${#failed[@]} -gt 0 ]]; then
  printf '✗ fixed HIGH/CRITICAL vulnerabilities (or a scan error) in %d of %d scan(s):\n' "${#failed[@]}" "$scans" >&2
  printf '  %s\n' "${failed[@]}" >&2
  exit 1
fi
echo "✓ ${scans} scan(s) clean"
