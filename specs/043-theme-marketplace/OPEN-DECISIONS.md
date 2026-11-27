# Open decisions: spec 043 theme marketplace

Only a decision valgul answers is written here as settled. OD-1 to OD-3 were raised on 2026-11-26 and asked as decision cards. OD-1 to OD-3 are settled. Planning raised OD-4 on 2026-11-26; it is settled. OD-5 is open (raised during implementation of T015 and T016); see the end of this file.

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

## OD-5: How is a theme source credential stored? (OPEN, raised 2026-11-27 by T015/T016)

data-model.md says `theme_sources.credential` is a blob encrypted with the panel key. Findings: the panel key exists only in standalone mode, is private to package `controlplane`, covers only `management_objects` rows of kind `secrets`, and is absent in combined (in-cluster) mode, where secrets live in Kubernetes. A new encrypted blob column would be unreadable in combined mode and skipped by key rotation.

What the branch implements now (provisional, not settled): migration `028_theme_marketplace.sql` has `theme_sources.credential_ref TEXT` (nullable) in place of the blob. The row holds only the name of a labelled credential Secret, managed through `kube.Client.Secrets(ns)` (Kubernetes Secret in combined mode, panel-encrypted `management_objects` in standalone, covered by key rotation). The credential is never stored in the row or returned by the API. Store code treats `credential_ref` as an opaque string.

- A. Labelled Secret, name in `credential_ref` (recommended, as implemented). Needs a secret name that fits 63 characters: cap source names or derive the name from the numeric id.
- B. Keep the encrypted blob as data-model.md says, with a new panel-key helper usable in both modes. Needs a new encryption path and rotation coverage for the new column.

Blocks T046 (source credential handling). Settle before T046 is merged; if B is chosen, replace the `credential_ref` column in 028 before it ships.

## Implementation notes (T015/T016, no decision needed)

- Preview bytes use `BYTEA` (PostgreSQL) and are stored as BLOB on SQLite. Content type is derived from the bytes when served (`themepkg.PreviewInfo`), so there are no `*_type` columns.
- Ids are generated in Go (random positive 53-bit, as `auth.newMCPTokenID`). No `REFERENCES`: dependent rows are deleted or nulled in Go transactions (`DeleteThemeSource`, `DeleteInstalledTheme`, `DeleteUserTx`).
- `bundlefetch` is added to `api/go.mod` (replace and require). `go mod tidy` also moved unrelated indirect versions (go-openapi swag 0.28 to 0.29, x/time 0.15 to 0.16, onsi/gomega added) because bundlefetch pulls in newer shared dependencies.

## OD-6: Official source identity and theme list (OPEN, raised 2026-11-27 by T021)

The seeded `official` source (`api/internal/themes/seed.go`) cannot verify anything yet:

- The keyless identity is a placeholder, `https://github.com/GameplanePanel/themes/.github/workflows/release.yaml@refs/heads/master`. cosign matches the Fulcio subject exactly (`bundlefetch/verify/verify.go:209`), and a release tag gives a different ref per tag, so no single string covers every release. Options: (A) pin the exact ref the T061 workflow signs with (for example `@refs/heads/master` if releases sign from master); (B) add a subject-regex option to `bundlefetch/verify` (a bundlefetch change, needs its own test pass). Until this is settled, every official theme reports `unverified`.
- The OCI spec needs an explicit `themes[]` list (registries cannot be enumerated). The seed writes `themes: []`, so the official source indexes nothing until the list exists (T062). Options: (A) ship the list in the API with each release, (B) let the source read an index artifact.
- Deletion: SeedOfficial stores a marker (`config` key `themes.official_seeded`) so a deleted official source is not re-created on restart. Still open: whether `DELETE` of `official` returns 409 (disable only, recommended in OD-8 notes) or is allowed.

Blocks T061 and T062 (identity and list), and the T046 rule that reserves the name `official`.

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

