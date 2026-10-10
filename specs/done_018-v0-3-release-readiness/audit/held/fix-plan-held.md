# Held fix plan (T050, OD-019, off-git)

**Status: PROPOSED.** This plan asks the maintainer to sign off (CLAUDE.md override 1: production code and test changes need that sign-off). Writing it changed no record: every finding's status, `Fix` and `Note` fields in `held/findings.md` are as they were, and nothing is staged or committed. All closures below are proposals only.

- **Scope:** the 57 rows in `audit/held/findings.md` (all `open`; none `imported`, none `fixing`). This count includes F-256, which records the maintainer's 2026-09-24 answer to `audit/held/questions.md` HQ-001 and was added to this plan as H31.
- **Sources:** `tasks.md` T050/T055, `spec.md` (FR-005, FR-007, FR-013, US3 AS-3), `research.md` R3 (severity), `docs/roadmap.md`, `OPEN-DECISIONS.md` (OD-019, OD-021, OD-022, OD-023), `test/e2e/buckets.sh`, `deploy/kind/e2e.sh`.
- **Public counterpart:** `audit/evidence/rc.1/fix-plan.md` was written after this plan (OD-024) and, by design, doesn't mention held work. Overlaps below cite public **finding IDs**, not public branch names. Map each cited ID to its public group when sequencing (for example F-212 is public group 3, `fix/018-charts-games-namespace-keep`).

## Summary

| | Count |
|---|---|
| Held findings considered (`open`) | 57 (S1: 2 · S2: 2 · S3: 34 · S4: 19) |
| Fix groups (one branch and one PR each) | 31 |
| Findings planned into groups | 57 |
| Proposed closures (not-a-defect / out-of-scope) | 0 |
| Re-check first (T049) | 0 |
| In flight (held) | 0 |
| Maintainer decisions requested (D1–D18) | 18 |

## How held groups are handled

These rules apply to every group below, on top of T055 and CLAUDE.md rules 11–14.

1. **Wording.** Branch names, commit messages, PR titles and PR bodies describe the change as hardening. They carry no reproduction steps, no description of how the gap could be used, and no held F-ID until the PR merges (OD-019). Only `held/findings.md` records which finding a PR fixes, in its `Fix` column, and that column is updated at T055/T056, not now.
2. **Tests are public once pushed.** A regression test explains its gap to anyone who reads it closely. Name and comment each test as a statement of the control it checks (for example "upload stays inside the data root"), with no account of the gap. Push a held branch only when it is ready for immediate review, and ask for a prompt merge.
3. **Labels.** Code groups use `type: security` plus their `area:` label, added through the REST API (CLAUDE.md 14). Docs-only groups use `type: docs`. D17 asks whether `type: security` may be visible before merge.
4. **CHANGELOG.** Each held PR adds one line under a generic "Security hardening" heading in the next `## [0.3.0-rc.N]` section (OD-014). Details follow after merge. Coordinate with #423, the rc.1 CHANGELOG PR that is in flight.
5. **`SECURITY_AUDIT.md`** has uncommitted rewording in the working tree (OD-019). H13 and H16 edit this file, so the maintainer must commit or discard that rewording before either branch is cut.
6. **Tiers.** The fix waves follow T055 and CLAUDE.md 13. RESTART.md records that sonnet was stopped by its safeguard on security material, so the scout brief and the tier-up review for held groups run on opus (the OD-020 precedent). The implementer starts at the T055 tier and escalates only after a demonstrated failure.
7. **Login budget** (buckets.sh header; counted as F-042 does, one `e2e-admin` login per `APIClient` call): `api-auth` 7, `api-roles` 7, `api-rbac` 8, `api-agent` 7, `api-mods` 5, `operator` 5 (its capture tests log in, although the header says the bucket does none), `multicluster` 1 (its own two-cluster job). The ceiling is about 7, so `api-rbac` has no room for overflow. A group that adds E2E either extends an existing test and reuses its session, or adds a new test to `api-mods` or `operator`, which have about two logins of room each. Keep a running tally as the waves land. Any test that writes `helmOverride.roleMappings` must stay out of `api-auth`. Every new E2E function is listed in its bucket, or `buckets.sh verify` fails.
8. **E2E environment facts** (`deploy/kind/e2e.sh:302-319`): the installs run with `web.enabled=false`, `capture.enabled=true` and the fake OIDC IdP. `networkPolicies.enabled` defaults to `true`. So nginx behaviour isn't visible to the Go E2E suite, and capture and NetworkPolicy objects are.
9. **CRD changes** (H02, H11, H15, H21, possibly H10) regenerate `zz_generated.deepcopy.go`, `operator/config/crd/`, `charts/gameplane/crds/` and `crd-manifests/` (CLAUDE.md 7). These generated files conflict between branches, so merge CRD-touching branches one at a time and regenerate after each rebase.
10. **Chart values.** Any new `values.yaml` key must render when the stored values lack it, because kubelab and users upgrade with `--reuse-values` (public F-214). The reverse also holds: `--reuse-values` keeps the previous chart's defaults, so a changed default (for example a narrowed `apiServerCIDRs`, D12) never reaches those installs and needs a release note. CI's `upgrade` bucket runs `helm upgrade` without `--reuse-values` (see H04), so it covers neither case. Only the live upgrade round (T061, OD-023) does.

## Order at a glance

Groups are ordered by the highest severity they hold. Within a severity, core paths come first (install/upgrade, auth, game-server lifecycle, backup/restore, console), then the rest, with docs last.

| # | Branch | Top | Findings | Path |
|---|--------|-----|----------|------|
| H01 | `fix/018-harden-api-cluster-scoping` | S1 | F-090, F-098 | auth |
| H02 | `fix/018-harden-module-bundle-integrity` | S1 | F-064, F-065, F-066, F-073 | lifecycle (module install) |
| H03 | `fix/018-harden-admin-settings-state` | S2 | F-143, F-142 | auth config |
| H31 | `fix/018-harden-server-owner-operations` | S2 | F-256 | auth |
| H04 | `fix/018-harden-chart-defaults` | S3 | F-231, F-230 | install |
| H28 | `fix/018-harden-metrics-endpoint` | S3 | F-227 | install |
| H05 | `fix/018-harden-release-signing` | S3 | F-246, F-247 | install/upgrade artifacts |
| H27 | `fix/018-harden-cluster-removal` | S3 | F-248, F-101 | auth |
| H06 | `fix/018-harden-authz-checks` | S3 | F-249, F-093 | auth |
| H07 | `fix/018-harden-auth-identity-lifecycle` | S3 | F-092, F-080, F-096, F-097 | auth |
| H08 | `fix/018-harden-client-ip-trust` | S3 | F-091 | auth |
| H09 | `fix/018-harden-account-removal-cleanup` | S3 | F-079, F-095, F-094 | auth |
| H10 | `fix/018-harden-tunnel-reconcile` | S3 | F-069, F-068 | lifecycle |
| H11 | `fix/018-harden-tunnel-supervisor-config` | S3 | F-177, F-178 | lifecycle |
| H12 | `fix/018-harden-operator-ownership-checks` | S3 | F-070, F-053 | backup/restore |
| H13 | `fix/018-harden-agent-rcon-errors` | S3 | F-112, F-114 | console |
| H30 | `fix/018-harden-agent-upload-confinement` | S3 | F-113 | other (agent files) |
| H14 | `fix/018-harden-dashboard-permission-gates` | S3 | F-145 | other |
| H15 | `fix/018-harden-module-verify-status` | S3 | F-144 | other |
| H16 | `fix/018-harden-netguard-policies` | S3 | F-147, F-148, F-146 | other (shared lib) |
| H17 | `fix/018-harden-capture-default-filter` | S3 | F-067 | other |
| H18 | `fix/018-harden-audit-export-fields` | S3 | F-078, F-228 | other |
| H19 | `fix/018-harden-syslog-bridge-delivery` | S3 | F-194, F-199 | other |
| H20 | `fix/018-harden-steamcmd-preset-defaults` | S3 | F-169 | other |
| H21 | `fix/018-harden-operator-rbac-scope` | S4 | F-072, F-071 | install (dev path) |
| H29 | `fix/018-harden-tunnel-credential-switch` | S4 | F-084 | lifecycle |
| H22 | `fix/018-harden-api-input-limits` | S4 | F-100, F-099 | other |
| H23 | `fix/018-harden-telemetry-ingest-validation` | S4 | F-202, F-203 | other |
| H24 | `fix/018-pin-dev-ingress-manifest` | S4 | F-235 | other (dev tooling) |
| H25 | `fix/018-harden-mcp-readonly-docs` | S4 | F-211 | docs |
| H26 | `fix/018-harden-security-docs-accuracy` | S4 | F-250, F-229 | docs |

H27 to H30 were split out of H01, H04, H10 and H13 at the tier-up review. H31 was added on 2026-09-24 for F-256, after the HQ-001 answer. IDs are labels; this table's row order is the order.

---

## S1

### H01 · `fix/018-harden-api-cluster-scoping`

- **Labels:** `type: security`, `area: api`
- **PR:** "fix(api): harden multi-cluster request scoping". Body: "Applies the home-cluster guard to every home-only route. Adds handler and multicluster e2e coverage for cluster scoping."
- **Findings:**
  - F-090 · S1 · Registry routes don't apply the remote-cluster guard (authorization scope)
  - F-098 · S3 · Capture download doesn't honour the selected cluster
- **Files likely touched:**
  - `api/internal/handlers/registry.go:66-207`, and its mount at `api/cmd/main.go:331`
  - `api/internal/handlers/capture.go:943,1065-1094`
- **Regression tests:**
  - Handler tests: each MountRegistry route and the capture download answer 404 for `?cluster=<registered-non-local>` without calling the home client.
  - A structural test that every handler holding a bare `*kube.Client` under namespaced RBAC is wrapped by `rejectRemoteCluster`.
  - E2E: extend `TestMultiCluster_ClusterDispatchAndScopedRBAC` (bucket `multicluster`) to show that a user bound only to the remote cluster can't change a home-cluster server through the registry/modpack routes, and that capture download is scoped.
- **specs.md:** yes, `api/specs.md` (the list of home-only routes).
- **design.pen:** check first. The dashboard sends `?cluster=` on every call (`web/src/lib/api.ts`), so after the fix the Mods tab's registry browser, the modpack install and the capture download answer 404 for a server on a remote cluster. No new visual is needed if the existing error state is acceptable there (the sibling `mod_updates` route already answers 404 the same way). If those controls should instead be hidden or explained on a remote cluster, that's a UI change and goes through `design.pen` first (rule 1, with the user's OK).
- **Depends on:**
  - Live re-verification needs OD-021 item 19 (which remote cluster `audit018-cluster-1` registers).
  - Split at the tier-up review: F-248 and F-101 moved to H27, so D1 no longer holds this S1 fix.
- **Overlap (sequence with public work):**
  - `capture.go`: F-074, F-187, F-189, F-192, F-087
  - the `api/cmd/main.go` router: F-074, F-075, F-135, F-223, F-224
  - Held H08 also edits `main.go` middleware.

### H02 · `fix/018-harden-module-bundle-integrity`

- **Labels:** `type: security`, `area: operator`
- **PR:** "fix(operator): harden module bundle pull and pin checks". Body: "Binds every pulled manifest and layer to the digest that was verified, honours `spec.digest` in convergence, routes registry and signature fetches through the dial guard, and corrects the `spec.oci.insecure` field doc."
- **Findings:**
  - F-064 · S1 · Pulled OCI manifest and layer bytes aren't bound to the verified digest
  - F-065 · S3 · The Module convergence check ignores `spec.digest`
  - F-066 · S3 · OCI and cosign fetches aren't dialled through netguard, though the docs say they are
  - F-073 · S4 · The `ModuleSource spec.oci.insecure` doc describes a TLS skip the code never does
- **Files likely touched:**
  - `operator/internal/oci/client.go` (`Pull` and `readBlob`: hash the bodies and compare, and use a guarded transport)
  - `operator/internal/oci/bundle.go`
  - `operator/internal/verify/verify.go` (a guarded transport for the go-containerregistry options)
  - `operator/internal/controller/module_controller.go:100-107,130-136`
  - `operator/api/v1alpha1/modulesource_types.go:150` (the field doc), then `make generate manifests`
  - `docs/architecture.md`, `README.md:146` and `docs/security.md` (netguard scope)
- **Regression tests:**
  - Unit tests in `operator/internal/oci`, using the existing test registry: a response body that doesn't match its digest is rejected, for both the manifest and a layer.
  - An envtest in `module_envtest_test.go`: a `spec.digest` change on a `Ready` Module is not treated as converged.
  - A unit test that the OCI and cosign transports use the guarded dialer.
  - E2E, bucket `operator`: next to `TestModule_VerifySignedBundleInstalls`, add a case where a Module pinned to a digest that doesn't match the source content doesn't reach `Ready`. Extend `TestModuleSource_RejectsSSRFTarget` to an OCI source if D5 picks wiring.
- **specs.md:** yes, `operator/specs.md` (module pull integrity and the dial-guard scope).
- **design.pen:** no.
- **Depends on:**
  - D5: wire netguard in, or narrow the docs? Wiring is recommended. D5 gates only F-066. If it's still open when the wave starts, cut H02 with F-064, F-065 and F-073 so the S1 fix isn't held, and take F-066 as a follow-up on the same files.
  - H16 changes the `IsAllowed` policy that the OCI path will use. H02 doesn't wait for it.
  - Live re-verification needs OD-021 items 6 and 21 (an `audit018-` OCI ModuleSource, and a location for the unsigned bundle).
  - H15 builds on this branch.
- **Overlap:**
  - Public **F-046** changes the same convergence condition at `module_controller.go:100-107`. Land it first and rebase H02, or merge the two changes.
  - Also in `module_controller.go`: F-050, F-062, F-163, F-168.
  - CRD regeneration: see rule 9.

## S2

### H03 · `fix/018-harden-admin-settings-state`

- **Labels:** `type: security`, `area: web`
- **PR:** "fix(web): harden Admin Settings draft and secret lifecycle". Body: "Re-seeds section drafts from the server after a reset, surfaces reset failures, and changes a managed Secret only together with a successful save of the config that references it."
- **Findings:**
  - F-143 · S2 · The next Authentication save re-applies a role-mapping reset
  - F-142 · S3 · Secret removal isn't tied to a successful config save
- **Files likely touched:**
  - `web/src/routes/AdminSettings.tsx`:
    - `useSectionForm` at `:197-224`: re-seed on a new `initial`, or key `AuthSection` (`:130`)
    - `handleReset` at `:1748-1757`
    - the trash handlers at `:307-315`, `:974-982` and `:1460-1468`
    - the add forms
  - Possibly `web/src/lib/api.ts`
- **Regression tests:**
  - Vitest in `AdminSettings.test.tsx`:
    - After a reset, a following save doesn't PUT the removed override.
    - Removing a row and leaving without saving calls no Secret delete.
    - A cancelled add leaves no Secret.
  - Playwright (mock), `web/e2e/specs/adminSettings.spec.ts`.
  - No `buckets.sh` bucket covers UI draft state. The API contract is unchanged.
- **specs.md:** yes, `web/specs.md` (section save semantics and the lifecycle of managed Secrets).
- **design.pen:** check first. No new visual is expected if reset failures use the existing error toast or inline-error pattern of the Authentication card. If no such state exists in `design.pen`, it has to be designed first (rule 1), and design edits need the user's OK before React work (memory: Pencil screenshots are blind).
- **Depends on:** none of OD-021, OD-022 or OD-023.
- **Overlap:**
  - `AdminSettings.tsx`: F-127, F-128, F-134.
  - Public F-076 changes the API reset response this UI calls (`config.go`). Land F-076 first so the UI handles the final response.
  - Held H07 changes how the API treats `helmOverride`. No file overlap.

### H31 · `fix/018-harden-server-owner-operations`

- **Labels:** `type: security`, `area: api` and `area: web`, plus `breaking` (CLAUDE.md 14). After this change, operator-role users can no longer run these operations on servers they don't own. D17(b) still applies to `type: security`.
- **PR:** "fix(api): require ownership or the admin role for owner-only server operations". Body: "Ownership transfer, collaborator edits, data wipe and server delete now require the caller to own the server or hold the admin role. The namespace server-write permission alone no longer covers them. Aligns the dashboard gates, the permission catalog label and the security docs with the rule."
- **Findings:**
  - F-256 · S2 · Owner-only server operations are open to every namespace `servers:write` holder
- **Files likely touched:**
  - `api/internal/rbac/rbac.go:109-144`. Recommended home for the check. Apply the owner-only rule from `:128-130` even when the namespace check at `:109` passes, before `next.ServeHTTP` at `:144`: admit an admin (`*` in the resolved cluster and namespace) first, otherwise fetch the server and admit only its owner. Update the package comment at `:11-15` to match.
  - Defence in depth in the handlers, which don't check ownership today: `api/internal/handlers/ownership.go:79-120` (`transfer`), `:125-230` (`setCollaborators`), `api/internal/handlers/lifecycle.go:57-92` (`wipeDataHandler`) and `api/internal/handlers/resources.go:291-322` (`deleteHandler`, for the servers GVR only). Use one owner-or-admin helper next to `isServerOwner` (`handlers/shares.go:555-572`).
  - `api/internal/rbac/catalog.go:27` (the `servers:write` label).
  - `web/src/components/server/ServerActionsMenu.tsx:35-39,62-85` (the `canManage` gate and the "Requires owner or operator role" hint), `web/src/routes/tabs/settings/Danger.tsx` (no gate today) and `web/src/routes/tabs/settings/Access.tsx:54-61`. A shared gate helper would go in `web/src/lib/auth.ts`.
  - Docs: `docs/security.md:124-135` (Per-GameServer access).
- **Regression tests:**
  - Unit tests in `api/internal/rbac/middleware_test.go`:
    - A caller with namespace `servers:write` who is neither owner nor admin gets 403 on `:transfer`, `:collaborators`, `:wipe-data` and `DELETE /servers/{name}`.
    - The owner and an admin get through.
    - Collaborators stay denied.
    - A server with no owner annotation is admin-only.
    - `servers:write` writes that aren't owner-only (`:start`, sub-resources) are unchanged.
    - An admin acting on a missing server still gets the handler's 404.
  - Handler tests in `ownership_test.go` and `lifecycle_fake_test.go` for the handler-level check.
  - Vitest in `ServerActionsMenu.test.tsx`, plus a Danger-tab test: owner-only items are disabled for a non-owner with `servers:write`, and enabled for the owner and an admin. The file's `defaultCan` mock grants `servers:write` only. If a current case relies on that mock alone to enable transfer, wipe or delete, that case has to change. Changing an existing test needs the maintainer's sign-off (override 1).
  - E2E: extend `TestAPI_OwnerCollaboratorAccess` (bucket `api-roles`, 7 `e2e-admin` logins, at its ceiling), reusing its admin session.
    - `CreateUser` adds a throwaway operator-role user. That costs no `e2e-admin` login, but it costs one more per-IP login (burst 10) when that user logs in.
    - As that user, assert 403 on the four operations against the test's server after the admin's transfer and while the server still exists. The owner and collaborator annotations stay unchanged.
    - If `api-roles` has no per-IP room, use a new test in `api-mods` or `operator` instead (rule 7), listed in `buckets.sh`.
  - Existing E2E that stays valid: `TestAPI_RBAC_OperatorCanWriteServers_NotUsers` (`api-rbac`) creates a server but never deletes or transfers it. The `multicluster` cleanup deletes a server that its operator-role user created and therefore owns. `TestAPI_OwnerCollaboratorAccess` transfers the server as admin.
- **specs.md:** yes.
  - `api/specs.md:404-407` (the owner/collaborator section): owner-only operations need the owner or an admin, whatever the caller's namespace permission.
  - `web/specs.md:376` (Danger zone gating).
- **design.pen:** check first. The menu hint copy changes, and the Danger tab gets a disabled state it doesn't have today. If `design.pen` has no disabled state for the Danger-zone actions, it has to be designed first (rule 1, with the user's OK; memory: Pencil screenshots are blind). The API change doesn't wait for the design. It can land first, and the dashboard shows the existing error state until then.
- **Depends on:**
  - D18: who counts as an admin here, and whether the rule should cover more than these four operations.
  - Release note: operator-role users lose these operations on servers they don't own. Servers without an owner annotation (created with kubectl or GitOps) can then be transferred, wiped or deleted only by an admin. Neutral wording (rules 1 and 4).
  - No migration or CRD change, unless D18 picks a new catalog permission.
  - Live re-verification uses `audit018-admin`, `audit018-operator` and a throwaway server (`audit018-hq001`), never `audit018-rbac-test` (F-256 step 5). It can run on the next RC after merge (T058).
- **Overlap:**
  - `rbac.go` with held H06 (`:243`) and public F-087 (spec statements). Land H31 first.
  - `web/src/lib/auth.ts` with held H14 and public F-138, if the gate helper goes there.
  - `resources.go` with held H11 and H29 and public F-118 and F-119, if the handler-level check is chosen.
  - `docs/security.md`: see the hot spots.

## S3

### H04 · `fix/018-harden-chart-defaults`

- **Labels:** `type: security`, `area: chart`
- **PR:** "fix(chart): harden default egress and agent mTLS Secret options". Body: "Tightens the default apiserver egress rule for game pods, and makes the bring-your-own agent CA path install cleanly."
- **Findings:**
  - F-231 · S3 · The default apiserver egress rule on 443/6443 reaches more than the apiserver
  - F-230 · S3 · The bring-your-own agent mTLS Secret path fails at install, and some values fields are unused
- **Files likely touched:**
  - For F-231: `charts/gameplane/templates/networkpolicies.yaml` (`allow-agent-to-apiserver` at `:48`, `allow-game-public-egress` at `:149`) and `values.yaml:335` (`apiServerCIDRs`).
  - For F-230: `charts/gameplane/templates/mtls.yaml` and `values.yaml:278` (`api.agentMTLS`).
  - Docs: `docs/install.md` and `docs/security.md`.
- **Regression tests:**
  - E2E, bucket `operator` (no login): read back the installed `allow-agent-to-apiserver` NetworkPolicy and assert its `ipBlock` matches the narrowed default. NetworkPolicies are on by default. If D12 picks documentation instead, the docs change is the fix and there's no behaviour to test.
  - F-230: a `helm template` render check that overridden names produce no chart-owned Secret. There's no chart render harness today, so this is also new tooling needing sign-off (see D11).
  - The `upgrade` bucket is not a `--reuse-values` guard: `TestUpgrade_FromPreviousRelease` runs `helm upgrade` with explicit `--set` flags and no `--reuse-values`, so it renders the new chart's defaults. Rule 10 is checked only by the live upgrade round (T061, OD-023), or by a `helm template` render with stored beta.8 values (the D11 harness).
- **specs.md:** no component `specs.md` for the chart. `docs/install.md` and `docs/security.md` change.
- **design.pen:** no.
- **Depends on:**
  - D12: narrow the default or document it? A narrowed default doesn't reach installs upgraded with `--reuse-values` (rule 10), so it also needs a release note.
  - D11: skip generating chart Secrets for overridden names, or document the ownership metadata?
  - OD-023: land this before the upgrade round (T061) so that round covers it.
- **Overlap (heavy):**
  - `networkpolicies.yaml` with public **F-215** (S2) and F-217, and with held H10 (F-068).
  - `values.yaml` with F-028/T054, F-212, F-214, F-219, F-221–F-224, F-226, F-252 and F-255.
  - `docs/install.md` with F-027 and F-212–F-226.

### H28 · `fix/018-harden-metrics-endpoint`

- **Labels:** `type: security`, plus `area: api` and `area: chart` (D13 = listener) or `area: web` (D13 = nginx rule)
- **PR:** "fix(api): serve Prometheus metrics on a dedicated listener", or "fix(web): serve metrics only to the in-cluster scraper" if D13 picks the nginx rule. Body: "Serves Prometheus metrics only on the in-cluster scrape path, and points the ServiceMonitor at it."
- **Findings:**
  - F-227 · S3 · `/metrics` is served on the public ingress host without a session
- **Files likely touched:**
  - D13 = listener: `api/cmd/main.go:260` (a separate metrics listener), `charts/gameplane/templates/api.yaml` (container and Service port), `charts/gameplane/templates/servicemonitors.yaml`, and a new `values.yaml` port key (rule 10).
  - D13 = nginx rule: `web/nginx.conf.template:94-115`.
  - Docs: `docs/install.md` and `docs/security.md`.
- **Regression tests:**
  - D13 = listener: a new `TestHelmInstall_MetricsNotOnPublicPort` (bucket `operator`, no login) checks the API's public port.
  - D13 = nginx rule: the Go E2E can't see the rule (`web.enabled=false`), so a container-level check of the web image in CI is needed. That check is new tooling and needs sign-off.
- **specs.md:** `api/specs.md` if D13 picks the listener (where metrics are served). No component `specs.md` changes for the nginx rule.
- **design.pen:** no.
- **Depends on:**
  - D13: nginx rule or separate listener? The listener is testable in the existing suite.
  - OD-023: land this before the upgrade round (T061) so that round covers it, with the new port key rendering when stored values lack it (rule 10).
  - Split from H04 at the tier-up review. This fix lands in the API or web image, not in the chart templates the rest of H04 edits, and its place at the end of the `api/cmd/main.go` chain (hot spots) would otherwise hold the chart-only fixes.
- **Overlap:**
  - `web/nginx.conf.template` with public **F-251** (S2). Sequence F-251 first.
  - `servicemonitors.yaml` with F-216 and F-217.
  - `api/cmd/main.go` with held H01, H07 and H08, and public F-074, F-075, F-081, F-135, F-223 and F-224.

### H05 · `fix/018-harden-release-signing`

- **Labels:** `type: security`, `area: shared`
- **PR:** "ci: harden release signing order and signing-secret scope". Body: "Checks for the signing key before any push, signs by digest before tags are attached, stops a sibling failure from cancelling signing, and scopes signing jobs to a protected environment."
- **Findings:**
  - F-246 · S3 · Release images are tagged before their signature exists
  - F-247 · S3 · Signing-secret confinement relies on convention, not repository configuration
- **Files likely touched:**
  - `.github/workflows/release.yaml`: `strategy` at `:19-36` gets `fail-fast: false`; the key check moves ahead of build-push at `:46-77`; push by digest, sign, then tag.
  - `.github/workflows/images.yaml` (`:61-94`, `:224-260`), `.github/workflows/publish-edge.yaml:87-144` and `.github/workflows/republish-modules.yaml`, each with a job-level `environment:`.
  - `docs/contributing.md:146-148`.
  - **Repository settings, which only the maintainer can change** (outward-facing):
    - an environment such as `release-signing`, with a deployment policy for `refs/heads/master` and `refs/tags/v*`
    - `COSIGN_*` moved into that environment
    - a tag ruleset for `v*`
- **Regression tests:**
  - No E2E applies. actionlint runs in CI.
  - Verification: read the step order, then run `cosign verify` for every image and chart tag of the next RC (T057/T058).
  - An optional structural check (for example a hack script asserting the sign-before-tag order) would be new tooling and needs sign-off.
- **specs.md:** no. The assumption at `specs/done_008-hardened-github-actions/spec.md:176` becomes true as written.
- **design.pen:** no.
- **Depends on:**
  - D16: the maintainer's repository-settings changes.
  - Land this before the next RC tag (RC-TAG-N, T057), so that tag exercises it.
- **Overlap:**
  - `release.yaml` with F-219 and F-245.
  - `publish-edge.yaml` with F-239 and F-245.
  - `docs/contributing.md` with F-036 and F-245.
  - In flight: #422 edits `ci.yaml` only, so there's no file overlap.

### H27 · `fix/018-harden-cluster-removal`

- **Labels:** `type: security`, `area: api`
- **PR:** "fix(api): harden cluster registration removal". Body: "Deletes a kubeconfig Secret on cluster removal only when it carries the API's kubeconfig label, and drops the cluster client as soon as its registration is removed."
- **Findings:**
  - F-248 · S3 · Cluster delete removes the referenced Secret without the kubeconfig-label check
  - F-101 · S4 · The cluster watcher ignores delete tombstones, so client removal can lag
- **Files likely touched:**
  - `api/internal/handlers/clusters.go:192-236`, which gets a label check before the Secret delete and a direct `reg.Remove`
  - `api/internal/kube/watch.go:57-61`, which unwraps `cache.DeletedFinalStateUnknown`
  - `docs/security.md` (the "Kubeconfig Secret handling" section, about `:609-620`), and `docs/install.md:442-492` if D1 changes the Path 1 behaviour
- **Regression tests:**
  - Handler tests: a cluster delete leaves an unlabelled Secret in place, and removes the client from the registry without waiting for the watch.
  - A watcher unit test for the tombstone path.
  - E2E: extend `TestMultiCluster_ClusterDispatchAndScopedRBAC` (bucket `multicluster`, 1 admin login). Delete its registration in the test body (today that happens only in cleanup), then assert that a `?cluster=` request for that id answers 404, and that a Secret without the kubeconfig label, named by a kubectl-applied Cluster CR, is still there after that CR is deleted over HTTP.
- **specs.md:** yes, `api/specs.md` (cluster-delete semantics).
- **design.pen:** no.
- **Depends on:**
  - D1: should kubeconfig Secrets created with kubectl or GitOps (Path 1) be deletable over HTTP at all?
  - Live re-verification needs OD-021 item 19 (which remote cluster `audit018-cluster-1` registers).
  - Split from H01 at the tier-up review. The two pairs touch disjoint files, and D1 gates only F-248, so it no longer holds the S1 fix.
- **Overlap:** `clusters.go` with F-083.

### H06 · `fix/018-harden-authz-checks`

- **Labels:** `type: security`, `area: api`
- **PR:** "fix(api): harden role-edit guards and event-stream authorization". Body: "Applies the user-management lockout guards to role edits, and limits the event stream to the kinds the caller may read in the resolved namespace."
- **Findings:**
  - F-249 · S3 · Role edits lack the last-user-manager and self-demotion guards
  - F-093 · S3 · The `/events` stream isn't filtered by the caller's per-kind permissions
- **Files likely touched:**
  - `api/internal/handlers/roles.go:160-238`
  - `api/internal/db/rbac.go:63-106`, reusing `RoleGrantsUserManagement`, `UserManagesUsers` and `UserManagerCount`
  - `api/internal/handlers/events.go:63-110`
  - `api/internal/rbac/rbac.go:243`
  - `docs/security.md:121-122`
- **Regression tests:**
  - Handler tests: a role edit that removes `users:manage` from the caller's own primary role, or leaves no user manager, is refused with 400. An SSE test with a custom role holding only `servers:read` gets server events only, including a namespace-scoped binding case.
  - E2E:
    - F-249: extend `TestAPI_CustomRole_Lifecycle` (`api-roles`, at its ceiling), reusing its admin session. The throwaway user it acts as costs no `e2e-admin` login.
    - F-093: an SSE kinds check with a custom role that holds only `servers:read`, added to the same `TestAPI_CustomRole_Lifecycle` extension, or as a new test in `api-mods` or `operator` (rule 7). `api-rbac` is over its budget.
- **specs.md:** yes, `api/specs.md` (role-update guards and `/events` filtering).
- **design.pen:** no. The refusal shows through the existing role-editor error display, and public F-126 (RoleEditorModal has no error handler) must land first for it to show at all.
- **Depends on:**
  - **HQ-001** was answered on 2026-09-24 (not intended) and recorded as F-256 (S2). It's in its own S2 group, H31, not here, so this S3 group doesn't hold it back. It also shares no handler with this group.
  - OD-021 item 15, the no-permission role used for the live check.
- **Overlap:**
  - `roles.go` and `users.go` with F-083.
  - `rbac.go` spec statements with F-087.
  - `rbac.go` with held H31. Land H31 first.
  - The role editor with F-126.

### H07 · `fix/018-harden-auth-identity-lifecycle`

- **Labels:** `type: security`, `area: api`
- **PR:** "fix(api): harden OIDC role re-evaluation and break-glass admin reset". Body: "Re-evaluates roles against the effective role-mapping policy, audits role assignments for every OIDC provider, keeps the rest of the auth config intact when enabling local login, and ends existing sessions on a forced reset."
- **Findings:**
  - F-092 · S3 · OIDC role re-evaluation is keyed to Helm mappings only
  - F-080 · S3 · Dashboard-managed OIDC providers don't audit role assignments
  - F-096 · S3 · `bootstrap-admin`'s local-login enable drops `helmOverride`
  - F-097 · S3 · `bootstrap-admin --force` leaves existing sessions valid
- **Files likely touched:**
  - `api/internal/auth/oidc.go:447-469`
  - `api/internal/auth/registry.go:308-342`
  - `api/cmd/main.go:228-231`
  - `api/cmd/bootstrap.go:114-124` and `:165-195` (preserve unknown top-level keys, and delete the account's sessions)
  - `docs/security.md:58-59,712-718`, checked against the fixed behaviour
- **Regression tests:**
  - Unit tests: `oidc_test.go` (an override-only mapping is re-evaluated, with the audit event and the last-user-manager exception), `auth/registry_test.go` (a dashboard provider gets the audit hook), and `api/cmd/bootstrap_test.go` (`helmOverride` survives; the session count after `--force` is 0).
  - E2E:
    - F-092 can't be reached in E2E. The e2e install sets a Helm role mapping (`deploy/kind/e2e.sh`, `api.oidc.roleMappings.admin[0]`), so the Helm base always has mappings and re-evaluation already runs. An extension of `TestAPI_OIDCHelmOverride_EffectiveAtLoginTime` would pass before the fix too. The `oidc_test.go` unit test is the regression test.
    - F-080: `TestAPI_DynamicAuthProviders` can't carry it, because its issuer is deliberately unreachable and no OIDC login happens. The fake IdP accepts one fixed client and redirect URI, the Helm provider's (`test/e2e/internal/fakeoidc/main.go`), so a login through a dashboard-managed provider needs a harness change (new tooling, sign-off). With it, extend `TestAPI_OIDCHelmOverride_EffectiveAtLoginTime` (`api-roles`, reusing its admin session; never `api-auth`) to assert the role-assignment audit row. Without it, unit tests only and a waiver is requested.
    - Break-glass (F-096/F-097): unit tests. An F-097 E2E could run `bootstrap-admin --force` through `kubectl exec` against a throwaway account in `api-mods` (never `e2e-admin`, whose reset would break the rest of the job), which costs no `e2e-admin` login. If that isn't worth it, a waiver is requested.
- **specs.md:** yes, `api/specs.md` (FR-014 audit for all providers, and `bootstrap-admin` behaviour).
- **design.pen:** no.
- **Depends on:** H18 should land after this group, so the new role-assignment rows carry `reason` to the sinks.
- **Overlap:**
  - `api/cmd/main.go` with F-223, F-224 and F-074.
  - `bootstrap.go` with F-076.
  - OIDC docs with F-021 and F-252.
  - Held H03 covers the `helmOverride` UI path.

### H08 · `fix/018-harden-client-ip-trust`

- **Labels:** `type: security`, `area: api`
- **PR:** "fix(api): harden client IP derivation behind trusted proxies". Body: "Uses forwarded addresses only when the direct peer is a trusted proxy, so per-client limits and audit attribution stay per client."
- **Findings:**
  - F-091 · S3 · Client IP derivation doesn't check the TCP peer against the trusted proxies
- **Files likely touched:**
  - `api/cmd/main.go:251,522-530`
  - A new wrapper middleware, for example under `api/internal/auth/`, around chi's `ClientIPFromXFF`, which is used from the chi v5.3.2 module.
  - `charts/gameplane/values.yaml:125` (comment only, unless D17 also narrows the default).
  - `docs/security.md` (client IP section).
- **Regression tests:**
  - Unit tests on the middleware chain for cases (a), (b) and (c) of the finding.
  - E2E: `TestAPI_LoginRateLimit` (bucket `ratelimit`) is the guard, since its port-forward peer is loopback and stays trusted. Distinct private-range peers can't be simulated in kind, so there is no new E2E.
- **specs.md:** yes, `api/specs.md` (how the client IP is derived).
- **design.pen:** no.
- **Depends on:** D17 (keep the broad default trusted list once the peer check exists?). It needs a release note for installs whose proxy sits outside the trusted list.
- **Overlap:**
  - The `api/cmd/main.go` middleware chain with F-074, F-075 and F-135.
  - `auth/ratelimit.go` with F-120.
  - Held H01 also edits `main.go`.

### H09 · `fix/018-harden-account-removal-cleanup`

- **Labels:** `type: security`, `area: api`
- **PR:** "fix(api): harden account removal and share-link revocation". Body: "Removes or revokes everything tied to a deleted account on every supported driver, and scopes share-link revocation to the link's server."
- **Findings:**
  - F-079 · S3 · User delete leaves `oidc_links` rows on SQLite, which blocks SSO re-provisioning
  - F-095 · S3 · User delete leaves the user's share links valid on SQLite. Its table title names oidc_links/preferences, but its subsection is about share links. A title fix is suggested; nothing was changed.
  - F-094 · S4 · Share-link revoke matches only id and cluster
- **Files likely touched:**
  - `api/internal/handlers/users.go:449-471`: delete or revoke `oidc_links`, `user_preferences` and `share_links` in the same transaction.
  - `api/internal/db/shares.go:289-300`: match cluster, namespace, server name and id, and return a not-found sentinel.
  - `api/internal/handlers/shares.go:251-258`.
  - Optionally a new append-only migration `api/internal/db/migrations/012_*.sql` that purges existing orphans.
  - Or, per D10, the DSN at `values.yaml:129`.
- **Regression tests:**
  - Store and handler tests:
    - After the creator is deleted, the token gets the uniform not-found response from `GET /shares/{token}`.
    - Re-login by the same IdP subject after a delete provisions a fresh user.
    - A mismatched revoke returns 404 and leaves the row unchanged.
  - E2E: one new test in `api-mods`, with one admin session for both halves (rule 7: `api-rbac` is over its budget and `api-auth` is full):
    - The share-link half: a throwaway user's link stops resolving once that user is deleted.
    - The SSO half uses the fake IdP, which is installed for every bucket (`e2e.sh:311-315`): the same IdP subject logs in again after the delete and gets a fresh user.
- **specs.md:** yes, `api/specs.md` (user deletion semantics and share revocation).
- **design.pen:** no.
- **Depends on:**
  - D10: explicit deletes, or enabling SQLite foreign keys? Explicit deletes plus a one-off cleanup migration are recommended.
  - OD-023 and research R6: a new migration is forward-only, so a rollback needs the DB snapshot. Land it before the upgrade round.
- **Overlap:**
  - Public **F-082** changes the same revoke function (`RevokeShareLink` 500 → 404). Land it first, or let H09 absorb the not-found sentinel.
  - `db/shares.go` with F-123 and F-120.
  - `users.go` with F-083.
  - Migration numbering with any public migration, such as a possible F-085 backfill fix.

### H10 · `fix/018-harden-tunnel-reconcile`

- **Labels:** `type: security`, `area: operator`, `area: chart`
- **PR:** "fix(operator): harden tunnel credential handling and relay egress". Body: "Documents the supported credential path, reports a refused credential on the GameServer status, and sets the relay egress scope the tunnel needs."
- **Findings:**
  - F-069 · S3 · The tunnel credential docs lead to a refused reconcile, with no status signal
  - F-068 · S3 · Playit tunnel egress and address reporting are incomplete
- **Files likely touched:**
  - `operator/internal/controller/gameserver_tunnel.go` (the playit case, plus a condition on refusal)
  - `operator/internal/controller/gameserver_controller.go` (the Reconcile early return must write status first)
  - `charts/gameplane/templates/networkpolicies.yaml` (tunnel pod egress)
  - `docs/tunnels.md` (the three setup sections, and address reporting)
- **Regression tests:**
  - An envtest: a credential Secret without an ownerReference produces a GameServer condition.
  - E2E, bucket `operator`: `TestGameServer_TunnelCredentialRefusalSurfaced`. No tunnel E2E exists today, and none of this needs an external relay.
- **specs.md:** yes, `operator/specs.md` and `tunnel/specs.md` (tunnel credentials and egress).
- **design.pen:** check. Conditions already render in `web/src/routes/tabs/Overview.tsx` and `tabs/settings/Networking.tsx`, so no new visual is expected.
- **Depends on:**
  - D2 (the intended playit egress scope).
  - Public **F-174**, which writes the playit endpoint to status, resolves the address-reporting half of F-068. Land F-174 first, and this group then covers egress and docs.
- **Overlap:**
  - `gameserver_tunnel.go` with F-052, F-055, F-174 and F-175.
  - `docs/tunnels.md` with F-031, F-052, F-174 and F-175.
  - The credential flow the new docs will recommend (`tunnelcreds.go`, resources validation) is changed by public **F-118** and **F-119** (both S2) and by held H29. Land those first.
  - `networkpolicies.yaml` with F-215, F-217 and held H04.

### H11 · `fix/018-harden-tunnel-supervisor-config`

- **Labels:** `type: security`, `area: optional-components`, `area: operator`, `area: api`
- **PR:** "fix(tunnel): harden relay config rendering". Body: "Validates and escapes every value written to the relay config, adds matching CRD patterns, and applies the configured device tags (or documents their status)."
- **Findings:**
  - F-177 · S3 · frpc config values beyond the token aren't escaped or validated
  - F-178 · S3 · Tailscale tags are accepted but not applied at registration
- **Files likely touched:**
  - `tunnel/main.go:141-169,291,304-332,378-397,424-432,478-482`
  - `operator/api/v1alpha1/gameserver_types.go:404-447` (add `Pattern` markers, then `make generate manifests`)
  - `api/internal/handlers/resources.go:544-554` (API-side validation)
  - `docs/tunnels.md:183-184`
- **Regression tests:**
  - Unit tests in `tunnel/main_test.go`: rendered TOML parses to exactly the generated keys for any accepted input, and the tailscale config carries the tags.
  - E2E, bucket `operator`: a new `TestCRD_Validation_TunnelFields`, where the CRD refuses a malformed server address or proxy name.
- **specs.md:** yes. `tunnel/specs.md` (drop the known-gap lines `:63` and `:119` if D7 picks implement), and the CRD field docs.
- **design.pen:** no.
- **Depends on:**
  - D7: implement the tags, or document that they aren't applied?
  - CRD regeneration sequencing (rule 9).
- **Overlap:**
  - Public **F-173** (S2) changes the same `renderTailscaleConfig`. Sequence F-173 first.
  - `tunnel/main.go` with F-172, F-174, F-176 and F-052.
  - `gameserver_types.go` with F-031, F-045, F-047, F-056, F-057, F-118 and F-119.
  - `resources.go` with F-118 and F-119.

### H12 · `fix/018-harden-operator-ownership-checks`

- **Labels:** `type: security`, `area: operator`
- **PR:** "fix(operator): harden ownership checks on restore and cleanup". Body: "Gives a restored server the objects it owns (or fails the restore clearly, with a deadline), and deletes fixed-name children only when this GameServer controls them."
- **Findings:**
  - F-070 · S3 · A volume-snapshot restore stalls when the source uses owned Secret or ConfigMap refs
  - F-053 · S4 · Fixed-name deletes run without an ownership check
- **Files likely touched:**
  - `operator/internal/controller/restore_volumesnapshot.go` (spec copy and `awaitRestoredServer`)
  - `operator/internal/controller/gameserver_config.go:295-325`
  - `operator/internal/controller/gameserver_rcon.go:88`
  - `operator/internal/controller/gameserver_controller.go:2202-2211`
- **Regression tests:**
  - Envtests: `backup_volumesnapshot_envtest_test.go` or `restore_envtest_test.go` (the restore of a server with env refs finishes or fails with a message), and `gameserver_envtest_test.go` (a same-named object that the GameServer doesn't control survives a reconcile).
  - E2E, bucket `operator`: `TestGameServer_UnownedNamesakeSurvives` for F-053. A VolumeSnapshot E2E isn't possible on kind without CSI snapshots, so F-070 gets envtest only.
- **specs.md:** yes, `operator/specs.md` (how a volume-snapshot restore handles references).
- **design.pen:** no.
- **Depends on:**
  - D15: re-own or copy the references, drop them, or fail the Restore?
  - Live re-verification of F-070 is blocked by OD-021 item 20 (no CSI snapshots on kubelab), and backups need OD-021 items 9 and 16.
- **Overlap:**
  - The restore path with F-049 and F-062.
  - `gameserver_controller.go` with F-045, F-047, F-056, F-058, F-105, F-124, F-179, F-187, F-188 and F-212.
  - `gameserver_config.go` with F-159 and F-063.
  - The imported F-015 rule (owned references only) is why the reconcile refuses the inherited references. Keep that rule.

### H13 · `fix/018-harden-agent-rcon-errors`

- **Labels:** `type: security`, `area: agent`
- **PR:** "fix(agent): harden RCON error handling and align the agent spec". Body: "Returns generic console errors, redacts connection details from logs, and aligns the agent spec with the enforced controls."
- **Findings:**
  - F-112 · S3 · WebRcon dial errors carry the RCON secret in their text
  - F-114 · S4 · Six agent spec statements differ from the enforced controls
- **Files likely touched:**
  - `agent/internal/rcon/websocket.go` (redact the URL path from `*url.Error`)
  - `agent/internal/console/console.go` (a generic `err` envelope, with detail logged only after redaction)
  - the players, status and actions log sites
  - `agent/internal/files/files.go` `resolve`, only if D3 picks the code change for F-114 item 3 (coordinate with H30)
  - `agent/specs.md:92,112-114,144,152,153,199,201,205`
  - `SECURITY_AUDIT.md:150,155`
- **Regression tests:**
  - Unit tests:
    - `agent/internal/rcon`: a sentinel secret is absent from the error text, including its path-escaped form.
    - `agent/internal/console`: the envelope carries no upstream detail.
    - For D3, if the spec wording is kept as the contract: a dotfile test and a startup test.
  - F-112 E2E isn't practical: it needs a Rust server with its RCON transport down (bot-heavy). Unit tests only; a waiver is requested.
- **specs.md:** yes, `agent/specs.md`.
- **design.pen:** no.
- **Depends on:**
  - D3: F-114 items 3 and 5, change the code or the spec?
  - The `SECURITY_AUDIT.md` working-tree state (rule 5).
  - Live re-verification needs OD-021 item 1 (which server the agent procedures use). F-112 also needs a server on the WebRcon transport (`rust`).
  - Split at the tier-up review: F-113 moved to H30, so a merge of the files work with public fixes doesn't carry the RCON change.
- **Overlap:**
  - `console.go` with F-103, `rcon.go` with F-104 and F-111, and `agent/specs.md` with F-106, F-109, F-110 and F-111.
  - `files.go` `resolve` with H30 and public F-102, F-115 and F-108, if D3 item 3 picks code.

### H30 · `fix/018-harden-agent-upload-confinement`

- **Labels:** `type: security`, `area: agent`
- **PR:** "fix(agent): confine the final upload path to the data root". Body: "Resolves every uploaded part's full destination inside the data root, and writes it through a temporary file and rename."
- **Findings:**
  - F-113 · S3 · The final upload path isn't confined
- **Files likely touched:** `agent/internal/files/files.go` (`upload` and `savePart`: full-path `resolve`, then write to a temporary file and rename).
- **Regression tests:**
  - Unit test in `agent/internal/files`: an upload whose name matches an existing out-of-root link gets 4xx, and the outside file is unchanged.
  - E2E: extend `TestAPI_AgentFilesRoundTrip` (bucket `api-agent`) with the upload confinement case, reusing its session. It sets up its precondition with `kubectl exec`.
- **specs.md:** no change expected. `agent/specs.md:14` and `:201` already state the control. H13 (F-114) edits `:201`, so rebase on it.
- **design.pen:** no.
- **Depends on:** OD-021 item 1 (which server the agent procedures use) for live re-verification.
- **Overlap (heavy, in `files.go`):**
  - Public **F-102** (S1, write atomicity), **F-115** (S1, a new file truncates an existing one) and F-108 (delete follows links) all touch the same write and resolve paths.
  - One shared safe-open-and-rename helper would serve all four. Either land the public files group first and rebase H30, or have the maintainer merge them into one PR described as file-handling hardening.
  - Split from H13 at the tier-up review, so that merge doesn't carry the RCON change.

### H14 · `fix/018-harden-dashboard-permission-gates`

- **Labels:** `type: security`, `area: web`
- **PR:** "fix(web): align dashboard permission gates with API enforcement". Body: "Gates capture controls on the capture permission in the server's namespace, and passes the server's namespace for mods and action controls."
- **Findings:**
  - F-145 · S3 · Dashboard permission gates differ from API enforcement
- **Files likely touched:**
  - `web/src/routes/ServerDetail.tsx:133-138`
  - `web/src/components/CaptureWidget.tsx:180-203`
  - `web/src/routes/tabs/Mods.tsx:68`
  - `web/src/lib/auth.ts:34-42`
- **Regression tests:**
  - Vitest: `can()` with namespace-only bindings, `CaptureWidget` gating, and `Mods` gating.
  - Playwright (mock) in `web/e2e/specs/rbacEnforcement.spec.ts`.
  - The API already enforces these permissions, so no `buckets.sh` change is needed.
- **specs.md:** yes, `web/specs.md` (the rule that each UI gate uses the same permission and namespace as the API).
- **design.pen:** check first. Settings → Network capture already has a gated state. If the Capture tab has no equivalent in `design.pen`, it has to be designed first (rule 1, with the user's OK).
- **Depends on:** live re-verification of the namespace-scoped case needs OD-021 item 15 (user create always adds a cluster-wide binding for the primary role, so a namespace-only user needs a role with no permissions).
- **Overlap:**
  - Public **F-116** (S2) fixes namespace propagation for the same controls (`Mods.tsx`, ServerActions). Sequence F-116 first.
  - `CaptureWidget` with F-126 and F-190.
  - `auth.ts` with F-138.
  - The similar gating bug in `Servers.tsx` is F-130, which could share a helper.

### H15 · `fix/018-harden-module-verify-status`

- **Labels:** `type: security`, `area: operator`, `area: web`
- **PR:** "fix: record module signature verification and badge from the record". Body: "Records on the Module status which digest was verified, and under which policy, and shows the solid verified badge only when that record exists."
- **Findings:**
  - F-144 · S3 · The installed-module "verified" badge isn't backed by a recorded verification
- **Files likely touched:**
  - `operator/api/v1alpha1/module_types.go` (a new status field next to `appliedDigest` at `:57-60`, then `make generate manifests`)
  - `operator/internal/controller/module_controller.go` (set the field when Verify runs; clear it or re-verify when the source policy changes)
  - `web/src/types.ts`
  - `web/src/lib/verify.ts:22-24,48-51`
  - `web/src/components/modules/ModuleCard.tsx:249-265`
- **Regression tests:**
  - Envtest: the status records a verification only when one ran.
  - Vitest in `verify.test.ts`: an installed entry with no recorded verification maps to the "policy" state.
  - E2E: extend `TestModule_VerifySignedBundleInstalls` (bucket `operator`) to assert the status field.
- **specs.md:** yes, `operator/specs.md` and `web/specs.md`.
- **design.pen:** no new visual expected, because it reuses the existing outline "policy" chip. Confirm in `design.pen`.
- **Depends on:**
  - **H02**: same controller. Rebase after H02, and regenerate the CRDs one branch at a time.
  - OD-021 item 6 for live re-verification.
- **Overlap:** `module_controller.go` with F-046, F-050 and F-062. There's no public web overlap.

### H16 · `fix/018-harden-netguard-policies`

- **Labels:** `type: security`, `area: shared`
- **PR:** "fix(netguard): harden address policies and document call sites". Body: "Extends both dial policies to further special-purpose and metadata ranges, and documents which policy each caller uses."
- **Findings:**
  - F-147 · S3 · `IsAllowed` doesn't refuse two cloud metadata addresses
  - F-148 · S4 · The `IsPublic` denylist misses some special-purpose ranges and the IPv4-compatible form
  - F-146 · S4 · The split of netguard policies across call sites is documented incompletely
- **Files likely touched:**
  - `netguard/netguard.go:1-21,56-68,77-132`
  - `netguard/netguard_test.go:14-66`
  - `netguard/specs.md:8,41-42,76`
  - `docs/security.md:308-313,344`
  - `SECURITY_AUDIT.md` (CGNAT wording)
  - `CLAUDE.md:56,250`
  - `docs/dependencies.md:333-338`
- **Regression tests:** unit table rows in `TestIsAllowed` and `TestIsPublic`, the ones each finding lists. The existing allowed rows must still pass: `10.0.0.1`, `fc00::1` and `127.0.0.1` for `IsAllowed`, and the public rows. The netguard coverage floor is 91%. It's a library, so no E2E. `TestModuleSource_RejectsSSRFTarget` (bucket `operator`) stays the integration guard.
- **specs.md:** yes, `netguard/specs.md`.
- **design.pen:** no.
- **Depends on:**
  - D9: extend the denylist, or narrow the wording? Extending is recommended.
  - The `SECURITY_AUDIT.md` working-tree state (rule 5).
  - Callers affected: the operator's git/http sources, notification sinks, the agent's loopback WebSocket RCON (must stay allowed), and, after H02 or H22 wiring, OCI and the registry.
- **Overlap:**
  - In flight **#421** edits `CLAUDE.md`. Rebase after it merges.
  - `docs/dependencies.md` with F-035.
  - `CLAUDE.md` with F-034, F-237 and F-240.

### H17 · `fix/018-harden-capture-default-filter`

- **Labels:** `type: security`, `area: operator`
- **PR:** "fix(operator): apply the template-port default capture filter". Body: "When no filter is given, builds the default capture filter from the template's advertised ports and protocols."
- **Findings:**
  - F-067 · S3 · A capture with no filter fails instead of using the template-port default
- **Files likely touched:** `operator/internal/controller/networkcapture_controller.go`, plus a filter builder that reads the GameTemplate ports.
- **Regression tests:**
  - A unit test for the filter builder.
  - E2E: extend `TestGameServer_NetworkCaptureStartStopDownload` (bucket `operator`, where capture is enabled in e2e) with a case that sends no filter.
- **specs.md:** yes, `operator/specs.md` (default filter behaviour).
- **design.pen:** no. The dashboard already sends no filter when the field is blank.
- **Depends on:** OD-021 item 17 (the `capture.enabled` override) for live re-verification.
- **Overlap:** `networkcapture_controller.go` with F-051, F-058, F-187, F-190 and F-192.

### H18 · `fix/018-harden-audit-export-fields`

- **Labels:** `type: security`, `area: api`
- **PR:** "fix(api): carry the audit reason to every sink and clarify S3 transport options". Body: "Adds the reason field to webhook, S3 and CSV audit output, and makes the S3 `insecure` option behave as documented (or documents what it does)."
- **Findings:**
  - F-078 · S3 · The audit reason field is missing from webhook, S3 and CSV output
  - F-228 · S3 · The audit S3 `insecure` option means plain HTTP, while the docs say it skips TLS verification
- **Files likely touched:**
  - `api/internal/audit/audit.go:221-229,728-739`
  - `api/internal/audit/s3.go:71-75,231-247`
  - `api/internal/handlers/audit.go:76` (a CSV column)
  - `charts/gameplane/values.yaml` (comment)
  - `docs/install.md` and `docs/security.md`
- **Regression tests:**
  - Unit sink tests assert that `reason` is present.
  - `s3_test.go` against a TLS test server, for whichever semantics D8 picks.
  - E2E: extend `TestAPI_AuditPaginationAndFilter` (bucket `api-auth`) to check the CSV header. No new login.
- **specs.md:** yes, `api/specs.md:338` (sink fields) and the S3 option text.
- **design.pen:** no. The CSV column is a download-format change, so it needs a CHANGELOG line.
- **Depends on:**
  - D8: the semantics of the S3 `insecure` option.
  - Land after H07, so dashboard-provider role assignments are covered.
- **Overlap:**
  - `s3.go` and `values.yaml` with F-224.
  - `audit.go` and `handlers/audit.go` with F-087, F-088 and F-195.

### H19 · `fix/018-harden-syslog-bridge-delivery`

- **Labels:** `type: security`, `area: optional-components`
- **PR:** "fix(audit-syslog-bridge): harden delivery reporting and intake deadlines". Body: "Reconnects or reports failure when the collector has closed the connection, and bounds how long a request body may take."
- **Findings:**
  - F-194 · S3 · The bridge reports delivery for a record written to a closed peer
  - F-199 · S4 · The bridge's body read has no time bound
- **Files likely touched:**
  - `audit-syslog-bridge/main.go:138-168` (handle), `:263` (write deadline), `:283-287` (the `http.Server` settings)
  - `audit-syslog-bridge/bridge_test.go`
  - `audit-syslog-bridge/specs.md:64`
  - `audit-syslog-bridge/README.md:32-35`
- **Regression tests:** unit tests in `bridge_test.go`:
  - After the peer closes, the next record either reaches a new connection or gets a 502.
  - A request with headers but no body is closed within the read bound.
  - There's no E2E bucket for the bridge.
- **specs.md:** yes, `audit-syslog-bridge/specs.md`.
- **design.pen:** no.
- **Depends on:** none.
- **Overlap:** `main.go` with F-198. `specs.md` with F-195, F-196 (untested reconnect and write deadline, which the same tests close) and F-197.

### H20 · `fix/018-harden-steamcmd-preset-defaults`

- **Labels:** `type: security`, `area: modules`
- **PR:** "fix(gp-module): make the steamcmd preset scaffold a non-root template". Body: "Changes the steamcmd preset so the scaffolded template runs the game as a non-root user, matching the preset description and the archetype contract."
- **Findings:**
  - F-169 · S3 · The steamcmd preset doesn't deliver its documented non-root default
- **Files likely touched:**
  - `gp-module/internal/archetypes/archetypes.go:121-122`
  - `gp-module/internal/scaffold/scaffold.go` (emit `spec.security`)
  - `gp-module/internal/archetypes/*_test.go`, `scaffold` tests
- **Regression tests:**
  - A unit test that the scaffolded steamcmd template has a non-root `spec.security`, or a non-root image variant pinned by digest.
  - E2E: extend `TestModule_ScaffoldAndPackage` (bucket `operator`).
- **specs.md:** yes, `gp-module/specs.md`. `specs/done_010-easy-module-building/contracts/archetypes-contract.md:13` is already the contract, so it doesn't change.
- **design.pen:** no.
- **Depends on:** D14: a non-root image variant, or a `spec.security` block?
- **Overlap:**
  - `archetypes.go` and specs with F-167, F-163, F-164 and F-166.
  - Held H26 (F-250) covers the same game-container theme in docs.

## S4

### H21 · `fix/018-harden-operator-rbac-scope`

- **Labels:** `type: security`, `area: operator`
- **PR:** "fix(operator): align dev RBAC manifests and markers with the split-role design". Body: "Makes the dev RBAC manifests bind roles that exist, narrows the kubebuilder markers to the cluster-wide reads and the few cluster-scoped writes, and documents the real grants and the direction of agent authentication."
- **Findings:**
  - F-072 · S4 · The operator dev RBAC manifests bind undefined roles, and the markers are broader than the design
  - F-071 · S4 · The operator RBAC and auth-direction docs differ from the implemented split
- **Files likely touched:**
  - `operator/config/rbac/*.yaml` (the binding, `role_namespace.yaml`, and `role.yaml` regenerated)
  - The kubebuilder `+kubebuilder:rbac` markers in several controllers, then `make manifests`
  - `operator/specs.md` and `docs/architecture.md`
- **Regression tests:**
  - The `operator` bucket's `TestHelmInstall_OperatorLogsClean` guards the chart roles, which don't change.
  - Review the regenerated `role.yaml` diff.
  - The dev path isn't in CI, so there's no new E2E.
- **specs.md:** yes, `operator/specs.md`.
- **design.pen:** no.
- **Depends on:** confirm that the chart's own ClusterRole in `charts/gameplane/templates/operator.yaml` stays as it is.
- **Overlap:**
  - Public **F-063** covers the same `operator/config/` dev path. Sequence or merge them.
  - `operator/specs.md` with F-059 to F-062.
  - `docs/architecture.md` with F-033, F-044, F-050, F-171, F-176 and F-240.

### H29 · `fix/018-harden-tunnel-credential-switch`

- **Labels:** `type: security`, `area: api`
- **PR:** "fix(api): keep one tunnel credential per provider". Body: "Replaces the stored tunnel credential when the provider changes, so the Secret holds exactly one provider's key and reads are deterministic."
- **Findings:**
  - F-084 · S4 · A tunnel credential provider switch leaves the previous key behind
- **Files likely touched:** `api/internal/handlers/tunnelcreds.go:136-141,225-231`.
- **Regression tests:**
  - An API handler test: after a provider switch, only the new key remains, and `get` is deterministic.
  - E2E: none exists for tunnel credentials. A switch round-trip costs one admin login, so it goes in `operator` (rule 7; H09 and H22 already plan to use `api-mods`), or a waiver is requested for this S4.
- **specs.md:** yes, `api/specs.md` (tunnel credentials).
- **design.pen:** no.
- **Depends on:** none.
- **Overlap:**
  - `tunnelcreds.go` with public **F-118** and **F-119** (both S2). Land those first.
  - Split from H10 at the tier-up review: it's an API change with no file in common with H10's operator, chart and docs work.

### H22 · `fix/018-harden-api-input-limits`

- **Labels:** `type: security`, `area: api`
- **PR:** "fix(api): bound module archive extraction and align registry fetch spec". Body: "Enforces a total decompressed budget during module archive extraction, and aligns the spec and the code on how registry fetches are dialled."
- **Findings:**
  - F-100 · S4 · Module archive extraction has no total decompressed budget
  - F-099 · S4 · `api/specs.md` claims netguard guards registry fetches
- **Files likely touched:**
  - `api/internal/handlers/module_upload.go:78-87,303-312`
  - `api/internal/registry/registry.go:204-209` (if D6 picks wiring)
  - `api/specs.md:430,548`
- **Regression tests:**
  - A unit test: `extractUploadArchive` fails before full expansion when the members together exceed the budget.
  - If wired, a unit test that a provider base URL at a link-local address returns `netguard.ErrBlockedAddr`.
  - E2E: no test reaches `POST /modules/sources/{name}/upload` today. `TestModuleSourceUpload` and `TestModule_ScaffoldAndPackage` (bucket `operator`) create the bundle ConfigMap directly, so they never run `extractUploadArchive`. Add an upload round-trip (a valid bundle is stored; a bundle over the total budget gets 4xx) as a new test in `api-mods` (one admin login, rule 7), or request a waiver for this S4.
- **specs.md:** yes, `api/specs.md`.
- **design.pen:** no.
- **Depends on:** D6: wire `IsPublic` into the registry client, or narrow the spec? Wiring is recommended.
- **Overlap:**
  - `module_upload.go` with F-083.
  - Body limits with F-075.
  - `api/specs.md` with F-085 to F-089.

### H23 · `fix/018-harden-telemetry-ingest-validation`

- **Labels:** `type: security`, `area: optional-components`
- **PR:** "fix(telemetry-receiver): enforce the documented ingest payload contract". Body: "Rejects payloads that are missing required fields, aren't a JSON object, or carry trailing content, and keeps the body-size limit for the whole body."
- **Findings:**
  - F-202 · S4 · Telemetry ingest accepts payloads missing required fields
  - F-203 · S4 · Telemetry ingest accepts trailing content after the first JSON value
- **Files likely touched:**
  - `telemetry-receiver/main.go:125-140`
  - `telemetry-receiver/main_test.go`
  - `telemetry-receiver/README.md:21` (check only)
- **Regression tests:** unit tests in `main_test.go`:
  - a missing field or a `null` body gets 400 and isn't counted
  - trailing content gets 400
  - an oversized body gets 413
- **specs.md:** no change. `telemetry-receiver/specs.md:14,45,55-57` already states the contract.
- **design.pen:** no.
- **Depends on:** none.
- **Overlap:** `main_test.go` with F-201. `specs.md` with F-200.

### H24 · `fix/018-pin-dev-ingress-manifest`

- **Labels:** `type: security`, `area: shared`
- **PR:** "chore(deploy): pin the dev ingress-nginx manifest to a release tag". Body: "Pins the kind dev cluster's ingress-nginx manifest to a release tag held next to the MetalLB version."
- **Findings:**
  - F-235 · S4 · The dev ingress-nginx manifest is fetched from a floating ref
- **Files likely touched:** `deploy/kind/up.sh:28-31,196`.
- **Regression tests:** none automated. CI doesn't install ingress-nginx, and this is a dev-only script. Verify by reading the change and by a `make dev-up` run by the maintainer.
- **specs.md:** no.
- **design.pen:** no.
- **Depends on:** none.
- **Overlap:** `up.sh` with public F-232, F-234 and F-237. In flight #422 edits `deploy/kind/upgrade.sh` only.

### H25 · `fix/018-harden-mcp-readonly-docs`

- **Labels:** `type: docs`, `area: optional-components`
- **PR:** "docs(mcp-server): state the read-only layers precisely". Body: "Describes what the package boundary does and doesn't enforce, and names RBAC as the layer that stops mutation."
- **Findings:**
  - F-211 · S4 · The MCP package-boundary read-only claim is broader than what's enforced
- **Files likely touched:** `mcp-server/main.go:8-17` (package doc), `mcp-server/specs.md:77-91`, `mcp-server/README.md:12-25`.
- **Regression tests:** none (docs). The CI doc gates (`check-specs`, `check-links`) apply.
- **specs.md:** yes, `mcp-server/specs.md`.
- **design.pen:** no.
- **Depends on:** none.
- **Overlap:** the same three files carry public F-207, plus F-208 and F-210. Merge them into one docs pass, or sequence them.

### H26 · `fix/018-harden-security-docs-accuracy`

- **Labels:** `type: docs`, `area: shared`
- **PR:** "docs: align security and install docs with the implemented posture". Body: "Corrects the stated game-container security posture and the empty `groupsClaim` behaviour."
- **Findings:**
  - F-250 · S4 · `docs/security.md` states a game-container posture the operator doesn't set
  - F-229 · S4 · `install.md` says an empty `groupsClaim` disables mapping
- **Files likely touched:** `docs/security.md:226-238,271-274` and `docs/install.md:131-134,147-148`.
- **Regression tests:**
  - Docs only: none, apart from the CI doc gates.
  - If D4 picks the code change for F-250: a unit test in `build_game_container_test.go`, E2E in bucket `operator`, and bot-fast as a regression guard. The group's labels then become `type: security`, `area: operator`.
- **specs.md:** no for the docs-only option. If D4 picks code, `operator/specs.md` changes.
- **design.pen:** no.
- **Depends on:** D4: docs only, or harden the game container?
- **Overlap:**
  - Public **F-033** edits the same capture paragraph of `docs/security.md`, and F-225 edits the podSecurity paragraph. Sequence or merge them.
  - `docs/install.md` with F-027 and F-212 to F-226.
  - Held H20 covers the same theme.

---

## In flight

No held finding is `fixing`. The public PRs in flight, and where they touch held groups:

| PR | Public finding | Files | Held overlap |
|----|----------------|-------|--------------|
| #420 | F-026 | `hack/check-doc-versions.sh` | none (docs groups H25 and H26 must still pass it) |
| #421 | F-030 | `CLAUDE.md` | **H16** (`CLAUDE.md:56,250`): rebase after #421 merges |
| #422 | F-029 | `deploy/kind/upgrade.sh`, `.github/workflows/ci.yaml`, `.claude/agents/ci-triager.md` | none (H24 edits `up.sh`; H05 edits the release, images and publish-edge workflows) |
| #423 | F-043 | `CHANGELOG.md` | every held PR adds a hardening line to the next RC section (rule 4) |

## Re-check first (T049)

| ID | Why re-check | How |
|----|--------------|-----|
| (none) | | |

All 57 held rows are `open`; none is `imported`. F-256 was confirmed against `c44cb179` (origin `master`, the RC-TAG-1 target), and its cited files are unchanged since `13a859ff`. The reviews read `master` at `13a859ff`, which is still `master` (checked 2026-09-24: `git log 13a859ff..master` is empty), or branch commit `213bdaa7`, which adds only spec files on top of it. No held finding can have been fixed since it was recorded, so T049 has nothing to do for held rows. Re-verification after each fix follows T058.

## Proposed closures

| ID | Proposed status | Justification | Roadmap citation |
|----|-----------------|---------------|------------------|
| (none) | | | |

These candidates were considered and rejected:
- **Docs-vs-code mismatches:** F-073, F-099, F-114, F-146, F-211, F-229, F-250. FR-013 requires correcting whichever side is wrong, so these are fixes, not `not-a-defect`.
- **Findings with limited reachability:** F-100, F-148, F-199. Limited reachability doesn't close a gap between the documented contract and the code (US3 AS-3 allows no other deferral).
- **Dev-only:** F-235. The repo's own pinning rule (`deploy/kind/up.sh:28-31`) makes it a defect.
- **Out-of-scope** requires a capability that `docs/roadmap.md` places after v0.3:
  - Nothing in the roadmap covers tunnels (F-068, F-069, F-177, F-178) or network capture (F-067).
  - The only multi-cluster follow-up (`docs/roadmap.md:21-22`) is about WebSocket streams. F-098 concerns an HTTP download.
  - The Postgres entry (`docs/roadmap.md:228-234`) doesn't apply to F-079 or F-095, because the defect is on the shipped SQLite DSN.
  - The HMAC-keyed audit chain (`docs/roadmap.md:59-60`) doesn't apply to F-078, F-194 or F-199.

## Decisions requested

These come from the findings' own "maintainer decides" points and from this plan. Held questions stay off-git, so if the maintainer wants them tracked, they go into `held/questions.md` as HQ-002 onwards. This plan didn't write them there.

| # | Group | Question | Recommendation |
|---|-------|----------|----------------|
| D1 | H27 | Should kubeconfig Secrets created with kubectl or GitOps (install.md Path 1) be deletable over HTTP? If not, cluster create also sets `managed-by=gameplane-api`, and delete requires it. | Require both labels |
| D2 | H10 | What egress scope do playit tunnel pods need? | Maintainer |
| D3 | H13 | F-114 items 3 and 5: change the code (reject dotfiles in `resolve`; refuse `--tls-cert` without `--tls-key`), or change the spec? | Code for item 5; maintainer for item 3 |
| D4 | H26 | F-250: docs only, or have the operator set `allowPrivilegeEscalation: false` and drop `NET_RAW` on game containers? | Docs now; code as a separate, bot-tested change |
| D5 | H02 | F-066: dial OCI and cosign through netguard, or narrow the docs? | Wire |
| D6 | H22 | F-099: dial registry fetches through `IsPublic`, or narrow the spec? | Wire |
| D7 | H11 | F-178: apply the tailscale tags, or document that they aren't applied? | Apply |
| D8 | H18 | F-228: keep TLS and skip verification only, or document that the option means plain HTTP? | Maintainer |
| D9 | H16 | F-148: extend the denylist, or narrow the "only globally routable" wording? | Extend |
| D10 | H09 | F-079/F-095: explicit deletes plus a cleanup migration, or enable SQLite foreign keys in the DSN? | Explicit deletes |
| D11 | H04 | F-230: skip generating chart Secrets for overridden names, or document the Helm ownership metadata? Is a new `helm template` check harness acceptable? | Skip generation; harness needs sign-off |
| D12 | H04 | F-231: narrow the `apiServerCIDRs` default, or document the current reach? | Maintainer (narrowing can break clusters whose apiserver sits elsewhere) |
| D13 | H28 | F-227: an nginx location rule, or a separate metrics listener? | Separate listener (testable in the `operator` bucket) |
| D14 | H20 | F-169: a non-root image variant, or a `spec.security` block? | `spec.security` |
| D15 | H12 | F-070: re-own or copy the references, drop them, or fail the Restore with a clear message? | Maintainer |
| D16 | H05 | Repository settings: a signing environment with a deployment policy, the `COSIGN_*` secrets moved into it, and a `v*` tag ruleset. Only the maintainer can do these. | Required |
| D17 | H08, all | (a) Keep the broad default trusted-proxy list once the peer check exists? (b) May `type: security` labels be visible on held PRs before merge? | (a) Keep, with docs; (b) maintainer |
| D18 | H31 | F-256: (a) Who counts as an admin for owner-only server operations? Holders of `*` only (the built-in admin role, since custom roles can't hold `*`), or also a new delegable catalog permission? (b) Scope: only transfer, collaborator edits, wipe and delete (as HQ-001 describes), or also other writes to servers the caller doesn't own (`PUT`, lifecycle verbs, console, files, Restore)? (c) Should F-256 stay S2, or be raised to S1? | (a) `*` only; (b) the four operations now, with the rest as a separate decision; (c) maintainer |

**Existing open decisions that affect these groups:**
- **HQ-001** was answered on 2026-09-24 and recorded as F-256 (group H31, decision D18).
- **OD-021** items affect live re-verification only, not the code fixes:
  - items 6 and 21: H02 and H15
  - items 9, 16 and 20: H12 (live re-verification of F-070 is blocked while there are no CSI snapshots)
  - item 15: H06 and H14
  - item 17: H17
  - item 19: H01 and H27
  - item 1: H13 and H30 (which server the agent procedures use)
- **OD-022:** no held group touches `.github/actions/` or `images/` today. If D14 picks a non-root image variant built from `images/common/steamcmd` instead of a `spec.security` block, H20 enters `images/`, which has no coverage row until OD-022 is settled, and overlaps public F-017's Dockerfile.
- **OD-023** (blocks T061): H04 and H28 (chart values and templates), H09 (a possible migration) and the CRD-changing groups H02, H11, H15 and H21 (and H10 if it changes the CRD) should merge before the upgrade round (T061), so the beta.8 → RC upgrade covers them. When T061 runs depends on the OD-023 option. Under (e) it waits for public F-212 (the games Namespace keep policy, public group 3) to ship in an RC, so these held groups belong in that RC or an earlier one. Under (a) the beta.8 → RC step runs only in CI, whose `upgrade` bucket doesn't use `--reuse-values` (rule 10), so the chart-value groups get no `--reuse-values` check at all.

## Shared-file hot spots

Files edited by more than one held group, or by held and public work together. Merge these in the order given, rebasing after each merge.

| File | Held groups | Public findings | Suggested order |
|------|-------------|-----------------|-----------------|
| `api/cmd/main.go` | H01, H07, H08, H28 (if D13 = listener) | F-074, F-075, F-081, F-135, F-223, F-224 | H01 → H08 → H07 → H28 |
| `operator/internal/controller/module_controller.go` | H02, H15 | F-046, F-050, F-062 | F-046 → H02 → H15 |
| `agent/internal/files/files.go` | H30, H13 (only if D3 item 3 = code) | F-102, F-115, F-108 | public S1 files fixes → H30 (or one merged PR) → H13 |
| `charts/gameplane/templates/networkpolicies.yaml` | H04, H10 | F-215, F-217 | F-215 → H04 → H10 |
| `web/nginx.conf.template` | H28 (if D13 = nginx) | F-251 | F-251 → H28 |
| `tunnel/main.go`, `gameserver_tunnel.go`, `docs/tunnels.md` | H10, H11 | F-052, F-055, F-172, F-173, F-174, F-175, F-031 | F-173/F-174 → H11 → H10 |
| `api/internal/handlers/tunnelcreds.go`, `resources.go` | H11, H29, H31 (`resources.go` only, if the handler-level check is chosen) | F-118, F-119 | F-118/F-119 → H31 → H11 → H29 |
| `api/internal/rbac/rbac.go` | H06, H31 | F-087 (spec statements only) | H31 → H06 |
| `api/internal/db/shares.go`, `handlers/shares.go` | H09 | F-082, F-123, F-120 | F-082 → H09 |
| `web/src/routes/AdminSettings.tsx` | H03 | F-127, F-128, F-134 (and F-076 API side) | F-076 → H03 |
| `web/src/routes/tabs/Mods.tsx`, `CaptureWidget.tsx`, `lib/auth.ts` | H14, H31 (`lib/auth.ts` only, if the gate helper goes there) | F-116, F-126, F-138, F-190 | F-116 → H14; H31 before H14 if both edit `lib/auth.ts` |
| `operator/api/v1alpha1/*_types.go` and generated CRDs | H02, H11, H15, H21 | F-031, F-045, F-047, F-056, F-057, F-118, F-119 | one at a time, regenerating after each rebase (rule 9) |
| `docs/security.md` | H02, H04, H06, H07, H08, H16, H18, H26, H27, H28, H31 | F-033, F-225, F-243 | docs-only H26 last |
| `docs/install.md` | H04, H18, H26, H27 (if D1 changes Path 1), H28 | F-027, F-212 to F-226 | H26 last |
| `charts/gameplane/values.yaml` | H04, H08, H09, H18, H28 (if D13 = listener) | F-028, F-212, F-214, F-219, F-221 to F-226, F-252, F-255 | F-214 first (keys safe under `--reuse-values`) |
| `SECURITY_AUDIT.md` | H13, H16 | (the uncommitted OD-019 rewording) | settle the working tree first |
| `CLAUDE.md` | H16 | #421 (F-030), F-034, F-237, F-240 | #421 → H16 |
| `.github/workflows/release.yaml`, `images.yaml`, `publish-edge.yaml` | H05 | F-219, F-239, F-245 | H05 before the next RC tag |
| `operator/config/` | H21 | F-063 | merge the two, or F-063 → H21 |
| `mcp-server/{main.go,specs.md,README.md}` | H25 | F-207, F-208, F-210 | merge into one docs pass |

## Classification notes (for the maintainer, no change made)

- Some held rows may not need holding under OD-019's definition, because they describe functional failures rather than a bypassable control: F-067, F-070, F-079, F-202 and F-203. If the maintainer releases them to `audit/findings.md`, their groups (H17, H12, H09, H23) can use ordinary fix wording.
- F-095's table title (oidc_links/preferences) doesn't match its subsection (share links). F-079 already covers oidc_links. A title fix is suggested.
