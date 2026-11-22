# Feature 009 — Phase 0 Research Consolidation

## Overview

Feature 009 addresses 14 open CodeQL security scanning alerts across the Gameplane codebase and integrates 21 open Dependabot pull requests for Go and npm dependency updates. This research consolidates the triage findings, remediation strategies, and integration sequencing settled in user decisions (2026-08-28).

---

## Decisions

### D-001: Triage Outcome — 13 False Positives, 1 Real Defect, 1 Latent Gap

**Decision**: The 14 open CodeQL alerts comprise:
- **13 false positives**: sites where existing in-code guards or caller-side validation prevent the flagged data flow, but CodeQL's sanitizer models do not recognize the pattern as a barrier.
- **1 real defect**: `test/e2e/internal/satisfactory/app.go:188`, unconditional `InsecureSkipVerify: true` in `queryServerState` with no loopback guard.
- **1 known latent gap** (not currently alerted by CodeQL): `agent/internal/rcon/websocket.go` line ~292 dials a WebSocket with no netguard policy; this is defense-in-depth since the input is admin-controlled via the GameServer CRD.

**Rationale**: CodeQL's built-in sanitizer models are conservative. When a guard is applied at the caller, outside the guarded function, or in a form CodeQL's model does not explicitly recognize (e.g., a custom validation function that is not in the standard library), the alert may fire even if the data flow is actually blocked. Conversely, the absence of an alert does not mean the code is secure — it means the analysis did not find a matching taint source and sink. Manual review confirms the latent gap.

**Alternatives considered**: Dismissing all 14 immediately as false positives without code changes. This was rejected because refactoring the true false positives into recognizable barrier patterns strengthens the codebase for future analysis rounds and removes the noise that drowns out future real alerts.

| # | Rule | Site | Verdict | Reasoning |
|---|---|---|---|---|
| 1 | clear-text-logging | api/internal/kube/watch.go:40 | FALSE POSITIVE | Taint source is the *string variable* `secretKey` (its value is a field name like `"kubeconfig"`), not secret bytes. Kubeconfig bytes never enter a log call. |
| 2 | clear-text-logging | api/internal/kube/watch.go:54 | FALSE POSITIVE | Same taint source and path as #1, in the UpdateFunc branch. |
| 3 | disabled-certificate-check | agent/internal/rcon/satisfactory.go:199 | FALSE POSITIVE | Conditional guard `if isLoopbackHost(host)` (lines 220–226) gates the `InsecureSkipVerify = true` assignment; loopback check accepts only literal `"localhost"` or parsed loopback IP. CodeQL does not analyze surrounding `if` conditions. Rationale documented in package comment (lines 60–76): Satisfactory generates self-signed certs with no CA supply mechanism; dial is confined to pod-local. |
| 4 | disabled-certificate-check | test/e2e/internal/satisfactory/app.go:188 | **REAL DEFECT** | `InsecureSkipVerify: true` set unconditionally in `queryServerState` with no loopback guard on `addr`. No surrounding conditional protects this assignment. |
| 5 | request-forgery | agent/internal/mods/mods.go:405 | FALSE POSITIVE | HTTP client is `netguard.HTTPClient(2*time.Minute, netguard.IsPublic)` (~line 758) with `CheckRedirect` capping at 5 hops and re-validating `hostAllowed` per hop. Callers validate scheme (http/https only) and pre-dial `hostAllowed(u.Hostname(), h.allowed)` against module's CRD allowlist. Guard is in caller, not recognized by CodeQL. |
| 6 | request-forgery | api/internal/ws/dialer.go:281 | FALSE POSITIVE | URL validation at lines 252–256: `isDNS1123Label(ns) && isDNS1123Label(name)` rejects non-DNS characters. URL built via `url.URL` struct with hardcoded `https` scheme and in-cluster host from `agentHost()`. Identical check in `wsProxy` at 176–179. CodeQL does not follow custom validators. |
| 7 | zipslip | agent/internal/mods/mods.go:508 | FALSE POSITIVE | Function `unzipInto` (497–553) has two-stage check: pre-check at 511 skips entries where `filepath.Join(dstClean, filepath.Clean(f.Name))` escapes `dstClean`; post-clean re-check at 527–532 returns error on zip-slip. First pass uses `continue` rather than error return, allowing tainted path to flow on one branch. |
| 8 | path-injection | agent/internal/mods/mods.go:389 | FALSE POSITIVE | Function `removeEntry` (381–385): `Clean+Join+Clean+HasPrefix` guard in-function. Caller `remove()` runs `safeName(name)` at line 586. Guard is duplicated across boundary. |
| 9 | path-injection | agent/internal/mods/mods.go:391 | FALSE POSITIVE | Same guard as #8, applied to `os.Remove()` instead of `os.RemoveAll()`. |
| 10 | path-injection | agent/internal/mods/mods.go:446 | FALSE POSITIVE | `os.Rename(tmpName, filepath.Join(h.dir, name))` in `download()`. Caller-side `safeName()` validation at lines 196 (in `install()`) and 306 (in `upload()`), but not in `download()` itself. CodeQL does not follow caller-applied guards. |
| 11 | path-injection | agent/internal/mods/mods.go:486 | FALSE POSITIVE | `os.RemoveAll(final)` in `swapInArchive` (478–482): `Clean+Join+Clean+HasPrefix` guard in-function. Caller passes `archiveFolderName(safeName(...))`. Guard applied before use, not recognized. |
| 12 | path-injection | agent/internal/mods/mods.go:490 | FALSE POSITIVE | `os.Rename(staging, final)` in `swapInArchive`. Same guards as #11, applied after line 482 prefix check. |
| 13 | path-injection | agent/internal/mods/mods.go:594 | FALSE POSITIVE | `os.Stat(target)` in `remove()`. Guard is `safeName()` at line 586, then `Clean+Join+Clean` at 591–593 in the same function, before the `Stat()` call. Guard is recognized within the same function boundary. |
| 14 | uncontrolled-allocation-size | api/internal/audit/audit.go:834 | FALSE POSITIVE | `out := make([]Event, 0, limit)` inside `(*Auditor).Page()`. Clamp at lines 820–822: `if limit <= 0 \|\| limit > 500 { limit = 100 }` runs before allocation in the same function. Handler `api/internal/handlers/audit.go:25` parses the raw value with `strconv.Atoi` and does not clamp. Taint reaches this function unclamped; clamp is in this function before use. CodeQL may not treat variable reassignment as a barrier. |

---

### D-002: Remediation Policy — Refactor-to-Recognized-Barrier First, API Dismissal as Fallback

**Decision**: For each false-positive alert, best-effort refactor the site into a shape that CodeQL's built-in sanitizer models recognize as a barrier. When refactoring is not feasible (the taint source cannot be removed, the guard logic is orthogonal to what CodeQL models, or a real security practice conflicts with CodeQL's expectations), dismiss the alert via the GitHub code-scanning API (`PATCH /repos/{owner}/{repo}/code-scanning/alerts/{n}`) with `state=dismissed`, `dismissed_reason=false positive`, and a written justification documented in the feature's `contracts/` directory.

Never add an in-source suppression directive (`//nolint`, `//#nosec`, `// eslint-disable-next-line`, `// @ts-ignore`).

**Rationale**: Constitution Principle III forbids in-source suppressions — they hide defects from a future reader of the code, require ongoing maintenance as the code evolves, and signal to other agents that suppression is the acceptable response to linter flags. GitHub's code-scanning dismissal mechanism is repository-scoped metadata with an audit trail, reviewable in the GitHub UI (`/security/code-scanning` tab → each alert's timeline), and does not suppress the finding for local analysis runs or future readers working from the source checkout. A dismissal is a decision by maintainers, recorded with justification, not a silence baked into the source.

Refactoring is preferred where possible because it genuinely fixes the underlying code shape, making it more maintainable and clearing the alert without ongoing maintenance of a dismissal record.

**Alternatives considered**: 
- Dismiss all 14 immediately (rejected: misses opportunities to improve the codebase and validate CodeQL against real barriers).
- Fix all 14 even where refactoring is not feasible (rejected: some fixes — e.g., removing `InsecureSkipVerify` from Satisfactory cert-pinning — are unworkable).

---

### D-003: Refactoring Strategy — Per-Alert-Family Barriers

**Decision**: The remediation approach is tailored to each CodeQL query and its false-positive pattern:

#### Path-Injection Alerts (#8, #9, #10, #11, #12, #13)

Currently, `safeName()` (agent/internal/mods/mods.go:625–645) validates filenames but its result is often used in the caller (e.g., line 196 in `install()`, line 306 in `upload()`), separate from the function that operates on the path. CodeQL does not follow sanitizers applied in the caller.

**Proposal**: Extract a unified path-confinement helper:

```
func ConfinePath(rootDir, untrustedName string) (string, error)
```

This helper takes:
- `rootDir`: the trusted sandbox directory
- `untrustedName`: the untrusted user input

It returns:
- A validated absolute path guaranteed to be within `rootDir`, or
- An error if the name is invalid or escapes the root

Implementation basis: `agent/internal/files/files.go:57–98` `resolve()` is the strongest existing pattern — it applies `filepath.Clean`, `filepath.EvalSymlinks` on both the target and the deepest existing ancestor, and a prefix check. The new helper should do the same, possibly reusing `resolve()` internally.

**Effect**: Callers pass untrusted input to `ConfinePath()`, get back a fresh string known to be confined, and use that result directly. Validation and use are now in the same function boundary, visible to CodeQL.

**Sites affected**: #8 `removeEntry`, #9 `removeEntry` (same fix), #10 `download`, #11 `swapInArchive`, #12 `swapInArchive` (same fix), #13 `remove`.

#### Zip-Slip Alert (#7)

Function `unzipInto` (agent/internal/mods/mods.go:497–553) has two-stage validation:
1. Pre-check at line 511: skip (via `continue`) entries whose resolved path escapes `dstClean`.
2. Post-clean re-check at lines 527–532: return error on zip-slip.

The first pass uses `continue`, so a tainted path still flows into the rest of the loop body for one iteration before being rejected.

**Proposal**: Single-exit validation that errors on any escaping entry, replacing the `continue` with an error return.

**Effect**: No tainted path flows past the first check.

#### Uncontrolled-Allocation Alert (#14)

Function `(*Auditor).Page()` (api/internal/audit/audit.go:820–834) clamps `limit` in place at lines 820–822, then allocates at line 834. The handler (api/internal/handlers/audit.go:25) parses `strconv.Atoi` without clamping, so the untrusted value reaches the auditor unclamped. CodeQL may not recognize a variable reassignment as a barrier.

**Proposal**: 
1. Clamp into a NEW variable (or introduce a small named constant/helper returning a bounded value), not by reassigning `limit` in place.
2. Additionally, clamp at the handler layer (api/internal/handlers/audit.go:25) so untrusted input is bounded as early as possible.
3. Introduce a named constant (e.g., `MaxAuditPageSize = 500`) to replace the inline `500` literal.

**Effect**: A fresh variable carries the known-bounded value through to the allocation, signaling to taint analysis that the value is controlled.

#### Request-Forgery Alerts (#5, #6)

Both sites pre-validate the destination URL or host:
- #5 uses `netguard.IsPublic` to validate against private/loopback ranges, but CodeQL rarely accepts a dial-time IP guard as a barrier for this query.
- #6 validates hostname against `isDNS1123Label`, hardcodes `https`, and builds the URL through the `url.URL` struct.

CodeQL's request-forgery query is conservative and often requires the untrusted value to be rejected at parse time (before becoming a `*url.URL`) or validated against an allowlist in CodeQL's model. Custom allowlists and custom validators are not recognized.

**Proposal**: These are the most likely candidates for dismissal. Document the rationale (custom validator not in CodeQL's model) and dismiss as `false positive` with full justification. A real fix — e.g., using an allowlist from a configuration struct and encoding it in a way CodeQL can statically analyze — is out of scope for this feature.

**Alternatives considered**: Introduce a CodeQL-recognized allowlist primitive or refactor to use only values from an allowlist struct; both add surface area for marginal benefit on false positives already well-guarded in practice.

#### Clear-Text-Logging Alerts (#1, #2)

The taint source is a *string variable named `secretKey`*, whose actual value is a field name like `"kubeconfig"`, not secret data. CodeQL's heuristic source detection flags variables with "secret" in the name.

**Proposal**: Rename the variable to remove the "secret"-naming heuristic (e.g., `kubeconfigField` or `secretDataKey`), and rename related fields if needed for consistency. Document that this is a cosmetic fix addressing CodeQL's heuristic source detection, not a real security leak — the surrounding logging is already safe.

**Effect**: CodeQL's heuristic source no longer fires; the alert clears. This is honest about what the fix is: it addresses a false-positive pattern, not a real vulnerability.

**Rationale**: Renaming is lower-friction than refactoring the logging path, and it acknowledges that CodeQL's heuristic is the problem, not the code's actual safety.

#### Disabled-Certificate-Check Alert (#3)

The code sets `InsecureSkipVerify = true` conditionally inside `if isLoopbackHost(host)`. CodeQL's query flags the assignment itself and does not analyze the surrounding conditional. Cert pinning is unworkable because Satisfactory regenerates its self-signed cert on every restart, making the pin invalid after restart.

**Proposal**: This alert is a planned dismissal. Document the rationale: loopback-only dial, Satisfactory's cert lifecycle, and CodeQL's limitation in not modeling the surrounding conditional. Mark as `dismissed_reason=false positive`.

**Alternatives considered**: Remove `InsecureSkipVerify` entirely and use a custom `VerifyPeerCertificate` with pinning. Rejected: Satisfactory regenerates certs on restart, so a pin is ephemeral and unreliable.

---

### D-004: The One Real Fix — Add Loopback Guard to Test E2E Satisfactory

**Decision**: Alert #4 (`test/e2e/internal/satisfactory/app.go:188`) is a genuine unguarded `InsecureSkipVerify: true` assignment inside `queryServerState`, with no loopback check on the `addr` parameter.

**Remediation**: Add a loopback guard identical to the one in `agent/internal/rcon/satisfactory.go:220–226`, so the TLS config only disables verification for localhost/loopback addresses:

```go
tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
if isLoopbackHost(addr) {
  tlsCfg.InsecureSkipVerify = true
}
// use tlsCfg
```

This fix is genuine — the code was truly unguarded — and it aligns the test harness with the production agent's defensive pattern.

**Rationale**: Unlike the production code, which runs in a controlled pod environment with admin input, the test code runs in CI and could theoretically receive an untrusted address. The fix brings it in line with production best practice.

**Alternatives considered**: Dismiss the alert as `used in tests`. Rejected because the code is genuinely unguarded; the test should be fixed to match the production pattern, not exempted.

---

### D-005: Known Latent Gap — Agent WebSocket Dial Without Netguard

**Decision**: `agent/internal/rcon/websocket.go` line ~292 (`ensureLocked`) dials `ws://<baseURL>/<escapedPw>` with no netguard policy. Input is admin-controlled via the GameServer CRD (the `spec.rcon.websocket.baseURL` field), so this is defense-in-depth rather than an active vulnerability.

**Rationale**: The CRD is only writable by admins (enforced by RBAC), so a malicious baseURL would require compromised or malicious cluster admin. A future feature (multi-cluster federation or user-provisioned game servers) might change this threat model and warrant adding a netguard check. For now, the gap is documented for future reference.

**Alternatives considered**: Add a netguard check now. Rejected: out of scope for this feature and not an active vulnerability given current RBAC.

---

### D-006: Dependabot Landing Strategy — Individual Merges with Sequencing

**Decision**: Merge all 21 open Dependabot PRs individually using `gh pr merge --admin` (merge-commit, not squash), rebasing as the main branch advances, in the following order to minimize cascading rebase churn:

1. **Smallest blast radius first** (2–3 go.mod directories):
   - #276: gopacket/gopacket (capture-sidecar, test/e2e) — 2 dirs
   - #279: sigstore/cosign/v2 (capture-sidecar, operator, test/e2e) — 3 dirs
   - #263: sigstore/sigstore (capture-sidecar, operator, test/e2e) — 3 dirs

2. **Medium blast radius** (5 go.mod directories):
   - #281: modernc.org/sqlite (api, capture-sidecar, mcp-server, operator, test/e2e) — 5 dirs
   - #267: go-chi/chi/v5 (agent, api, capture-sidecar, operator, test/e2e) — 5 dirs

3. **Workspace-wide bumps** (7–8 go.mod directories):
   - #274: golang.org/x/mod (agent, api, capture-sidecar, mcp-server, operator, sentinel, test/e2e) — 7 dirs
   - #273: minio/minio-go/v7 (agent, api, capture-sidecar, mcp-server, operator, sentinel, telemetry-receiver, test/e2e) — 8 dirs
   - #271: k8s.io/api (agent, api, capture-sidecar, mcp-server, operator, sentinel, test/e2e) — 7 dirs
   - #269: golang.org/x/net (agent, api, capture-sidecar, mcp-server, operator, sentinel, test/e2e) — 7 dirs
   - #265: google/go-containerregistry (agent, api, capture-sidecar, mcp-server, operator, sentinel, telemetry-receiver, test/e2e) — 8 dirs

4. **npm PRs in parallel with Go** (web/ only, no go.sum interference):
   - All 11 npm PRs (#280, #278, #277, #275, #270, #266, #264, #262, #283) can merge concurrently with the Go sequence above.

5. **Deferred to gated phase** (see D-007):
   - #272: typescript 6.0.3 → 7.0.2 (major)
   - #268: @eslint/js 9.39.5 → 10.0.1 (major)

**Rationale**: Each merge to master invalidates the branch-base for all other open PRs touching `go.mod` or `go.sum`. Merging smallest-blast-radius first minimizes the rebase footprint for subsequent PRs. The 7–8-module workspace-wide bumps each touch the same set of files and conflict with each other; merging them in sequence (accepting the churn) is faster than maintaining a single integration branch, and allows each PR's CI to run independently, catching issues at merge time rather than after integration.

npm PRs touch only `web/package.json` and `web/package-lock.json`, never `go.mod` or `go.sum`, so they can merge in parallel without coordinating with the Go sequence.

**Alternatives considered**:
- Integration branch: consolidate all 21 PRs on a single branch, test once, then merge. Rejected: extends development time and hides individual PR CI failures.
- Squash-merge: keep the history cleaner. Rejected: user decision 3 specifies merge-commit; user authorized `--admin` for green PRs, implying merge-commit workflow.

---

### D-007: TypeScript 7 and ESLint 10 — Separate Gated Phase

**Decision**: Defer PR #272 (TypeScript 6.0.3 → 7.0.2, major version) and PR #268 (@eslint/js 9.39.5 → 10.0.1, major version) to a second, gated phase after the first 19 PRs are merged and CI is green on master.

**Rationale**: Major version bumps carry breaking changes and require validation and potential config updates:

- **TypeScript 7**: Changes to the type system and compiler behavior; existing code may require type annotation adjustments. All frontend `.tsx` files must be re-checked under TS 7's rules.
- **ESLint 10**: Drops the legacy `eslintrc` format (the repo uses `eslint.config.js` already, so this is not a blocker), requires Node 20.19+ / 22.13+ / 24+, updates the rule set, and may flag existing patterns as new violations.

Validating these separately reduces noise during the main Dependabot wave and allows dedicated CI cycles and possible config adjustments (e.g., `web/tsconfig.json`, `web/eslint.config.js`) to be reviewed as a unit.

**Characteristics known from the brief**:
- PR #272 shows **4 failing checks** (type errors in the frontend build).
- PR #268 shows **1 failing check** (likely linting rule change or Node version issue).

These are not "already green" like the 19 non-major bumps; they require investigation and potentially code changes beyond a version bump.

**Alternatives considered**: Merge both in the main wave and fix failures on master. Rejected: concentrates risk; the 19 simpler PRs should land first to stabilize the baseline, and TS 7 / ESLint 10 fixes can be iterated independently.

---

### D-008: Spec Scope Correction — 21 PRs, Not 20

**Decision**: The feature spec states "20 Dependabot PRs" (#262–#281), but PR #283 (npm security group: brace-expansion + js-yaml) is also open and in-scope. The corrected count is **21 PRs**.

**Rationale**: PR #283 is a GitHub-flagged security update group (`SECURITY group`), addresses real CVEs (CVE-2026-13149 in brace-expansion, DoS in js-yaml), and is marked green in CI. It is not mentioned in the spec but is a legitimate part of the feature scope.

**Spec updates required**: 
- FR-021: "All 20 open Dependabot pull requests" → "All 21 open Dependabot pull requests"
- User Story 5, Acceptance Scenario 2: "all 20 Dependabot pull requests" → "all 21 Dependabot pull requests"
- Success Criteria SC-002: "100% of the 20 open Dependabot" → "100% of the 21 open Dependabot"

**Alternatives considered**: Exclude #283 as "not originally named in the spec". Rejected: scope creep of the opposite kind — omitting a real security update contradicts the feature's goal.

---

### D-009: Verification Approach — CI-Only, Post-Merge Alert Closure

**Decision**: Verification of remediation success happens entirely on CI, not locally. Specifically:

1. **Before merge**: Feature branch CI (linting, unit tests, integration tests) validates that code changes compile and do not introduce new defects.

2. **After merge to master**: CodeQL runs its default-branch analysis (`schedule: weekly` + on-push to master in `.github/workflows/ci.yaml`), re-analyzes the codebase, and reports updated alert state to the `/security/code-scanning` tab.

3. **Alert closure confirmation**: A CodeQL alert transitions to `fixed` state only when a default-branch analysis no longer reports it. This happens automatically after a code-scanning analysis on master succeeds.

Dependabot PRs are verified by their individual CI runs (lint, build, test) and by the final CI run on master after all PRs are merged.

**Rationale**: Constitution Principle VI (CI Bears the Heavy Lifting) forbids local test/lint runs. CodeQL's alert closure is a GitHub-side property (once an alert is reported as fixed by the analysis, GitHub marks it fixed in the UI). An agent cannot locally run CodeQL; only GitHub Actions does. Therefore, remediation is complete only after merged-to-master CI analysis confirms the alerts are gone.

**Timeline**: Alerts reach `fixed` status within minutes to hours of master's CI analysis completing, visible in the `/security/code-scanning` tab.

**Alternatives considered**: Local CodeQL runs to verify alerts before pushing. Rejected: violates Principle VI.

---

## Contracts

### Code-Scanning Dismissal Records

For each false-positive alert dismissed via the GitHub API, the feature's `contracts/` directory will record:
- Alert #(number)
- Rule and site
- Dismissal reason: `false positive`
- Justification: explanation of why the alert is a false positive (taint reaches the site but CodeQL does not recognize the barrier, or the guard is applied in the caller)

Example:
```
## Alert #5: go/request-forgery (agent/internal/mods/mods.go:405)

**Dismissed as**: false positive

**Justification**: HTTP client is initialized with `netguard.IsPublic`,
which validates against private/loopback ranges at dial time. Caller
pre-validates the URL scheme (http/https only) and hostname against the
module's allowlist before dialing. CodeQL does not recognize custom IP
validation or caller-side guards as barriers for this query.
```

### Dependabot Merge Log

As each PR is merged, record:
- PR number
- Bump (library name and version)
- Merge commit SHA
- Rebase comment (if any)

This log will be appended to `contracts/dependabot-merge-log.md` for audit trail and future reference.

---

## Risk & Uncertainty

### No Uncertainties Remaining

All decisions in this research are settled by the brief and user decisions. No further clarification is needed on:
- False-positive triage (13 identified and verified)
- Refactoring strategy per alert family (concrete proposals for each)
- Dependabot merge order (sequenced by blast radius)
- Deferral strategy for TS 7 / ESLint 10 (gated phase with known blockers)
- Verification approach (CI-only, post-merge)

---

## Next Steps

1. **Codegen**: Run `make generate && make manifests` if CRD changes are needed (none are expected for this feature).
2. **Phase A (Planning)**: Develop detailed task breakdown per remediation site, including commit message templates and test expectations.
3. **Phase B–C (Implementation)**: Refactor path-confinement sites, uncontrolled-allocation clamp, and add loopback guard to test satisfactory. Merge Dependabot PRs in the sequenced order.
4. **Phase D (Verification)**: Confirm CI passes on master; confirm CodeQL analysis marks alerts as fixed; confirm all 21 PRs are merged.

