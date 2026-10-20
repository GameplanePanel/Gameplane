#!/usr/bin/env bash
# govulncheck-all.sh - runs govulncheck over every Go module listed in go.work.
#
# Spec 013 G1. Used by the "vuln (go)" CI job and .github/workflows/vuln-weekly.yaml.
#
# Module tags:
#   operator, api  -> -tags=envtest   (api is also scanned once more with -tags=postgres)
#   test/e2e       -> -tags=e2e
#   others         -> no tags
#
# Exit codes from govulncheck: 3 = reachable vulnerabilities found, other
# non-zero = tool/build error. Every scan runs; failures are summarised at the end.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

if ! command -v govulncheck >/dev/null 2>&1; then
  echo "error: govulncheck not found in PATH" >&2
  exit 1
fi

if [[ ! -f go.work ]]; then
  echo "error: go.work not found at repo root" >&2
  exit 1
fi

# Module directories: the lines inside the `use ( ... )` block, e.g. "\t./api".
mapfile -t MODULES < <(awk '
  /^use[[:space:]]*\(/ { inuse = 1; next }
  inuse && /^\)/        { inuse = 0; next }
  inuse {
    line = $0
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
    if (line == "" || line ~ /^\/\//) next
    sub(/^\.\//, "", line)
    print line
  }
' go.work)

if [[ ${#MODULES[@]} -eq 0 ]]; then
  echo "error: no modules found in go.work use block" >&2
  exit 1
fi

# Build the scan list as "dir|tagflag" entries (tagflag may be empty).
SCANS=()
for dir in "${MODULES[@]}"; do
  case "$dir" in
    operator|api) tagflag="-tags=envtest" ;;
    test/e2e)     tagflag="-tags=e2e" ;;
    *)            tagflag="" ;;
  esac
  SCANS+=("$dir|$tagflag")
  if [[ "$dir" == "api" ]]; then
    SCANS+=("api|-tags=postgres")
  fi
done

FAILED=()
total=0
for entry in "${SCANS[@]}"; do
  dir="${entry%%|*}"
  tagflag="${entry#*|}"
  total=$((total + 1))

  echo "== govulncheck $dir ${tagflag:-no tags}"

  set +e
  (cd "$dir" && govulncheck ${tagflag:+"$tagflag"} ./...)
  rc=$?
  set -e

  if [[ $rc -ne 0 ]]; then
    if [[ $rc -eq 3 ]]; then
      reason="reachable vulnerabilities"
    else
      reason="govulncheck error"
    fi
    FAILED+=("$dir ${tagflag:-no tags} (rc=$rc: $reason)")
  fi
done

if [[ ${#FAILED[@]} -gt 0 ]]; then
  echo ""
  echo "govulncheck non-zero in ${#FAILED[@]} of $total scans:"
  for f in "${FAILED[@]}"; do
    echo "  - $f"
  done
  exit 1
fi

echo "✓ no reachable vulnerabilities in $total scans"
exit 0
