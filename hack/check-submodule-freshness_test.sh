#!/usr/bin/env bash
# check-submodule-freshness_test.sh: Proof for hack/check-submodule-freshness.sh
# (spec 013 T035 / G6 / OD-1). Builds an upstream repo and a superproject in a
# temp dir, registers gitlinks without cloning, and checks three cases:
#   - both pointers on upstream main tip        -> pass
#   - one pointer behind main tip (ancestor)     -> pass
#   - pointer on an unmerged side branch         -> fail
#
# Usage: hack/check-submodule-freshness_test.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
CHECK="${REPO_ROOT}/hack/check-submodule-freshness.sh"

WORK="$(mktemp -d)"
cleanup() {
  rm -rf "${WORK}"
}
trap cleanup EXIT

GIT=(git -c user.name=t -c user.email=t@invalid)

up="${WORK}/up"
sup="${WORK}/sup"

# Upstream: main with c1 -> c2; side branch s1 forked from c1, never merged.
git init -q -b main "${up}"
echo one >"${up}/f"
"${GIT[@]}" -C "${up}" add f
"${GIT[@]}" -C "${up}" commit -q --no-gpg-sign -m c1
c1="$(git -C "${up}" rev-parse HEAD)"
echo two >>"${up}/f"
"${GIT[@]}" -C "${up}" commit -q -am c2 --no-gpg-sign
c2="$(git -C "${up}" rev-parse HEAD)"
git -C "${up}" checkout -q -b side "${c1}"
echo side >"${up}/g"
"${GIT[@]}" -C "${up}" add g
"${GIT[@]}" -C "${up}" commit -q --no-gpg-sign -m s1
s1="$(git -C "${up}" rev-parse HEAD)"
git -C "${up}" checkout -q main

# Superproject: gitlinks registered without cloning the submodules.
git init -q -b main "${sup}"
set_pointers() {
  local mod_sha="$1" web_sha="$2"
  git -C "${sup}" update-index --add --cacheinfo "160000,${mod_sha},modules"
  git -C "${sup}" update-index --add --cacheinfo "160000,${web_sha},website"
  "${GIT[@]}" -C "${sup}" commit -q --no-gpg-sign -m "pointers"
}

failed=0

run_check() {
  local expect="$1" label="$2" rc
  set +e
  SUBMODULE_FRESHNESS_REPO="${sup}" \
    SUBMODULE_URL_MODULES="${up}" \
    SUBMODULE_URL_WEBSITE="${up}" \
    "${CHECK}" >"${WORK}/out.txt" 2>&1
  rc=$?
  set -e
  local got="fail"
  if (( rc == 0 )); then got="pass"; fi
  if [[ "${got}" == "${expect}" ]]; then
    echo "✓ ${label} (expected ${expect}, got ${got})"
  else
    echo "✗ ${label} (expected ${expect}, got ${got})"
    sed 's/^/    /' "${WORK}/out.txt"
    failed=1
  fi
}

# Each case starts from an empty superproject so only its own pointers exist.
reset_sup() {
  rm -rf "${sup}"
  git init -q -b main "${sup}"
}

reset_sup
set_pointers "${c2}" "${c2}"
run_check pass "both on main tip"

reset_sup
set_pointers "${c1}" "${c2}"
run_check pass "one behind main tip"

reset_sup
set_pointers "${s1}" "${c2}"
run_check fail "pointer on unmerged side branch"

exit "${failed}"
