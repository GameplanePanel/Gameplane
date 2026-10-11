# Contract: `hack/check_changed_coverage.py`

Changed-lines coverage gate for partial runs (FR-009, settled OD-1). Python stdlib only.

## Invocation

```text
python3 hack/check_changed_coverage.py --base <sha> --head <sha> \
  --threshold <percent> (--go-profile <file> --module <dir> | --vitest-json <file>)
```

- `--go-profile`: a Go cover profile (`mode: atomic`) from the partial `go test` run,
  merged with the envtest profile for operator and api. Paths in the profile are import
  paths; `--module` maps them back to repo paths through `go.mod`.
- `--vitest-json`: Vitest's `coverage/coverage-final.json` (Istanbul format).
- `--threshold`: see OD-5 in [OPEN-DECISIONS.md](../OPEN-DECISIONS.md).

## Behaviour

1. Added lines come from `git diff -U0 <base> <head>` for files inside the module (or
   `web/src`), excluding `_test.go`, `*.test.ts(x)` and the paths the module's own
   coverage config already excludes (`.testcoverage.yml` `exclude.paths`,
   `vitest.config.ts` `coverage.exclude`).
2. An added line counts only if the profile has a statement block covering it. Lines
   with no statement (comments, blank lines, type declarations) are ignored.
3. Covered share = covered statement lines ÷ statement lines. With zero statement lines
   the check passes and says so.
4. Exit 1 when the share is below the threshold, listing each uncovered `file:line`.
5. Prints a one-line result to `$GITHUB_STEP_SUMMARY`.

## Not changed

Full runs keep `make cover-go-merge && make cover-go-check` and the Vitest thresholds
exactly as today. No `.testcoverage.yml` or `vitest.config.ts` threshold is edited.
