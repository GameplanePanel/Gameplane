# Feature 009 — Data Model

This feature introduces no CRD, no database table, and no API schema.

---

## Security Alert

**What it is**: A code-scanning alert from GitHub's CodeQL analysis, representing a potential security vulnerability flagged by static analysis rules.

**Attributes**:

| Field | Type | Description |
|---|---|---|
| `number` | int | GitHub code-scanning alert ID (1–14, as observed 2026-08-28) |
| `rule_id` | string | CodeQL rule identifier (e.g. `go/path-injection`, `go/clear-text-logging`) |
| `severity` | string | CodeQL severity: `error`, `warning`, or `note` |
| `security_severity_level` | string | CVSS-style level: `critical`, `high`, `medium`, `low`, or `none` |
| `file` | string | Repo-relative path where the alert is located |
| `line` | int | Line number of the flagged statement |
| `message` | string | Human-readable summary of the finding |
| `state` | string | Alert state (see State Machine below) |
| `dismissed_reason` | string | Required when `state=dismissed`; one of: `false positive`, `won't fix`, `used in tests` |
| `dismissed_comment` | string | Optional; maintainer-written justification for the dismissal |

**State Machine**:

```
OPEN → FIXED     (default-branch analysis stops reporting the alert)
OPEN → DISMISSED (maintainer calls PATCH /repos/{owner}/{repo}/code-scanning/alerts/{n})
DISMISSED → REOPENED (code change re-triggers the alert)
```

**Validation Rules**:

- A dismissed alert MUST include a non-empty `dismissed_reason` from the enum.
- An alert transitions to `fixed` automatically when the default branch (master) analysis no longer reports it; this is a GitHub-side result, not an in-repo action.
- Dismissals are made via the GitHub code-scanning API; no in-source suppression directives (`//nolint`, `//#nosec`, etc.) are permitted per constitution Principle III.

**Inventory of 14 Open Alerts** (ground truth as of 2026-08-28):

| # | Rule | File | Line | Verdict | Disposition |
|---|---|---|---|---|---|
| 1 | go/clear-text-logging | api/internal/kube/watch.go | 40 | FALSE POSITIVE (taint source is a field name string, not secret bytes; kubeconfig bytes never enter a log call) | Refactor or dismiss `false positive` |
| 2 | go/clear-text-logging | api/internal/kube/watch.go | 54 | FALSE POSITIVE (same as #1, UpdateFunc path) | Refactor or dismiss `false positive` |
| 3 | go/disabled-certificate-check | agent/internal/rcon/satisfactory.go | 199 | FALSE POSITIVE (guarded by `isLoopbackHost` check; local pod-internal dial; self-signed cert; rationale documented in package) | Refactor or dismiss `false positive` |
| 4 | go/disabled-certificate-check | test/e2e/internal/satisfactory/app.go | 188 | REAL DEFECT (`InsecureSkipVerify=true` unconditional, no loopback guard) | Fix (move to loopback-only guard or use local trust manager) |
| 5 | go/request-forgery | agent/internal/mods/mods.go | 405 | FALSE POSITIVE (`netguard.HTTPClient` with `CheckRedirect` cap and per-hop `hostAllowed` validation; scheme and allowed list validated pre-dial) | Refactor or dismiss `false positive` |
| 6 | go/request-forgery | api/internal/ws/dialer.go | 281 | FALSE POSITIVE (DNS1123Label validation + hardcoded `https` scheme + in-cluster host from `p.agentHost()`) | Refactor or dismiss `false positive` |
| 7 | go/zipslip | agent/internal/mods/mods.go | 508 | FALSE POSITIVE (pre-check at line 511 rejects escaping entries; post-clean re-check at 527–532 returns error) | Refactor or dismiss `false positive` |
| 8 | go/path-injection | agent/internal/mods/mods.go | 389 | FALSE POSITIVE (`os.RemoveAll(target)` guarded by Clean+Join+Clean+HasPrefix at 381–385) | Refactor or dismiss `false positive` |
| 9 | go/path-injection | agent/internal/mods/mods.go | 391 | FALSE POSITIVE (`os.Remove(target)`, same guards) | Refactor or dismiss `false positive` |
| 10 | go/path-injection | agent/internal/mods/mods.go | 446 | FALSE POSITIVE (`os.Rename()` to `filepath.Join(h.dir, name)`; caller validates with `safeName()`) | Refactor or dismiss `false positive` |
| 11 | go/path-injection | agent/internal/mods/mods.go | 486 | FALSE POSITIVE (`os.RemoveAll(final)` guarded by Clean+Join+Clean+HasPrefix at 478–482) | Refactor or dismiss `false positive` |
| 12 | go/path-injection | agent/internal/mods/mods.go | 490 | FALSE POSITIVE (`os.Rename()`, same guards) | Refactor or dismiss `false positive` |
| 13 | go/path-injection | agent/internal/mods/mods.go | 594 | FALSE POSITIVE (`os.Stat(target)` guarded by `safeName()` at 586, then Clean+Join+Clean at 591–593) | Refactor or dismiss `false positive` |
| 14 | go/uncontrolled-allocation-size | api/internal/audit/audit.go | 834 | FALSE POSITIVE (`make([]Event, 0, limit)` clamped by 820–822; handler parses at 25 but Auditor clamps pre-allocation in same function) | Refactor or dismiss `false positive` |

**Spec Drift Note**: The spec says 14 alerts (correct), but does not acknowledge that 13 of the 14 are false positives — #4 is the single real defect. The un-flagged latent gap in agent/internal/rcon/websocket.go is separate from the 14 CodeQL alerts entirely. Verdicts documented above supersede any implicit assumption in the spec's requirements that all 14 are real defects.

---

## Path Confinement Contract

**What it is**: The security invariant that every mod filesystem operation must satisfy to prevent path traversal, symlink escapes, and archive extraction outside a sandbox.

**Invariant**: A validated path is the result of:
1. Joining a cleaned absolute root directory with an untrusted component
2. Running `filepath.Clean()` on the result
3. Verifying the path is either equal to the root or begins with `root + os.PathSeparator`
4. Where the path may exist: running `filepath.EvalSymlinks()` on the result AND on the deepest existing ancestor, verifying both satisfy the prefix check

**Current Implementations**:

| Function | Location | Guards | Coverage |
|---|---|---|---|
| `safeName` | agent/internal/mods/mods.go:625–645 | Rejects `.`, `..`, leading dot, len>200, control chars, `/` and `\` | Input validation for single path component; does NOT address traversal in multi-level paths |
| `archiveFolderName` | agent/internal/mods/mods.go:557–565 | Strips `.tar.gz`, `.tgz`, `.zip` suffix; no confinement check | Filename normalization only |
| `resolve` | agent/internal/files/files.go:57–98 | The most complete implementation: Clean, prefix-check, `EvalSymlinks` on target and deepest ancestor, error on escape | Full confinement contract; used by file server and remote file access |
| `extractUploadArchive` | api/internal/handlers/module_upload.go:290–363 | `path.Clean`, rejects absolute, `..`, `../` prefix; collects into a map (no on-disk joins) | Used only for API upload parsing; safe because no filesystem join |

**Validation Rules**:

- Every `os.RemoveAll`, `os.Remove`, `os.Rename`, `os.Stat`, archive extraction, or path-based file creation MUST use the contract or a Guardian function that enforces it
- `filepath.EvalSymlinks` on a symlink that points outside the root is a defect; the contract checks both the result AND the deepest existing ancestor
- Callers MUST validate the untrusted component BEFORE passing it to a guardian (e.g., `safeName` for single components); guardians do not re-check
- When path components come from an archive member or HTTP parameter, each segment MUST be run through `safeName` or equivalent before joining

---

## Bounded Query Window

**What it is**: The audit log pagination limit, enforcing a server-side cap on slice allocation to prevent memory exhaustion DoS.

**Attributes**:

| Field | Type | Value | Notes |
|---|---|---|---|
| `raw_limit` | string | client-supplied `?limit=<N>` | Untrusted; from HTTP query parameter, parsed with `strconv.Atoi` |
| `default_limit` | int | 100 | Used when `raw_limit` is absent or invalid |
| `maximum_limit` | int | 500 | Hard cap; no allocation exceeds this |
| `clamp_location_today` | string | api/internal/audit/audit.go:820–822 | In `(*Auditor).Page()` function; clamping happens AFTER allocation site is reached |
| `parse_location` | string | api/internal/handlers/audit.go:25 | Handler parses raw value with `strconv.Atoi` but does NOT clamp; untrusted value passed downstream |

**Validation Rule** (CRITICAL):

The allocated slice capacity MUST be provably in `[1, 500]` regardless of input. Today the clamping happens in the right place (`Auditor.Page`), but the handler's separate parse means the invariant is not statically guaranteed — a future refactor could break it. The contract is:

```go
// Pseudocode:
rawLimit := strconv.Atoi(req.URL.Query().Get("limit"))  // untrusted
limit := clamp(rawLimit, 1, 500)  // [1, 500]
out := make([]Event, 0, limit)    // now safe; capacity proven in range
```

**Spec Drift Note**: The spec assumes alert #14 is a real defect (`uncontrolled-allocation-size`). It is actually a false positive — the allocation IS clamped in the right place — but the parse/clamp split means the contract should be tightened to make the invariant explicit to the linter.

---

## Dependency Upgrade Unit

**What it is**: One Dependabot pull request, carrying a version bump across one or more Go modules or npm packages.

**Attributes**:

| Field | Type | Description |
|---|---|---|
| `pr_number` | int | GitHub PR number |
| `ecosystem` | string | `gomod` or `npm` |
| `package` | string | Importable package name (e.g. `modernc.org/sqlite`, `vitest`) |
| `from_version` | string | Current pinned version in the repo (e.g. `1.55.0`) |
| `to_version` | string | Proposed version (e.g. `1.57.0`) |
| `modules_touched` | string[] | Directories that have `go.mod` or `package.json` entries for this package |
| `blast_radius` | int | Count of modules touched (proxy for test/build surface) |
| `ci_status` | string | `passing` (all checks green), `failing` (one or more check red) |
| `security_advisory` | bool | `true` if CVE/GHSA fix, `false` if routine upgrade |

**Inventory of 21 Open Dependabot PRs** (as of 2026-08-28):

**Go Ecosystem** (10 PRs):

| PR | Package | From | To | Modules | Blast | Status | Security |
|---|---|---|---|---|---|---|---|
| #281 | modernc.org/sqlite | 1.55.0 | 1.57.0 | api, capture-sidecar, mcp-server, operator, test/e2e | 5 | passing | false |
| #279 | sigstore/cosign/v2 | 2.6.4 | 2.6.5 | capture-sidecar, operator, test/e2e | 3 | passing | false |
| #276 | gopacket/gopacket | 1.6.1 | 1.7.1 | capture-sidecar, test/e2e | 2 | passing | false |
| #274 | golang.org/x/mod | 0.38.0 | 0.40.0 | agent, api, capture-sidecar, mcp-server, operator, sentinel, test/e2e | 7 | passing | false |
| #273 | minio/minio-go/v7 | 7.2.1 | 7.3.0 | agent, api, capture-sidecar, mcp-server, operator, sentinel, telemetry-receiver, test/e2e | 8 | passing | false |
| #271 | k8s.io/api | 0.36.3 | 0.36.4 | agent, api, capture-sidecar, mcp-server, operator, sentinel, test/e2e | 7 | passing | false |
| #269 | golang.org/x/net | 0.57.0 | 0.58.0 | agent, api, capture-sidecar, mcp-server, operator, sentinel, test/e2e | 7 | passing | false |
| #267 | go-chi/chi/v5 | 5.3.1 | 5.3.2 | agent, api, capture-sidecar, operator, test/e2e | 5 | passing | false |
| #265 | google/go-containerregistry | 0.21.7 | 0.22.0 | agent, api, capture-sidecar, mcp-server, operator, sentinel, telemetry-receiver, test/e2e | 8 | passing | false |
| #263 | sigstore/sigstore | 1.10.8 | 1.10.9 | capture-sidecar, operator, test/e2e | 3 | **failing** | false |

**npm Ecosystem** (11 PRs, all in `web/`):

| PR | Package | From | To | Status | Security |
|---|---|---|---|---|---|
| #283 | npm_and_yarn (SECURITY group) | — | — | **passing** | **true** (brace-expansion CVE-2026-13149, js-yaml DoS) |
| #280 | @types/react-dom | 19.2.3 | 19.2.4 | passing | false |
| #278 | vitest | 4.1.10 | 4.1.11 | passing | false |
| #277 | @vitejs/plugin-react | 6.0.4 | 6.1.0 | passing | false |
| #275 | @types/node | 26.1.2 | 26.2.0 | passing | false |
| #272 | typescript | 6.0.3 | **7.0.2** (MAJOR) | **failing** (4 checks) | false |
| #270 | @tanstack/react-router | 1.170.18 | 1.170.32 | passing | false |
| #268 | @eslint/js | 9.39.5 | **10.0.1** (MAJOR; drops eslintrc, updates eslint:recommended, needs Node ^20.19\|\|^22.13\|≥24) | **failing** (1 check) | false |
| #266 | @playwright/test | 1.62.0 | 1.62.1 | passing | false |
| #264 | @testing-library/jest-dom | 7.0.0 | 7.0.1 | passing | false |
| #262 | @typescript-eslint/parser | 8.65.0 | 8.67.0 | passing | false |

**Notes**:

- **Spec Drift**: The spec says "20 Dependabot PRs (#262–#281)" and lists 10 npm PRs. The actual count is 21: PR #283 (security group: `brace-expansion` + `js-yaml`) was omitted from the spec but is a real open PR that must be in scope.
- **Failing PRs**: #263 (sigstore, 1 check), #272 (TypeScript 7, 4 checks), #268 (ESLint 10, 1 check).
- **User Decision** (2026-08-28): #272 and #268 are deferred to a SEPARATE, GATED phase after the other 19 are merged; individual merge strategy (merge-commit, not squash, per PR, accepting extra CI cycles).

---

## TLS Trust Boundary

**What it is**: The two locations where the codebase disables certificate verification, and the runtime properties that make each defensible or defective.

**Inventory**:

| Site | Location | `InsecureSkipVerify` Guarded | Guard | Rationale | Disposition |
|---|---|---|---|---|---|
| **Satisfactory RCON (prod)** | agent/internal/rcon/satisfactory.go:199 | YES | `if isLoopbackHost(host)` where `isLoopbackHost` (lines 220–226) accepts only literal `localhost` or a parsed loopback IP | Satisfactory generates self-signed cert on startup with no CA supply mechanism; dial never leaves the pod; pod-internal loopback-only transport. Rationale documented in package doc (lines 60–76). | FALSE POSITIVE: CodeQL does not model the loopback guard or `isLoopbackHost`. Best-effort refactor (e.g., a custom `VerifyPeerCertificate` that calls `isLoopbackHost`) or dismiss `false positive`. Note: Satisfactory regenerates cert on restart, so pinning has a lifecycle cost. |
| **Satisfactory test harness (test/e2e)** | test/e2e/internal/satisfactory/app.go:188 | NO | None | `queryServerState` sets `InsecureSkipVerify: true` unconditionally with no loopback check on `addr`. | **REAL DEFECT**: Fix by adding an `isLoopbackHost` check or moving to a local trust manager. |

**Validation Rule**:

Any `tls.Config{InsecureSkipVerify: true}` assignment MUST be guarded by a verified loopback check OR use an alternative like `VerifyPeerCertificate` + pinning. Unconditional disabling is not permitted. CodeQL flags the assignment itself, not the guard, so the fix must eliminate the assignment in non-loopback contexts.

---

## Spec Drift Summary

The specification states:
- "14 open alerts" (correct)
- "20 Dependabot PRs (#262–#281)" (off by 1: actually 21, includes #283)
- Implicitly assumes all 14 alerts are real defects (incorrect: 13 are false positives, 1 is real)

The BRIEF.md ground truth corrects the count and categorizes each alert. This data model records that correction explicitly.
