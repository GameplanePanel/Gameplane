#!/usr/bin/env bash
# check-coverage-gaps.sh: Validate the coverage gap record for spec 013
# (expand-test-coverage).
#
# Purpose:
#   specs/done_013-expand-test-coverage/contracts/coverage-gap-record.md defines the
#   layout and rules of coverage-gaps.md. This script enforces them so that a
#   gap row cannot be marked closed without evidence, and the Summary counts
#   cannot drift from the table. It runs in the `docs` CI job.
#
# Usage:
#   hack/check-coverage-gaps.sh [FILE]
#   COVERAGE_GAPS_FILE=path hack/check-coverage-gaps.sh
#   Default: specs/done_013-expand-test-coverage/coverage-gaps.md
#   An optional first argument overrides COVERAGE_GAPS_FILE.
#
# Rules checked (contracts/coverage-gap-record.md):
#   1. Exactly one markdown table; header is the nine columns in order.
#   2. ID matches ^G-[0-9]{2}$ and is unique.
#   3. Category in {static, e2e-dashboard, e2e-optional, unit, e2e-postgres}.
#   4. Priority in {P1, P2, P3}.
#   5. Status in {open, in-progress, closed, closed-unproven, unit-only-by-design}.
#   6. closed: Evidence has a backtick-quoted token and an
#      https://github.com/GameplanePanel/ link.
#   7. unit-only-by-design: Notes non-empty. closed-unproven: Evidence as for
#      closed, and Notes cite an OD-<n> ruling.
#   8. "**Summary**:" line has the form
#      C/T closed · U closed unproven · I in progress · O open · B unit-only by design
#      and every number equals the count computed from the table.
#
# Only '|' splits table cells. Gap cells may contain backticks and '≥'.
# Violations go to stderr as "<file>:<line>: <message>"; exit 1 if any.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

FILE="${1:-${COVERAGE_GAPS_FILE:-specs/done_013-expand-test-coverage/coverage-gaps.md}}"

if [[ ! -f "$FILE" ]]; then
  echo "$FILE:1: file not found" >&2
  exit 1
fi

read -r -d '' AWK_PROG <<'AWK' || true
function trim(s) {
  gsub(/^[ \t]+|[ \t]+$/, "", s)
  return s
}

function report(ln, msg) {
  printf "%s:%d: %s\n", file, ln, msg > "/dev/stderr"
  bad = 1
}

# split_row fills cells[1..n] with the trimmed fields between the outer pipes.
function split_row(line, cells,   s, n, i) {
  s = line
  sub(/^\|/, "", s)
  sub(/\|[ \t]*$/, "", s)
  n = split(s, cells, /\|/)
  for (i = 1; i <= n; i++) cells[i] = trim(cells[i])
  return n
}

# check_evidence enforces rules 6 and 7 (Evidence part) for closed and
# closed-unproven rows.
function check_evidence(ln, ev, st) {
  if (ev == "") {
    report(ln, st " row has empty Evidence")
    return
  }
  if (ev !~ /`[^`]+`/) {
    report(ln, st " Evidence must name a backtick-quoted repo path or job name")
  }
  if (index(ev, "https://github.com/GameplanePanel/") == 0) {
    report(ln, st " Evidence must link https://github.com/GameplanePanel/")
  }
}

# count_check reports one Summary number that does not match the table.
function count_check(ln, label, claimed, actual) {
  if (claimed != actual) {
    report(ln, "Summary " label " count is " claimed ", table has " actual)
  }
}

BEGIN {
  bad = 0
  tables = 0
  prev = 0
  total = 0
  have_sum = 0
  catok["static"] = 1
  catok["e2e-dashboard"] = 1
  catok["e2e-optional"] = 1
  catok["unit"] = 1
  catok["e2e-postgres"] = 1
  prok["P1"] = 1
  prok["P2"] = 1
  prok["P3"] = 1
  stok["open"] = 1
  stok["in-progress"] = 1
  stok["closed"] = 1
  stok["closed-unproven"] = 1
  stok["unit-only-by-design"] = 1
}

{
  is_tbl = (substr($0, 1, 1) == "|")
  if (is_tbl && !prev) {
    tables++
    tline = 0
    if (tables == 2) {
      report(NR, "more than one table; expected exactly one")
    }
  }
  prev = is_tbl

  if (is_tbl && tables == 1) {
    tline++
    if (tline == 1) {
      n = split_row($0, c)
      hdr = ""
      for (i = 1; i <= n; i++) {
        hdr = hdr (i > 1 ? "|" : "") c[i]
      }
      if (hdr != "ID|Category|Gap|Story|FR|Priority|Status|Evidence|Notes") {
        report(NR, "header must be: ID | Category | Gap | Story | FR | Priority | Status | Evidence | Notes")
      }
    } else if (tline == 2) {
      if ($0 !~ /^\|[-: |]+\|[ \t]*$/ || index($0, "---") == 0) {
        report(NR, "second table line must be the |---| separator")
      }
    } else {
      total++
      n = split_row($0, c)
      if (n != 9) {
        report(NR, "row has " n " cells; expected 9")
      } else {
        id = c[1]
        category = c[2]
        pri = c[6]
        st = c[7]
        ev = c[8]
        notes = c[9]

        if (id !~ /^G-[0-9][0-9]$/) {
          report(NR, "ID '" id "' must match G-NN")
        } else if (id in seen) {
          report(NR, "duplicate ID " id)
        }
        seen[id] = 1

        if (!(category in catok)) {
          report(NR, "Category '" category "' is not allowed")
        }
        if (!(pri in prok)) {
          report(NR, "Priority '" pri "' must be P1, P2 or P3")
        }
        if (st in stok) {
          cnt[st]++
        } else {
          report(NR, "Status '" st "' is not allowed")
        }

        if (st == "closed" || st == "closed-unproven") {
          check_evidence(NR, ev, st)
        }
        if (st == "closed-unproven" && id != "G-04") {
          report(NR, "closed-unproven is allowed only for G-04 (OD-4)")
        }
        if (st == "closed-unproven" && notes !~ /OD-4([^0-9]|$)/) {
          report(NR, "closed-unproven Notes must cite the OD-4 ruling")
        }
        if (st == "unit-only-by-design" && notes == "") {
          report(NR, "unit-only-by-design Notes must be non-empty")
        }
      }
    }
  }

  if (!have_sum && index($0, "**Summary**:") == 1) {
    have_sum = 1
    sum_ln = NR
    sum_txt = trim($0)
  }
}

END {
  if (tables == 0) {
    report(1, "no markdown table found")
  }
  if (!have_sum) {
    report(1, "missing **Summary** line")
  } else {
    s = sum_txt
    sub(/^\*\*Summary\*\*:[ \t]*/, "", s)
    p_n = split(s, p, " · ")
    if (p_n != 5 \
        || p[1] !~ /^[0-9]+\/[0-9]+ closed$/ \
        || p[2] !~ /^[0-9]+ closed unproven$/ \
        || p[3] !~ /^[0-9]+ in progress$/ \
        || p[4] !~ /^[0-9]+ open$/ \
        || p[5] !~ /^[0-9]+ unit-only by design$/) {
      report(sum_ln, "Summary must be: **Summary**: C/T closed · U closed unproven · I in progress · O open · B unit-only by design")
    } else {
      claimed_closed = p[1] + 0
      claimed_total = substr(p[1], index(p[1], "/") + 1) + 0
      count_check(sum_ln, "closed", claimed_closed, cnt["closed"] + 0)
      count_check(sum_ln, "total", claimed_total, total)
      count_check(sum_ln, "closed unproven", p[2] + 0, cnt["closed-unproven"] + 0)
      count_check(sum_ln, "in progress", p[3] + 0, cnt["in-progress"] + 0)
      count_check(sum_ln, "open", p[4] + 0, cnt["open"] + 0)
      count_check(sum_ln, "unit-only by design", p[5] + 0, cnt["unit-only-by-design"] + 0)
    }
  }
  if (bad) {
    exit 1
  }
}
AWK

if awk -v file="$FILE" "$AWK_PROG" "$FILE"; then
  echo "✓ coverage gaps OK ($FILE)"
  exit 0
else
  exit 1
fi
