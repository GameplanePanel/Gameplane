# Open Decisions: v0.3 Release Readiness Audit

### OD-031: Tailscale game forwarding (F-173) — RESOLVED 2026-10-21

Decision (maintainer reply on #722): embed `tailscale.com/tsnet` and forward both TCP and UDP from advertised tailnet listeners to the GameServer's backing Service. This explicitly ends the tunnel module's stdlib-only dependency policy. Device registration alone cannot establish readiness; the same change must provide actual tailnet/listener health. The previous Known Gaps entry referred to a decision that had never been recorded.

The bridge opens only advertised protocol/port pairs, never a subnet route. Tailnet grants/ACLs govern peer access. The node uses the existing unprivileged container context, with no NET_ADMIN or TUN device. A per-Pod EmptyDir retains identity across container restarts; Pod replacement re-registers with a reusable auth key and requires an available MagicDNS hostname. No durable PVC is introduced. Invalid or refused requested tags remain not ready rather than falling back to an untagged device. UDP queues, session counts, idle lifetimes and shutdown are bounded. See `tunnel/specs.md` and `docs/tailscale-forwarding-integration.md` for the implementation contract and integration procedure.

These values are unsettled. They are not contracts; resolve them during `/speckit-clarify` or `/speckit-plan`.

## OD-001: How the release candidate is deployed onto kubelab — RESOLVED 2026-09-23

Decision: public pre-release tag (option b). Recorded in `spec.md` Clarifications, FR-018, FR-019. Each publication needs explicit maintainer approval; kubelab's original image settings are noted for restoration.

Original question:

kubelab's live Helm release uses side-loaded images and a git module source that differ from the repository's local-development defaults. Deploying without care could repoint the registry, tag, or module source. Options: (a) side-load release-candidate images and `helm upgrade --reuse-values` with only intended overrides; (b) publish candidate images to the public registry under a pre-release tag. Needs the maintainer's choice.

## OD-002: Release-blocking severity threshold — RESOLVED 2026-09-23

Decision: every finding of any severity blocks the release; only "not a defect" or roadmap-cited out-of-scope closures are allowed. Recorded in `spec.md` Clarifications, FR-005, FR-007, SC-003, SC-009. Severity only orders the fix work.

## OD-003: Meaning of "no longer beta" — RESOLVED 2026-09-23

Decision: drop the beta suffix (v0.3.0), status wording becomes pre-v1 release, live upgrade from last beta, remaining caveats stay in the roadmap. Recorded in `spec.md` Clarifications, FR-015, FR-016. Exact README/roadmap wording is a planning detail.

## OD-004: Where findings and the report live — RESOLVED 2026-09-23

Decision: files inside this spec folder. Recorded in `spec.md` Clarifications and FR-020.

## OD-005: Upgrade baseline if kubelab runs a build newer than beta.8 — RESOLVED 2026-09-23

Decision: option (b). Snapshot the real database, reinstall kubelab's Gameplane release at public `v0.2.0-beta.8` with a fresh database, seed `audit018-` state, upgrade to the RC, verify, then restore the real database. Applies only if round 0 finds kubelab's schema is ahead of beta.8. Otherwise kubelab is simply moved to public beta.8 first. Recorded in research R6 and contracts/rc-deploy.md §2a.

Original question:

FR-015 requires the upgrade from the last beta (`v0.2.0-beta.8`) to be verified live. kubelab currently runs side-loaded images from a private tag. If that build already carries API database migrations newer than beta.8, installing public beta.8 first would be a **downgrade** over a newer schema. The migrations are forward-only (`api/internal/db/migrations/`), so this could break the API or lose data.

Round 0 checks this: compare the migration level in kubelab's API database with the highest migration shipped in `v0.2.0-beta.8`.

Options if kubelab is ahead:
- (a) Run the live upgrade from kubelab's current build to the RC, and rely on CI `e2e-upgrade` (baseline bumped to beta.8) for beta.8 → RC. This technically falls short of FR-015's "verified live".
- (b) Snapshot the database, reinstall kubelab's Gameplane release at beta.8 with a fresh database, seed it, then upgrade. Restore the real database afterwards. This is invasive to a long-lived cluster.
- (c) Temporarily provision a separate single-node cluster for the beta.8 → RC step only.

Needs the maintainer's choice. No option is assumed.

## OD-006: Real node-loss test on kubelab — RESOLVED 2026-09-23

Decision: yes, go ahead without asking again. Stop `k3s-agent` for a few minutes on a worker that holds no pre-existing stateful game server, then restart it. Eviction on a cordoned node is still tested as the drain case. Recorded in research R8 and contracts/test-resources.md.

Original question:

FR-011 asks for node-loss behaviour. Actually taking a kubelab node down affects every pre-existing workload on it, which conflicts with FR-009. The plan's default (research R8) is to test drain behaviour by cordoning a node and evicting only the `audit018-` pod. A real node loss would run only with explicit run-time approval, on a worker that holds no pre-existing stateful game server. Otherwise the row is recorded as `blocked`, with the eviction test as its alternative.

Question: may a real node loss be simulated (e.g. stopping `k3s-agent` on one worker for a few minutes)? If so, on which node and in what time window?

## OD-007: kubelab not reachable from the audit machine — RESOLVED 2026-09-23

Decision: the maintainer will fix networking on the devbox. Until `kubectl get nodes` works, work that needs no cluster goes ahead: reviews, inventory, known-bug import. Live rows wait.

Update 2026-09-23: networking fixed. `kubelab-api` now resolves to `10.43.153.36` (`kubelab-control.kubelab.svc.cluster.local`), and `kubectl get nodes` lists `kubelab-control`, `kubelab-worker-1` and `kubelab-worker-2`, all Ready, on k3s `v1.36.2+k3s1`. Live work is unblocked.

Original question:

`~/kubelab.yaml` points at `https://kubelab-api:6443`, but `kubelab-api` doesn't resolve from the devbox ("no such host"). Every live step is blocked until this is fixed. The component reviews, the inventory and the known-bug import can go ahead in the meantime.

Question: is a VPN, `/etc/hosts` entry, or different kubeconfig needed?

## OD-008: Scope of the live tamper test for audit-chain integrity — RESOLVED 2026-09-23

Decision: live tamper on `audit018-` rows in kubelab's real `audit_events` table. How to recover the chain afterwards is still open: see OD-009.

Original question:

FR-008 needs an active attempt to tamper with the audit log. The plan's default (`contracts/test-resources.md`) runs the tamper test against a **copy** of the API database snapshot, never the live database. Doing it live would mean editing rows in kubelab's real `audit_events` table, even `audit018-` ones, and that permanently breaks the real chain from that point on.

Question: is the copy-based test acceptable as the "active violation attempt"? Or do you want a live tamper, in which case how should the broken chain be recovered afterwards?

## OD-009: Recovering the audit chain after the live tamper test — RESOLVED 2026-09-23

Decision: option (a). Take a database snapshot right before each tamper and restore it straight after, then confirm `Verify` is ok again. Scope: all three tamper types, each on `audit018-` rows: UPDATE a row, DELETE a middle row, and truncate the tail. Audit events written between the snapshot and the restore are lost, so run the test in a quiet window with no other audit activity going on. Recorded in contracts/test-resources.md.

Original question:

OD-008 chose a live tamper. Once an `audit018-` row in kubelab's real `audit_events` table is edited, `Verify` reports a break from that row onwards. The chain design keeps a checkpoint and a head anchor (`api/internal/audit/audit.go:263-296`), but no documented re-anchoring procedure exists.

Options, for the maintainer to choose:
- (a) Take a database snapshot right before the tamper and restore it right after. Audit events written in between are lost.
- (b) Undo the edit by restoring the exact original row values, so the hashes match again. This works for an UPDATE but not a DELETE.
- (c) Leave the break in place and record it as a known, intentional break in `rounds.md`. The dashboard's tamper banner will then show permanently.
- (d) Add or use an admin re-anchor or checkpoint operation. That is product work and would need its own spec.

Also: is DELETE / tail-truncation in scope for the live test, or UPDATE only?

---

## Pending maintainer input from `/speckit-implement` (opened 2026-09-23)

The items below are open. Work that doesn't depend on them carries on.

### RC-TAG-2: publish `v0.3.0-rc.2` — PENDING

T057 for rc.2. The maintainer chose (2026-09-27) to cut rc.2 only after the remaining held PRs have merged. #471–#474 are in; the maintainer then chose (2026-09-27 ~20:05 UTC) to also wait for the second batch of fix PRs, #502–#517.

- **Waiting on**: the held PRs; then the rc.2 CHANGELOG PR [#496](https://github.com/ValgulNecron/Gameplane/pull/496) (branch `chore/018-rc2-changelog`, draft) being finished, merged, and green on the merge commit; then your approval to tag (FR-019).
- **SHA to tag**: the #496 merge commit (to be filled in).
- **Command after approval**: `git tag -a v0.3.0-rc.2 <sha> -m "v0.3.0-rc.2" && git push origin v0.3.0-rc.2` (annotated, unsigned, per OD-013). Then verify as in T014 and deploy as in T015 under `## rc.2` in `audit/rounds.md`.

### RC-TAG-1: publish `v0.3.0-rc.1` — APPROVED and TAGGED 2026-09-24

The maintainer approved tagging `c44cb179` (AskUserQuestion, 2026-09-24). `v0.3.0-rc.1` was created (`git tag -a`, OD-013) and pushed. T014 verification of `release.yaml` is recorded under `## rc.1` in `audit/rounds.md`.

- **Waiting on**: the rc.1 CHANGELOG PR [#423](https://github.com/ValgulNecron/Gameplane/pull/423) (T013, branch `chore/018-rc1-changelog`) being merged, CI green on the merge commit, then your approval to tag (FR-019).
- **Other open 018 PRs, which need your review before merge**:
  - [#420](https://github.com/ValgulNecron/Gameplane/pull/420): doc-version checker (T052, F-026)
  - [#421](https://github.com/ValgulNecron/Gameplane/pull/421): CLAUDE.md gp-module (T053, F-030)
  - [#422](https://github.com/ValgulNecron/Gameplane/pull/422): upgrade baseline beta.8 (T051, F-029)
- **SHA to tag**: #420, #421 and #423 merged on 2026-09-24 (#423 → `b386103e`). #422 merged too, so the target is its merge commit **`c44cb179`** (tip of `master`), which includes all four. CI on `c44cb179` is green (run 36032816629: 74/74 jobs pass after one re-run of `e2e api-auth / arm64`, which had failed on a transient proxy.golang.org download error; CodeQL passes). Waiting on the maintainer's approval to tag.
- **Command after approval**: `git tag -a v0.3.0-rc.1 <sha> -m "v0.3.0-rc.1" && git push origin v0.3.0-rc.1` (annotated, unsigned, per OD-013).

### OD-010: RC-03 leaves out the `imported` status — RESOLVED 2026-09-24

Decision: yes. An `imported` finding (copied in but not yet re-checked live) blocks the release. RC-03 now reads "zero findings in `imported`, `open`, `fixing` or `fixed-unverified`", recorded under `## Change log` in `audit/release-criteria.md`.

Original question:

`release-criteria.md` RC-03 is copied word for word from tasks.md T005: "zero findings in `open`, `fixing` or `fixed-unverified`". But data-model.md lists `imported` as a blocking status at go/no-go too, and the quickstart final-validation grep counts it. As written, a finding left `imported` would not fail RC-03.
- Question: add `imported` to RC-03? RC-03 was committed as written. Changing it now needs a dated `## Change log` line citing your decision.

### OD-011: should `hack/check-doc-versions.sh` match bare `X.Y.Z`? — RESOLVED 2026-09-24

Decision: option (c). The checker matches every bare `v?0.x.y` as well as `-(beta|rc).N`. Dependency version lines in the audited docs get a `<!-- doc-versions: dependency -->` marker, which the checker treats as allowlisted, like the historical marker. Follow-up: PR #420 (`fix/018-doc-versions-checker`) gets a new commit adding the bare pattern, the marker rule and the header comment, plus the markers on the dependency lines in the 18 audited files (mostly `docs/dependencies.md`). F-026 stays `fixing` until then.

Original question:

T052 asks the checker to recognise `X.Y.Z` as well as `X.Y.Z-(beta|rc).N`. The 18 audited docs contain about 37 bare `v0.x.y` strings that aren't Gameplane versions. Most are dependency versions in `docs/dependencies.md` (controller-runtime `v0.24.1`, `k8s.io/*` `v0.37.0`, `golang.org/x/*`). A bare pattern would flag all of them.
- What was done: PR for T052 (branch `fix/018-doc-versions-checker`) adds multi-digit `[0-9]+` and `-rc.N` only. On current master that selects exactly the same strings as before. That's enough to catch stale `0.2.0-beta.N` / `0.3.0-rc.N` strings once `appVersion` becomes `0.3.0`.
- Options for bare versions: (a) leave them unmatched and document it (current PR); (b) match bare `X.Y.Z` only in a Gameplane context (`--version`, `gameplane:`, `/gameplane/<component>:v`, `Status:` lines); (c) match all bare `0.x.y` and add a `<!-- doc-versions: dependency -->` marker to dependency lines.
- Finding F-026 stays `fixing` until this is settled.

### OD-012: no test harness for `hack/check-doc-versions.sh` (test sign-off) — RESOLVED 2026-09-24

Decision: approved. Add a fixture harness under `hack/testdata/check-doc-versions/`, with a `make` target that CI runs. It covers current-version pass, stale beta, stale rc, bare version, the historical marker, the example markers and the new dependency marker. It lands in PR #420 alongside the OD-011 change.

Original question:

T052 says to add a fixture case to the checker's existing harness, or else log the gap. No harness exists: `hack/` holds only the four scripts, and nothing under `test/` or `.github/` exercises the checker with fixtures. Adding one is a new test, which needs your sign-off (CLAUDE.md override 1).
- Question: approve a small fixture harness (e.g. `hack/testdata/check-doc-versions/` plus a `make` target run in CI)?

### OD-013: tag signing on the devbox — RESOLVED 2026-09-24

Decision: option (c), annotated unsigned tags. T014, T057 and T073 use `git tag -a v0.3.0-rc.N <sha> -m "v0.3.0-rc.N"` instead of `git tag -s`, still only after each approval. Images and the chart stay cosign-signed by `release.yaml`.

Original question:

Tasks T014, T057 and T073 use `git tag -s`. The devbox has no GPG secret key, and neither `user.signingkey` nor `tag.gpgsign` is set, so a signed tag can't be made here.
- Options: (a) you create and push each tag yourself; (b) you set up a signing key (GPG or SSH `gpg.format=ssh`) on the devbox; (c) use an annotated, unsigned tag (`git tag -a`). Images and chart are still cosign-signed by `release.yaml` either way.

### OD-014: CHANGELOG convention for release-candidate sections — RESOLVED 2026-09-24

Decision: keep the PR #423 approach. Each `## [0.3.0-rc.N]` section is a highlights summary, `## [Unreleased]` keeps the full list, and T072 moves that full list into `## [0.3.0]`.

Original question:

T013 says the `## [0.3.0-rc.1]` section "summarises the unreleased entries". The rc.1 PR reads that as follows:
- the rc.1 section is a short summary with highlights;
- `## [Unreleased]` keeps the full list, which grows until T072 moves it into `## [0.3.0]`.

`## [Unreleased]` was also missing most user-facing changes merged since beta.8 (about 60 PRs). The rc.1 PR backfills it from the merged PR bodies, and this is recorded as a docs finding.
- Question: is "summary in rc sections, full list stays in Unreleased" the convention you want? The alternative is to move the entries out of Unreleased into each rc section.

### OD-015: admin access for the live API work (T012 and every live API row) — RESOLVED 2026-09-24

Decision: option (b). The audit runs `bootstrap-admin --username audit018-admin` inside the API pod. The image's entrypoint is `/api` (`api/Dockerfile`), so the command is `kubectl exec -n gameplane-system deploy/gameplane-api -- /api bootstrap-admin --username audit018-admin`; the earlier `gameplane-api bootstrap-admin` wording was wrong (corrected 2026-09-24). The generated password is kept off-git in `~/gameplane-audit-018/admin.env` (mode 600). The user row is written directly, so there's no API audit event for its creation; record that in `rounds.md`. The account is an `audit018-` test resource and is deleted at cleanup. The other role accounts (`audit018-operator`, `audit018-viewer`, `audit018-collab`) are then created through the API as that admin.

Original question:

T012 needs "a single admin login". Later rows need admin, operator and viewer sessions. The devbox has no admin credentials, and kubelab stores none in a Secret. Only the Helm release, the agent CA and the agent client cert Secrets are in `gameplane-system`.
- Options: (a) you create an `audit018-admin` account in the dashboard, then put its password in `~/gameplane-audit-018/admin.env` (mode 600, off-git); (b) the audit runs `gameplane-api bootstrap-admin --username audit018-admin` inside the API pod, which writes a user row straight into the live DB without an API audit event; (c) you share an existing admin login (not recommended).
- Recommendation: (a).

### OD-016: reaching the API and dashboard on kubelab — RESOLVED 2026-09-24

Decision: option (a), `kubectl port-forward -n gameplane-system svc/gameplane-web 18080:80` from the devbox, with `GP=http://127.0.0.1:18080`. This matches `procedures/conventions.md`. The Secure session cookies are captured from the response headers and sent back by hand. The ingress path itself isn't exercised; that is recorded as a known gap for the report.

Original question:

The kubelab ingress host is `gameplane.local` (Ingress `gameplane-system/gameplane`), and it doesn't resolve from the devbox. The API and web Services are ClusterIP only.
- Options for API rows: (a) `kubectl port-forward -n gameplane-system svc/gameplane-api` from the devbox, which skips the ingress path; (b) an `/etc/hosts` entry for `gameplane.local` pointing at the Traefik address, which keeps the real user path.
- Also: Chrome MCP runs in your browser. Which URL should dashboard rows (T030) use?

### OD-017: no worker node is free of pre-existing game servers (node-loss test) — RESOLVED 2026-09-24

Decision: use `kubelab-worker-2`. Stop `k3s-agent` there for a few minutes (OD-006). The pre-existing `soak-pool-west-0` goes down with the node. Record the window, and check that `soak-pool-west` comes back `Running` with the same UID and PVC once the node returns. `INV-NODE-003` is no longer a blocked candidate.

Original question:

OD-006 approved stopping `k3s-agent` on "a worker that holds no pre-existing stateful game server". On 2026-09-23 both workers hold one:
- `kubelab-worker-1`: `soak-no-preference-0` and `squad-0` (`squad` is `Failed`, `ImagePullBackOff`)
- `kubelab-worker-2`: `soak-pool-west-0`
- `kubelab-control` hosts `mc-fabric-0` and `soak-bogus-pool-0`

Question: which node and time window may be used? Or should the `soak-*` servers count as non-stateful for this test? Until you answer, `INV-NODE-003` can only be `blocked`, with the cordon + eviction test as the alternative.

### OD-018: live steps blocked by this session's auto-mode safety check

On 2026-09-23, the harness's auto-mode safety check blocked a read-only `kubectl exec` into the kubelab API pod. The block holds for the rest of that session. Steps that need `kubectl exec` or `kubectl cp` into the API pod wait for a session run outside auto mode, or for you to run them:
- the DB migration level (T012)
- the `sqlite3 .backup` snapshots (T015, T048, T061)
- the tamper test (T048)

Read-only `kubectl get` / `helm get` steps still work.

Update, same session: the known-bug import (T009–T011, `audit/findings.md` F-001 to F-030) failed three times:
1. A haiku agent reported that its file writes were blocked.
2. A sonnet agent was stopped by the model's cyber safeguard (`[cyber]`, request `req_011CfMG7KiFY6yzieM3p47EJ`), most likely because of the exploit-style wording in `SECURITY_AUDIT.md` and the security-fix PR bodies.
3. A re-scoped third attempt, which referenced `SECURITY_AUDIT.md` only by section title, was refused at launch by the auto-mode safety check.

`findings.md` therefore still has only its header. T009–T011 need a session outside auto mode. Suggested scoping for that run: import the six `SECURITY_AUDIT.md` findings as title + section-reference rows only, and read PR #350 by title and file list only.

Facts gathered for T012 in the meantime:
- kubelab runs side-loaded images `gameplane-test/api:016`, `gameplane-test/operator:016` and `gameplane-test/web:016`, under a Helm release labelled chart `gameplane-0.2.0-beta.8`, revision 6.
- `v0.2.0-beta.8` ships API migrations up to `006_share_links.sql`, and `master` has up to `011_user_theme_preferences.sql`.
- So kubelab's DB is very likely ahead of beta.8 (OD-005 path (b)). The DB itself must still confirm this.

### OD-019: security findings stay unpushed until fixed — RESOLVED 2026-09-24

Decision (maintainer, 2026-09-24): "every security finding remain[s] unpushed until fixed". The maintainer then asked for the security material that had already been pushed to be removed. The branch was rebuilt without the `SECURITY_AUDIT.md` rewording commit, `audit/procedures/security.md`, `audit/evidence/rc.0/inventory-SEC.md`, the partial `audit/evidence/review-web/notes-routes.md` and the `## SEC` inventory rows, then force-pushed. Those files now live in `audit/held/`, and the reworded `SECURITY_AUDIT.md` stays uncommitted in the working tree.

How it's applied:
- A finding is a **security finding** when it concerns a security boundary or control: authentication, authorization/RBAC, login privacy, SSRF guard, console-injection guard, audit-chain integrity, secret handling, network exposure, privilege, or supply chain. This includes the `SECURITY_AUDIT.md` items not yet remediated, security candidates from code reviews, and any `INV-SEC-*` boundary that doesn't hold.
- Its row, its `### F-NNN` subsection, and its evidence live in `audit/held/` (git-ignored via the root `.gitignore` until the 2026-09-29 update below): `audit/held/findings.md` and `audit/held/evidence/<ID>/`. Review agents write security candidates to `audit/held/review-<component>.md`, never to `evidence/review-*/notes.md`.
- IDs come from the same `F-NNN` sequence, so a held finding leaves a gap in `audit/findings.md`. When its fix PR merges, the row and subsection move into `audit/findings.md` unchanged.
- Fix PRs for held findings are titled and described as hardening, without reproduction steps, until merged.
- Fixed items (for example merged security-fix PRs imported in T009) are not held.

Update (maintainer, 2026-09-29): once every held finding had an open or merged fix PR, the maintainer asked for the held records to be published. `audit/held/` was committed to this branch in 8249db80, and a `!specs/done_018-v0-3-release-readiness/audit/held` negation now follows the `**/held` rule in the root `.gitignore`; the `**/held` rule stays for future audits.

Update (2026-10-04): every held fix PR is merged; the 57 held rows moved to audit/findings.md as fixed-unverified.

### OD-020: verification tier for the opus component reviews (T045) — RESOLVED 2026-09-24

Decision: an independent opus verifier per review chunk (a fresh agent that tries to refute each candidate). The T036–T042 reviews ran on opus because sonnet reviewers were stopped by the model's safeguard in the first session. Fable is not used: the maintainer treats CLAUDE.md rule 13's fable restriction as a ban because of cost. `coverage.md` records the tier as `opus → opus (independent, OD-020)`.

### OD-021: procedure design questions from the correctness pass — RESOLVED 2026-09-24

The sonnet correctness pass (2026-09-24) fixed names, paths, headings, evidence locations and secret handling in the procedures files. These items need your call before the live rounds:

1. `agent.md`: about 27 procedures address `$GP/servers/minecraft-java/…`, but `minecraft-java` is a template, not a server. Options: (a) one shared `audit018-agent-mc` GameServer, created by a new first procedure and deleted by a last one; (b) each procedure creates and deletes its own server.
2. `agent.md` quiesce-pause / quiesce-resume: the API gateway doesn't proxy `/quiesce` or `/unquiesce` (only the operator's Backup path reaches them). Options: (a) test quiesce through a Backup of an `audit018-` server; (b) port-forward to the agent pod directly; (c) mark `n/a` as internal-only.
3. `agent.md` console-nuclearoption and console-pty (example `terraria`): the baseline shows the `nuclear-option` and `terraria` Modules `Failed`. Re-check Module health right before the round, or use another module for those transports?
4. `api.md` users-get: there is no `GET /users/{id}` route. Withdraw the row (`n/a`), or retarget to `GET /users`?
5. `helm.md` default-module-source and upload-module-source: toggling `defaultModuleSource.enabled` / `uploadModuleSource.enabled` deletes or recreates the pre-existing ModuleSources `default` / `uploads`, which the audit must never write to. Mark both `blocked` (alternative: a `helm template` check), or allow the toggle with an immediate restore?
6. `helm.md` module-signature-verification: kubelab's default source is `git`, and cosign verification applies only to OCI sources. Mark it `blocked`, or test with a separate `audit018-` OCI ModuleSource created with `kubectl` (no Helm change)?
7. `helm.md` web-dashboard-ui: `web.enabled=false` removes `svc/gameplane-web`, the port-forward target every procedure uses. Run it last, with a temporary port-forward to `svc/gameplane-api`?
8. `helm.md` existing-storage-claim: switching `api.storage.existingClaim` swaps the live API database for an empty PVC while the toggle is on. Run it in isolation with a DB snapshot, or mark it `blocked`?
9. `modules.md`: backups reference the e2e fixture Secret `e2e-restic-creds`, which kubelab doesn't have. Create one `audit018-restic` Secret and repository per round (following the naming rule), or reuse the e2e fixture name as an exception?
10. `modules.md` nuclear-option: `Automatable?` says "Partial"; the contract allows only `yes` or `no`. Which one?
11. `nodes.md` drain: the check that recovered pods land on a different node can fail on a correct recovery (no anti-affinity; the node is uncordoned). Drop that assertion, or keep it with a "same node is acceptable after uncordon" note?
12. `web.md`: about 75 procedures propose the bucket `api`, which doesn't exist in `test/e2e/buckets.sh`. Map each to an existing bucket, or add a new bucket?
13. `web.md` users-manage-service-accounts and users-manage-oidc-providers: both tabs are placeholders ("tracked for v1.1"). Withdraw as `n/a` with a `docs/roadmap.md` citation?
14. `web.md` servers-filter-by-namespace: kubelab has one games namespace. Create an `audit018-` namespace with one server, or mark it `blocked`?
15. Round setup: which role does `audit018-collab` get? The API's user create always adds a cluster-wide binding for the primary role, so "collaborator only" access may need a role with no permissions (created as `audit018-norole`). Round teardown, not `api.md`, now deletes `audit018-collab` and `~/gameplane-audit-018/collab.env`.
16. `crd.md` backups (INV-CRD-015..023) need a restic repository, which kubelab doesn't have. Use an `audit018-restic` restic-server Deployment (based on `test/e2e/fixtures/restic-server.yaml`), or an external bucket? This is the same choice as item 9.
17. `crd.md` NetworkCapture rows INV-CRD-031..035 need `capture.enabled=true`, a per-round Helm override that is restored afterwards. Approve it, or mark those rows `blocked`?
18. INV-CRD-034 (sidecar crash): the capture image is distroless, so `kill 1` can't run. Count deleting the game pod (the controller reports PodRestarted) as the test, or mark the row `blocked` with that as the closest alternative?
19. INV-CRD-029/030 (Cluster CR): which remote cluster should `audit018-cluster-1` register? Options: kubelab's own kubeconfig (its API address must be reachable from the operator pod), a second test cluster, or `blocked`.
20. INV-CRD-036/037 (VolumeSnapshot restore): k3s local-path has no CSI snapshots. Mark `blocked` unless a snapshot-capable driver is installed?
21. INV-CRD-026 (unsigned module): which registry location should hold the unsigned test bundle?
22. Inventory titles vs code: INV-CRD-012 says "Delete game server with finalizer", but GameServers have no finalizer. INV-CRD-020 names a `Resuming` phase that is never set, and the GameServer `Stopped` phase is never set either. Retitle the rows, or record the unused enum values as a finding?
23. Fixtures: (a) `audit018-test-template` copies `minecraft-java`, whose Module was stuck `Pulling` in the baseline; (b) the crash-loop fixture pulls `busybox:1.36` from Docker Hub; (c) modulesource-oci-sync needs egress to ghcr.io; (d) wake-on-connect needs a Minecraft client or ping tool on the devbox. OK as is?
24. gameserver-create-from-template expects Running "within 2 minutes"; a first Minecraft boot (image pull and world generation) can take longer. What bound?

**Resolution (maintainer, 2026-09-24):**

1. Each agent procedure creates and deletes its own `audit018-` server (no shared server).
2. Test quiesce through a Backup of an `audit018-` server, and check the save sequence in the agent log.
3. Re-check the `nuclear-option` and `terraria` Modules before the round. If they are still Failed, root-cause and fix them and file a finding. Don't switch modules to avoid the bug.
4. Retarget users-get to `GET /users`.
5. default-module-source and upload-module-source: `helm template` check only. The live toggle is blocked.
6. and 21. Module signature: an in-cluster `audit018-registry` plus an `audit018-` OCI ModuleSource created with kubectl. Push one signed and one unsigned bundle; the unsigned one must be rejected.
7. web-dashboard-ui runs last, through a temporary port-forward to `svc/gameplane-api`, then `web.enabled` is restored.
8. existing-storage-claim runs alone at the end of the round against an `audit018-` PVC: DB snapshot before, revert, snapshot-diff after.
9. and 16. Restic: an `audit018-restic` restic-server (from `test/e2e/fixtures/restic-server.yaml`) plus an `audit018-restic` Secret per round, both removed at teardown.
10. nuclear-option `Automatable?` is `yes` (bucket `bot-heavy`), with a note that the client join is manual.
11. Drain: assert the pod is Running on another node while the drained node is still cordoned, then uncordon.
12. Add a new Go e2e bucket for the web.md procedures that currently say `api` (new `buckets.sh` entry plus a CI job). This is its own task.
13. The service-accounts and OIDC placeholder tabs are `n/a`, with a `docs/roadmap.md` citation.
14. Create an `audit018-games2` namespace with one small server per round, both removed at teardown.
15. `audit018-collab`: primary role `audit018-norole` (no permissions) plus a collaborator grant on one `audit018-` server. Teardown removes all three.
17. `capture.enabled=true` is approved as a per-round override. Record it in rounds.md, restore it, and snapshot-diff.
18. INV-CRD-034: deleting the game pod mid-capture (the controller reports PodRestarted) counts as the crash test.
19. INV-CRD-029/030 (Cluster CR): blocked. Home-cluster-only routes answer 501 for a remote cluster (#430).
20. INV-CRD-036/037: install a snapshot-capable CSI driver (csi-driver-host-path plus the snapshot controller) on kubelab for the round, then remove it.
22. Retitle INV-CRD-012/020 to match the code, and file an S4 finding for the `Resuming`/`Stopped` phases that are never set.
23. Fixtures:
    - (a) Fix the `minecraft-java` Module stuck in Pulling (check it against F-258/#445), then copy it.
    - (b) The crash-loop fixture uses `busybox:1.37.0`, the operator's config-init image.
    - (c) kubelab reaches ghcr.io, so modulesource-oci-sync runs live.
    - (d) The devbox has no Minecraft ping tool. Use the repo's full Minecraft client (the e2e game bot) for wake-on-connect.
24. A cold first boot must reach Running within 10 minutes. Record the actual boot time.

**Follow-up decisions (maintainer, 2026-09-27):** item 12's new bucket is named `web-api`, and it gets built when the first test for those procedures is written. Item 13 cites a new `docs/roadmap.md` entry (#497). A code finding from item 14 was filed as F-263 (S3).

Fixes that follow from the code and need no decision are queued as work, not questions: the `api.md` reset-password body (the handler requires a new password in the request), the order of `api.md` servers-delete relative to the procedures that still need that server, shares on an `audit018-` server instead of `mc-fabric`, the `helm.md` network-policies step that checks a label the chart never sets, `nodes.md` cleanup of the `kubectl debug` node pod, the `upgrade.md` reinstall command after `helm uninstall --keep-history` (checked against kubelab's Helm version before running), and `agent.md` evidence steps that print to the terminal instead of saving a file. The `crd.md` pass is re-running on opus (the sonnet pass was stopped by its safeguard).

### OD-022: coverage rows for `.github/actions/` and `images/` — RESOLVED 2026-09-24

Decision: option (a). Add `.github/actions/` and `images/` rows to `coverage.md` and to contracts/audit-records.md, and review both components (T042 scope).

The tier-up check of the findings import (2026-09-24) found findings whose component has no `coverage.md` row. The data model says a finding's component comes from the coverage table.
- F-006 and F-013 are in `.github/actions/` (the CI cluster-dump action). The table only has `.github/workflows/`.
- F-017 and part of F-012 are in `images/` (the shared base images, for example `images/common/steamcmd/Dockerfile`). Neither the table nor contracts/audit-records.md lists `images/`.

Options: (a) add `.github/actions/` and `images/` rows to `coverage.md` and the contract, which also means reviewing both (T042 scope); (b) remap: `.github/actions/` counts under the `.github/workflows/` row and `images/` under `modules/`, with each finding's Note saying so. Until this is decided, the findings keep their real paths as the component.

### OD-023: OD-005 path (b) would delete kubelab's game servers — RESOLVED 2026-09-24

Decision: option (e). Fix F-212 first (a keep policy on the games namespace in the chart), ship it in an RC, then run the beta.8 reinstall on kubelab with that RC's chart. The DO NOT RUN banner in `procedures/upgrade.md` stays until that RC is deployed and the namespace carries the keep policy. The F-212 fix is the first fix wave.

T012 found kubelab's API database at migration 011, ahead of beta.8's 006, so OD-005 picks path (b): reinstall at public beta.8 with a fresh database. The T045 verification then kept F-212 (S1): the chart's games Namespace is an ordinary release resource with no `helm.sh/resource-policy: keep`, and GameServers carry no finalizer. So `helm uninstall gameplane` deletes `gameplane-games` and every GameServer, StatefulSet and `<gs>-data` PVC in it, including the pre-existing `mc-fabric`, `soak-bogus-pool`, `soak-no-preference`, `soak-pool-west` and `squad`. `procedures/upgrade.md` baseline-beta8 step 3 does exactly that, and now carries a DO NOT RUN banner.

Options:
- (a) OD-005 option (a): upgrade kubelab from its current build to the RC live, and rely on CI `e2e-upgrade` (baseline beta.8, PR #422) for beta.8 → RC.
- (c) OD-005 option (c): a separate throwaway single-node cluster for the beta.8 → RC step.
- (d) Annotate kubelab's `gameplane-games` Namespace with `helm.sh/resource-policy: keep` before the uninstall. This writes to a pre-existing object, which the audit rules forbid without your OK. Test on a copy first.
- (e) Wait for the F-212 fix (a keep policy in the chart), ship it in an RC, and use that.

Related (verified, same chunk): F-214 (C-charts-gameplane-03) shows `helm upgrade --reuse-values` from 0.2.0-beta.8 fails to render unless the stored values already contain the `capture` and `operator.gameDataStorage` keys. T015's rc deploy (contracts/rc-deploy.md §2) needs those keys passed explicitly.

### OD-024: sign-off to start the fix waves (T055) — RESOLVED 2026-09-24

Decision: option (a), the whole plan is approved, public and held. Every PR still needs the maintainer's review before merge (ruleset `protect main`).

T050 produced the fix plan: [audit/evidence/rc.1/fix-plan.md](audit/evidence/rc.1/fix-plan.md), checked one tier up. It groups the git-bound findings into `fix/018-*` branches, S1 first, each with its regression test. 25 imported findings are listed to re-check live first (T049), and the 4 with open PRs (#420–#423) are listed as in flight. A separate plan for the held security findings is in `audit/held/fix-plan-held.md` (off-git, OD-019).

Every fix branch changes production code and adds tests, which needs your sign-off (CLAUDE.md override 1). Options: (a) approve the whole plan; (b) approve by severity (for example S1 and S2 now, S3/S4 later); (c) approve group by group. Nothing is closed as `not-a-defect` or `out-of-scope` without you: the plan proposes none.

### OD-025: design-first fix groups are done on another machine — RESOLVED 2026-09-24

Decision (maintainer): Pencil screenshots don't work on this devbox, so the fix groups that need a `design.pen` pass before any React work are done by the maintainer on a machine where screenshots work. The devbox sessions skip them entirely: no `design.pen` edits and no React for these groups. From [audit/evidence/rc.1/fix-plan.md](audit/evidence/rc.1/fix-plan.md) that is groups 3 (`fix/018-web-file-overwrite-guard`), 7 (`fix/018-web-tunnel-wizard-flow`), 12 (`fix/018-web-settings-lifecycle`), 13 (`fix/018-web-server-create-placement`) and 36 (`fix/018-web-ui-polish`). No held group needs a design pass. The other web groups are plumbing or logic fixes with no design change, and stay in the devbox fix waves.

### OD-026: how the tunnel supervisor learns playit's assigned address (F-174) — RESOLVED 2026-09-24

Fix group 8 (`fix/018-tunnel-core-reliability`) was split: F-172, F-052 and F-173 go ahead, and F-174 waits on this. `tunnel/main.go:282-285` is a TODO ("the exact mechanism ... is TBD"): nothing in the tunnel binary learns the address playit.gg assigns, so `status.endpoints` stays empty for playit tunnels. The operator already grants the tunnel a status-patch RBAC rule for this (`tunnel_rbac.go`). Options:
- (a) poll playitd's control socket (`--socket-path`) for the assigned address;
- (b) parse playitd's log output;
- (c) query the playit.gg account API with the tunnel's secret key;
- (d) let the user enter the address (a GameServer field or annotation) and have the operator copy it into status;
- (e) document the limitation for v0.3 and close F-174 as out-of-scope with a roadmap line.

Decision (maintainer, 2026-09-24): option (a), poll playitd's control socket and patch `status.endpoints`. Implemented in #447, stacked on #428.

### T054: `gameplane-module` tag for the v0.3.0 chart default — RESOLVED 2026-09-24

Decision: a new `v0.3.0` tag in `gameplane-module`, cut only after the live module rows (`INV-MOD-*`) pass on the release candidate. `charts/gameplane/values.yaml:473` is then pinned to it on `fix/018-module-source-ref`. The README, docs and website game counts (review findings C-docs-01, C-website-01, C-website-02) are updated to the 30-module catalog in the same release. The tag in the other repo is outward-facing, so it's confirmed with you when it's ready.

Original question:

`charts/gameplane/values.yaml:473` pins the default git module source to `ref: v0.2.0-beta.6` (finding F-028).
- Question: which `gameplane-module` tag should v0.3.0 point to? Should a new tag (e.g. `v0.3.0`) be cut in that repo after the live module rows pass?

### OD-027: Backup/restore Job retry and deadline limits — RESOLVED 2026-10-05

Provisional values shipped by the live-test sweep fix: `BackoffLimit: 2` (same as the wipe Job) and `ActiveDeadlineSeconds: 86400` (24 h) on backup and restore Jobs, so a pod stuck in `CreateContainerConfigError` (e.g. its destination Secret was deleted) can no longer keep a Backup in flight forever. No existing requirement sets these numbers. Confirm or replace them (for example, scale the deadline with volume size or make it a Helm value).

The values ship in #549 (merged 2026-10-05) and remain provisional pending the maintainer.

(Added on master by the live-test sweep, #549, as "OD-005"; renumbered to OD-027 when merged into this branch because OD-005 is the upgrade-baseline decision.)

Decision (maintainer, 2026-10-05): make them Helm values. #573 adds `operator.backupJobBackoffLimit` (default 2) and `operator.backupJobActiveDeadlineSeconds` (default 86400), passed to the operator as `--backup-job-backoff-limit` / `--backup-job-active-deadline-seconds`; a missing key keeps the default, negative values are rejected.

### OD-028: API cluster-wide pod list (B15) — RESOLVED 2026-10-05

The live-sweep fix for the Cluster page's per-node pod usage (B15, [F-285](audit/findings.md)) adds `list` on `pods` in all namespaces to the `<release>-api-read` ClusterRole. A pod list exposes the plain-text env values of every workload on the cluster to the API service account.

Decision (maintainer, 2026-10-05, as recorded in #549): accept the RBAC widening. Shipped in #549 (merged 2026-10-05) and documented in `docs/security.md` under "API cluster-wide pod list".

### OD-029: the live round runs on the latest `master` commit, not on rc.2 — RESOLVED 2026-10-05

Decision (maintainer, 2026-10-05): the live round was run on the latest `master` commit on kubelab (the 2026-10-04 live sweep, fixes in #548 and #549) instead of on a published rc.2. That sweep stands in for the formal per-row round: T025–T033 are withdrawn, and its bugs are recorded as F-273..F-293 (`audit/rounds.md` § live-sweep 2026-10-04). Per-row `INV-*` outcomes were not recorded, so `audit/inventory.md` rows keep their pre-round Outcome. T034 (cleanup after the round), the upgrade and node rounds (T061, T062, T065), and RC-TAG-2 are not changed by this decision.

### OD-030: screen and feature rows recorded as pass from the live sweep — RESOLVED 2026-10-05

Decision (maintainer valgulnecron, 2026-10-05), attestation: "all screen and feature where tested live in a cluster". It refers to the 2026-10-04 live sweep on kubelab (OD-029; `audit/rounds.md` § live-sweep 2026-10-04; fixes in #548 and #549; findings F-273..F-293). On that basis every `audit/inventory.md` row in the `WEB`, `API`, `CRD`, `AGT`, `AUX`, `HELM` and `MOD` areas whose Outcome was `untested` (459 rows) is recorded as `pass`, Round `live-sweep 2026-10-04`, with Evidence pointing at this entry. The Reason and Findings cells are unchanged.

Not covered: the `UPG` (5) and `NODE` (3) rows stay `untested`, the 16 `INV-SEC-*` rows in `audit/held/inventory-SEC.md` stay `untested`, and the 19 rows that were already `blocked` stay `blocked` (CRD 2, AUX 5, MOD 12). Finding statuses are not changed by this decision: the findings from the sweep keep their current status.

This is a maintainer attestation, not per-row evidence: the sweep did not record a per-row outcome, and no `evidence/INV-*/` record was created for these rows. The report and the release notes say so. It resolves the "no row is `pass`" gap noted under OD-029 for these areas only; it does not change T034, the upgrade and node rounds (T061, T062, T065) or RC-TAG-2.
