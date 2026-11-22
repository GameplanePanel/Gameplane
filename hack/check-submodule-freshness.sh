#!/usr/bin/env bash
# check-submodule-freshness.sh: Fail when a submodule gitlink in the
# superproject points at a commit that is not on the upstream main branch.
#
# Purpose:
#   Catches a superproject pointer bump made before the submodule PR merged.
#   Such a pointer references a commit that exists only on a feature branch,
#   so a fresh clone of the superproject cannot fetch it. Spec 013 G6 / OD-1.
#
# Checked submodules: every gitlink at HEAD plus every path .gitmodules declares
#   (a declared path with no gitlink fails). For each one:
#   upstream URL: ${SUBMODULE_URL_<PATH>} (path upper-cased, '-' -> '_', e.g.
#                 SUBMODULE_URL_MCP_SERVER), else its .gitmodules url
#   branch:       ${SUBMODULE_BRANCH}, else its .gitmodules branch, else main
#   The fixture superproject in check-submodule-freshness_test.sh has no
#   .gitmodules, so it relies on the gitlinks and the URL overrides.
#
# Environment:
#   SUBMODULE_FRESHNESS_REPO  superproject to check (default: this repo root).
#                             Used by hack/check-submodule-freshness_test.sh.
#
# Usage: hack/check-submodule-freshness.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -n "${SUBMODULE_FRESHNESS_REPO:-}" ]]; then
  cd "${SUBMODULE_FRESHNESS_REPO}"
else
  cd "${SCRIPT_DIR}/.."
fi

# Union of HEAD gitlinks and .gitmodules paths, in a stable order.
mapfile -t PATHS < <(
  {
    git ls-tree -r HEAD | awk '$2=="commit"{print $4}'
    if [[ -f .gitmodules ]]; then
      git config -f .gitmodules --get-regexp '^submodule\..*\.path$' | awk '{print $2}'
    fi
  } | sort -u
)

# gitmodules_value <path> <key>: the .gitmodules value for the entry at <path>.
gitmodules_value() {
  [[ -f .gitmodules ]] || return 0
  local key name
  key="$(git config -f .gitmodules --get-regexp '^submodule\..*\.path$' |
    awk -v p="$1" '$2==p{print $1; exit}')"
  [[ -n "${key}" ]] || return 0
  name="${key%.path}"
  git config -f .gitmodules --get "${name}.$2" || true
}

TMP="$(mktemp -d)"
cleanup() {
  rm -rf "${TMP}"
}
trap cleanup EXIT

failures=0

for path in "${PATHS[@]}"; do
  override="SUBMODULE_URL_$(tr '[:lower:]-' '[:upper:]_' <<<"${path}")"
  url="${!override:-$(gitmodules_value "${path}" url)}"
  branch="${SUBMODULE_BRANCH:-$(gitmodules_value "${path}" branch)}"
  BRANCH="${branch:-main}"
  if [[ -z "${url}" ]]; then
    echo "✗ ${path}: no upstream URL (.gitmodules url or ${override})"
    failures=$((failures + 1))
    continue
  fi

  sha="$(git ls-tree HEAD "${path}" | awk '$2=="commit"{print $3}')"
  if [[ -z "${sha}" ]]; then
    echo "✗ ${path}: no gitlink at HEAD"
    failures=$((failures + 1))
    continue
  fi

  bare="${TMP}/${path}.git"
  git init -q --bare "${bare}"
  if ! git -C "${bare}" fetch -q "${url}" "refs/heads/${BRANCH}:refs/heads/${BRANCH}"; then
    echo "✗ ${path}: could not fetch ${BRANCH} from ${url}"
    failures=$((failures + 1))
    continue
  fi

  if git -C "${bare}" cat-file -e "${sha}^{commit}" 2>/dev/null &&
    git -C "${bare}" merge-base --is-ancestor "${sha}" "refs/heads/${BRANCH}"; then
    echo "✓ ${path}: ${sha} is on ${BRANCH}"
  else
    echo "✗ ${path}: pointer ${sha} is not on ${url} ${BRANCH} (bump the superproject only after the submodule PR merges)"
    failures=$((failures + 1))
  fi
done

if (( failures > 0 )); then
  exit 1
fi
exit 0
