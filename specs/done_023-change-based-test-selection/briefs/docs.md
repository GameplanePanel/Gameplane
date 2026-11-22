# Brief: PR 5, docs (T048–T050)

Branch `claude/project-thread-63yei6-pr5`, stacked on PR 4. Apply exactly; touch nothing else.

## 1. `docs/contributing.md`: new subsection

Insert right before the line `## Testing` (it follows the `### Security and drift gates`
list), keeping one blank line before and after:

```markdown
### How CI picks tests

On a pull request, CI runs only the work your change can affect (spec 023). The
`detect changes` job decides, and its job summary has a **Test selection** table that
lists every suite as `run` or `skip`, with the changed files that chose it or the reason
everything ran.

What a partial run narrows:

- **Go unit tests** run only the packages that own a changed file, plus every package
  that imports them, across modules. A module's own `go.mod`, `go.sum` or
  `.testcoverage.yml` reruns that whole module. The module coverage gate then becomes a
  changed-lines gate: the lines you add must meet the module's own minimum
  (`hack/check_changed_coverage.py`).
- **Web unit tests** run `vitest related` on the changed `web/src` files, with the same
  changed-lines gate at the Vitest `lines` threshold. Lint and build always run whole.
  Shared inputs (`package.json`, the lockfile, the Vite/Vitest/TS/ESLint configs,
  `src/test/**`, `i18n/locales/**`) run the full suite.
- **Go e2e tests** run only the tests whose components changed, per
  `test/e2e/suite-components.json`. Jobs with no selected test are skipped.
- **`ci.yaml` edits** run only the jobs whose lines changed, plus the jobs they need.
  `.golangci.yml` runs only the lint legs. Other workflow files run only `workflow lint`.

Everything still runs on every push to `master`, and when a selection input changes
(`hack/ci_scope.py`, `hack/check_changed_coverage.py`, their tests, `Makefile`,
`go.work`, `go.work.sum`, `.github/actions/**`, `test/e2e/buckets.sh`,
`test/e2e/suite-components.json`). It also runs everything for a `ci.yaml` edit outside
any job or in `changes`/`report`, a path no rule maps, and any selection error.

To force a full run on your PR, add the label `ci: full-run` (read on the next push), or
start the `ci` workflow by hand on your branch (`workflow_dispatch`).

When you add an e2e test or a component, keep `test/e2e/suite-components.json` in step:
add the test name pattern under `suites` with the components and paths it exercises, or
list the component under `unexercised` with a reason. The `e2e bucket coverage` job
(`ci_scope.py verify-suites`) fails when a registered test matches no entry or a
component is missing. The full rules are in
`specs/done_023-change-based-test-selection/contracts/suite-components.md`.
```

## 2. `docs/agent-architecture.md`: task map row (T049)

old:
```markdown
| Translations, language packs, localized server or module messages (i18n) |
```
Keep that whole row unchanged and add this row directly below it:
```markdown
| CI test selection: which jobs, packages and tests a PR runs (`hack/ci_scope.py`, `hack/check_changed_coverage.py`, `test/e2e/suite-components.json`, the `changes` job in `.github/workflows/ci.yaml`) | `docs/contributing.md` ("How CI picks tests"), `specs/done_023-change-based-test-selection/contracts/` (`ci-scope-cli.md`, `suite-components.md`, `changed-coverage.md`) |
```

## 3. `specs/done_023-change-based-test-selection/tasks.md`

Change `- [ ] T048`, `- [ ] T049` and `- [ ] T050` to `- [X]`.

## 4. Checks

Only check that the links and paths named above exist (`ls`). No link checker, no lint.
