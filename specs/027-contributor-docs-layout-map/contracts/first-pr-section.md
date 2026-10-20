# Contract: "Your first PR" section in `docs/contributing.md`

Placed directly after the guide's opening paragraph, linked from it and from `README.md` "Contributing".

## Steps (numbered, 6 items)

1. Fork and branch (one branch per change).
2. Find the owning folder in the table below (and `docs/layout.md` for where new code goes).
3. Run the compile check for that row only. Do not run test or lint suites locally; CI runs them.
4. Commit with `git commit -s` and a conventional prefix.
5. Open the PR; check it has one `type:` and one `area:` label.
6. Wait for CI (time in the table); fix what it reports.

## Change-type table

| Change | Owning folder | Local check | CI wall clock (2026-10-20) |
|---|---|---|---|

Rows (minimum): docs only; spec only; web only; one small Go service; operator/CRD (adds `make generate && make manifests`); API route; game module (PR in `GameplanePanel/module`, then pointer bump); website (PR in `GameplanePanel/website`, then pointer bump).

CI times come from research R5. Rows with no measured run say "same as any Go change" or "measured in that repo". Under the table: the source runs and a note that spec 023 changes these numbers.

## Gates a first PR trips on

Bullets: signed commits; labels; no local test/lint; design first in `design.pen` for UI; never hand-edit `.pen`; submodule work goes to its own repo first.
