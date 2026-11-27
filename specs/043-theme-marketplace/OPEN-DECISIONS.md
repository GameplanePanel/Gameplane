# Open decisions: spec 043 theme marketplace

Only a decision valgul answers is written here as settled. OD-1 to OD-3 were raised on 2026-11-26 and asked as decision cards. OD-1 to OD-3 are settled. Planning raised OD-4 on 2026-11-26; it is settled. OD-5 is settled. OD-6 and OD-7 were raised during implementation and valgul settled both on 2026-11-27 (OD-6: same as modules; OD-7: A, `themes:manage`). No decision in this file is open.

## OD-1: Who can install themes from the catalog? (SETTLED: B, valgul 2026-11-26)

- A. Admins (a new `themes:write` permission) install themes for the whole instance; every user then picks from the installed list in Theme settings. (Recommended: same model as modules, one reviewed set of CSS per instance, and the audit log shows who brought each theme in.)
- B. Every user installs themes for themselves only; nothing is shared between users.
- C. Both: admins install for everyone, and users may also apply any catalog theme to themselves only.

**Settled (valgul, decision card 2026-11-26):** B, "Each user, for themselves". Any signed-in user installs catalog themes for their own account only; nothing is shared between users. Admins still manage theme sources (spec FR-001). With no instance-wide install, the earlier admin-set instance default theme was dropped; fallback is Modern Pink.

## OD-2: What can a theme package contain? (SETTLED: A, valgul 2026-11-26)

- A. Light and dark color tokens plus an optional CSS overlay, sanitized like user custom CSS (no `@import`, no external `url()`, 32 KB), plus preview images. (Recommended: covers every example overlay and keeps the spec 016 safety rules.)
- B. Color tokens only, no CSS. Safest, but most example overlays (layouts, glass, hover effects) could not be published.
- C. Tokens, CSS and bundled fonts or images inside the package. Richest, but needs new sanitizing and size rules for binary files.

**Settled (valgul, decision card 2026-11-26):** A, "Colors + sanitized CSS": light and dark color tokens, an optional CSS overlay sanitized like user custom CSS, plus preview images.

## OD-3: Where are official themes published? (SETTLED: A, valgul 2026-11-26)

- A. As signed OCI artifacts from a new repository `GameplanePanel/themes`, read through the same source kinds as modules. (Recommended: themes ship on their own release cadence, like modules, without touching the module repo.)
- B. Inside the existing `GameplanePanel/module` repository, next to the game modules.
- C. Built into the dashboard image (no source to configure); community themes still come from sources.

**Settled (valgul, decision card 2026-11-26):** A, "New GameplanePanel/themes repo": signed OCI artifacts from a new repository `GameplanePanel/themes`. Sessions cannot create repositories, so valgul creates it before the publishing tasks.

## OD-4: Where do theme sources live, and who fetches themes? (SETTLED: A, valgul 2026-11-26)

Raised by planning (research.md R1). Themes are per panel user (OD-1), while module sources are cluster resources reconciled by the operator, and a standalone panel (spec 025) has no operator.

- A. In the panel database, managed through the API; the API indexes and pulls themes. The OCI client and cosign verification move from `operator/internal/{oci,verify}` into a new shared Go module `bundlefetch/` that the operator's module sources and the API's theme sources both use. (Recommended: works on standalone and multi-cluster panels, one implementation of signature checks.)
- B. A cluster-scoped `ThemeSource` CRD reconciled by the operator, reusing `operator/internal/modsrc` as is. Smallest change, but standalone panels get no themes, and with several clusters themes depend on one cluster.
- C. In the panel database, with the API using its own small OCI and cosign client. No operator change, but two copies of signature handling to keep in step.

**Settled (valgul, decision card 2026-11-26):** A, "Panel database, shared fetch code". Theme sources live in the panel database and the API fetches themes; the OCI, git, HTTP and local fetch code and cosign verification move from the operator into a shared Go module `bundlefetch/` used by both module sources and theme sources.

## PR-B (bundlefetch extraction): raised during implementation

- **B1. Coverage total for `bundlefetch`.** Not a decision after all: `bundlefetch/.testcoverage.yml` keeps `total: 90`, and PR-B adds new test files `bundlefetch/source/*_test.go` (new tests need no sign-off, see tasks.md "Tests"). If CI still reports under 90, more new tests are added; the gate is not lowered.
- **B5. Logger hook in `bundlefetch/verify`.** SETTLED (valgul, decision card 2026-11-27): "Context logger, slog fallback". `fulcioPools` uses `logr.FromContext(ctx)` and falls back to `slog.ErrorContext` when the context has no logger; the operator logs as before and the warning text is unchanged.

## OD-5: How is a theme source credential stored? (SETTLED: A, valgul 2026-11-27)

data-model.md says `theme_sources.credential` is a blob encrypted with the panel key. Findings: the panel key exists only in standalone mode, is private to package `controlplane`, covers only `management_objects` rows of kind `secrets`, and is absent in combined (in-cluster) mode, where secrets live in Kubernetes. A new encrypted blob column would be unreadable in combined mode and skipped by key rotation.

valgul chose A on the decision card ("Secret, name kept on the source"). The branch implements: migration `028_theme_marketplace.sql` has `theme_sources.credential_ref TEXT` (nullable) in place of the blob. The row holds only the name of a labelled credential Secret, managed through `kube.Client.Secrets(ns)` (Kubernetes Secret in combined mode, panel-encrypted `management_objects` in standalone, covered by key rotation). The credential is never stored in the row or returned by the API. Store code treats `credential_ref` as an opaque string.

- A. Labelled Secret, name in `credential_ref` (recommended, as implemented). Needs a secret name that fits 63 characters: cap source names or derive the name from the numeric id.
- B. Keep the encrypted blob as data-model.md says, with a new panel-key helper usable in both modes. Needs a new encryption path and rotation coverage for the new column.

Blocks T046 (source credential handling). Settle before T046 is merged; if B is chosen, replace the `credential_ref` column in 028 before it ships.

## Implementation notes (T015/T016, no decision needed)

- Preview bytes use `BYTEA` (PostgreSQL) and are stored as BLOB on SQLite. Content type is derived from the bytes when served (`themepkg.PreviewInfo`), so there are no `*_type` columns.
- Ids are generated in Go (random positive 53-bit, as `auth.newMCPTokenID`). No `REFERENCES`: dependent rows are deleted or nulled in Go transactions (`DeleteThemeSource`, `DeleteInstalledTheme`, `DeleteUserTx`).
- `bundlefetch` is added to `api/go.mod` (replace and require). `go mod tidy` also moved unrelated indirect versions (go-openapi swag 0.28 to 0.29, x/time 0.15 to 0.16, onsi/gomega added) because bundlefetch pulls in newer shared dependencies.

## OD-6: Official source identity and theme list (SETTLED: "Same as modules", valgul 2026-11-27)

- Official themes are signed with the same cosign key pair as the official modules. The official source's verify policy is keyed with the project public key `signing/cosign.pub` (offline, no Rekor), not keyless.
- The chart carries a copy of that key in `themes.officialSource.verify.cosignPublicKey`, the same block as `defaultModuleSource.oci.verify.cosignPublicKey`. Verify is off by default, as for official modules.
- The official theme list ships in the chart as `themes.officialSource.themes`, the same way `defaultModuleSource.oci.modules` does. It is empty until GameplanePanel/themes releases add names. Every API start reconciles the official row's URL, theme list and verify policy from the chart; the admin's enabled flag and refresh interval are kept.
- This replaces the earlier answers: the keyless tag identity, the placeholder identity (removed from the code), and the index-artifact option.
- Deletion: the `themes.official_seeded` marker is removed, because a deleted official source is not re-created by the start. `DELETE` of `official` returns 409 (disable only), so a missing row can only mean a fresh database.

## Implementation notes (T019/T021, no decision needed)

- `api/internal/themes` is a Service built from `Deps{Store, Logger}` only (no cluster dependency). Kube, credentials and the local-root setting are added by the indexer and source tasks.
- `handlers.MountThemes(p)` mounts empty `/themes` and `/users/me/themes` routers. `MountUsers` keeps its signature (`cmd/runtime_postgres_test.go` calls it).
- Helm: `themes.officialSource.enabled` (default true) renders `GAMEPLANE_THEMES_OFFICIAL_SOURCE` on the API Deployment; `charts/gameplane/values.schema.json` does not exist in this chart, so only `values.yaml` and `templates/api.yaml` changed.
- Env-only config: `GAMEPLANE_THEMES_OFFICIAL_SOURCE` has no CLI flag (same as `GAMEPLANE_TELEMETRY_BUNDLED`).

## Implementation notes (T020/T024, indexer)

- The indexer pulls before it compares digests: `oci.Client` has no HEAD or resolve call, so "skip when the digest is unchanged" saves the validate, verify and upsert work, not the download (packages are at most 1.5 MiB).
- Latest version (brief OD 13, applied as recommended, not settled): the highest tag without a pre-release part, else the highest pre-release. Build metadata after `+` is ignored.
- `bundlefetch/oci` gained `WithArtifactType` and `WithMaxLayerBytes` (defaults unchanged). Theme pulls use `application/vnd.gameplane.theme.v1+json` and a layer cap of `themepkg.MaxPackageBytes`.
- Credentials: indexing is anonymous until T046 wires the `credential_ref` Secret (OD-5). Private sources fail with a source error until then.
- Local sources: `Deps.LocalRoot` exists, but `cmd/main.go` does not set it yet (no `GAMEPLANE_THEMES_LOCAL_ROOT` flag). Local sources fail with "local sources are disabled" until that flag lands with T046 (brief item 5).
- The scheduler is `Service.Run`, one source at a time, with a 2-minute timeout per source. It is not started from `cmd/main.go` yet (leader wiring is a later task).
- A signature failure gives status `unverified` with rule `signature`; the detail is generic and the cause is logged. Verifiers are built lazily per source and policy (cache key covers the policy and the insecure flag).
- Tests: `api/internal/themes/indexer_test.go` (new) uses an in-process registry fake and an in-memory SQLite store. It is not run locally (CI only).


## Implementation notes (T033–T048 API, no decision needed unless valgul objects)

- `DELETE /themes/sources/official` answers 409: the official source can only be disabled, as the T004 design brief says ("marked as built-in and cannot be deleted, only disabled") and FR-002 allows.
- Install limit answers 409 `server.themes.install_limit`; a malformed or foreign installed id answers 404; refresh on a disabled source answers 409 `source_disabled`.
- `refreshInterval` is in seconds (default 3600, floor 300). Source names are DNS labels of at most 40 characters (room for a derived Secret name, OD-5).
- OD-5 follow-up (valgul 2026-11-27, card "Should the theme source dialog take a Secret name, like module sources?": "Secret name, like modules"): the bullet below is replaced. `POST`/`PUT /themes/sources` take an optional `credentialSecret`, the name of a Secret the admin creates in the credentials namespace, stored in `credential_ref` and shown in responses. The panel only reads that Secret (`.dockerconfigjson` for oci; `token` or `username` + `password` for git and http) and never writes or deletes it. PUT without it keeps it, `null` or `""` clears it, and it stays when the location changes, as on a ModuleSource. Contract: contracts/themes-api.md.
- Superseded by the follow-up above: OD-5 (option A) was first implemented as: `POST`/`PUT /themes/sources` take an optional `credential`. An oci source takes docker config JSON (`{"auths": {...}}`); a git or http source takes `{"token"}` or `{"username","password"}`; a local source takes none (422). The value is stored as a labelled Secret `gameplane-theme-source-<name>` (label `gameplane.local/theme-source=true`) through `kube.Client.Secrets(credentialsNS)`, and the row keeps only its name in `credential_ref`. PUT without `credential` keeps it, `null` clears it, and a moved location drops it. Delete removes the Secret. Responses and errors never carry the value. The indexer reads the Secret for each pull; the signature check still pulls anonymously.
- Source list items carry an extra `builtin` boolean (additive to `contracts/themes-api.md`).

## OD-7: Permission for theme sources (SETTLED: A, valgul 2026-11-27)

**Settled (valgul, 2026-11-27): A, new permission.** A new admin-only permission `themes:manage` governs theme sources (research.md R6; rbac catalog key `themes:manage`). It is not `modules:manage`.
