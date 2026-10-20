#!/usr/bin/env bash
# Proves the "vuln (go)" gate fails on a reachable vulnerable symbol (spec 013, G1).
#
# Fixtures under hack/testdata/govulncheck/ have no replace directive: govulncheck
# ignores modules whose version is "", so the proof vendors the fixture module
# into a temp copy of each case and gives it a real v1.0.0 version. Nothing is
# fetched (GOPROXY=off) and vendor/ is never committed (it is gitignored).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

FIXTURES="$PWD/hack/testdata/govulncheck"
DB="file://$FIXTURES/db"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

export GOWORK=off GOFLAGS=-mod=vendor GOPROXY=off

stage() {
  local case="$1"
  cp -R "$FIXTURES/$case" "$tmp/$case"
  mkdir -p "$tmp/$case/vendor/example.invalid/vulnfixture"
  cp "$FIXTURES/vulnfixture/"*.go "$tmp/$case/vendor/example.invalid/vulnfixture/"
  cat > "$tmp/$case/vendor/modules.txt" <<'EOF'
# example.invalid/vulnfixture v1.0.0
## explicit; go 1.22
example.invalid/vulnfixture
EOF
}

fail=0

run() {
  local case="$1" want="$2" rc
  set +e
  (cd "$tmp/$case" && govulncheck -db "$DB" ./...)
  rc=$?
  set -e
  if [ "$rc" -eq "$want" ]; then
    echo "✓ $case: govulncheck exit $rc (want $want)"
  else
    echo "✗ $case: govulncheck exit $rc (want $want)"
    fail=1
  fi
}

for case in fail pass; do
  stage "$case"
done

run fail 3
run pass 0

exit "$fail"
