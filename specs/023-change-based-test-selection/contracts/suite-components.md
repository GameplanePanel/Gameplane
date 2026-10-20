# Contract: `test/e2e/suite-components.json`

Declares what each e2e test exercises (FR-003). Lives beside `test/e2e/buckets.sh`,
which stays the only source of bucket membership.

## Shape

```json
{
  "version": 1,
  "universal": [
    "charts/", "operator/api/v1alpha1/", "operator/config/", "containers/",
    "deploy/kind/e2e.sh", "test/e2e/env.go", "test/e2e/internal/fakeoidc/",
    "test/e2e/go.mod", "test/e2e/go.sum", "test/e2e/buckets.sh",
    "test/e2e/suite-components.json"
  ],
  "suites": [
    { "tests": "^TestHelmInstall_", "components": ["*"] },
    { "tests": "^TestCRD_Validation_", "components": ["operator"] },
    { "tests": "^TestAPI_Agent", "components": ["api", "agent"],
      "paths": ["test/e2e/test_helpers_e2e_test.go"] },
    { "tests": "^TestTelemetry", "components": ["api", "telemetry-receiver", "telemetryschema"],
      "paths": ["test/e2e/telemetry_e2e_helpers_test.go", "test/e2e/telemetry_client_test.go"] }
  ],
  "unexercised": [
    { "component": "audit-syslog-bridge", "reason": "no e2e image or test; unit tests only" }
  ]
}
```

The entries above are illustrative. The real list is written in the implementation task
from the per-test map in [research.md](../research.md).

## Rules

1. `tests` is a Go regular expression matched against test names from
   `buckets.sh list <bucket>`. Every test in every bucket, including `bot-heavy`, matches
   at least one entry; a test matching several entries takes the union.
2. `components` holds component ids from [data-model.md](../data-model.md#component),
   or `"*"` for "every change selects this test".
3. `paths` are repo-relative prefixes. A changed path under one of them selects the test.
   A changed `test/e2e/<file>_test.go` always selects the tests defined in that file
   (US1 scenario 4), without needing a `paths` entry.
4. A changed path under any `universal` prefix selects every suite.
5. Every component with code that is not listed in `unexercised` is named by at least
   one entry. `unexercised` entries carry a one-line reason.
6. Editing this file or `buckets.sh` selects every e2e suite (rule 4), so a mapping
   change is always proven on a full e2e run.

## Enforced by

`python3 hack/ci_scope.py verify-suites`, run in the existing `e2e-buckets` job next to
`buckets.sh verify`, and covered by new cases in `hack/test_ci_scope.py`.
