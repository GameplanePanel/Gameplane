# Contract: features sync check

## Command

```sh
make check-features-sync      # runs hack/check-features-sync.sh
make test-features-sync       # runs hack/test-check-features-sync.sh (fixture tests)
```

| Env var | Default | Purpose |
|---|---|---|
| `FEATURES_MD` | `FEATURES.md` | Repo catalog path (fixtures override). |
| `FEATURES_MDX` | `website/src/content/docs/features.mdx` | Website page path (fixtures override). |

## Behaviour

1. If either file is missing, exit 2 with `features-sync: missing <path>` (a missing website submodule checkout is an error, never a pass).
2. Parse each file into an ordered list of `(area, capability, maturity, default)` per the [catalog format](./features-catalog.md). Lines inside fenced code blocks are skipped.
3. Validate each row: maturity and default in their fixed sets; `stable` with `default` other than `on` is an error.
4. Compare the two lists in order. Report every difference, not just the first.
5. Exit 0 when both files parse, validate and match; exit 1 otherwise.

## Output on failure (one line per problem)

```
features-sync: <file>:<line>: invalid maturity "<value>"
features-sync: <file>:<line>: stable row must have default "on"
features-sync: area mismatch at position <n>: "<repo area>" vs "<website area>"
features-sync: only in FEATURES.md: <area> / <capability>
features-sync: only in features.mdx: <area> / <capability>
features-sync: <area> / <capability>: maturity "<repo>" vs "<website>"
features-sync: <area> / <capability>: default "<repo>" vs "<website>"
features-sync: <area> / <capability>: order differs
features-sync: duplicate row in <FEATURES.md|features.mdx>: <area> / <capability>
```

## Fixture tests (FR-015, SC-005)

`hack/testdata/check-features-sync/` holds one directory per case with a `FEATURES.md`, a `features.mdx` and, for failing cases, an `expected.txt` file containing the expected output lines. Besides the messages above, the check reports an invalid default as `features-sync: <file>:<line>: invalid default "<value>"`.

- `pass/in-sync`: matching files with differing Description and Docs cells (must pass).
- `pass/code-fence`: a table-looking block inside a code fence is ignored.
- `fail/missing-row-website`, `fail/missing-row-repo`, `fail/maturity-mismatch`, `fail/default-mismatch`, `fail/area-mismatch`, `fail/order-differs`, `fail/invalid-label`, `fail/stable-off`, `fail/missing-file`.

## CI wiring

- `docs and repository checks` job: new step `git submodule update --init website` (only the website submodule), then `make check-features-sync` and `make test-features-sync`.
- `docs` path filter adds `FEATURES.md`, `website`, `hack/check-features-sync.sh`, `hack/test-check-features-sync.sh`, `hack/testdata/check-features-sync/**`.
- `make lint` gains `check-features-sync`.
- If spec 023's change-based selection has landed, the same paths are mapped to the docs job there.
