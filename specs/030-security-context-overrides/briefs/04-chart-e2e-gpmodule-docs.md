# Brief 04: chart, E2E, gp-module, module repo, docs

Scope: T030, T042, T053, T054, T058, T064 (chart, E2E), T038 (gp-module), T036 (module repo, validate.py, schema), T065-T068 (docs and specs). Facts checked against the tree at `/home/user/Gameplane` on `feat/030-security-context-overrides` and the module repo at `/home/user/module`. No source file was edited.

Note on the tree: `modules/` in `/home/user/Gameplane` is an empty, uninitialised submodule. The module repo itself is at `/home/user/module` (contains `validate.py`, `test-validate-py.sh`, `palworld/`, `.schema/`).

## Discrepancies

1. **Chart T058 placement (CORRECTED by review).** `charts/gameplane/templates/api.yaml:16` opens `{{- if not .Values.api.standalone }}` but it closes at `:136`, BEFORE the `gameplane-api-logs` Role/RoleBinding (`:137-164`). So that Role is rendered in standalone installs too. `:165` `{{- end }}` closes the `apiEnabled` guard opened at `:8`. Insert the new Role/RoleBinding after `:164` and BEFORE `:165` (inside `apiEnabled`; do not place after `:165`). No standalone decision is needed (Q2 withdrawn).
2. **Chart T058 `create` verb.** Kubernetes does not enforce `resourceNames` on `create` (the name is unknown at authorisation time). A Role with `resourceNames: [gameplane-security-settings]` and verb `create` grants create on every ConfigMap in the release namespace. Options: a second Role without `resourceNames` for `create` only, or drop `create` and pre-create the ConfigMap via Helm (then the API only needs get/update/patch). Surface to the human before implementing.
3. **Chart T058 test fixture.** No Helm RBAC fixture exists. The only chart test is `charts/gameplane/tests/monitoring-rules.sh` (run from `.github/workflows/ci.yaml:1232`). CI runs `helm template` at `ci.yaml:1291, 1313, 1329`. T058's "add a Helm check if a fixture exists" resolves to: none exists. Adding one is a new test, so ask first (CLAUDE.md rule 1).
4. **suite-components.json line numbers.** Spec says `:32, 46`. Actual: `"^TestGameServer_"` at `test/e2e/suite-components.json:34` and `"^TestAPI_"` at `:48`. Mapping content is as the spec says.
5. **buckets.sh tally comment location.** Spec cites `buckets.sh:355-362` for the "operator: 4 admin logins" tally. That text is in the comment block above `bucket_multicluster` (starts `:356`, tally line `:363`, function `:367`). `bucket_operator()` is at `test/e2e/buckets.sh:36` and has no tally comment. The T064 comment must be added above `:36` (before `bucket_operator() {`). The "4" figure is not verified here (no login count was done).
6. **Busybox helpers do not take a security block.** `test/e2e/test_helpers_e2e_test.go:42-70` (`applyBusyboxTemplate`) builds a GameTemplate with no `security` key. `:73-113` (`applyBusyboxGameServer`, `applyBusyboxGameServerSpec`) builds a GameServer with only `templateRef` and optional `capture`. Spec's "`:42-90`" range is loose. T030 needs either a new helper variant that accepts a `security` map, or inline `unstructured` objects inside `gameserver_security_e2e_test.go`.
7. **gp-module template validator is not in rules.go.** The template-schema check is `validateTemplateSchema` in `gp-module/internal/validator/schema.go:279`. Template-level calls are wired in `gp-module/internal/validator/validator.go:167-170` (`validateTemplateSchema`, `validateImageDigest`, `validatePorts`, `validateConfigSchemaRules`). `rules.go` holds `validatePorts` (`:147`) and `validateConfigSchemaRules` (`:261`). New checks may live in `rules.go` (as the plan says) but must be called from `validator.go` next to `:169-170`.
8. **gp-module has no security knowledge today.** `grep -i security|risky|fsGroup` over `internal/validator` and `internal/preview` returns nothing. The only `DefaultSecurity` uses are `internal/archetypes/archetypes.go:83-85` (type `SecurityDef`, field `DefaultSecurity`) and `internal/scaffold/scaffold.go:167-168, 213-217`. Scaffold lines match the spec.
9. **gp-module preview has no security output.** `internal/preview/preview.go` `Result` struct is at `:44-60` (fields `Game`...`RenderedManifest`); `FormatHuman` at `:317`. Preview needs new fields and output lines. There is no existing security section to extend.
10. **gp-module rule IDs.** `internal/validator/report.go:22-38` is the rule-ID const block. No ID for risky/ownership exists. Either reuse `RuleTemplateSchemaViolation` (`report.go:25`) or add new IDs in that block.
11. **validate.py rule locations (T036).** Spec cites `validate.py:610-660, 903-958`. Actual: `rule_nonroot_requires_runasuser` at `:611`, `rule_runasuser_requires_home` at `:654`, `rule_puid_image_needs_fsgroup` at `:902` (docstring `:903`, body to `:963`). Ranges are roughly right.
12. **validate.py placement.** Image-dependent rules run inside `for ref in image_refs: ... if not result.get("ok"): continue` (`validate_module`, `:1128-1151`). `validate_module` returns early at `:1131-1132` when there are no image refs. The three new template-only checks do not need network or image config and should go after `findings += rule_rcon_protocol(spec)` (`:1153`), outside the image loop. `Finding(level, rule, message)` at `:134-137`; `ERROR`/`WARN` constants at `:111-112`.
13. **test-validate-py.sh covers layout only.** The script (`/home/user/module/test-validate-py.sh`, 111 lines) tests directory-layout rules through a `check <name> <expected> <actual>` helper (`:9-15`) and a `create_valid_fixture` (`:~30`) that writes `template.yaml`. Cases grep `validate.py` output for rule IDs. New security cases follow this pattern. No existing case covers template security rules.
14. **Module-authoring section range (T065).** Spec cites `docs/module-authoring.md:1137-1173`. Actual `### Security context` heading is `:1157`; section ends at `:1194` (next heading `### Reserved in-pod ports` at `:1195`). `:1129` is `### Console mode and game log`; `:1137-1156` is inside that section.
15. **docs/security.md insertion point (T066).** `## Pod security` is at `:435` (correct). Its last subsection is `### Network Capture Security Exception` at `:478`. `## Module supply chain` at `:564` is the next level-2 heading. Insert the new `### Server security overrides` before `:564`.
16. **Palworld template gap.** `/home/user/module/palworld/template.yaml:63-64` is `security:` / `fsGroup: 1000`. No task edits it, yet `data-model.md:41` and `contracts/crd-gametemplate-security.md` illustrate Palworld marking `runAsUser` risky. Also, a template with `fsGroup` and no `volumeOwnershipCheck` now means `OnlyWhenWrong` (the default flip), which changes Palworld's behaviour. Ask whether T036 should add `risky`/`volumeOwnershipCheck` to Palworld.
17. **CRD line citations (used by T036 regen chain).** `data-model.md:7, 18` and `contracts/crd-gametemplate-security.md:5` cite `GameSecuritySpec` at `gametemplate_types.go:185`. Actual type declaration is `:194`; the field is `:186` (`Security *GameSecuritySpec`), with the comment at `:181-185`. Contract cites `GameTemplateSpec.Security` at `:177`; actual is `:186`. Outside this area; noted for the operator brief.
18. **Makefile target writes into the submodule.** `hack/gen-module-schema.py:21` CRD path, `:22` OUT path `modules/.schema/gametemplate.schema.json`. The script writes directly into the submodule checkout (no copy step). The spec's "output copied into the module repo" is only true when `modules/` is initialised. Makefile `module-schema` is at `Makefile:387-388`. `/home/user/module/.schema/` already holds `gametemplate.schema.json` and `module.schema.json`.
19. **T053 non-admin role.** Built-in role names: `api/internal/rbac/rbac.go:48-49` (`RoleOperator = "operator"`, `RoleViewer = "viewer"`); admin holds `*`. Test helper `Env.CreateUser(t, admin, role, prefix)` at `test/e2e/env.go:713`. Use role `operator` for the non-admin. This relies on the operator role holding `servers:write` and not `servers:security` (OD-1). Not verified in code here; verify in `rbac.go` before relying on it.
20. **KubectlExec target form.** `env.go:259` `KubectlExec(t, ns, target, cmd...)` takes a `pod/<name>` target. Existing tests use `"pod/"+gs+"-0"` (e.g. `api_agent_e2e_test.go:185`). T030's `id -u` and T054's pod UID checks must use `"pod/"+gsName+"-0"`.
21. **T067 spec anchors.** Verified headings: `operator/specs.md` `## Reconcilers` :352, `## Key invariants` :775, `## Security considerations` :844. `api/specs.md` `## External interface / contracts` :88, `## Security considerations` :655. `web/specs.md` `## ServerDetail Settings Sub-sections` :990, `## External Interface / API Client` :1015. `gp-module/specs.md` `## Responsibilities` :16, `## Key Invariants` :84. `mcp-server/specs.md` `## Responsibilities` :10, `### Registered tools (7 total)` :51. `test/e2e/specs.md` `### Backup preparation acceptance` :160-170, `## Non-goals` :171 (insert the two-test subsection before `:171`). `test/e2e/specs.md` `## The bucket contract` at :185.
22. **T068 task map.** `docs/agent-architecture.md` task map is the table at `:47-58` (header `| Task | Read |`, `| --- | --- |`). No security row exists. The five new files named in T068 do not exist yet (checked). The spec's `operator/internal/controller/security_settings.go` naming matches plan.md:132 only with the controller path; plan.md:132 lists it under `internal/controller/`.

## Edit briefs

### T030 (E2E, operator bucket, zero logins)
- New file `test/e2e/gameserver_security_e2e_test.go`, first line `//go:build e2e`, package `e2e`. Pattern: `gameserver_e2e_test.go:51` uses `t.Parallel()`.
- Template: inline `unstructured` GameTemplate (pattern `test_helpers_e2e_test.go:45-66`) with `spec.security: {fsGroup: 1000}` (module default). Server: inline GameServer (pattern `:88-113`) with `spec.security: {runAsUser: {value: <uid>}}`.
- Assert: `envInstance.KubectlExec(t, ns, "pod/"+gs+"-0", "id", "-u")` (`env.go:259`) equals the override uid; `id -g` and other ids at module default.
- Use `waitStatefulSetReplicas` (`test_helpers_e2e_test.go:261`) before exec.
- Unique names via a per-test suffix; cleanup via `t.Cleanup` like `:76-80`.

### T042 (same test, part 2)
- Change override, assert same pod UID (`pod.metadata.uid`), `status.security.pending == true`, `status.security.applied` unchanged.
- Restart, assert new uid and `pending == false`. Remove override, restart, assert module default uid.

### T054 (same test, part 3, zero logins)
- Save root `fsGroup` override (`value: 0`), stop server, ensure ConfigMap `gameplane-security-settings` in the control-plane namespace is absent or `"false"` (`kubectl`, via `envInstance.Kubectl`, `env.go:~245`).
- Start; assert replicas stay 0 and condition `SecurityAllowed=False` reason `RootOverrideDisallowed`.
- Set ConfigMap `allowRootOverrides: "true"` with `kubectl apply`; assert the server starts.
- Set back to `"false"`; assert running pod keeps UID and `restartCount`.
- Control-plane namespace name: from `envInstance` (confirm the field name in `env.go` before use; not checked here).

### T053 (new file `test/e2e/api_security_override_e2e_test.go`, one admin login plus one non-admin)
- Admin: `envInstance.BootstrapAdmin` then `envInstance.APIClient(t, adminUsername, adminPassword)` (`env.go:301, 503`; pattern `api_auth_e2e_test.go:119-127`).
- Non-admin: `envInstance.CreateUser(t, admin, "operator", "e2e-sec")` (`env.go:713`), then `envInstance.APIClient(t, u, pw)`.
- Refused 403 on create and update without `servers:security`; accepted after granting it (a role with the key; admin can create a custom role or grant it via the role endpoint, to be confirmed in `api/internal/rbac`).
- Root value refused 403 while `PUT /admin/config/security` is off; accepted after admin sets `{"allowRootOverrides":true}` (`contracts/cluster-setting.md`).
- ConfigMap mirror checked with `kubectl get configmap` after each flip.
- Audit log rows checked through the existing audit endpoint used in other `api_*_e2e_test.go` files (not verified which).

### T064 (`test/e2e/buckets.sh`)
- Add `TestGameServer_SecurityOverrideAppliesOnRestart` and `TestAPI_SecurityOverride_PermissionAndRootRule` to `bucket_operator()` heredoc (`:36-125`, `EOF` at `:125`), in alphabetical position within their groups.
- Add tally comment immediately above `bucket_operator() {` at `:36`.
- No change to `suite-components.json`, CI matrix, or any other bucket. `buckets.sh verify` must pass.

### T058 (`charts/gameplane/templates/api.yaml`)
- Insert after `:164` (last line of the logs RoleBinding) and before the `{{- end }}` at `:165` (closes the `apiEnabled` guard from `:8`; next block starts `:170`):
```yaml
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ .Release.Name }}-api-security-settings
  namespace: {{ .Release.Namespace }}
  labels: {{- include "gameplane.labels" . | nindent 4 }}
rules:
  - apiGroups: [""]
    resources: [configmaps]
    resourceNames: [gameplane-security-settings]
    verbs: [get, update, patch]
  # create cannot be restricted by resourceNames (see brief Discrepancy 2)
  - apiGroups: [""]
    resources: [configmaps]
    verbs: [create]   # DECISION PENDING
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ .Release.Name }}-api-security-settings
  namespace: {{ .Release.Namespace }}
  labels: {{- include "gameplane.labels" . | nindent 4 }}
roleRef: { apiGroup: rbac.authorization.k8s.io, kind: Role, name: {{ .Release.Name }}-api-security-settings }
subjects:
  - kind: ServiceAccount
    name: gameplane-api
    namespace: {{ .Release.Namespace }}
```
- The second rule is a placeholder: do not ship `create` without the human decision. Operator ClusterRole `get;list;watch` on ConfigMaps is `operator.yaml:66` (confirmed, in `gameplane-operator-watch` ClusterRole, `:16-114`); no change to operator.yaml.
- No chart test added without sign-off (Discrepancy 3).

### T036 (module repo `/home/user/module`, separate PR)
- Regenerate: `make module-schema` from the Gameplane root (`Makefile:387`), which runs `hack/gen-module-schema.py`. Output: `modules/.schema/gametemplate.schema.json`. Requires the CRD regen from T007 first. Run only after the human has the branch ready (CLAUDE.md rule 8: no local tests; running the generator is not a test but needs the submodule initialised).
- `validate.py` new function(s), called from `validate_module` after `:1153`:
  - `risky` member not in {`runAsUser`, `runAsGroup`, `fsGroup`, `volumeOwnershipCheck`} → ERROR.
  - `risky.<member>.reason` length > 200 → ERROR.
  - `spec.security.volumeOwnershipCheck` set and `spec.security.fsGroup` unset → WARN.
  - Use `get_path(spec, "security.risky")`, `get_path(spec, "security.fsGroup")` (`validate.py:515`).
  - Severity constants `ERROR`/`WARN` (`:111-112`).
- `test-validate-py.sh`: add cases after the existing layout cases (end of file, before the summary); follow the `check` helper (`:9-15`). Each case greps output for the new rule ID.
- `palworld/template.yaml`: no change unless the human decides (Discrepancy 16).

### T038 (gp-module)
- `gp-module/internal/validator/rules.go`: add three functions (template-only, operating on `*yaml.Node`, path lookup via `common.FindNode(node, "spec.security.risky")` as used in `schema.go` around the backupFlush check). Wire in `gp-module/internal/validator/validator.go` after `:170`.
- Rule IDs: add to `report.go:22-38` or reuse `RuleTemplateSchemaViolation`.
- Tests: add table cases to `gp-module/internal/validator/validator_test.go` (pattern `TestValidate_*`, e.g. `:260` ConfigSchemaAndMemory). Coverage gate for gp-module is 80 (CLAUDE.md).
- `gp-module/internal/preview/preview.go`: add `Security` (or similar) to `Result` at `:44-60`; populate in `GeneratePreview` after template parse (`:80-85`); print in `FormatHuman` (`:317`): resolved defaults, ownership check, risky settings with reasons. Tests in `preview_test.go` (pattern `TestPreviewManifest` `:37`).
- Leave `internal/scaffold/scaffold.go:167-168, 213-217` and `internal/archetypes/archetypes.go:83-85` unchanged (spec: no archetype asks for risky).

### T065 (`docs/module-authoring.md`)
- Replace `### Security context` section, lines `1157-1194` (not `1137-1173`).
- Content per FR-013: block is a default, servers may override; new fields `volumeOwnershipCheck` and `risky` (field table); when to mark risky; reason wording (one sentence, what breaks); when to set `EveryStart`; default flip to `OnlyWhenWrong`.

### T066 (`docs/security.md`)
- Insert `### Server security overrides` before `## Module supply chain` at `:564` (after `### Network Capture Security Exception` `:478`). Covers: `servers:security` (admin by default), root rule and `allowRootOverrides`, `RootOverrideDisallowed`, apply-on-restart, applied fsGroup for backups, ownership-check trade-off, audit trail, `kubectl` writers bypass API checks.

### T067 (specs)
- `operator/specs.md`: update `## Reconcilers` (`:352`), `## Key invariants` (`:775`), `## Security considerations` (`:844`).
- `api/specs.md`: `## External interface / contracts` (`:88`), `## Security considerations` (`:655`).
- `web/specs.md`: `## ServerDetail Settings Sub-sections` (`:990`), `## External Interface / API Client` (`:1015`).
- `gp-module/specs.md`: `## Responsibilities` (`:16`), `## Key Invariants` (`:84`).
- `mcp-server/specs.md`: `### Registered tools (7 total)` (`:51`), T022 read-only note.
- `test/e2e/specs.md`: new `### Server security override acceptance` subsection before `## Non-goals / boundaries` (`:171`); lists the two tests.

### T068 (`docs/agent-architecture.md`)
- Add a row to the task map table (`:47-58`), format `| <task> | <paths, comma-separated, backticked> |`. Suggested row: `| Server security overrides (run-as user or group, fsGroup, root rule, ownership check) | operator/specs.md, api/specs.md, web/specs.md, docs/security.md, docs/module-authoring.md, operator/internal/controller/gameserver_security.go, operator/internal/controller/security_settings.go, api/internal/handlers/resources_security.go, api/internal/handlers/security_settings_sync.go, web/src/components/SecurityAdvanced.tsx |`. None of these files exist yet (checked).

## Open questions for the human (do not decide in code)
- Q1: T058 `create` verb scope (Discrepancy 2).
- Q3: Helm RBAC check: add a new chart test or skip (Discrepancy 3).
- Q4: Palworld `risky` and `volumeOwnershipCheck` (Discrepancy 16).
- Q5: T053 role for the non-admin: `operator` (needs confirmation that it holds `servers:write` and not `servers:security`).
