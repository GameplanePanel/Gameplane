# Contract: `hack/ci_scope.py` modes and outputs

`ci_scope.py` stays Python stdlib only and never installs Go or Node. Existing modes
(`modules`, `images`, `bots`, `dockerfiles`) keep their current outputs; this feature
adds keys and modes, it does not rename any.

## Inputs (all modes that diff)

| Input | Source |
|---|---|
| `--base`, `--head` | `github.event.pull_request.base.sha \|\| github.event.before`, `github.sha` |
| `GITHUB_REF`, `GITHUB_EVENT_NAME` | Actions env |
| `CI_FULL_RUN` | `"true"` when the PR has the `ci: full-run` label (`contains(github.event.pull_request.labels.*.name, 'ci: full-run')`) |

Full scope when: `GITHUB_REF == refs/heads/master`, `GITHUB_EVENT_NAME ==
workflow_dispatch`, `CI_FULL_RUN == true`, the diff is `None`, a path is unmapped, or a
path forces everything (research R8).

## Mode `modules` — added keys

| Key | Type | Meaning |
|---|---|---|
| `scope` | `"full"` \| `"partial"` | Run-level scope (FR-009 switch) |
| `go-packages` | JSON object: module → list of package patterns | `["./..."]` on full scope or when a module's own `go.mod`/`go.sum` changed |
| `web-tests` | JSON: `"all"` or list of `web/src/...` source paths for `vitest related` | |
| `e2e-exclude` | JSON matrix-exclude list of `{bucket}` | For `e2e-go` and `e2e-go-arm64` |
| `e2e-run` | JSON object: bucket → anchored `-run` regex of the selected tests | Replaces `buckets.sh regex <bucket>` in the e2e jobs (OD-6); equals it on full scope |
| `e2e-multicluster`, `e2e-upgrade`, `e2e-web-live`, `e2e-capture-overhead` | `"true"`/`"false"` | Folded into the existing `combine` step booleans |
| `ci-jobs` | JSON list of job ids selected by the `ci.yaml` line mapper | Folded into each job's gate |
| `unmapped` | JSON list of paths | Non-empty forces full scope and fails `selection-rules` |
| `selection` | JSON list of SelectionResult | For the summary and `report` |

## Mode `bots` — changed

Emits only the selected `bot-fast` tests. When none is selected it emits an empty
matrix and `bots-any=false`, and `e2e-game-bot` is gated on `bots-any`.

## Mode `verify-suites` — new

Exit 0 when [suite-components.md](./suite-components.md) rules 1, 2 and 5 hold; otherwise
exit 1 and print each violation. No diff needed.

## Step summary

Appended to `$GITHUB_STEP_SUMMARY` by mode `modules`:

```markdown
### Test selection (partial)
| Suite | Decision | Why |
|---|---|---|
| e2e operator (amd64, arm64) | run | changed: agent/internal/console/pty.go |
| e2e telemetry (amd64, arm64) | skip | not affected |
| go api | run | changed: api/internal/handlers/servers.go (+2) |
```

A full-scope run prints one line stating why (`full: master push`, `full: label ci:
full-run`, `full: unmapped path tools/new.sh`, …) followed by the table with every row
`run`.

## Failure behaviour

Any exception in diffing or parsing selects full scope and prints the exception in the
summary. It never selects less than today.
