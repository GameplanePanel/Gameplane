# Spec 043 T002: existing tests this spec moves or changes

Checked against `origin/master` on 2026-11-26. No test body is changed, weakened or deleted. Every change below is either a whole file moving to a new folder, single test functions moving to a new file with the body unchanged, or an import path changing. The test-diff peer review (a fresh agent that sees only the test diff) runs on PR-B, and its verdict is added here.

## Correction to the tasks.md assumption

`tasks.md` T002 assumed that all of `operator/internal/oci` and `operator/internal/verify` move to `bundlefetch`. They can't, because some of that code reads Kubernetes Secrets and the `ModuleSource` CRD. The API, including the standalone panel, can't depend on those. The split is:

- **Moves to `bundlefetch`** (no Kubernetes imports): the OCI client, bundles, locale layers, and the cosign verifier core (`newKeyed`, `newKeyless`, `baseCheckOpts`, `poolsFromCAs`, `fulcioPools`, `tufRootWarning`, `cosignVerifier`).
- **Stays in the operator** as a thin adapter: `oci/auth.go` (`CredentialFromSecret`, which reads docker-config Secrets) and `verify.Build`, `readKey`, `authFor`, `isDockerHubRegistry` and `registryVerifier` (which read `ModuleSource` plus Secrets, then call `bundlefetch`).

## A. Whole files that move, bodies unchanged (package path only)

| From | To | Tests |
|---|---|---|
| `operator/internal/oci/client_test.go` | `bundlefetch/oci/client_test.go` | 5: TestClient_Pull, …RequiresManifestToMatchDigest, …RequiresLayersToMatchDigest, …RejectsForeignArtifact, TestClient_ListTags_DropsNonSemverAndSorts |
| `operator/internal/oci/client_extra_test.go` | `bundlefetch/oci/client_extra_test.go` | 5: TestRepo_BadReference, TestRepo_WithCredentials, TestListTags_RepositoryDoesNotExist, TestPull_TagNotFound, TestPull_SkipsUntitledLayers |
| `operator/internal/oci/locale_test.go` | `bundlefetch/oci/locale_test.go` | 4: TestLocaleTag, TestLocaleLayers, TestClient_Pull_LocaleLayers, TestSplitLocaleLayers |
| `operator/internal/oci/locale_budget_test.go` | `bundlefetch/oci/locale_budget_test.go` | 2: TestLocaleBudget_Take, TestClient_Pull_LocaleTotalBudget |
| `operator/internal/oci/testregistry_test.go` | `bundlefetch/oci/testregistry_test.go` | helper only (fake registry: newFakeRegistry, digestFor, digestFromString), used only by the four files above |
| `operator/internal/verify/verify_fulcio_test.go` | `bundlefetch/verify/verify_fulcio_test.go` | 6: TestPoolsFromCAs_* (4), TestTufRootWarning_UnsetReturnsFalse, TestTufRootWarning_SetReturnsExplicitWarning |

## B. Single functions that move to a new file, bodies unchanged

From `operator/internal/verify/verify_branches_test.go` to new `bundlefetch/verify/cosign_test.go`. They test only `newKeyed` / `cosignVerifier` / `baseCheckOpts`, which move.

- TestNewKeyed_TransparencyLogPolicy
- TestCosignVerifier_Verify

The other 4 tests in that file stay where they are: TestBuild_VerifyWithoutOCIErrors, TestBuild_NeitherKeyNorKeylessErrors, TestBuild_KeySecretMissingDataKeyErrors, TestAuthFor_Branches.

## C. Import path change only (`operator/internal/oci` → `github.com/GameplanePanel/gameplane/bundlefetch/oci`)

Each of these uses only `oci.MaxLocaleLayerSize`, `oci.IsOversizedLocale`, `oci.OversizedLocale` or `oci.SplitLocaleLayers`.

- `operator/internal/modsrc/bundle_locales_test.go`
- `operator/internal/modsrc/dir_locales_test.go`
- `operator/internal/modsrc/dir_locales_budget_test.go`
- `operator/internal/modsrc/dir_locales_capped_test.go`
- `operator/internal/modsrc/dir_locales_review_test.go`
- `operator/internal/modsrc/upload_locales_test.go`
- `operator/internal/controller/module_locales_envtest_test.go`

## D. None needed

- `operator/internal/oci/auth_test.go` (13 tests): stays, because `auth.go` stays.
- `operator/internal/verify/verify_test.go` (10 tests): stays, because every test calls `Build` with a fake Kubernetes client.
- `operator/internal/controller/module_envtest_test.go`, `module_failed_settle_test.go`: import `operator/internal/verify`, which stays.
- Other `operator/internal/modsrc/*_test.go` (bundle, dir, fetcher, git, http, local, oci, upload, http_locales): no import of the moved packages.
- `api/internal/handlers/users_customcss_security_test.go`: keeps calling `sanitizeCustomCSS`, which becomes a one-line wrapper over `themepkg.SanitizeCSS`.
- `api/internal/handlers/users_preferences_test.go`, `web/src/lib/useThemePreferences.test.ts`, `web/src/routes/ThemeSettings.test.tsx`, `web/src/lib/theme-export.test.ts`: no edit planned. The new `installed` theme type gets its own new test files (T040). If a CI failure later shows one of these asserts a shape that changes, I stop and bring you the exact before/after first.

## Sign-off

valgul: APPROVED AS WRITTEN, 2026-11-26 (decision card).

## PR-B record (2026-11-27)

Done as listed. Two details the list above did not spell out, both without any change to a test body:

- `bundlefetch/verify/cosign_test.go` carries a byte-identical copy of the `testPubPEM` helper, because the two moved tests use it and the staying tests in `verify_branches_test.go` keep the original.
- `verify_branches_test.go` loses its `strings` import, which only the two moved tests used; gofmt re-sorted the changed import line in `module_locales_envtest_test.go`.

Test-diff peer review (fresh agent, test diff only): **no weakening**. Six files are pure renames at 100 % similarity, the two moved tests match line by line, and the seven other edits are one-line import swaps.

New test files added by PR-B (not moves): `bundlefetch/source/{dirs,http,git,local,oci}_test.go`.

## PR-B follow-up: new bundlefetch test expectations (main -> master)

`bundlefetch/source/git_test.go` (new in PR-B): the cases "unclean subPath rejected" and "clone failure propagates" expect the default ref `master` instead of `main`, following #1121. `bundlefetch/source/http_test.go`: `switch { case want == ... }` became `switch want { case ... }` (lint only). Fresh-agent peer review of the test diff: no weakening. valgul: APPROVED AS WRITTEN, 2026-11-27 (decision card).
