# Move briefs (spec 028)

One brief per move PR, named `<NN>-<slug>.md`. Each follows the section list in
[`../contracts/move-brief.md`](../contracts/move-brief.md).

The `hack/move_parity.py` script parses three of the sections, so keep these table shapes:

- **3. Moves**: `| source path | destination path | whole file or declarations |`
- **4. Exports**: `| old identifier | new identifier | package |`, one row per name that crosses a new package line
- **5. New files**: `| path | copy of `source` |`, for helper files and `suite_envtest_test.go` copies

Backticks are stripped before parsing. Header and `---` rows are skipped.

The `move parity (spec 028)` CI job runs `python3 hack/move_parity.py ci` on any PR that adds or edits a brief here. It checks that every test name survives and that each changed test file differs only by the OD-6 and OD-9 edits. It skips itself when the PR carries no brief.
