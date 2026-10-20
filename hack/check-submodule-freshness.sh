#!/usr/bin/env bash
# check-submodule-freshness.sh: Fail when a submodule gitlink in the
# superproject points at a commit that is not on the upstream main branch.
#
# Purpose:
#   Catches a superproject pointer bump made before the submodule PR merged.
#   Such a pointer references a commit that exists only on a feature branch,
#   so a fresh clone of the superproject cannot fetch it. Spec 013 G6 / OD-1.
#
# Checked submodules (path -> upstream URL, overridable through env):
#   modules -> ${SUBMODULE_URL_MODULES:-https://github.com/GameplanePanel/module}
#   website -> ${SUBMODULE_URL_WEBSITE:-https://github.com/GameplanePanel/website}
#   Upstream branch: ${SUBMODULE_BRANCH:-main}
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

BRANCH="${SUBMODULE_BRANCH:-main}"
URL_MODULES="${SUBMODULE_URL_MODULES:-https://github.com/GameplanePanel/module}"
URL_WEBSITE="${SUBMODULE_URL_WEBSITE:-https://github.com/GameplanePanel/website}"

PAIRS=(
  "modules ${URL_MODULES}"
  "website ${URL_WEBSITE}"
)

TMP="$(mktemp -d)"
cleanup() {
  rm -rf "${TMP}"
}
trap cleanup EXIT

failures=0

for pair in "${PAIRS[@]}"; do
  path="${pair%% *}"
  url="${pair#* }"

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
