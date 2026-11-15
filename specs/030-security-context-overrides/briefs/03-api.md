# Brief 03: API (T009-T014, T028, T033, T047-T049, T055-T059)

Factual scout brief. Repo `/home/user/Gameplane`, branch `feat/030-security-context-overrides`. All line numbers are as found today (read-only scout, no source edited). Spec sources: `spec.md`, `plan.md`, `research.md` (R9-R13), `data-model.md`, `contracts/rbac-permission.md`, `contracts/api-server-security.md`, `contracts/cluster-setting.md`, `contracts/audit-events.md`, `tasks.md`.

Abbrev: VAP = `validateAndProtectGameServer`. CS = `writeServerValidationError`.

---

## 0. Citation corrections (spec says -> code has)

| Spec citation | Actual | Note |
|---|---|---|
| `resources.go:538` (VAP) | `resources.go:570` | comment block 548-569 |
| create calls VAP `:218` | `resources.go:250` | inside `else` after `resolveNS` (`:246-258`) |
| update calls VAP `:290` | `resources.go:322` | `live` fetched `:300-313` |
| clone calls VAP `lifecycle.go:243` | `lifecycle.go:243` | correct; `cloneHandler(reg)` at `lifecycle.go:196` |
| capture block `resources.go:565-583` | `resources.go:597-614` | `// 1. Validate spec.capture` at 597 |
| `resources.go:538` insertion "after capture step" | insert after line 614 (`}`), before blank 615 and `// 2. Validate spec.serviceAccountName` at 616 | |
| `MountResources` call `main.go:382` | `api/cmd/main.go:396` | `MountLifecycle` `:400`, `MountConfigWithTelemetry` `:407`, `MountCapture` `:454` |
| config audit pattern `config.go:174-179` | `config.go:175-178` | |
| rule table config:manage `rbac.go:223-228` | config rules are `rbac.go:209` (GET config:read) and `rbac.go:214` (`{segment:"admin",prefix:"/admin/config",perm:"config:manage"}`). `rbac.go:223-228` is registries and telemetry. | no rule change needed |
| `servers:write` route `rbac.go:267` | `rbac.go:253` | `{segment: "servers", perm: "servers:write"}` |
| catalog `servers` group `catalog.go:25-29` | correct; `servers:console` is `catalog.go:28` | |
| `privilegedPermissions` `privileged_grants.go:21` | correct | SQL `IN (...)` lists at `privileged_grants.go:43` and `:57` |
| `writeServerValidationError` `resources_snapshot.go:24` | correct (`:24-33`) | see Discrepancy D1 |
| `MountResources` `resources.go:51` | correct | |
| `newValidators` `config.go:462` | correct (`:462-471`) | |
| `config.go` PUT handler | `config.go:115` (`put`) | |
| `db.Store` config get | `api/internal/db/config.go:13` `ConfigValue(ctx, key) (string, bool, error)`; `ConfigValueTx` `:19` | no exported setter |
| `audit.MarkRecorded` | `audit/audit.go:602` (package func, takes ctx) | |
| `Auditor.WriteSync` | `audit/audit.go:711` `(ctx, method, path, target, reason string, status int) error` | |
| capture precedent | `handlers/capture.go:1252-1258` (`auditWriteOrFail`) | matches |
| `User.Can` | `auth/perms.go:123` `Can(perm string, namespaced bool, cluster, ns string) bool`; nil-safe (`if u == nil return false`) | |
| `T012` "resources_security_test.go" | file already exists (519 lines), see D7 | |

---

## 1. Shared facts (verified)

- **Permission reads (captures:manage precedent)**, `resources.go:578-582`:
  ```go
  u := auth.UserFromContext(ctx)
  isAdmin := false
  canManageCaptures := false
  if u != nil {
      isAdmin = u.Role == "admin" || u.Can("*", false, cl, ns)
      canManageCaptures = isAdmin || u.Can("captures:manage", true, cl, ns)
  }
  ```
  For security: `canSetSecurity := isAdmin || u.Can("servers:security", true, cl, ns)` (same block, same `u != nil` guard). `cl := scope.RequestedCluster(req)` in handlers (`resources.go:249`, `:321`, `lifecycle.go:242`).
- **Capture step shape to mirror** (`resources.go:597-614`): if `!canManage`: `live == nil` -> refuse when present and non-nil; else compare with `reflect.DeepEqual` -> refuse on change; absent in request and present in live -> `_ = unstructured.SetNestedField(desired.Object, liveRaw, "spec", "capture")` (restore). Plain `errors.New` returns, mapped to 403 by CS.
- **Errors/status**: `writeServerValidationError` (`resources_snapshot.go:24-33`):
  ```go
  code := http.StatusForbidden
  if errors.Is(err, errTemplateRefImmutable) { code = http.StatusConflict }
  if errors.Is(err, errSnapshotSourceUnavailable) { code = http.StatusServiceUnavailable }
  httperr.WriteCode(w, req, code, err)
  ```
  `httperr.WriteCode` (`httperr/httperr.go:202-217`) uses the passed `status`, and for a `*httperr.Message` in the chain uses only `m.English` and `m.ID`/`m.Args`, not `m.Status`.
  `httperr.WriteMessage(w, r, m)` (`httperr.go:224-238`) uses `m.Status` (>=500 masked). `httperr.Write` (`:245`) uses `classify` which honors `*Message` status (`:260-266`). `messages.New(id, args) *httperr.Message` (`messages/messages.go:252-262`).
- **decode of request body**, `resources.go:512-520`: `json.NewDecoder(...).Decode(&map[string]any)`. Integers arrive as **float64**. Live objects from the dynamic client are **int64**. See D9.
- **Config PUT** (`config.go:115-181`): section lookup `h.validators[section]` (`:117`) -> 400 `server.config.unknown_section`; body read; empty body -> 400 `server.empty_body` (`:126-129`); `canon, err := validate(body)` (`:131`); errors: `*httperr.Message` -> `WriteMessage`; else `WriteCode(422, err)` (`:136-140`). Telemetry branch `config.go:151-160` (`if section == "telemetry" {...}`), generic upsert `config.go:161-172` (`} else if _, err := h.db.DB.ExecContext(... INSERT INTO config ...`), audit loop `config.go:175-179`, response `config.go:181`.
- **Audit row precedent in config** (`config.go:175-178`): after the config write, `_ = h.auditor.WriteSync(req.Context(), http.MethodPut, "/admin/config/auth", evt.role, reason, http.StatusOK)` (error ignored).
- **validators map** `config.go:462-471`:
  ```go
  return map[string]func([]byte) (json.RawMessage, error){
      "general": validateGeneral, "auth": validateAuth(helmOIDCPresent), "notifications": validateNotifications,
      "telemetry": validateTelemetry, "i18n": validateI18n, registry.ConfigSectionModRegistries: validateModRegistries,
  }
  ```
  `validateTelemetry` (`config.go:769-779`) is the model: unmarshal, canonicalize, `json.Marshal`. `getAll` (`config.go:80-114`) returns only keys in `h.validators`, so `security` appears automatically once registered.
- **Kube clients / registry** (`api/internal/kube/registry.go`, `kube/client.go:18-27`): `*kube.Registry` with `IDs() []string` sorted (`registry.go:249-258`), `Get(id) (*Client, bool)` (`:160`), `Default()` (`:169`), `Management()` (`:138`). `kube.Client` has `Dynamic dynamic.Interface`, `Typed kubernetes.Interface`. Per-request cluster: `resolveCluster(w, req, reg)` (`resources.go:113`), `scope.RequestedCluster(req)` (`scope/cluster.go:46`).
- **Namespaces the API uses** (`api/cmd/main.go`):
  - `cfg.namespace` from `GAMEPLANE_NAMESPACE`, default `gameplane-system` (`main.go:726`). Chart sets it to the pod's own namespace via fieldRef (`charts/gameplane/templates/api.yaml:398-399`), i.e. the release namespace.
  - `credentialsNS, moduleConfigMapNS := cfg.managementNamespaces()` (`main.go:231`). `managementNamespaces` (`main.go:625-634`): `credentials = GAMEPLANE_CREDENTIALS_NAMESPACE || cfg.namespace`; standalone -> `(credentials, cfg.namespace)`; else `(credentials, credentials)`. Chart sets `GAMEPLANE_CREDENTIALS_NAMESPACE` (`api.yaml:402`) when not standalone.
  - `moduleConfigMapNS` is passed to `MountModulesWithRegistry` (`main.go:423`).
- **Operator's control-plane namespace** (for the ConfigMap T057): `operator/cmd/main.go:273-277` defaults to `POD_NAMESPACE` / `gameplane-system`; the chart passes `--control-plane-namespace={{ gameplane.operatorClusterCredentialsNamespace }}` (`charts/gameplane/templates/operator.yaml:396`). That helper (`_helpers.tpl:220-226`) is `<release>-credentials` (via `gameplane.credentialsNamespace`) when API enabled and not standalone, else `operator.controlPlaneNamespace` or release namespace. **So the operator reads the ConfigMap in `credentialsNS` (non-standalone) or the release namespace (standalone), which is `moduleConfigMapNS` from `main.go:231` in both modes.** See D12.
- **Chart API RBAC**: `charts/gameplane/templates/api-credentials.yaml` (rendered only when `apiEnabled` and not standalone, line 3). Role `{{ .Release.Name }}-api-credentials` in `$namespace` (= credentials namespace) at `:14-37`; configmaps rule at `:31-36`:
  ```yaml
    - apiGroups: [""]
      resources: [configmaps]
      {{- if $rbac.catalogAdmin }}
      verbs: [get, list, create, update, delete]
      {{- else }}
      verbs: [get, list]
      {{- end }}
  ```
  RoleBinding `:39-50`, subject `ServiceAccount gameplane-api` in release namespace (`:47-49`). `charts/gameplane/templates/api.yaml` has the cluster read ClusterRole (`:21`), Roles `-api-games` `:74` and `-api-logs` `:139` in release namespace, no configmap write. Operator ClusterRole has `configmaps` get/list/watch at `operator.yaml:66`.
- **Route mounting**: `mountOne` (`resources.go:57-66`) mounts `POST /` -> `createHandler` (`:60`), `PUT /{name}` -> `updateHandler` (`:65`).
- **Clone** `cloneHandler(reg *kube.Registry)` (`lifecycle.go:196`), `MountLifecycle(r, reg)` (`lifecycle.go:40`), `stampOwner` (`ownership.go:41`). Clone: `clone := src.DeepCopy()` (`:216`), `RemoveNestedField` for nodeDiagnostic (`:234`), `validateCloneConfig` (`:237`), **VAP call `:243` with `live == nil`**, Create `:246-248`. Clone has no store and no auditor.
- **Envtest suite** `api/internal/handlers/suite_envtest_test.go`: TestMain builds `mountedR` (`:~119-142`) with `MountResources(mountedR, reg, nil)` (`:124`), `MountLifecycle(mountedR, reg)` (`:125`), `captureAuditStore` (`file:captureaudit?mode=memory&cache=shared&_pragma=journal_mode(WAL)`), `captureAuditor := audit.New(captureAuditStore)` (`:121`), `MountConfig(mountedR, captureAuditStore, captureAuditor, false, "", nil)` (`:141`), `apiSrv`/`apiBase`. `kubeC` is the envtest client; `reg.Set(scope.DefaultCluster, kubeC)` pattern at `:86-96` in suite.
- **Envtest router helpers to copy**:
  - `newCaptureRBACRouter` (`capture_rbac_envtest_test.go:84-100`): `reg := kube.NewRegistry(scope.DefaultCluster); reg.Set(scope.DefaultCluster, kubeC); r := chi.NewRouter(); r.Use(rbac.Middleware(reg)); MountCapture(r, reg, audit.New(captureAuditStore), ...)`.
  - `doAsUser(t, h, u, method, path, body)` (`capture_rbac_envtest_test.go:103-118`) injects user via `auth.WithUser(t.Context(), u)`.
  - `newConfigRBACRouter` (`config_envtest_test.go:36-44`): `r.Use(rbac.Middleware(kube.NewRegistry("default")))` (no clusters registered), `MountConfig(r, captureAuditStore, audit.New(captureAuditStore), false, "", nil)`.
  - `doConfigAsUser` (`config_envtest_test.go:47-67`), `configAdminUser` (`:69`), `configOperatorUser` (`:81`), `configViewerUser` (`:105`).
  - `doJSON`/`getDynamic`/`gvrServers()` in `resources_envtest_test.go:306-347` use `apiBase` (no user, no RBAC middleware).
  - `createRBACTestServer(t, name, ownerID)` (`capture_rbac_envtest_test.go:128-160`) creates a GameServer fixture in `scope.DefaultNamespace` via `kubeC.Dynamic`.
  - User fixtures: `operatorUser()` `:163`, `viewerUser()` `:181`, `adminUser()` `:199` (Perms keyed `"*": {"*": {...}}`).
- **Unit fixtures** `api/internal/handlers/resources_fake_test.go:15-21`: `mountResourcesRouter(k)` = `reg.Set(scope.DefaultCluster, k)`, `MountResources(r, reg, nil)` (nil store, no auditor). `fakeKubeClient(objs...)` in `modules_fake_test.go:27`; `newServerObj(ns, name)` in `lifecycle_fake_test.go:28`.
- **Existing security unit test file** `api/internal/handlers/resources_security_test.go` (519 lines): helpers `doWithUser(t, h, method, path, body, user) *httptest.ResponseRecorder` (`:16`), `testOperatorUser()` (`:40`, perms servers:read/write only), `testAdminUser()` (`:56`, `{"*": {"*": {}}}`). Tests `TestResources_Security_CaptureBypassBlocked` (`:69`), `..ServiceAccountNameOverrideBlocked` (`:98`), `..ImageOverrideBlocked` (`:140`), `..SecretRefExfiltrationBlocked` (`:182`), `..TunnelCredentialsSecretProtected` (`:435`). Names do not collide with the new `servers:security` work.
- **Rbac test helpers** `api/internal/rbac/rbac_test.go`: `userForRole(t, store, role)` (`:17-24`, binds seeded perms at `scope.DefaultCluster` `"*"`), `loadRolePerms` (`:26`), `migratedStore(t)` (`:44`, sqlite `:memory:`), `TestAllow` (`:57`).
- **Seeded roles**: `api/internal/db/migrations/sqlite/003_roles.sql:38-60` (admin `*`; operator explicit list at `:43-60` with `servers:console` at `:49`; viewer has no rows here). Postgres twin `migrations/postgres/003_roles.sql:54`. `servers:security` is in NO seed row (admin `*` covers it).
- **Message registry**: `messages/entries_server.go` (`serverEntries`, sorted by id, `Entry{ID, English, Description, Status}`); roles block `:111-151`, `server.shares.expires_at_in_past` at `:152-157` (`{` on 152). `messages.go:216-229` merges `entries` + `serverEntries` and sorts. `export.go` `ExportJSON` (for i18n sync).
- **i18n**: `i18n/locales/en/app.json` (2-space indent): `server.roles.role_not_found` block `:1038-1041`, `server.shares.expires_at_in_past` starts `:1042`. `i18n/locales/fr/app.json` (4-space indent): `server.roles.role_not_found` `:862-865`, `server.shares.expires_at_in_past` `:866`. `i18n/locales/fr/.review.json` holds per-key `sourceHash` (e.g. `"server.roles.role_not_found": "07ba515a3db3"` line ~221). `i18n-check` (`i18n/cmd/i18n-check/main.go`): `--check-server` flags server.* drift both ways vs the registry (`:543-563`); `--sync-server` replaces en server.* from the registry; `--stamp` records sourceHash for translated entries that lack one (`:77`).

---

## 2. Per-task edits

### T009 Catalog entry
File `api/internal/rbac/catalog.go`, insert after line 28 (`servers:console`), inside the `servers` group (lines 25-29):
```go
{Key: "servers:security", Label: "Set or change a server's security overrides (run-as user and group, filesystem group, volume ownership check)", Namespaced: true},
```
Do NOT add to `privilegedPermissions` (`handlers/privileged_grants.go:21`). Justification: contracts/rbac-permission.md "Catalog entry", "Privilege grant guard"; OD-1; research R10. No seed row, no migration (admin `*` covers it). No route rule (rule table unchanged).

### T010 Catalog and admin tests
- `api/internal/rbac/catalog_test.go:20-38` `TestNamespaced` map: optionally add `"servers:security": true`. Existing cases end at line 36-37 (`"audit:read": false`, then `"*"` and `"unknown:key"`).
- `api/internal/rbac/rbac_test.go`: new test. For each of `RoleAdmin`, `RoleOperator`, `RoleViewer` call `userForRole(t, store, role)` (`:17`), assert `u.Can("servers:security", true, scope.DefaultCluster, scope.DefaultNamespace)` true only for admin. Store from `migratedStore(t)` (`:44`). Also assert `ValidPermission("servers:security")` and `Namespaced("servers:security")` (`catalog.go:111`, `:117` funcs).
- `TestCatalog_CoversSeededPermissions` (`catalog_test.go:68`) and `TestCatalog_CoversRuleTablePermissions` (`catalog_test.go:104`) need no change (no seed or rule references the key).

### T011 Message registry and locale keys
Add three entries to `api/internal/messages/entries_server.go` after line 151 (end of `server.roles.role_not_found`), before `server.shares.expires_at_in_past` (`:152`). Sort order holds (`server.roles.*` < `server.security.*` < `server.shares.*`).
```go
{
    ID:          "server.security.forbidden",
    English:     "changing security settings requires the servers:security permission",
    Description: "API error from the GameServer save path (resources_security.go) when a caller without servers:security changes spec.security.",
    Status:      http.StatusForbidden,
},
{
    ID:          "server.security.root_disabled",
    English:     "root values (user 0, group 0, filesystem group 0, or the image default for user or group) are not allowed until an administrator enables root overrides",
    Description: "API error from the GameServer save path when a new or changed root value is saved while the allow-root-overrides setting is off. Argument setting names the first offending setting.",
    Status:      http.StatusForbidden,
},
{
    ID:          "server.security.invalid",
    English:     "invalid security override: {detail}",
    Description: "API error from the GameServer save path when spec.security has an invalid shape. Argument setting names the offending setting; argument detail says what is wrong.",
    Status:      http.StatusUnprocessableEntity,
},
```
Text from contracts/api-server-security.md "Errors". Add matching en keys in `i18n/locales/en/app.json` after line 1041 (before `"server.shares.expires_at_in_past"` at 1042), 2-space indent, shape `"server.security.forbidden": {"defaultMessage": "<English>", "description": "<same Description>"}`. `i18n-check --check-server` requires the en text and description to equal the registry exactly.
Add fr keys in `i18n/locales/fr/app.json` after line 865, 4-space indent, French `defaultMessage` (translator review needed; proposal: forbidden "la modification des paramètres de sécurité exige l'autorisation servers:security"; invalid "dérogation de sécurité invalide : {detail}"). Then `fr/.review.json` needs `sourceHash` entries via `i18n-check --stamp` (not run by fixers; CI verifies).
Test requirements: `messages/messages_test.go` checks only sorted/unique, non-empty descriptions, `httperr` ids registered. `messages/usage_test.go:TestEveryMessagesNewLiteralIsRegistered` checks each literal `messages.New("server.…")` in `api/internal` non-test files is registered, and at least 100 sites. Neither verifies that every entry is used (D2).

### T012 Field-level security step (new file)
- NEW `api/internal/handlers/resources_security.go`. Function name not present in repo (grep: no `security` func in handlers outside tests and comments).
- Call site: insert after `resources.go:614` in VAP (before `// 2.` at 616). Current code at 597-616 (capture block then blank). Add a block that runs the cases from contracts/api-server-security.md table (order: 1 unchanged accept; 2 absent restore; 3 changed+no permission 403; 4 shape 422; 5 root rule 403).
- Call sites that already call VAP and need no signature change for T012 alone: create `resources.go:250`, update `resources.go:322`, clone `lifecycle.go:243`.
- Case 1 compare: normalize numbers before compare (see D9). Case 2 restore: copy the capture pattern `_ = unstructured.SetNestedField(desired.Object, liveRaw, "spec", "security")` (`SetNestedField` deep-copies values, so use JSON-native types, see D10).
- Clone (`live == nil`): present and non-nil spec.security without permission -> 403 (research R10 "clone reads as create").
- Refusals must carry status: return `messages.New("server.security.forbidden", nil)` (a `*httperr.Message`, status 403). Requires CS fix, see D1 and D11.

### T013 Unit tests for T012 cases
- File: NOT a new file (D7). Options: append to existing `api/internal/handlers/resources_security_test.go` (already has `doWithUser`, `testOperatorUser`, `testAdminUser`), or create `resources_security_override_test.go`.
- Fixtures: `newServerObj(ns, name)` (`lifecycle_fake_test.go:28`) + `fakeKubeClient(...)` (`modules_fake_test.go:27`) + `mountResourcesRouter(k)` (`resources_fake_test.go:15`). The router passes **nil store and nil auditor** (D10). Unit tests for create/update/clone can call the validator directly with `ctx` carrying the user (`auth.WithUser`) to avoid the router; pass a `*db.Store` only where the root rule is exercised (T055 tests).
- Cases: both absent (unchanged, 200), equal (200), restored field (absent in request, live present, no permission: 200, field restored), forbidden on create (`live == nil`), update and clone.

### T014 Envtest for permission behavior
- New file `api/internal/handlers/resources_security_envtest_test.go`. Does not exist yet (ls confirmed).
- Build a dedicated router (do not use `mountedR`, D10): `reg := kube.NewRegistry(scope.DefaultCluster); reg.Set(scope.DefaultCluster, kubeC); r := chi.NewRouter(); r.Use(rbac.Middleware(reg)); MountResources(r, reg, captureAuditStore, audit.New(captureAuditStore)); MountLifecycle(r, reg, ...)` (signature changes per T055/T059, D13). Use `doAsUser` (`capture_rbac_envtest_test.go:103`) style with a user holding `servers:read`, `servers:write` (copy `operatorUser` shape without `servers:security`), and `adminUser()`.
- Fixture: `createRBACTestServer` (`capture_rbac_envtest_test.go:128`) creates a bare server in `scope.DefaultNamespace` with `templateRef.name: minecraft`; for clone and update tests use a unique name (`uniqueResourceName`, `suite_envtest_test.go:157`).
- Note: `rbac.Middleware` `servers:write` rule `rbac.go:253`; `/servers` PUT and POST need `servers:write`. Owner fallback `rbac.go:122-150` does not cover full PUT (contract).

### T028 Shape-case tests (unit + envtest)
- Extend `resources_security_test.go` and `resources_security_envtest_test.go` (see T013/T014). Cases: `value` 4294967296, `-1`, both `value` and `useImageDefault`, neither, `useImageDefault:false`, unknown member, bad enum (`volumeOwnershipCheck` not in `EveryStart|OnlyWhenWrong`), non-integer `value` (e.g. 1.5). Expect 422 `server.security.invalid` with `args.setting`, and nothing created (`kubeC.Dynamic` Get returns NotFound). A valid override (`value: 2000`) stored and returned.
- Shape rules: contracts/api-server-security.md case 4; data-model.md (`SecurityIDOverride`).

### T033 Shape validation
- Function in `resources_security.go`. Input is `spec.security` from the desired object (map from `decode`, numbers are float64, see D9). Validate each of `runAsUser`, `runAsGroup`, `fsGroup`: a map with exactly one of `value` (whole number 0..4294967295, accept float64 when `v == math.Trunc(v)`, and int64 for safety) or `useImageDefault` (must be `true`); unknown members rejected; `volumeOwnershipCheck` string in the enum (or absent). Return `messages.New("server.security.invalid", map[string]any{"setting": name, "detail": text})` with status 422.
- Note the CRD CEL stays the second line (T006/T008, not this brief).

### T047 Config section tests
- Extend `api/internal/handlers/config_envtest_test.go` and add validator unit tests (new in `config_validators_test.go`, pattern `TestValidateI18n` at `:240-250`, and `TestUpdatesSectionRemoved` at `:233-238`).
- Router: `newConfigRBACRouter` (`config_envtest_test.go:36-44`) registers no cluster, so the ConfigMap write has zero clusters (D13). The envtest for the ConfigMap must register `kubeC` (`reg.Set(scope.DefaultCluster, kubeC)`) and pass `reg` plus namespace into `MountConfig` (signature change, D13).
- Users: `configViewerUser()` (`config_envtest_test.go:105`) for the 403 case, `configAdminUser()` (`:69`) for success. Reads via `GET /admin/config` (`getAll` `config.go:80`).
- ConfigMap failing write: use a `Typed` fake whose ConfigMap create/update returns an error (e.g. `k8sfake` reactor `PrependReactor`), or use `k8sfake.NewSimpleClientset()` with a reactor. Expect 502 per contracts/cluster-setting.md? (see D14; existing `httperr` writes 500 for unknown errors).

### T048 Root rule tests
Unit and envtest in `resources_security_test.go` and `resources_security_envtest_test.go`. Cases: new or changed `runAsUser` or `runAsGroup` with `value: 0` or `useImageDefault: true`, or `fsGroup` `value: 0`: 403 `server.security.root_disabled` with `args.setting` while off, 200 once on (`PUT /security-settings?cluster=<id>` by admin). `fsGroup` `useImageDefault` allowed. Unchanged stored root value passes an unrelated whole-spec PUT with setting off. Removal and replacement always allowed.

### T049 Audit tests
Unit: `reason` builder (new func in `resources_security.go` or `resources_security_audit.go`). Envtest: rows via `GET /admin/audit` or direct `audit` store read, and `GET /admin/audit/verify`. See audit-events.md "Events" and "reason". Failure tests: a broken store (`newBrokenAuditStore`, `capture_rbac_envtest_test.go:323-357`) with `TestCaptureAudit_BrokenStore_FailsOperation` (`:359`) as the model for "failed WriteSync returns 500, nothing saved".

### T055 Root rule (API)
- Read the setting: `store.ConfigValue(ctx, "security")` (`db/config.go:13`), decode `{"allowRootOverrides": bool}`; absent or unparsable -> false (fail closed). Guard `store == nil` (tests pass nil, D10).
- Predicate (contract api-server-security.md "A root value"): `runAsUser`/`runAsGroup` with `value == 0` or `useImageDefault == true`; `fsGroup` with `value == 0`. Count only new or changed root values (vs `live`; `live == nil` means new). Unchanged stored root value passes.
- Error: `messages.New("server.security.root_disabled", map[string]any{"setting": "<first offending>"})`, status 403. Order of "first offending": runAsUser, runAsGroup, fsGroup (assumption; contract says "the first offending setting").
- Read only when a new or changed root value exists ("one indexed config read, only on a request that carries a new or changed root value", contract).

### T056 Register `security` validator and flip audit
- Add `"security": validateSecurity,` to the map in `newValidators` (`config.go:462-471`). `validateSecurity(body []byte) (json.RawMessage, error)` modeled on `validateTelemetry` (`config.go:769-779`): struct `{AllowRootOverrides *bool \`json:"allowRootOverrides"\`}`; nil -> error (422 via `config.go:136-140`); canonical `json.Marshal`. Unknown members dropped by struct decode.
- `put` branch: insert a `section == "security"` branch next to the telemetry branch at `config.go:151`, or before the generic upsert at `config.go:161`. It must NOT fall into the generic `INSERT INTO config` branch (`config.go:161-172`) because the order is ConfigMap first (T057).
- Flip detection: read the old value with `h.db.ConfigValue(ctx, "security")` (or `db.ConfigValueTx` inside a tx) before writing; if the new value differs, write the audit row before the writes:
  - `h.auditor.WriteSync(req.Context(), http.MethodPut, "/security-settings", "security/<cluster id>", "allowRootOverrides:false->true", http.StatusOK)`; on error `httperr.WriteCode(w, req, 500, errors.New("audit write failed"))` and return (pattern `capture.go:1252-1258`); on success `audit.MarkRecorded(req.Context())`.
  - Then ConfigMap write (T057), then config row upsert. If a later write fails, write a second row `reason: security_save_failed`, status error code (audit-events.md "Write order" 2).
  - No flip -> no extra row (generic middleware row stands).
- Reason string: `allowRootOverrides:false->true` or `true->false` (direction from old to new).
- Config row upsert SQL for reuse (`config.go:161-171`):
  ```sql
  INSERT INTO config(key, value, updated_at) VALUES (?, ?, ?)
  ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
  ```
  with `db.NowTimestamp()`. (`db/telemetry.go` has unexported `upsertConfigTx`, D15.)

### T057 ConfigMap mirror (new file)
- NEW `api/internal/handlers/security_settings_sync.go`.
- ConfigMap: name `gameplane-security-settings`, namespace = `moduleConfigMapNS` (`main.go:231`; see D12), labels `app.kubernetes.io/managed-by: gameplane-api`, data `allowRootOverrides: "true"|"false"`.
- Writer: loop `for _, id := range reg.IDs()` (`kube/registry.go:249`); `c, ok := reg.Get(id)` (`:160`); skip nil `c` or nil `c.Typed`; `c.Typed.CoreV1().ConfigMaps(ns)` Get then Create (NotFound) or Update. Fail the request on any error (T057: "failing the request if the write fails").
- Start-up sync: rewrite from the config row (contract cluster-setting.md "Storage and routes" operator mirror). Call site: `api/cmd/main.go`, after `reg`/`store` are built and before serving; `ns` is `moduleConfigMapNS` (`main.go:231`) which is in scope there (declared at `:231`, used by `:423`).
- Config handler needs `reg` and `ns` (MountConfig has neither today, D13).

### T058 Chart RBAC
- Target file is `charts/gameplane/templates/api-credentials.yaml` (D12), NOT `api.yaml`. Add a rule to the Role `{{ .Release.Name }}-api-credentials` (`:14-37`, namespace `$namespace` = credentials namespace):
  ```yaml
    - apiGroups: [""]
      resources: [configmaps]
      resourceNames: ["gameplane-security-settings"]
      verbs: [get, create, update, patch]
  ```
  (the existing `configmaps` rule `:31-36` has no resourceNames, so do not reuse it for the write verbs).
- Standalone: `api-credentials.yaml:3` renders only when not standalone. For standalone the namespace is the release namespace (`moduleConfigMapNS` = `cfg.namespace`), so an equivalent Role/RoleBinding for the API (subject `gameplane-api`) in the release namespace is also needed (in `api.yaml` where the other API Roles live, `:74-158`). Verify against `charts/gameplane/templates/_helpers.tpl:220-226` before writing.
- Chart test fixtures: `charts/gameplane/tests/` has only `monitoring-rules.test.yaml` (no RBAC fixture found). No chart test change expected.

### T059 Audit wiring in MountResources, create, update, clone
- `MountResources(r chi.Router, reg *kube.Registry, store *db.Store)` (`resources.go:51`): add `auditor *audit.Auditor` param (contract: "gains the audit writer"). Thread into `mountOne` (`:55`) and `createHandler(reg, gvr)` (`:222`), `updateHandler(reg, gvr)` (`:275`). Call site `main.go:396`: pass `auditor` (in scope, `main.go:269`).
- Create (`resources.go` create block, VAP call `:250`, Kubernetes write `:259` namespaced / `:241` cluster): before `:259`, if `spec.security` changed (`before {}`, `after` listed), `WriteSync(req.Context(), req.Method, req.URL.Path, name, reason, http.StatusCreated)`; on error 500 `audit write failed`, return; on success `audit.MarkRecorded`. After a failed Kubernetes write add `security_save_failed` row.
- Update (VAP call `:322`, Kubernetes write `:340` namespaced / `:333` cluster): same, before `:340`, `status http.StatusOK`.
- Refusals (403/422): the validator has no `req` or auditor, so return a typed error (e.g. `*securityRefusal{status, reason string, err *httperr.Message}`) and have the handler write the row (`reason` `security_forbidden`, `security_root_disabled:runAsUser`, `security_invalid:fsGroup`) with `WriteSync`; on WriteSync failure leave MarkRecorded unset (middleware fallback), still return the refusal (audit-events.md "Write order" 3).
- Clone: `cloneHandler(reg)` (`lifecycle.go:196`) has no auditor. Contract audit-events.md "Events" lists clone, T059 does not. Needs `MountLifecycle(r, reg)` (`lifecycle.go:40`) signature change to take the auditor (and store for T055 when clone passes VAP, D11). Call sites: `main.go:400`, `lifecycle_fake_test.go:24`, `lifecycle_suspend_test.go:41`, `suite_envtest_test.go:125`.
- `reason` builder: JSON `security:{"before":{...},"after":{...}[,"risky":[...]]}`. Values: not set -> `null`; number -> number; image default -> `"image"`; ownership check -> `"EveryStart"`/`"OnlyWhenWrong"`. Only changed settings. `risky` lists changed settings whose template `risky` member is set (names only). Template: read `kube.GVRs["templates"]` cluster-scoped by `spec.templateRef.name` (same pattern as `managedTemplateBlocked`, `resources.go:452-454`). `risky` does not exist in the code yet (D8).
- Confirm the clone path is covered by the shared validator: yes, `lifecycle.go:243` calls the same VAP (`:243`).

---

## 3. Discrepancies

**D1. `writeServerValidationError` does not pass status-carrying errors through.** Evidence: `resources_snapshot.go:24-33` sets `code := 403` by default and calls `httperr.WriteCode(w, req, code, err)`. `WriteCode` (`httperr.go:202-217`) writes the `code` it is given and only takes `m.English`, `m.ID`, `m.Args` from a `*Message`, ignoring `m.Status`. So a `messages.New("server.security.invalid", ...)` (status 422) would reach the client as 403. Contract claim "the new errors carry their own status, so writeServerValidationError passes them through" is false today. Fix in CS: `var m *httperr.Message; if errors.As(err, &m) { httperr.WriteMessage(w, req, m); return }` before the code switch. Existing errors (`errTemplateRefImmutable` 409, `errSnapshotSourceUnavailable` 503) must keep their codes (they are plain errors, so unaffected).

**D2. Messages tests do not require every ID to be used.** Evidence: `messages/messages_test.go` (`TestAllSortedAndUnique`, `TestEveryEntryHasDescription`, `TestKnownIDsAreRegistered`, ...) and `messages/usage_test.go:TestEveryMessagesNewLiteralIsRegistered` check only that literal ids are registered. Nothing flags an unused entry. Task T011 and the contract ("the existing messages_test.go and usage_test.go require every ID to be used") are inaccurate. CI for locale drift is `i18n-check --check-server` (`i18n/cmd/i18n-check/main.go:543-563`), which does check en/registry parity.

**D3. Config empty body is 400, not 422.** Evidence: `config.go:126-129` returns `messages.New("server.empty_body", nil)`; `messages.go:94-98` sets `Status: http.StatusBadRequest`. Contract cluster-setting.md ("rejects ... an empty body (422)") and T047 say 422. Either change the test expectation to 400 (matches existing sections) or change the contract. Tests are not changed without sign-off (CLAUDE.md rule 1), so this is for the maintainer.

**D4. Config flip audit order differs from the existing pattern.** Evidence: existing audit rows in `put` are written after the config row (`config.go:175-178`) and errors are ignored (`_ =`). Contract audit-events.md "Write order" requires `WriteSync` before the ConfigMap and config row writes, returning 500 with no change if it fails. Build the new branch to the contract, not to the `config.go:175-178` pattern. Only the row format (`reason`, `target`) is copied.

**D5. Stale line citations in the spec and contracts.** See section 0 (VAP at 570, create at 250, update at 322, capture block 597-614, `MountResources` main.go:396, config rule rbac.go:214, servers:write rbac.go:253, audit 175-178, `rbac.go:223-228` is the wrong range).

**D6. Clone is listed in the audit contract but not in T059.** Evidence: audit-events.md "Events" row "Override set, changed or removed (create, update, clone)"; T059 lists only create and update. `cloneHandler` has no `*audit.Auditor` (`lifecycle.go:196`). Decide: add clone audit (needs `MountLifecycle` signature change, D13) or change the contract.

**D7. `resources_security_test.go` already exists.** Evidence: `api/internal/handlers/resources_security_test.go` (519 lines, 5 existing tests, helpers `doWithUser`, `testOperatorUser`, `testAdminUser`). T013 says "Unit tests in ... resources_security_test.go" (fine if extending), but its phrasing and the file list in T014/T028/T048/T049 mix extend and create. `resources_security_envtest_test.go` does NOT exist yet (ls). New production file `resources_security.go` does NOT exist (ls). Decided by OD-9 item 2 (briefs/00-rulings.md): never edit the existing file; use the new `security_overrides_test.go` and `security_overrides_envtest_test.go`.

**D8. `risky` does not exist in the code.** Evidence: `grep -rli risky --include=*.go .` returns 0 files. The audit `risky` marker (T059, audit-events.md) depends on the template CRD field from T005 (`GameSecuritySpec.risky`, `operator/api/v1alpha1/gametemplate_types.go:185`), which is not yet written. The API reads templates as unstructured, so the field path is `spec.security.risky.<setting>.reason` (confirm with T005). Until T005 lands, the `risky` key is always absent.

**D9. Numeric types differ between request and live object, so DeepEqual gives false "changed".** Evidence: `resources.go:512-520` `decode` uses `json.Decoder` into `map[string]any`, so integers are **float64**. Live objects from `k.Dynamic` are unstructured with **int64** integers (standard client-go decode; module cache not present locally to double-check, so treat as expected). `reflect.DeepEqual(float64(2000), int64(2000))` is false. The capture precedent compares only bools/strings, so it never hit this. Case 1 "unchanged" must compare numerically (e.g. normalize both sides via `json.Marshal` or convert all numbers to float64 before DeepEqual).

**D10. Nil store and nil auditor in every existing router, and in the envtest `mountedR`.** Evidence: `MountResources(…, nil)` in `resources_fake_test.go:19`, `suite_envtest_test.go:124`, `server_identity_test.go:52`, `config_create_race_test.go:48,139`, `resources_annotations_test.go:21`, `resources_snapshot_test.go:224`, `config_clone_race_test.go:40`, `shares_test.go:1096` (store is non-nil there). A nil `*audit.Auditor` makes `WriteSync` (`audit.go:711`) dereference nil; a nil `*db.Store` makes `ConfigValue` (`db/config.go:13`) dereference nil. Any test that sets a security block through these routers panics unless (a) the new code nil-guards, or (b) the call sites are updated to pass a real store and auditor. Updating existing tests needs sign-off (CLAUDE.md rule 1). Use new dedicated routers for the security envtests (D13-style).
- Also `SetNestedField` deep-copies via `runtime.DeepCopyJSONValue`, which (per apimachinery) panics on Go `int`; fixtures must use `float64` or `int64` (unverified locally, module cache absent).

**D11. The root rule and audit need store/auditor inside the validator, and `cloneHandler` has neither.** Evidence: VAP signature `resources.go:570-575` `(ctx, k, cl, ns, name, desired, live)`, no store or auditor. `cloneHandler(reg)` `lifecycle.go:196` and `MountLifecycle(r, reg)` `lifecycle.go:40`. T055 says "through the `*db.Store` MountResources already receives", which is true for create and update only. Options: add `sec securityDeps{store, auditor}` param to VAP and to `MountLifecycle`; or pass store and auditor through a closure. Decide before T012/T055.

**D12. ConfigMap namespace and Role location.** Evidence: T058 says Role in the release namespace via `api.yaml`. The operator reads the ConfigMap in its control-plane namespace, which the chart sets to `<release>-credentials` when the API is enabled and not standalone (`charts/gameplane/templates/operator.yaml:396`, `_helpers.tpl:220-222`). The API writes to `moduleConfigMapNS` (`main.go:231`), which equals `GAMEPLANE_CREDENTIALS_NAMESPACE` in non-standalone and `cfg.namespace` in standalone. `GAMEPLANE_NAMESPACE` (`api.yaml:398-399`) is the release namespace, so using `cfg.namespace` for the non-standalone write would put the ConfigMap where the operator does not look. Also the API's existing grant in that namespace is `api-credentials.yaml` (`:14-37`), so the new rule belongs there (and an equivalent for standalone in `api.yaml`).

**D13. Config and lifecycle constructors have no registry or namespace; test call sites are many.** Evidence: `MountConfigWithTelemetry(r, store, auditor, helmOIDCPresent, storageClass, helmPolicy, tel)` (`config.go:55`) has no `*kube.Registry` or namespace. `MountConfig` is called in 14 places across 8 files (`main.go:407`, `config_test.go`, `config_envtest_test.go:41` and `:736`, `config_db_test.go`, `telemetry_envtest_test.go`, `registry_secret_test.go`, `suite_envtest_test.go:141`, `config.go`). `MountLifecycle` has 4 call sites (`main.go:400`, `lifecycle_fake_test.go:24`, `lifecycle_suspend_test.go:41`, `suite_envtest_test.go:125`). Adding parameters means updating all of them. Test fixtures in `config_envtest_test.go:36-44` register no cluster (`kube.NewRegistry("default")`), so a ConfigMap write there would hit zero clusters and the flip test would pass vacuously unless the fixture registers `kubeC`.

**D14. Failing ConfigMap write status.** Evidence: T047 and contract cluster-setting.md say "502 with the existing error body" for a failing ConfigMap write, and T047 says "a failing ConfigMap write returns an error". Contract cluster-setting.md "Storage and routes" says 502. `httperr.Write` (`httperr.go:245-258`) returns 500 for an unclassified error. To return 502 use `httperr.WriteCode(w, req, http.StatusBadGateway, ...)` (only for 5xx, the message is masked to the generic status text, `WriteCode` `:203-205`).

**D15. No exported db helper to write a config row.** Evidence: the only writes are inline SQL in `config.go:161-171` and `config.go:270-280` and an unexported `upsertConfigTx` in `db/telemetry.go:~109-115`. Reuse the inline SQL in the handler (as today) or add an exported `db` helper. Either is fine; flag for consistency with contract "ConfigMap first then the config row".

**D16. Multi-cluster ConfigMap writes on remote clusters are not designed.** Evidence: `reg.IDs()` covers all registered clusters (`kube/registry.go:249`), but the remote cluster's control-plane namespace and whether the API's remote kubeconfig may write ConfigMaps there is not defined anywhere in the chart I read (`operator.yaml` `remoteClusters` branch and `api-credentials.yaml` only cover the home cluster). Contract says "every registered cluster the API can reach"; the remote namespace (`operatorClusterCredentialsNamespace` on the remote chart) and its RBAC are unverified. Open item for the maintainer.

**D17. Envtest suite router cannot exercise the new paths as-is.** `mountedR` (`suite_envtest_test.go:119-142`) has nil store (`:124`), no rbac middleware, and no auditor on `MountResources`. `resources_envtest_test.go` helper `doJSON` uses `apiBase` with no user at all, so `validateAndProtectGameServer` sees `u == nil` (permission false) for every call. Security envtests must build a dedicated router like `newCaptureRBACRouter` (`capture_rbac_envtest_test.go:84-100`) with `rbac.Middleware`.

**D18. T013's "clone with live == nil" and create share one branch.** Confirmed: `lifecycle.go:243` passes `nil` live, same as `resources.go:250`. No discrepancy in behavior; noted because T013 lists clone separately.

---

## 4. Open items (not verifiable by read)

- `api/internal/rbac/middleware` `Can` namespace semantics for `scope.DefaultNamespace` vs `"*"` bindings used in T010: `userForRole` binds at `"*"` namespace (`rbac_test.go:17-24`); `Can` treats a cluster-wide `"*"` binding as holding namespaced perms (`perms.go` comment at `:118-128`). Assumed, not run.
- SQL dialect: no new migration (contract). `config` row key `security` needs no schema change.
- Postgres seed `migrations/postgres/003_roles.sql` unchanged.
- Local verification: per CLAUDE.md rule 8, only `go build ./...` is allowed locally. Not run by this scout.
