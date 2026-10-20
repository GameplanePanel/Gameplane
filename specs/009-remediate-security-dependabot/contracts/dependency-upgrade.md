# Dependabot PR Merge Contract

## Per-PR Gate (Required Before Merge)

Every Dependabot PR MUST satisfy:

1. **All checks in our own `ci` workflow green** (`.github/workflows/ci.yaml`): `lint`, `go`, `web`, `web-e2e-mock`, `helm`, `chart-template`, `go-e2e-unit`, `e2e-buckets`, `e2e-go` (all buckets and architectures), `e2e-multicluster`, `e2e-upgrade`, `e2e-web-live`, `e2e-game-bot`, and `report`.

2. **GitHub-owned workflows are NOT merge gates**: CodeQL, Advanced Security, Copilot, Dependabot, and Dependabot Security Update workflows can fail without blocking the merge. These are known to have infrastructure flakes and are not owned by this project.

3. **No changes are required to address GitHub-owned workflow failures** — they are not indicative of actual defects in the code.

---

## Merge Mechanics

Each Dependabot PR is merged **individually**, not consolidated into an integration branch. The merge command is:

```bash
gh pr merge <PR_NUMBER> -R GameplanePanel/Gameplane --admin --merge
```

**Flag requirements**:

- `--admin`: **Required**. The master branch has a ruleset with an "update" rule that blocks a plain `gh pr merge` call. Only a `--admin` merge (using your user's authorization for green PRs) bypasses this restriction.
- `--merge`: **Required**. Use a merge commit, not squash. Merge commits preserve the PR's commit history and are reversible with `git revert` if needed; squash loses that history.

**Authorization**: You have authorized `--admin` merges for all green PRs in this repository, as per the project's merge policy.

---

## Rebase Protocol

When a merge to master occurs, it can invalidate a sibling Dependabot PR's `go.sum` entries if that PR is based on an older master state.

**Rebase workflow**:

1. After a Dependabot PR merges to master, check the next queued PR's CI run.
2. If a `go.mod`/`go.sum` conflict is detected (CI reports a tidying or sum-check error), comment on that PR:
   ```
   @dependabot rebase
   ```
3. Dependabot will automatically rebase the PR onto the new master state, re-run CI, and update the PR's head branch.
4. Once rebased CI is green, proceed to merge.

**Expected behavior**: Workspace-wide bumps (e.g., `x/mod`, `x/net`, `minio-go`) that touch 7–8 modules will cause multiple sibling PRs to require rebase. This is normal and expected; the rebase workflow is the standard recovery path.

---

## Ordering Rule (Ascending Blast Radius)

Dependabot PRs are merged in order of **ascending blast radius** (number of Go modules or projects touched), with **Go and npm running in parallel** since they touch disjoint file trees.

**Rationale**: Merging the smallest, most-isolated PRs first reduces the risk of compounding failures when a larger PR introduces an issue. Parallel tracks for Go and npm avoid sequential delays on unrelated updates.

### Merge Sequence (ordered by blast radius, with parallel tracks noted)

**Phase 1 — Smallest blast radius (1 module or 1 pkg)**

These can be merged in any order; each touches only one package or a single small module.

```
npm: #283 (SECURITY: brace-expansion + js-yaml)
npm: #280 (@types/react-dom)
npm: #278 (vitest)
npm: #277 (@vitejs/plugin-react)
npm: #275 (@types/node)
npm: #270 (@tanstack/react-router)
npm: #266 (@playwright/test)
npm: #264 (@testing-library/jest-dom)
npm: #262 (@typescript-eslint/parser)

Go:  #276 (gopacket, 2 modules: capture-sidecar, test/e2e)
```

**Phase 2 — Medium blast radius (3 modules)**

```
Go:  #279 (sigstore/cosign/v2, 3 modules: capture-sidecar, operator, test/e2e)
```

**Phase 3 — Larger blast radius (5+ modules)**

These touch multiple core modules and should be validated thoroughly after Phase 1–2.

```
Go:  #267 (chi/v5, 5 modules)
Go:  #281 (modernc.org/sqlite, 5 modules: api, capture-sidecar, mpc-server, operator, test/e2e)

Go:  #271 (k8s.io/api, 7 modules)
Go:  #274 (golang.org/x/mod, 7 modules)
Go:  #269 (golang.org/x/net, 7 modules)

Go:  #273 (minio/minio-go/v7, 8 modules)
Go:  #265 (google/go-containerregistry, 8 modules)
```

**Phase 4 — Blocked (awaiting diagnosis or deferred)**

These PRs have failing checks or require a separate gated phase and are NOT merged in this pass:

```
Go:  #263 (sigstore/sigstore 1.10.9, 3 modules) — 1 FAILING CHECK, cause not yet diagnosed
Go:  #272 (TypeScript 7.0.2, MAJOR, 4 FAILING CHECKS) — DEFERRED to separate gated phase
npm: #268 (@eslint/js 10.0.1, MAJOR, 1 FAILING CHECK) — DEFERRED to separate gated phase
```

---

## Acceptance Criterion Per PR

A Dependabot PR is considered successfully merged when:

1. The target version from the version inventory in the PR title/body is present on the master branch after the merge (e.g., `sqlite 1.57.0` appears in all affected `go.mod` files).
2. GitHub marks the PR as closed by Dependabot (usually within seconds of the merge commit being pushed).
3. The next CI run on master (triggered by the merge commit) shows all `ci` workflow checks green.

**Verification command**:
```bash
# Confirm the bumped version is in go.mod or package.json on master
git checkout master && git pull
grep "sigstore/cosign" go.mod  # example for PR #279
# or
grep '"vitest"' web/package.json  # example for PR #278
```

---

## Exception List

### Exception #263: sigstore/sigstore 1.10.9 (Go, 3 modules)

**Status**: 1 failing check; cause not yet diagnosed.  
**Action**: Deferred from this merge wave. Requires investigation of the failing check (likely a transitive dependency conflict with `sigstore/cosign` or `k8s.io/api`). Investigation may happen in parallel; if the cause is identified as a non-blocker, #263 can be merged as a follow-up once the diagnosis is documented.  
**PR #279 (cosign 2.6.5)**: Merges in Phase 3. #263 is separate and can be merged afterward if the diagnosis clears it, or skipped if it remains blocked.

### Exception #272: TypeScript 7.0.2 (npm, MAJOR, 4 failing checks)

**Status**: Major version bump with 4 failing checks; likely requires type annotations or config updates in the frontend code.  
**Action**: **DEFERRED to a separate gated phase**. This PR will be merged only after all non-major Dependabot PRs (#262–#281 except #268/#272) are on master and passing. A dedicated TypeScript 7 migration task will then be created with explicit scope and review criteria.  
**Rationale**: TypeScript major version bumps often require manual type annotation changes. This PR is intentionally isolated to avoid compounding failure modes when other packages have unrelated issues.

### Exception #268: @eslint/js 10.0.1 (npm, MAJOR, 1 failing check)

**Status**: Major version bump (drops eslintrc, drops deprecated methods, requires Node ^20.19 || ^22.13 || >=24, updates eslint:recommended, minimatch v10); 1 failing check.  
**Action**: **DEFERRED to a separate gated phase** alongside #272. ESLint 10 is a semver-major bump that may require config changes in `web/eslint.config.js` and possibly rule adjustments. Like TypeScript 7, it is merged only after all other PRs stabilize.  
**Rationale**: ESLint major bumps often introduce new rule categories or renamed rules. Isolating it prevents it from masking issues in other dependency updates.

### Exception: #283 Security (SECURITY brace-expansion + js-yaml)

**Status**: NOT originally named in the specification (spec says 20 PRs #262–#281).  
**Note**: This is a real security fix (CVE-2026-13149 / GHSA-mh99-v99m-4gvg in brace-expansion + DoS in js-yaml). #283 is green and must be merged before or alongside the first wave. It is included in Phase 1 (smallest blast radius, 1 package, `web/` only).  
**Correction**: The feature's 20-PR estimate in the spec should be 21 PRs; #283 is within scope.

---

## Summary Table

| Phase | PRs | Blast Radius | Status | Notes |
|-------|-----|--------------|--------|-------|
| 1 | #283, #280, #278, #277, #275, #270, #266, #264, #262, #276 | 1–2 modules | All green | Merge in any order |
| 2 | #279 | 3 modules | All green | Cosign; proceed after Phase 1 |
| 3 | #267, #281, #271, #274, #269, #273, #265 | 5–8 modules | All green | Core libraries; validate Phase 1–2 first |
| Blocked | #263 | 3 modules | 1 failing | Diagnosis pending; can follow-up merge if cleared |
| Gated | #272, #268 | — | 4 + 1 failing | Deferred to separate TypeScript 7 & ESLint 10 phase |

---

## Key Points for Execution

- **No integration branch**: Each PR is merged individually with `--admin --merge`, not consolidated first.
- **Rebase on conflict**: Use `@dependabot rebase` to update `go.sum` if a sibling PR's CI fails after a merge.
- **Parallel tracks**: npm PRs and Go PRs can be merged concurrently (they touch disjoint files); order within each track by blast radius.
- **All-green gate**: Do not merge on partial-green or ignored flakes; every `ci` workflow check must pass.
- **Exceptions are isolated**: #263, #272, #268 are tracked separately and do not block the main wave.

---

## T035 — PR #263 diagnosis (sigstore 1.10.8 → 1.10.9): BLOCKED, needs a code migration

Diagnosed 2026-08-29 from CI logs. This supersedes the "Diagnosis pending" row above.

**Failing check**: `lint (operator)` — run 32909850638, job 98001714918. It is the only
failing check on the PR.

**Verbatim log excerpt** (`gh api repos/GameplanePanel/Gameplane/actions/jobs/98001714918/logs`):

```
##[error]/home/runner/work/Gameplane/Gameplane/operator/internal/verify/verify.go:20:2: SA1019: "github.com/sigstore/sigstore/pkg/fulcioroots" is deprecated: Use https://pkg.go.dev/github.com/sigstore/sigstore-go@main/pkg/tuf (staticcheck)
	"github.com/sigstore/sigstore/pkg/fulcioroots"
	^
1 issues:
* staticcheck: 1
```

**Root cause**: sigstore 1.10.9 marks `pkg/fulcioroots` deprecated. `golangci-lint`'s
staticcheck raises SA1019 on the import, and per constitution Principle III the finding
cannot be silenced with `//nolint`. The affected call sites are:

- `operator/internal/verify/verify.go:20` — the import
- `operator/internal/verify/verify.go:146` — `fulcioroots.Get()`
- `operator/internal/verify/verify.go:150` — `fulcioroots.GetIntermediates()`

**Confirmed this is caused by the bump, not by a linter upgrade**: master pins
`github.com/sigstore/sigstore v1.10.8` (`operator/go.mod:22`) and its `ci` run on
`880e030` is green with the identical import present. The deprecation therefore arrives
with 1.10.9.

**Classification**: (a) a genuine incompatibility introduced by the new version — **not** a
stale `go.sum`. `@dependabot rebase` will not clear it.

**Remediation required** (a code change, not a merge): migrate `verify.go` off
`sigstore/pkg/fulcioroots` to the `sigstore-go` TUF-based API that the deprecation notice
points at. The exact replacement symbols are **not yet determined** and must be read from
upstream `sigstore-go/pkg/tuf` documentation rather than guessed — this is signing-path
code, so a wrong root-of-trust source is a security regression, not a lint fix.

**Disposition**: #263 stays open and unmerged. It is not a blocker for the rest of the
dependency wave (disjoint from every other PR's failure mode), and it should be handled as
its own unit of work alongside the other gated majors (#272, #268).

**Update (2026-08-29)**: #263 merged as commit 5291807 after T071 (PR #287) migrated verify.go off the deprecated fulcioroots API. The SA1019 blocker has been resolved.

---

## dompurify — 4 Dependabot security alerts with no possible Dependabot PR

Researched 2026-08-29 against the live npm registry; the parent chain was read from the
committed lockfile.

**Alerts** (all `web/package-lock.json`, all **runtime** scope): #9 medium (patched 3.4.13),
#2 medium (3.4.11), #4 low (3.4.12), #1 low (3.4.9). Highest patched floor: **3.4.13**.

**Why Dependabot cannot open a PR** — verified chain:

```
@monaco-editor/react ^4.7.0   (direct, production — web/package.json:18)
  └─ monaco-editor 0.56.0     (peer — web/package-lock.json ~6864)
       └─ dompurify "3.4.8"   (EXACT pin, no range — web/package-lock.json ~6870)
```

No entry in the chain carries `"dev": true`, which is why these four report runtime scope.
Dependabot cannot bump a transitive dependency whose parent pins one exact version.

**There is no clean upgrade path.** Verified live against `registry.npmjs.org`:

- `monaco-editor` latest is **0.56.0** — i.e. the version already installed — and it still
  pins `dompurify: "3.4.8"` exactly.
- `@monaco-editor/react` latest is **4.7.0** (already installed); the `4.8.0-rc.3`
  prerelease still declares only the peer *range* `monaco-editor: ">= 0.25.0 < 1"` and
  pins no dompurify version.
- `dompurify` latest is **3.4.14**.

So bumping `@monaco-editor/react` cannot clear the alerts. An npm `overrides` entry is the
only available lever short of waiting for upstream Monaco to move.

**Recommended remediation** — add to `web/package.json` as a **top-level sibling** of
`dependencies`/`devDependencies` (not nested inside either):

```json
"overrides": {
  "dompurify": "^3.4.13"
}
```

`^3.4.13` clears all four alerts (it is the highest `first_patched_version` among them) and
admits the current 3.4.14.

**Blocked on lock regeneration.** `web/package-lock.json` must be regenerated in the same
change or `npm ci` fails CI, and the lockfile must never be hand-edited. Regeneration is
`npm install` in `web/` — *not* `npm ci`, which refuses to run when `package.json` and the
lock disagree and so cannot apply a newly added `overrides` block. Per the project's
no-local-execution rule this cannot be done in an agent session; it needs the maintainer to
run `npm install` in `web/` and commit both files together.

**Reachability — UNVERIFIED, do not treat as settled.** Monaco is embedded in
`web/src/routes/tabs/Files.tsx` (game-server config file editor) and
`web/src/routes/tabs/settings/Placement.tsx` (Kubernetes tolerations/affinity JSON). Both
edit operator/admin-supplied content rather than arbitrary user HTML, and dompurify is
reached only via Monaco's markdown-sanitizing hover/IntelliSense widgets. That suggests a
narrow surface, but whether attacker-controlled markdown can actually reach those widgets
here was **not** traced to a conclusion. Resolve this before deciding urgency; the version
remediation above is correct either way.

---

## T054 — PR #272 diagnosis (typescript 6.0.3 → 7.0.2): BLOCKED upstream

**Status**: blocked by an upstream peer-dependency constraint. No code change on our side
can clear it today. #272 stays open.

**Failing checks**: `web`, `web e2e (mock)`, `e2e web live` — all three fail at the
dependency-install step, before any TypeScript is compiled. There are no type errors to fix
because `tsc` never runs.

**Verbatim CI excerpt** (job 99082482156, run 33245283178):

```
npm error code ERESOLVE
npm error ERESOLVE could not resolve
npm error While resolving: @typescript-eslint/eslint-plugin@8.65.0
npm error Found: typescript@7.0.2
npm error   dev typescript@"^7.0.2" from the root project
npm error Could not resolve dependency:
npm error peer typescript@">=4.8.4 <6.1.0" from @typescript-eslint/eslint-plugin@8.65.0
npm error Conflicting peer dependency: typescript@6.0.3
```

**Root cause**: `@typescript-eslint/*` declares `peerDependencies.typescript` as
`>=4.8.4 <6.1.0`, which excludes all of TypeScript 7.

**No upgrade path exists yet.** Verified live against `registry.npmjs.org` on 2026-08-29 —
every published dist-tag of `@typescript-eslint/eslint-plugin`:

| dist-tag | version | peer `typescript` |
|---|---|---|
| `latest` | 8.68.0 | `>=4.8.4 <6.1.0` |
| `canary` | 8.68.1-alpha.6 | `>=4.8.4 <6.1.0` |
| `rc-v8` | 8.0.0-alpha.62 | (none declared) |

So even the canary channel still caps at TypeScript 6. Bumping the plugin cannot help, and
the alpha `rc-v*` tags are pre-release lines that declare no peer at all — not a basis for a
production toolchain.

**Rejected workarounds**: `--legacy-peer-deps` / `--force` would install a plugin whose
type-checking internals genuinely do not understand TypeScript 7's AST, silently degrading
lint and type diagnostics. An `overrides` entry pinning the peer would do the same. Neither
is a fix; both trade a loud failure for a silent one.

**Disposition**: #272 stays open and unmerged pending a `@typescript-eslint` release that
declares TypeScript 7 support. This is the same shape of blocker as #263 — an upstream
dependency that has not yet caught up — and is tracked as an exception, not a defect in this
repo. T055/T056 cannot proceed until upstream ships.

---

## T057 — PR #268 (@eslint/js 9.39.5 → 10.0.1): NOT blocked; premise was stale

The task text assumed a failing check. Re-queried on 2026-08-29: **there is none.** All 21
status checks were `SUCCESS` or `SKIPPED` (run 33245582221) and the PR was `MERGEABLE`.

The three ESLint 10 hazards the task flagged were each checked and none applies here:

- **eslintrc support dropped** — not applicable; `web/` already uses flat config
  (`web/eslint.config.js`).
- **Deprecated `SourceCode` / rule-context methods removed** — not applicable; the project
  ships no custom ESLint rules, only configuration.
- **Node floor raised to `^20.19 || ^22.13 || >=24`** — satisfied; CI runs Node 24.

**Disposition**: merged 2026-08-29 via `--admin --merge` (T059). T058 (the migration work)
is a no-op — there was nothing to migrate.

---

## T072 — dompurify: the planned `overrides` fix would be a FALSE remediation (2026-08-29)

The earlier analysis in this file recommended adding `"overrides": { "dompurify": "^3.4.13" }`
and treated the open question as "does dompurify ship in the bundle?". That question is now
answered, and the answer invalidates the recommendation.

### What was verified

Resolved against the **locked** `monaco-editor@0.56.0`, fetched from `registry.npmjs.org`
rather than from `node_modules` — the local `node_modules` is stale (dated 2026-07-26, holding
monaco 0.55.1 and dompurify 3.2.7 against a lock naming 0.56.0 and 3.4.8), so it is not
evidence of anything.

1. **monaco-editor 0.56.0 VENDORS DOMPurify.** The tarball contains
   `package/esm/vs/base/browser/dompurify/dompurify.js`, whose license header reads
   `DOMPurify 3.4.8`.
2. **The vendored copy is what monaco uses.** `domSanitize.js:3` imports it by relative path:
   `import purify from './dompurify/dompurify.js';`
3. **The npm package is never imported.** A grep for `from 'dompurify'` across the whole of
   `package/esm/` in 0.56.0 returns nothing. monaco declares `dompurify: 3.4.8` in its
   `dependencies`, but no code imports it by bare name.
4. Nothing in `web/src` imports dompurify either, and only `monaco-editor` depends on it in
   `web/package-lock.json`.

### Why the override is worse than doing nothing

All four advisories cover the vendored version:

| Alert | GHSA | Vulnerable range | First patched |
|---|---|---|---|
| #9 medium | GHSA-55q2-fjhq-7xh7 | `<= 3.4.12` | 3.4.13 |
| #4 low | GHSA-c2j3-45gr-mqc4 | `<= 3.4.11` | 3.4.12 |
| #2 medium | GHSA-cmwh-pvxp-8882 | `<= 3.4.10` | 3.4.11 |
| #1 low | GHSA-vxr8-fq34-vvx9 | `< 3.4.9` | 3.4.9 |

The vendored copy is **3.4.8**, inside all four ranges. An `overrides` entry raises the npm
package — which never ships — and leaves the vendored 3.4.8 exactly where it is. Dependabot
would mark all four alerts resolved while the bundled code is unchanged. That is a false
negative manufactured on purpose, and it is worse than an open alert, which at least tells
the truth.

### Actual exposure

Low, but not provably zero. The vendored sanitizer is reached through
`markdownRenderer.js` -> `domSanitize.js`, i.e. Monaco's markdown-rendering surfaces (hover,
IntelliSense, signature help). Verified on master: `web/src` registers **no**
`registerHoverProvider`, `registerCompletionItemProvider` or `registerSignatureHelpProvider`,
constructs no `MarkdownString`, and calls no `setSchemas` / `setDiagnosticsOptions` /
`loader.config` / `useMonaco` / `beforeMount` / `onMount`. There are two mount points:
`routes/tabs/settings/Placement.tsx:108,132` (both `language="json"`) and
`routes/tabs/Files.tsx:362` (`guessLang`). Monaco's own built-in language features could still
surface a markdown tooltip, so this is "no provider we register feeds it", not "unreachable".

Planting content also already requires privilege: the file write/upload routes are gated at
**operator+** in `api/internal/ws/dialer.go`, while reads are viewer+.

### The only real fix

Upgrade `monaco-editor` once upstream ships a release vendoring >= 3.4.13. **0.56.0 is
currently the latest release**, so there is nothing to upgrade to today. No npm-level action
can reach the vendored file.

### Disposition — decided 2026-08-29

My recommendation was to leave the four open as an honest signal. **The maintainer decided to
dismiss them instead**, and all four (#1, #2, #4, #9) were dismissed via the Dependabot API
with `dismissed_reason=not_used`. Open Dependabot alerts: 0.

That reason is accurate for what the alerts actually flag — the npm `dompurify` package, which
is never imported and does not ship. The dismissal comment states the caveat explicitly so the
record is not misleading:

> npm dompurify is never imported: monaco-editor 0.56.0 vendors its own copy
> (esm/vs/base/browser/dompurify/dompurify.js) via a relative import, so it does not ship.
> Caveat: the vendored copy is also 3.4.8, fixable only by a monaco upgrade.
> See specs/009 dependency-upgrade.md

**Residual risk, tracked not closed**: the vendored 3.4.8 sits inside all four advisory ranges
and no npm-level action can reach it. Upgrade `monaco-editor` when upstream vendors >= 3.4.13
(0.56.0 is the latest release today), and revisit sooner if any markdown-rendering provider is
ever registered in `web/src`, since that is what would make the sanitizer reachable.

Note also that `dismissed_comment` is capped at **280 characters** on the Dependabot API, the
same as the code-scanning API.
