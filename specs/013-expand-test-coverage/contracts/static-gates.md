# Contract: Static Analysis Gates

Each gate below is a binding contract for its implementing task. Common rules for all of them:
- SHA-pinned actions, with downloaded binaries checksum-verified
- `permissions: contents: read`, plus only what the gate needs
- registered in the `report` job
- no ignore files or inline suppressions
- a proof script whose exit code is 0 only when the gate fails on its fail input **and** passes on its pass input

## G1 `vuln-go`: Go dependency vulnerabilities

| | |
|---|---|
| Job name | `vuln (go)` |
| Triggers | `pull_request` when scope `go`; `push` to `master`; weekly `schedule` |
| Command | per module in `go.work`: `govulncheck ./...`, with `-tags envtest` (operator, api), `-tags e2e` (test/e2e), and an extra `-tags postgres` pass for api |
| Fails when | any vulnerability with a **reachable** symbol (exit 3) |
| Passes when | no reachable findings; imported-only findings are printed, not blocking |
| Proof | `hack/check-govulncheck_test.sh`: `-db file://$PWD/hack/testdata/govulncheck/db` against `hack/testdata/govulncheck/{fail,pass}` (synthetic module `example.invalid/vulnfixture`, synthetic advisory `GO-0000-0001`) |
| Fix path | bump the dependency (or Go toolchain) in the affected `go.mod`; never `-exclude` |

## G2 `vuln-images`: container images

| | |
|---|---|
| Job name | `vuln (images)` |
| Triggers | `pull_request` when scope `images`; `needs: build-images`; also a step in `publish-edge.yaml` and `release.yaml` **before** `cosign sign` |
| Command | `trivy image --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1` for each image the e2e bake builds (incl. mcp-server, audit-syslog-bridge, tunnel-frp, web) |
| Fails when | any HIGH/CRITICAL finding with a fixed version available |
| Proof | `hack/check-image-scan_test.sh`: fail = a digest-pinned end-of-life `alpine` image; pass = one freshly built Gameplane distroless image from the job |
| Fix path | base image digest bump, or the Go/npm dependency bump in the image |

## G3 `hadolint`: Dockerfile lint

| | |
|---|---|
| Location | a step in the existing `workflow-lint` job |
| Command | `hadolint --config .hadolint.yaml $(git ls-files '*Dockerfile*' ':!hack/testdata/**' ':!*.dockerignore')` |
| Config | `.hadolint.yaml`: `failure-threshold: warning`; `trustedRegistries` per OD-2; **no `ignored:` list** |
| Fails when | any warning or error |
| Proof | `hack/check-hadolint_test.sh` on `hack/testdata/hadolint/{fail,pass}/Dockerfile` |
| Pre-existing | fixed in the gate's own PR, after the first CI run lists them |

## G4 `dependency-review`

| | |
|---|---|
| Job name | `dependency review` |
| Triggers | `pull_request` only |
| Action | `actions/dependency-review-action`, `fail-on-severity: high`, `comment-summary-in-pr: on-failure`; no `allow-ghsas`, no `allow-licenses` change |
| Fails when | the PR adds or changes a dependency with a high or critical advisory |
| Proof | one closed, never-merged validation PR adding a known high-severity dependency, showing the job red. Its URL is recorded in the gap record (plan Complexity Tracking) |

## G5 `codegen-drift`

| | |
|---|---|
| Job name | `codegen drift` |
| Triggers | `pull_request` when scope `go` or `chart`; `push` to `master` |
| Command | `make generate manifests && make tidy && git diff --exit-code` |
| Fails when | any tracked file changes: deepcopy, `operator/config/{crd,rbac}`, `charts/gameplane/{crds,crd-manifests}`, any `go.mod`/`go.sum` |
| Proof | `hack/check-codegen-drift_test.sh` in a temp `git worktree`: perturb `operator/api/v1alpha1/gameserver_types.go` and expect failure; leave it clean and expect a pass |

## G6 `submodule-freshness`

| | |
|---|---|
| Job name | `submodule freshness` |
| Triggers | `pull_request` when the `modules` or `website` gitlink changes |
| Command | `hack/check-submodule-freshness.sh`: for each submodule, fetch upstream `main`; `git merge-base --is-ancestor <gitlink> origin/main` |
| Fails when | a committed pointer is not on the upstream default branch (unmerged submodule commit) |
| Allowed | a pointer behind the tip |
| Proof | `hack/check-submodule-freshness_test.sh` builds bare upstream + superproject repos in `mktemp -d` and checks an on-branch pointer (pass) and an off-branch pointer (fail) |
| Status | interpretation pending **OD-1**; not implemented until it is ruled |
