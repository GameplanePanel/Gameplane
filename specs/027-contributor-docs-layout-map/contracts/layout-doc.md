# Contract: `docs/layout.md`

The layout doc is what spec 028 applies. It MUST contain these sections, in this order.

## 1. Kinds

One subsection per kind: **Service**, **Library**, **CLI tool**, **Web app**. Each states:

- what makes a module that kind (one sentence);
- the folder tree, as a fenced `text` block;
- what goes in each folder (one line per folder);
- where tests live;
- which root files are allowed (`go.mod`, `go.sum`, `specs.md`, `README.md`, `Dockerfile`, `.testcoverage.yml`, and for services nothing else ending in `.go`).

Values come from OD-3 (Go kinds) and OD-4 (web).

## 2. Layout map

One table, one row per `go.work` module plus `web` (18 rows), sorted by kind then name:

| Module | Kind | Target | Gap today |
|---|---|---|---|

"Gap today" is `matches` or a concrete move (e.g. "`main.go` 1,052 lines at root → `cmd/main.go` + `internal/<concern>/`"), with the `master` SHA it was measured on stated above the table.

## 3. Exceptions

One bullet per exception with its reason. At least: `test/e2e`, `operator/api/v1alpha1`, `i18n`.

## 4. Enforcement

One paragraph matching OD-5.

## 5. Not decided here

One paragraph: package split boundaries and file-size thresholds belong to spec 028.

## Rules

- Every path in the doc resolves (`check-links`).
- No section repeats CLAUDE.md text; it links instead.
