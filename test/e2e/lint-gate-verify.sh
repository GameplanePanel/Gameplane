#!/usr/bin/env bash
# Verifier for the lint gate: validates consistency between go.work
# and the lint job's matrix and conditional steps in .github/workflows/ci.yaml.
#
# Of the 10 rules in specs/done_004-lint-backlog-wave2/contracts/lint-gate.md,
# this script enforces R-001, R-002, R-003, R-004, R-006, R-008, R-009, and
# R-010 via line-oriented scans of ci.yaml (and go.work for R-001). R-005
# (exact-string `if:` matching) and R-007 (a new go.work member must add a
# matrix entry in the SAME PR) are deliberately out of scope: both need
# information this script does not have — R-005 needs a real expression
# parser to distinguish exact-string equality from regex/prefix matching
# rather than just grepping for the pattern that's already expected; R-007
# is a cross-commit/PR-history requirement, not something a single tree
# snapshot can observe. verify() prints a NOTE naming both on every run.
#
# Usage:
#   lint-gate-verify.sh [verify]     run all consistency checks (default)
#
# Exit codes:
#   0: all checks passed
#   1: hard check failed
#
set -euo pipefail

# Hard failure; exit 1 immediately.
fail() {
	printf '%s\n' "ERROR: $*" >&2
	exit 1
}

# Determine repo root. This script lives at test/e2e/, so the root is TWO
# levels up, not one — a single ".." lands in test/ and every relative path
# then silently misses.
# Can be overridden with --root <dir> for testing against fixture trees.
REPO_ROOT="$(dirname "$0")/../.."
ROOT_EXPLICIT=0

# --root may appear before or after the subcommand.
SUBCOMMAND=""
while [ $# -gt 0 ]; do
	case "$1" in
	--root)
		if [ $# -lt 2 ]; then
			fail "--root requires an argument"
		fi
		REPO_ROOT="$2"
		ROOT_EXPLICIT=1
		shift 2
		;;
	*)
		SUBCOMMAND="$1"
		shift
		;;
	esac
done

# Change to repo root so all relative paths work correctly.
cd "$REPO_ROOT"

# When the root was DERIVED from $0, assert it looks like the Gameplane tree.
# Without this, a wrong derivation surfaces as a confusing "go.work not found"
# instead of naming the real problem. Skipped for an explicit --root.
if [ "$ROOT_EXPLICIT" -eq 0 ] && { [ ! -f go.work ] || [ ! -f .github/workflows/ci.yaml ]; }; then
	fail "derived repo root does not look like the Gameplane tree (cwd: $(pwd)); expected go.work and .github/workflows/ci.yaml to exist"
fi

# --- Helpers ---

# True if $1 is a YAML job-boundary line: a bare key at exactly the 2-space
# job indentation used throughout ci.yaml (e.g. "  go:"), with nothing after
# the colon. Every scan below walks forward from the lint job's own "  lint:"
# line and must stop at the NEXT job and nowhere else.
#
# A previous version of every scanner in this file used
# "^[[:space:]]*[a-z-]+:[[:space:]]*\$" — no anchor on the indentation at
# all — to mean "stop at the next job". That matches ANY bare key at ANY
# depth: "strategy:", "matrix:", "steps:", even "with:" nested three levels
# deep inside a step. Since the real lint job's very first line after
# "lint:" is "    strategy:" (a bare key one level in), every one of those
# scanners broke out of its loop before ever reaching "steps:", so every
# check silently stopped seeing real content and either failed for the
# wrong reason or (worse) failed on a config that is actually correct.
# Anchoring to the literal 2-space job indentation is the fix: nested keys
# are indented deeper and can never be mistaken for a sibling job.
is_job_boundary_line() {
	[[ "$1" =~ ^[[:space:]][[:space:]][a-z][a-z0-9_-]*:[[:space:]]*$ ]]
}

# True if the given ci.yaml line contains a "matrix.module == '<value>'" (or
# double-quoted) clause whose value equals $2.
#
# A naive regex anchored right after "if:" only ever sees the FIRST clause
# on the line. The real lint job's operator/api build-tag step is written
# as a single combined condition:
#
#   if: matrix.module == 'operator' || matrix.module == 'api'
#
# so an anchored match finds "operator" (harmless) but can never find "api"
# — the "api" clause starts partway through the line, not right after
# "if:". This walks every "matrix.module == '...'" occurrence on the line
# instead of assuming there is exactly one, so OR'd conditions are handled
# regardless of which side of the "||" the module we're looking for sits on.
line_has_module_equals() {
	local line="$1"
	local module="$2"
	local rest="$line"
	local value

	while [[ "$rest" =~ matrix\.module[[:space:]]*==[[:space:]]*[\'\"]([^\'\"]*)[\'\"] ]]; do
		value="${BASH_REMATCH[1]}"
		if [ "$value" = "$module" ]; then
			return 0
		fi
		# Drop everything through the match just found so the next loop
		# iteration searches only what comes after it — otherwise the
		# same leftmost clause would match forever.
		rest="${rest#*"${BASH_REMATCH[0]}"}"
	done
	return 1
}

# Extract module list from go.work.
# Returns: sorted list of module paths, one per line.
get_go_work_modules() {
	grep -A 100 '^use (' go.work | grep -B 100 '^)' | grep '^\s*\./' | \
		sed 's/^\s*\.\/\s*//;s/\s*$//' | sort
}

# Extract matrix modules from ci.yaml.
# This uses grep to find the lint job's matrix.module line.
# Returns: sorted list, one per line.
get_matrix_modules() {
	local found_lint=0
	local found_matrix=0

	while IFS= read -r line; do
		# Detect the start of the lint job. Anchored to the job's own
		# 2-space indentation so a step whose name/comment happens to
		# contain the substring "lint:" elsewhere in the file can never
		# be mistaken for the job header.
		if [[ "$line" =~ ^[[:space:]][[:space:]]lint:[[:space:]]*$ ]]; then
			found_lint=1
			continue
		fi

		# If we've found lint, look for its matrix section.
		if [ "$found_lint" -eq 1 ]; then
			# Leaving the lint job entirely (next job header) means the
			# matrix was never found. This function is always invoked as
			# `matrix_mods=$(get_matrix_modules)` — a plain command
			# substitution assignment inside a function that verify()
			# calls as the left side of "||" (so -e is being ignored for
			# the whole call, per bash's documented -e-in-subshell rule).
			# Calling fail() (exit 1) here would only kill THIS
			# subshell — the caller would see it as an ordinary nonzero
			# exit status and silently proceed with an empty matrix_mods,
			# misreporting every go.work module as missing. Report the
			# specific problem here on stderr and return 1; the caller is
			# responsible for turning that into its own fail().
			if is_job_boundary_line "$line"; then
				printf '%s\n' "ERROR: lint job matrix.module not found before the job ended (matrix: or module: missing/moved in ci.yaml)" >&2
				return 1
			fi

			# matrix: starts the matrix block.
			if [[ "$line" =~ ^[[:space:]]*matrix: ]]; then
				found_matrix=1
				continue
			fi

			# Inside matrix, look for module: list.
			if [ "$found_matrix" -eq 1 ]; then
				if [[ "$line" =~ module:.*\[ ]]; then
					# Extract the module list: module: [a, b, c]
					# First, extract just the [...] part.
					local bracket_part
					bracket_part=$(echo "$line" | sed -n 's/.*\(\[.*\]\).*/\1/p')
					if [ -n "$bracket_part" ]; then
						# Remove brackets, split by comma, trim whitespace, and sort.
						echo "$bracket_part" | tr -d '[]' | tr ',' '\n' | \
							sed 's/^\s*//;s/\s*$//' | grep -v '^$' | sort
						return 0
					fi
				fi
			fi
		fi
	done <.github/workflows/ci.yaml

	# See the comment above on why this is a plain "report + return 1"
	# rather than fail() — fail() here would only ever terminate the
	# command-substitution subshell this function runs in.
	printf '%s\n' "ERROR: lint job not found in ci.yaml (expected a top-level '  lint:' key)" >&2
	return 1
}

# Predicate: does the conditional step matching the module declare
# "working-directory: ${{ matrix.module }}"?
# Args: module_name
# Returns: 0 (true) if such a step and working-directory line are found,
# 1 (false) otherwise. Writes nothing to stdout — this is a pure predicate,
# not a value extractor; callers test its exit status.
get_step_working_directory() {
	local module="$1"
	local in_lint=0
	local in_step=0
	local step_matches=0

	while IFS= read -r line; do
		# Track when we enter the lint job.
		if [[ "$line" =~ ^[[:space:]][[:space:]]lint:[[:space:]]*$ ]]; then
			in_lint=1
			continue
		fi

		if [ "$in_lint" -eq 1 ]; then
			# If we hit another job, stop.
			if is_job_boundary_line "$line"; then
				break
			fi

			# Look for step markers. Any list item at this indentation
			# starts a new step — "- name: ..." is the common case, but
			# "- uses: ..." (e.g. the real ci.yaml's go-cache step) is
			# just as much a step boundary and must reset state the same
			# way, or the previous step's tracking silently leaks into it.
			if [[ "$line" =~ ^[[:space:]]*- ]]; then
				in_step=1
				step_matches=0
				continue
			fi

			# Within a step, look for the if: condition matching this module.
			if [ "$in_step" -eq 1 ]; then
				if [[ "$line" =~ if: ]] && line_has_module_equals "$line" "$module"; then
					step_matches=1
				fi

				# Once we've matched the module, look for working-directory.
				# A new step boundary (any "- ..." line) is already caught
				# by the reset above before control reaches here, so there
				# is nothing further to detect "leaving the step" — the
				# next loop iteration naturally starts the new step fresh.
				if [ "$step_matches" -eq 1 ] && [[ "$line" =~ working-directory:[[:space:]]*\$\{\{[[:space:]]*matrix.module[[:space:]]*\}\} ]]; then
					return 0
				fi
			fi
		fi
	done <.github/workflows/ci.yaml

	# Not found is OK for this helper; the caller decides if it's an error.
	return 1
}

# Check if a conditional step for the module exists and has the expected build tag.
# Args: module_name expected_build_tag
# Returns: 0 if step exists with correct tag, 1 otherwise.
check_step_build_tag() {
	local module="$1"
	local expected_tag="$2"
	local in_lint=0
	local in_step=0
	local step_module=""

	while IFS= read -r line; do
		# Track when we enter the lint job.
		if [[ "$line" =~ ^[[:space:]][[:space:]]lint:[[:space:]]*$ ]]; then
			in_lint=1
			continue
		fi

		if [ "$in_lint" -eq 1 ]; then
			# If we hit another job, stop.
			if is_job_boundary_line "$line"; then
				break
			fi

			# Look for step markers. Any list item at this indentation
			# starts a new step — "- name: ..." is the common case, but
			# "- uses: ..." (e.g. the real ci.yaml's go-cache step) is
			# just as much a step boundary and must reset state the same
			# way, or the previous step's tracking silently leaks into it.
			if [[ "$line" =~ ^[[:space:]]*- ]]; then
				in_step=1
				step_module=""
				continue
			fi

			# Within a step, check the if: condition. Matches both a lone
			# "matrix.module == 'operator'" and the combined
			# "matrix.module == 'operator' || matrix.module == 'api'" —
			# see line_has_module_equals for why a single anchored regex
			# cannot do this.
			if [ "$in_step" -eq 1 ]; then
				if [[ "$line" =~ if: ]] && line_has_module_equals "$line" "$module"; then
					step_module="$module"
				fi

				# Once we've matched the module, look for --build-tags. A
				# new step boundary is already caught by the reset above
				# before control reaches here, so there is nothing further
				# to detect "leaving the step".
				if [ "$step_module" = "$module" ]; then
					if [[ "$line" =~ args:[[:space:]]*--build-tags=$expected_tag ]]; then
						return 0
					fi
				fi
			fi
		fi
	done <.github/workflows/ci.yaml

	return 1
}

# Check if the golangci-lint version is pinned to the expected value.
# Args: expected_version (e.g., "v2.14.0")
# Returns: 0 if version is found and matches, 1 otherwise. On failure,
# prints one line to stdout naming what was actually found so the caller
# can report the offending value, not just "it didn't match".
check_golangci_version() {
	local expected_version="$1"
	local in_lint=0
	local found_version_count=0
	local mismatched_count=0
	local mismatched_values=""

	# expected_version is interpolated directly into a =~ regex below, so
	# any regex metacharacter in it (notably "." in a version like
	# "v2.14.0") must be escaped first — otherwise "." matches ANY
	# character, and a typo like "v2X14Y0" would count as a match.
	local expected_version_re="${expected_version//./\\.}"

	while IFS= read -r line; do
		# Track when we enter the lint job.
		if [[ "$line" =~ ^[[:space:]][[:space:]]lint:[[:space:]]*$ ]]; then
			in_lint=1
			continue
		fi

		if [ "$in_lint" -eq 1 ]; then
			# If we hit another job, stop.
			if is_job_boundary_line "$line"; then
				break
			fi

			# Look for version: lines.
			if [[ "$line" =~ version:[[:space:]]*$expected_version_re[[:space:]]*$ ]]; then
				found_version_count=$((found_version_count + 1))
			elif [[ "$line" =~ version:[[:space:]]*([^[:space:]]+) ]]; then
				mismatched_count=$((mismatched_count + 1))
				mismatched_values="$mismatched_values ${BASH_REMATCH[1]}"
			fi
		fi
	done <.github/workflows/ci.yaml

	# At least one version should be found, and no mismatches allowed.
	if [ "$found_version_count" -gt 0 ] && [ "$mismatched_count" -eq 0 ]; then
		return 0
	fi

	if [ "$mismatched_count" -gt 0 ]; then
		printf 'found:%s (expected %s)' "$mismatched_values" "$expected_version"
	else
		printf 'no "version:" line found in the lint job (expected %s)' "$expected_version"
	fi
	return 1
}

# --- Checks ---

# R-001: Every go.work module appears exactly once in matrix.module.
check_r001_matrix_completeness() {
	local go_mods
	go_mods=$(get_go_work_modules)
	local matrix_mods
	if ! matrix_mods=$(get_matrix_modules); then
		fail "R-001: could not extract lint job matrix.module from ci.yaml (see the preceding error for detail)"
	fi

	# Check for missing modules.
	while IFS= read -r mod; do
		if ! printf '%s\n' "$matrix_mods" | grep -qx "$mod"; then
			fail "R-001: Module '$mod' listed in go.work but missing from lint job matrix"
		fi
	done <<<"$go_mods"

	# Check for extra modules.
	while IFS= read -r mod; do
		if ! printf '%s\n' "$go_mods" | grep -qx "$mod"; then
			fail "R-001: Module '$mod' in lint job matrix but not in go.work"
		fi
	done <<<"$matrix_mods"

	# Check for duplicates in matrix.
	local dup
	dup=$(printf '%s\n' "$matrix_mods" | sort | uniq -d)
	if [ -n "$dup" ]; then
		fail "R-001: Duplicate module(s) in matrix: $dup"
	fi
}

# R-004: No continue-on-error: true on the job or steps; no || true in lint steps.
check_r004_no_continue_on_error() {
	local in_lint=0
	local line_num=0

	while IFS= read -r line; do
		line_num=$((line_num + 1))

		# Track when we enter the lint job.
		if [[ "$line" =~ ^[[:space:]][[:space:]]lint:[[:space:]]*$ ]]; then
			in_lint=1
			continue
		fi

		if [ "$in_lint" -eq 1 ]; then
			# If we hit another job, stop.
			if is_job_boundary_line "$line"; then
				break
			fi

			# Check for continue-on-error.
			if [[ "$line" =~ continue-on-error:[[:space:]]*true ]]; then
				fail "R-004: Found 'continue-on-error: true' in lint job at line $line_num (must fail on findings)"
			fi

			# Check for || true.
			if [[ "$line" =~ \|\|[[:space:]]*true ]]; then
				fail "R-004: Found '|| true' in lint job at line $line_num (must fail on findings)"
			fi
		fi
	done <.github/workflows/ci.yaml
}

# R-006: No deferral comments on lint steps.
check_r006_no_deferral_comments() {
	local in_lint=0

	while IFS= read -r line; do
		# Track when we enter the lint job.
		if [[ "$line" =~ ^[[:space:]][[:space:]]lint:[[:space:]]*$ ]]; then
			in_lint=1
			continue
		fi

		if [ "$in_lint" -eq 1 ]; then
			# If we hit another job, stop.
			if is_job_boundary_line "$line"; then
				break
			fi

			# Check for deferral keywords in step names and comments. A
			# pure "#"-comment line (e.g. a commented-out/deferred matrix
			# entry) is exactly what R-006 is about, not just a step whose
			# "name:" happens to contain the word — so both shapes must be
			# examined, not just lines that look like "name: ...".
			if [[ "$line" =~ name: ]] || [[ "$line" =~ \# ]]; then
				if [[ "$line" =~ (pending|temporary|TODO|cleanup|WIP|FIXME) ]]; then
					fail "R-006: Found deferral comment in lint step: $line (module is actively linted, not deferred)"
				fi
			fi
		fi
	done <.github/workflows/ci.yaml
}

# R-002/R-008/R-009: Operator and API have envtest build tags, test/e2e has e2e
# build tags, working-directory matches matrix entry, version is pinned.
check_r002_r008_r009_build_tags_and_version() {
	# x/tools v0.50.0 in this release reads Go 1.27.3 export-data version 5.
	# Keep the workflow, devcontainer and valid fixtures on this exact pin.
	local version_required="v2.14.0"

	# Check operator has envtest build tag.
	if ! check_step_build_tag "operator" "envtest"; then
		fail "R-002: Module 'operator' missing conditional step with --build-tags=envtest"
	fi

	# Check api has envtest build tag.
	if ! check_step_build_tag "api" "envtest"; then
		fail "R-002: Module 'api' missing conditional step with --build-tags=envtest"
	fi

	# Check test/e2e has e2e build tag.
	if ! check_step_build_tag "test/e2e" "e2e"; then
		fail "R-002: Module 'test/e2e' missing conditional step with --build-tags=e2e"
	fi

	# Check working-directories are set correctly.
	get_step_working_directory "operator" >/dev/null || fail "R-008: Operator step missing working-directory"
	get_step_working_directory "api" >/dev/null || fail "R-008: API step missing working-directory"
	get_step_working_directory "test/e2e" >/dev/null || fail "R-008: test/e2e step missing working-directory"

	# Check golangci-lint version is pinned.
	local version_detail
	if ! version_detail=$(check_golangci_version "$version_required"); then
		fail "R-009: golangci-lint version is not pinned to $version_required ($version_detail)"
	fi
}

# R-003: The lint job's strategy MUST set `fail-fast: false`, so a failure
# in one matrix entry does not cancel the others still queued or running —
# without it, a late-matrix module (e.g. tunnel) could be skipped entirely
# whenever an earlier module fails, masking multi-module failures.
check_r003_fail_fast_false() {
	local in_lint=0
	local found_false=0
	local found_true=0

	while IFS= read -r line; do
		# Track when we enter the lint job.
		if [[ "$line" =~ ^[[:space:]][[:space:]]lint:[[:space:]]*$ ]]; then
			in_lint=1
			continue
		fi

		if [ "$in_lint" -eq 1 ]; then
			# If we hit another job, stop.
			if is_job_boundary_line "$line"; then
				break
			fi

			if [[ "$line" =~ fail-fast:[[:space:]]*false[[:space:]]*$ ]]; then
				found_false=1
			elif [[ "$line" =~ fail-fast:[[:space:]]*true[[:space:]]*$ ]]; then
				found_true=1
			fi
		fi
	done <.github/workflows/ci.yaml

	if [ "$found_true" -eq 1 ]; then
		fail "R-003: lint job strategy has 'fail-fast: true' (must be false so all matrix entries run even if one fails)"
	fi

	if [ "$found_false" -eq 0 ]; then
		fail "R-003: lint job strategy is missing 'fail-fast: false' (a later matrix module would never run if an earlier one fails)"
	fi
}

# R-010: The linter config file MUST be .golangci.yml at the repository
# root, not module-specific variants — otherwise different modules would be
# linted under different rulesets and findings would be classified
# inconsistently.
check_r010_single_golangci_config() {
	if [ ! -f ".golangci.yml" ]; then
		fail "R-010: no .golangci.yml found at the repository root"
	fi

	# Fixture trees under testdata/ deliberately carry their own root-level
	# .golangci.yml (e.g. case-combined-condition, whose positive test
	# requires it to pass this very check when run with --root pointed at
	# that fixture). The Go toolchain already ignores testdata/ for build
	# and lint purposes, so pruning it here just keeps those fixtures from
	# being reported as drift when this check runs against the real repo
	# root, where testdata/ is a subdirectory rather than the root itself.
	local extra
	extra=$(find . -name .git -prune -o -name testdata -prune -o \( -name '.golangci.yml' -o -name '.golangci.yaml' \) -print |
		grep -v '^\./\.golangci\.yml$' || true)
	if [ -n "$extra" ]; then
		fail "R-010: found per-module golangci config variant(s) besides the repository-root .golangci.yml: $extra"
	fi
}

# --- Main ---

verify() {
	local fail_count=0

	# fail() exits the whole process the moment any check finds a real
	# violation (see its doc comment), so in practice at most one of these
	# ever contributes to fail_count — the first hard failure ends the run
	# right there. The counter mirrors joincoverage.sh's verify() on
	# purpose: it's what lets a check return a plain 1 (no violation, just
	# "not satisfied") without itself deciding whether that's fatal.
	check_r001_matrix_completeness || fail_count=$((fail_count + 1))
	check_r004_no_continue_on_error || fail_count=$((fail_count + 1))
	check_r006_no_deferral_comments || fail_count=$((fail_count + 1))
	check_r002_r008_r009_build_tags_and_version || fail_count=$((fail_count + 1))
	check_r003_fail_fast_false || fail_count=$((fail_count + 1))
	check_r010_single_golangci_config || fail_count=$((fail_count + 1))

	# R-005 (exact-string `if:` matching) and R-007 (a new go.work member
	# must add a matrix entry in the SAME PR) are the two contract rules
	# this script does not enforce — see the file header for why. Say so
	# out loud on every run rather than let their absence pass unnoticed.
	printf '%s\n' "NOTE: R-005 (exact-string if: matching) and R-007 (same-PR matrix entry for new go.work members) are not implemented — this run did not check them." >&2

	if [ "$fail_count" -ne 0 ]; then
		exit 1
	fi

	exit 0
}

# --- Dispatch ---

case "${SUBCOMMAND:-verify}" in
verify)
	verify
	;;
*)
	cat >&2 <<'EOF'
Usage:
  lint-gate-verify.sh [verify]  Run all consistency checks (default)

Exit codes:
  0: All checks passed
  1: Hard check failed
EOF
	exit 2
	;;
esac
