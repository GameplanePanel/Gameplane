#!/usr/bin/env bash
# submodules.sh: the one reader of .gitmodules for the split Gameplane components.
#
# A component is a Go service that lives in its own GameplanePanel repository
# and is checked out here as a submodule at its old path, so the Dockerfiles,
# bake file and chart keep their paths. It is a .gitmodules entry with
# `component = true` (modules/ and website/ are submodules but not components).
# Adding a split is one .gitmodules entry; every consumer below reads it:
# CI init steps, Makefile image rules, deploy/kind/e2e.sh, hack/ci_scope.py
# and hack/check-submodule-freshness.sh (which checks every gitlink).
#
# Usage:
#   hack/submodules.sh components     print each component path, one per line
#   hack/submodules.sh init           check out every component submodule
#   hack/submodules.sh ensure <path>  check out <path> if it is a component that
#                                     is not checked out yet; warn when the
#                                     checkout differs from the recorded gitlink
#   hack/submodules.sh check          fail when a component is missing from the
#                                     static workflow path filters that must
#                                     name it (GitHub cannot read .gitmodules)
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

components() {
	[[ -f .gitmodules ]] || return 0
	local key value name
	while read -r key value; do
		[[ "${value}" == true ]] || continue
		name="${key#submodule.}"
		name="${name%.component}"
		git config -f .gitmodules --get "submodule.${name}.path"
	done < <(git config -f .gitmodules --get-regexp '^submodule\..*\.component$' || true)
}

is_component() {
	local path
	while read -r path; do
		[[ "${path}" == "$1" ]] && return 0
	done < <(components)
	return 1
}

ensure() {
	local path="$1" expected actual
	is_component "${path}" || return 0
	if [[ ! -e "${path}/.git" ]]; then
		echo "${path} submodule not checked out; running git submodule update --init ${path}" >&2
		git submodule update --init -- "${path}"
		return 0
	fi
	expected="$(git rev-parse "HEAD:${path}")"
	actual="$(git -C "${path}" rev-parse HEAD)"
	if [[ "${expected}" != "${actual}" ]]; then
		echo "warning: ${path} checkout ${actual} differs from the recorded gitlink ${expected}; run git submodule update ${path}" >&2
	fi
}

# Each component path must appear as a bare list item (the gitlink itself, so a
# pointer bump matches) in these filters. ci.yaml: e2etree (images the e2e loads),
# submodules (freshness gate) and github (workflow-lint reads the Dockerfiles).
check() {
	local failures=0 path filter
	local ci=.github/workflows/ci.yaml
	while read -r path; do
		for filter in e2etree submodules github; do
			if ! awk -v f="${filter}" -v p="${path}" '
				/^            [a-z0-9_]+:[[:space:]]*$/ { cur = $1; sub(/:$/, "", cur); next }
				cur == f && $0 ~ "^              - .?" p ".?[[:space:]]*$" { found = 1 }
				END { exit found ? 0 : 1 }' "${ci}"; then
				echo "✗ ${path}: missing from the '${filter}' path filter in ${ci}"
				failures=$((failures + 1))
			fi
		done
		for wf in .github/workflows/publish-edge.yaml .github/workflows/devcontainers.yaml; do
			if ! grep -Eq "^[[:space:]]+- [\"']?${path}[\"']?[[:space:]]*$" "${wf}"; then
				echo "✗ ${path}: missing from the path filters in ${wf}"
				failures=$((failures + 1))
			fi
		done
	done < <(components)
	if ((failures > 0)); then
		echo "add each component submodule path to the filters above (see hack/submodules.sh)" >&2
		return 1
	fi
	echo "✓ every component submodule is in the workflow path filters"
}

case "${1:-}" in
components) components ;;
init)
	mapfile -t paths < <(components)
	if ((${#paths[@]} > 0)); then
		git submodule update --init -- "${paths[@]}"
	fi
	;;
ensure)
	[[ $# -eq 2 ]] || { echo "usage: $0 ensure <path>" >&2; exit 2; }
	ensure "$2"
	;;
check) check ;;
*)
	echo "usage: $0 components|init|ensure <path>|check" >&2
	exit 2
	;;
esac
