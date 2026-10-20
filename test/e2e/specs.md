# test/e2e — Specification

**Status:** pre-v1 (v0.3.0)
**Module / package:** `github.com/GameplanePanel/gameplane/test/e2e`
**Go version:** 1.26.0

## Purpose

The `test/e2e` module is Gameplane's end-to-end test suite: it stands up a real (kind-based) Kubernetes cluster, installs the Helm chart, and drives the operator/api/agent/sentinel stack as real components rather than mocks — CRDs applied through the typed/dynamic client, HTTP calls through a real port-forward, RCON/wire-protocol joins against real game server containers. Where the other modules' test tiers (unit, envtest) prove a component's own logic in isolation, this module proves the whole product works together on a cluster shaped like the ones users actually run.

It was one of three `go.work` modules without CI lint coverage until this feature (`api`, `agent`, and `test/e2e` — see FR-001); all 13 `go.work` members (`netguard`, `gameaction`, `gameproto`, `operator`, `agent`, `api`, `sentinel`, `audit-syslog-bridge`, `telemetry-receiver`, `mcp-server`, `svcutil`, `tunnel`, and `test/e2e`) are golangci-lint gated in CI now — see the `lint` job's matrix in `.github/workflows/ci.yaml`. `test/e2e` lints with `--build-tags=e2e`, same tier as `operator`/`api`'s `--build-tags=envtest`, run as their own step so the catch-all lint step excludes all three and nothing is linted twice.

## Responsibilities

- Boot and tear down (or reuse) a kind cluster, install the working-tree Helm chart, and wait for every component to come up healthy.
- Exercise the full CRD lifecycle for all 8 kinds (GameServer, GameTemplate, Backup, BackupSchedule, Restore, Module, ModuleSource, Cluster) against a real API server and operator.
- Drive the REST + WebSocket API as a real HTTP client would: login, session/CSRF handling, RBAC matrix checks, console/PTY/log streaming, file upload/download.
- Launch real game server containers and either speak their wire protocol directly (the CI `bot-fast` bucket: Minecraft, Terraria, Garry's Mod — narrower than `fastGameSet`'s manual/local default scope; see `test/e2e/internal/specs.md`) or query them, proving a Gameplane-managed server is genuinely playable, not merely "Running" in Kubernetes. The per-game protocol clients and the shared probe harness that does this live one level down, in `test/e2e/internal/` — see `test/e2e/internal/specs.md` for that layer's Path A / Path B contract, protocol clients, and JoinDepth model. This file does not duplicate that; it covers the outer suite that hosts and buckets those probes.
- Validate multi-cluster dispatch, upgrade-from-previous-release, wake-on-connect, module signing/SSRF rejection, and backup/restore round trips end to end.
- Enforce, via `buckets.sh`, that every test in the suite is assigned to exactly one CI bucket so nothing silently drops out of coverage.

### Remote gateway parity acceptance

The multicluster bucket runs `TestMultiCluster_GatewayParity` against two real
clusters. It installs the checked-out gateway chart on the remote cluster with
temporary, separate server/client trust, then calls it through the central API.
Same-named servers prove file/mod operations and CR configuration stay at the
selected site. A real Minecraft fixture verifies RCON console, actions, player
lists and live status. Captures are compared through the public API, gateway and
remote sidecar before cleanup is checked against the sidecar and CR directly.
The test also exercises wrong identities, scoped permission denials, mounted
certificate rotation, overlapping and revoked roots, changed endpoints and
missing/malformed credential Secrets. Registry provider metadata and env-mode
modpack configuration are deterministic; external registry search behavior is
covered by API provider tests rather than third-party availability in this test.

This scenario is non-parallel because it temporarily changes the shared remote
Helm release and mounted trust. Its temporary resources and gateway installation
are removed through test cleanup. CI sets `GAMEPLANE_E2E_REQUIRE_GATEWAY=1`, so a
missing second cluster fails instead of skipping the scenario. The multicluster
Go invocation has a 45-minute deadline within the existing 60-minute job budget.

## Non-goals / boundaries

- Does **not** replace the unit or envtest tiers for any other module — it exercises integration behavior, not the exhaustive branch coverage those tiers own (this module carries no `.testcoverage.yml` coverage gate).
- Does **not** run by default on a developer machine or in ordinary `go build`/`go vet`/`go test` invocations — see the build-tag section below.
- Does **not** implement game-protocol parsing itself for anything beyond the CI `bot-fast` bucket's join proof; the deep per-game clients and heavy-game set live under `test/e2e/internal/` and are documented there.
- Does **not** own CRD business logic, RBAC rules, or protocol codecs — it only calls the real components' real surfaces and asserts on the outcome. Any behavior change belongs in `operator`, `api`, `agent`, `gameproto`, or the module the test is exercising.
- Heavy-resource games (`bot-heavy` bucket: Ark, CS2, DayZ, Don't Starve Together, Enshrouded, Factorio, Palworld, Project Zomboid, Rust, Satisfactory, 7 Days to Die, Valheim, V Rising) are **deliberately never run in CI** — not on PRs, nightly, `schedule:`, or `workflow_dispatch:` — because of runner disk (~14GB) and download-size constraints. They are still bucketed (for discovery/audit), but "bucketed" is not evidence of "executed in CI"; see the comment above `bucket_bot_heavy` in `buckets.sh`.

## The `//go:build e2e` boundary — why this module is invisible to ordinary tooling

53 of this directory's 82 `.go` files carry `//go:build e2e` (every `*_test.go` file plus the shared non-test helpers they depend on: `env.go`, `gameprobe_job.go`). Without `-tags=e2e`, the Go toolchain — `go build ./...`, `go vet ./...`, `gofmt`, an editor's language server, a plain `go test ./...` — silently skips every one of those files. This has a direct, previously-burned consequence documented project-wide in `MEMORY.md`: **`go vet ./...` does not compile envtest/e2e-tagged files**, so a bad import or a broken reference introduced here passes local checks and every other CI job cleanly, and only reddens the run once the dedicated e2e job compiles with the tag. There is no cheap local signal for a broken build in this module short of `go vet -tags=e2e ./...` (or, per the project's "nothing runs locally" rule, waiting for CI). Treat any change under `test/e2e/` as unverified until the e2e CI job runs it.

The golangci-lint job mirrors this: it must be invoked with `--build-tags=e2e` or it lints an effectively-empty package (only the 29 untagged files: `Dockerfile`-adjacent Go tooling, `internal/` package code not gated by the tag, fixtures, etc.).

## The bucket contract — the module's most important invariant

`buckets.sh` is the single source of truth for how CI's e2e suite is sharded into parallel jobs (`operator`, `api-auth`, `api-roles`, `api-rbac`, `api-agent`, `api-mods`, `ratelimit`, `bot-fast`, `bot-heavy`, `multicluster`, `upgrade`). Each bucket is a flat list of **exact Go function names** (e.g. `TestGameServer_MinecraftJavaBot_Joined`), and `buckets.sh regex <name>` turns that list into an **anchored** `^(A|B|...)$` regex passed to `go test -run`.

This is a name-matching contract, not a structural one:

- **Anchoring matters.** `^(...)$` is deliberate — an unanchored regex would let `TestGameServer` also match `TestGameServer_Cascading`, silently running one bucket's test inside another's job.
- **Renaming a `func Test...` breaks CI silently everywhere except the one place that catches it.** If a test is renamed without updating `buckets.sh`, the old name still matches nothing (dropping the test from every bucket) or the new name matches nothing in any bucket (same result) — either way, the compiler, `go vet`, and lint all stay green. The **only** thing that fails is `buckets.sh verify`, run as the dedicated "e2e bucket coverage" CI job: it greps `^func Test[A-Za-z0-9_]+` out of every `*_test.go` file (a flat directory, so a file grep is an exact inventory — see the comment on `suite_tests` in `buckets.sh`), diffs that set against the union of every bucket's list, and fails on any test that's in the suite but in no bucket, in more than one bucket, or bucketed but no longer present (deleted/renamed).
- **New tests must be added to a bucket in the same change.** There is no "unbucketed but intentional" escape hatch beyond the explicit, reviewed `unbucketed()` list in `buckets.sh` (currently empty) — every addition there needs a stated reason.
- **Buckets are cut by login pressure, not feature area.** The API's login rate limiter is per-IP (burst 10, 5/min) and per-username (burst 6, 3/min), and every test in one job shares a single client IP through the shared `kubectl port-forward` — so a bucket's budget is bounded by admin logins, not CPU. `operator` does zero logins and runs wide; each `api-*` bucket stays within roughly 7 admin logins so retries absorb overflow without exhausting the per-user burst. `ratelimit` deliberately drains the shared limiter and is intentionally its own last, non-parallel `go test` invocation so it doesn't starve every other test's logins.

## The `Env` helpers and context threading (`env.go`)

`Env` (built once per test process by `newEnv`/`newEnvForContext`, from `TestMain`) wraps the typed and dynamic Kubernetes clients plus shell-out helpers for operations the typed client makes awkward. As of this feature, the process-spawning helpers take an explicit `context.Context` and use `exec.CommandContext` rather than the unbounded `exec.Command`, binding each child process's lifetime to the work that started it instead of letting it outlive a cancelled or completed caller:

- **`Kubectl(ctx, args...)`** and **`KubectlWithStdin(ctx, stdin, args...)`** both run `exec.CommandContext(ctx, "kubectl", all...)`. Both still validate every argument up to a literal `--` separator against shell metacharacters (`|;&$\`<>()\\`) before building `all` — this is the caller-supplied-args gosec finding scoped by the `.golangci.yml` G204 exclusion on `env.go`: gosec can't see that the args come from the suite's own test code and are pre-validated, not from untrusted external input.
- **`tryPortForward(ctx, ns, target, remotePort)`** (the single-attempt implementation behind the public, retrying `PortForward`) also runs its `kubectl port-forward` child via `exec.CommandContext(ctx, ...)`. **The context passed here must outlive the call that starts the forward** — it only bounds the *maximum* process lifetime, not the normal teardown path. The returned `stop` func is the real owner of the forward's lifecycle: every caller `defer`/`t.Cleanup`s it well before the test (and therefore its context) ends. Binding the forward to anything shorter-lived than the test itself — e.g. a context scoped to just the readiness-poll loop — would risk the process being killed by context expiry out from under an in-flight caller still using the tunnel. `PortForward` retries `tryPortForward` up to 4 times on a fresh local port before giving up, tolerating a loaded self-hosted runner where a single attempt's readiness window is too short.

## Conventions (already normative via `CLAUDE.md`, restated here for the module's own record)

- **`t.Parallel()`**: tests call it unless shared infrastructure requires serialization. The `ratelimit` bucket deliberately drains the shared login limiter; gateway parity temporarily changes the shared remote Helm release and trust. These are documented exceptions.
- **Per-test unique resource names**: CRs, namespaces, and users a test creates get a name unique to that test run (commonly time-based, e.g. `CreateUser`'s `fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())`) so parallel tests never collide on a shared cluster.
- **`ociPushMu`**: a package-level mutex guarding tests that push through the shared, fixed-name `oras-push` Job (`module_e2e_test.go`, `module_verify_e2e_test.go`). Because the Job's name and namespace are fixed fixtures, two tests racing to apply/wait on it would stomp each other; `ociPushMu.Lock()`/`Unlock()` serializes them.
- **`ensureResticRepo(t)`**: guards tests that run a backup against the shared restic repository fixture (`backup_e2e_test.go`, `backupschedule_e2e_test.go`, `failure_paths_e2e_test.go`) — restic repo initialization is not safe to race.
- **Per-job login budget**: keep an `api-*` bucket to roughly 7 admin logins (see the bucket contract section above); tests observing raw login HTTP status codes (e.g. asserting a specific 429) must stay non-parallel so the status they observe isn't perturbed by a sibling test's concurrent login.

## Directory & package layout

```
test/e2e/
├── *_test.go                 # 40+ e2e test files, flat in the package, all //go:build e2e;
│                              # named by area (api_*, gameserver_*, backup*, module*, *_bot_e2e_test.go, ...)
├── buckets.sh                 # CI bucket source of truth (see above); also `verify`'s the mapping
├── env.go                     # Env: typed/dynamic k8s clients, Kubectl/KubectlWithStdin/PortForward,
│                              # APIClient (session+CSRF HTTP client), CreateUser, OCIPush helpers
├── gameprobe_job.go            # Launches the in-cluster game-bot probe Job (see internal/specs.md)
├── e2e_suite_test.go           # TestMain: cluster bring-up/reuse, chart install, shared fixtures
├── test_helpers_e2e_test.go    # Misc test-local helpers
├── gamebot_helpers_e2e_test.go # Shared helpers for the *_bot_e2e_test.go files
├── Dockerfile                  # Builds the in-cluster game-bot probe image (docker-bake target)
├── Dockerfile.fakeoidc         # Builds the e2e fake-OIDC issuer fixture image
├── fixtures/                   # YAML manifests applied via Env.ApplyYAML (oras-push Jobs, sample CRs, etc.)
├── testdata/                   # Static test fixtures (non-YAML)
├── internal/                   # Per-game protocol clients + shared probe harness — see internal/specs.md
│   ├── fakeoidc/               # Fake OIDC issuer implementation (not part of the probe harness)
│   ├── probe/, protocol/, <game>/
├── joincoverage.sh, joincoverage_test.sh  # JoinDepth coverage reporting for the bot suite
└── go.mod, go.sum              # Standalone module; workspace-linked via go.work
```

## External interface / contracts

This module has no runtime HTTP/RPC surface of its own — its "interface" is the `go test -tags=e2e -run <bucket regex>` invocation CI makes per bucket, plus the environment variables the suite reads:

| Env var | Purpose |
|---|---|
| `GAMEPLANE_E2E_CLUSTER` | kind cluster name (default `gameplane-e2e`) |
| `GAMEPLANE_E2E_REQUIRE_GATEWAY` | `1` requires the real two-cluster gateway acceptance scenario; missing remote infrastructure is a failure |
| `GAMEPLANE_E2E_TAG` | image tag the chart is installed with (default `e2e`) |
| `GAMEPLANE_E2E_CONTEXT` | kubeconfig context to act in; overrides the derived `kind-<cluster>` default — set this (with `GAMEPLANE_E2E_REUSE_CLUSTER=1`-style bring-up) to run the suite against an existing cluster instead of kind |
| `KUBECONFIG` | standard kubeconfig path override, honored by `newEnvForContext`'s loader |

### Key exported helpers on `Env` (env.go)

- **`Eventually(t, timeout, cond)`** / **`Consistently(t, duration, interval, cond)`** — the two polling primitives every status-convergence and invariant-holds assertion in the suite is built on.
- **`Kubectl`/`KubectlWithStdin`/`KubectlExec`** — shell out to `kubectl`, context-bound (see above).
- **`ApplyYAML(t, fixturePath)`** — applies a manifest from `fixtures/`.
- **`PodIsReady`, `CRDExists`** — typed/dynamic client lookups used by convergence checks.
- **`PortForward(t, ns, target, remotePort)`** — retrying wrapper over `tryPortForward`; returns `(localPort, stopFunc)`.
- **`APIClient(t, username, password)`** — logs in over a fresh port-forward (retrying through 429s with backoff, since the shared IP shares the login limiter across parallel tests), returns a session+CSRF-aware HTTP client whose `Do`/`Get`/`Post`/`Patch`/`Delete` attach `X-Gameplane-CSRF` on mutating methods.
- **`BootstrapAdmin(t, username, password)`** — runs the API's `bootstrap-admin` subcommand once per test process (`sync.Once`); a second call with a different credential pair fails loudly rather than silently using stale creds, since argon2id hashing inside the running pod is heavy enough that repeating it per test risks an OOM-kill.
- **`OCIPush`/`OCIPushFromFixture`** — apply a ConfigMap+Job fixture and wait for it to succeed; guarded by `ociPushMu` at call sites that share the fixed job name.
- **`CreateUser(t, admin, role, prefix)`** — creates a uniquely-named user via the admin `APIClient`; caller owns cleanup registration.

## Key invariants

1. **The `//go:build e2e` tag hides breakage from every tool except the dedicated e2e CI job.** No other gate — `go vet`, `gofmt`, ESLint-equivalent, the standard lint step — sees these files without `-tags=e2e`. See the boundary section above.
2. **Every test function name is bucketed exactly once, verified structurally, not by convention.** `buckets.sh verify` is the enforcement mechanism; a stray or duplicate entry fails CI, but only in that one job.
3. **A test's bucket is chosen for login pressure, not code area.** Moving a test between files does not require moving it between buckets; renaming the `func Test...` does, and must happen in the same change as the `buckets.sh` update.
4. **Process-spawning `Env` helpers are context-bound but the port-forward's process lifetime and its logical lifetime are deliberately decoupled.** `tryPortForward`'s `ctx` is a safety net (max process lifetime), not the primary teardown path — the `stop()` closure is. Passing a short-lived context risks killing an in-flight tunnel; not calling `stop()` leaks a `kubectl` child.
5. **Heavy games are bucketed but never CI-executed** — bucketing proves discoverability/audit, not that the games run in the default pipeline.
6. **This module owns no CRD/RBAC/protocol business logic** — every assertion here is a black-box observation of another module's real behavior; a failing e2e test's fix almost always lands in `operator`, `api`, `agent`, or `gameproto`, not here.

## Dependencies

**Internal (via go.work):** none directly imported by the outer suite; the per-game probe binaries under `internal/` are built standalone with `GOWORK=off` (see `internal/specs.md`) and share no code with the agent or operator by design (Path B ground truth).

**External (from `go.mod`):**

| Module | Version | Purpose |
|---|---|---|
| `github.com/coder/websocket` | v1.8.15 | WebSocket client (console, log-tail, PTY tests) |
| `k8s.io/api` | v0.36.3 | Kubernetes core types |
| `k8s.io/apimachinery` | v0.36.3 | K8s API machinery (metav1, schema, dynamic) |
| `k8s.io/client-go` | v0.36.3 | Typed + dynamic Kubernetes clients |
| `sigs.k8s.io/yaml` | v1.6.0 | YAML handling for fixture parsing |

Verify from `test/e2e/go.mod`.

## Security considerations

- **`env.go`'s shell-metacharacter check is a defense against accidental, not adversarial, input.** All arguments originate from the suite's own test code; the check exists so a bad string interpolation in a test doesn't become a shell-injection-shaped bug, not because the module treats external actors as a threat.
- **`insecureCookieJar` (env.go) deliberately ignores the `Secure` cookie attribute** so the e2e HTTP client can carry the API's `Secure` session/CSRF cookies over the plain-HTTP port-forward tunnel. This is a test-only accommodation for talking to a single localhost host; production still sets `Secure: true` and real browsers still enforce it.
- **`WriteAdminPasswordFile`** drops the bootstrapped admin password to `test/e2e/.tmp/admin-password` at mode `0600`, gitignored, solely so the (separate) Playwright suite can reuse the same credentials in live mode; it is not consumed by anything in this module's own Go tests.
- **`internal/satisfactory/app.go`'s gosec G402 exclusion** (TLS `InsecureSkipVerify`-shaped finding) belongs to the probe harness one level down — see `internal/specs.md` — not to this outer module.

## Testing & coverage

This module *is* a test tier; it carries no unit tests of its own and no `.testcoverage.yml` gate. Its own correctness is enforced by:

- **`buckets.sh verify`** — the "e2e bucket coverage" CI job, structural proof every test is in exactly one bucket.
- **CI's per-bucket jobs** — `operator`, `api-auth`, `api-roles`, `api-rbac`, `api-agent`, `api-mods`, `ratelimit`, `bot-fast`, `multicluster`, `upgrade` all run against a real kind cluster per job; `bot-heavy` is bucketed but never scheduled (see Non-goals).
- **`joincoverage.sh`/`joincoverage_test.sh`** — reports which games/protocols the bot suite actually reaches JoinDepth on, cross-referenced with `internal/specs.md`'s JoinDepth model.

## References

- **`test/e2e/internal/specs.md`** — the per-game protocol client + probe harness layer this file cross-references rather than duplicates: Path A/Path B contract, JoinDepth model, per-game wire clients.
- **`test/e2e/buckets.sh`** — canonical bucket definitions; run `buckets.sh buckets`/`list`/`regex`/`verify` directly to inspect or debug the mapping.
- **`.golangci.yml`** — the G204 (`env.go`) and G402 (`internal/satisfactory/app.go`) gosec exclusions, and the `--build-tags=e2e` lint invocation.
- **`CLAUDE.md`** — "e2e test conventions" section (bucket requirement, `t.Parallel()`, shared-state guards, login budget) this file expands on.
- **`docs/contributing.md`** — human-facing test-tier overview.
- **`Makefile`** — `make test-e2e`, `make test-e2e-keep`, `make test-e2e-bucket BUCKET=<name>`.
- **`deploy/kind/`** — cluster bring-up scripts (`cluster.yaml`, `e2e.sh`, `upgrade.sh`) this suite's `TestMain` and the `upgrade` bucket depend on.
