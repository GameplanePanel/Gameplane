# Research: Theme marketplace (spec 043)

Settled inputs: OD-1 (each user installs for themselves), OD-2 (tokens + sanitized CSS + previews), OD-3 (signed OCI from `GameplanePanel/themes`). Spec 044 (plugins) keeps its own catalog: the plugins thread reported valgul chose "two separate catalogs" on its card (2026-11-26), so nothing here is shared with spec 044. OD-4 below was raised by this plan and settled by valgul (A, 2026-11-26).

What exists today, checked on `master` @ `9f5bc8c3`:

- Module sources are a cluster-scoped CRD `ModuleSource` (`operator/api/v1alpha1/modulesource_types.go:306`) with types `oci`, `git`, `http`, `local` (+ uploads), `allow` globs, `refreshInterval`, and cosign `verify` (key or keyless, optional Rekor). The operator indexes them through `operator/internal/modsrc.Fetcher` (`Index`, `Pull`), pulls with `operator/internal/oci` (oras-go, artifact type `application/vnd.gameplane.module.v1+json`) and verifies with `operator/internal/verify` (cosign/sigstore-go). These are `internal` packages of the operator module, so the API cannot import them.
- The API manages module sources as a UX gateway (`api/internal/handlers/module_sources.go`, permissions `modules:read` / `modules:manage` in `api/internal/rbac/catalog.go:45`).
- Spec 016 stores each user's theme in `user_preferences` (`api/internal/db/preferences.go`): `theme_type` (`preset`|`custom_colors`), `preset_id`, `appearance_mode`, custom colors, `custom_css_enabled`, `custom_css`. CSS is sanitized server-side by `sanitizeCustomCSS` (`api/internal/handlers/users.go:306`: 32 KiB, no `@import`, no external `url()`) and client-side by `web/src/lib/theme-sanitize.ts`. Custom colors expand to 14 semantic tokens (`web/src/lib/theme-derivation.ts:11`, `CUSTOM_THEME_TOKEN_NAMES`) with WCAG floors 4.5:1 text and 3:1 accent/surface.
- The standalone panel (spec 025) runs the API with no operator and no local cluster; modules there are per registered cluster.

## R1: Where theme sources live and who fetches them (OD-4, settled: A)

- **Decision (OD-4 A)**: Theme sources are panel data, not cluster resources. They are rows in the API database, managed by admins, and the API indexes and pulls them. The OCI client and cosign verification move out of `operator/internal/` into a new shared Go module `bundlefetch/` (oci, git, http, local fetch of a file set + digest + cosign verify), which the operator's `modsrc` and the API's new theme catalog both use.
- **Rationale**: Themes are per user of the panel (OD-1), like `user_preferences`, not per cluster. A standalone panel has no operator, and with several clusters a CRD would force picking one cluster's `ThemeSource`. Rule 10 (operator authority) covers cluster workloads; panel-only UX data already lives in the API (preferences, MCP tokens, SFTP credentials). Sharing the fetch/verify code keeps one implementation of signature checks.
- **Alternatives**: (B) a cluster-scoped `ThemeSource` CRD reconciled by the operator, reusing `modsrc` as is: smallest code change, but themes vanish on a standalone panel and are tied to whichever cluster the panel runs next to. (C) API-side sources with a small oras client written in the API: no operator refactor, but two copies of OCI and cosign handling to keep in step.

## R2: Package format

- **Decision**: One OCI artifact per theme version, artifact type `application/vnd.gameplane.theme.v1+json`, tag = semver version, under `<url>/<theme-name>:<version>`. Layers: `theme.yaml` (metadata + tokens, `application/vnd.gameplane.theme.metadata.v1+yaml`), optional `theme.css` (`text/css`), `previews/light.png` and `previews/dark.png` (`image/png` or `image/webp`). Git, HTTP and local sources carry the same files in a directory per theme. Full schema: [contracts/theme-package.md](contracts/theme-package.md).
- **Limits**: CSS 32 KiB (same as user CSS), each preview 512 KiB and at most 1600×1000 px, whole package 1.5 MiB, `theme.yaml` 16 KiB. No other files (OD-2): a package with extra layers or files is Invalid.
- **Rationale**: Mirrors the module bundle (`MediaTypeMetadata`, `MediaTypeIconPNG`), so the same push and sign tooling works; the token set is spec 016's 14 tokens, so an installed theme renders through the existing custom-colors code path.
- **Alternatives**: A single JSON blob (spec 016 export format) per artifact: simpler, but previews would be base64 inside JSON and inflate the catalog response.

## R3: Validation and sanitization in one shared place

- **Decision**: New stdlib-only Go module `themepkg/` (like `telemetryschema/`): `theme.yaml` schema and strict decode, token rules (14 tokens per mode, lowercase `#rrggbb`), the CSS sanitizer moved from `api/internal/handlers/users.go` (the handler then calls `themepkg.SanitizeCSS`, so user custom CSS and theme CSS share one rule set), contrast checks (text 4.5:1, accent/surface 3:1 → `lowContrast` flag, not a failure), size and file-set limits, preview header checks (PNG/WebP magic bytes and dimensions). Used by the API (listing and install), and by `gp-module theme validate`. It also ports the accent+surface → 14-token derivation from `web/src/lib/theme-derivation.ts` to Go for `gp-module theme from-export`; a shared fixture file (inputs and expected tokens) is checked by both the Vitest and the Go tests so the two stay identical.
- **Rationale**: FR-008 asks for the same checks at validation, listing and install; one package means they cannot drift. Coverage minimum 90 like the other shared libraries.
- **Web**: `theme-sanitize.ts` stays as the client-side pre-check for user CSS; installed theme CSS is trusted only after the server's check and is never re-entered by the user.

## R4: Catalog indexing and caching

- **Decision**: The API refreshes each enabled source on its `refreshInterval` (default 1h, minimum 5m) and on demand ("Refresh now"). For each theme it lists versions, then pulls only the latest version whose digest changed, verifies the signature when the source requires it, validates it with `themepkg`, and stores the result in `theme_catalog_entries` (metadata, tokens, sanitized CSS, previews, digest, flags). Unchanged digests are skipped, except that editing a source's verification settings re-checks every entry of that source against the new policy at once, whatever its digest: an entry that fails becomes `unverified` and cannot be installed until it passes. One source failing marks only that source (`lastError`); other sources' entries stay.
- **Rationale**: SC-006 (catalog within 3 s with a source down) needs the catalog served from the database, never from live registry calls. Packages are small (≤ 1.5 MiB), and only the latest version of each theme is cached.
- **Install = snapshot**: installing copies the cached latest version's tokens and CSS (re-checked with `themepkg`) into the user's `user_installed_themes` row. The user keeps that version until they update (US3, FR-010), and it keeps working when the source is disabled, removed or the theme is unpublished (FR-013). Previews are not copied; a "Source removed" theme shows a placeholder preview. Because install reads only the cache, an unreachable registry does not block an install of a theme already cached as `valid`; it fails only when the theme has no `valid` cached entry (spec edge case "Source down during install or update"). Installs and updates are refused for entries of a disabled source (themes-api.md).
- **Alternatives**: Pull on install: makes install depend on the registry being up and repeats the signature check per user.

## R5: Selection and rendering

- **Decision**: `user_preferences.theme_type` gains `installed` with a new nullable `installed_theme_id` (FK to the user's own `user_installed_themes` row). Rendering reuses the custom-colors path: the web app writes the theme's tokens for the active light or dark mode into `<style id="gameplane-custom-theme-vars">`, then the theme CSS into a new `<style id="gameplane-installed-theme-css">` placed before the user's own overlay, so the user's custom CSS stays last in the cascade (US2.4).
- **Single-mode themes**: a theme that ships tokens for only one mode uses them for both and the catalog says "Dark only" or "Light only".
- **Safe mode**: the existing safe-mode switch (spec 016 FR-009) also skips the installed theme's CSS and tokens and renders Modern Pink (FR-009, edge case "theme that hides critical controls").
- **Removal fallback**: removing the selected theme sets `theme_type=preset`, `preset_id=pink` in the same transaction (FR-011).
- **Unauthenticated pages**: unchanged; `enforceUnauthenticatedTheme.ts` already forces Modern Pink and drops overlays, and gains the new style element id.

## R6: Preview without saving

- **Decision**: `GET /themes/catalog/{id}` returns the cached tokens and sanitized CSS; the web app applies them in memory only, shows a banner with Keep (= install + select) and Exit preview, and restores the saved preferences on exit, route change away from the theme pages, or reload. Nothing is written server-side during a preview.

## R7: Permissions and audit

- **Decision**: New permission `themes:manage` ("Manage theme sources"), granted to the built-in admin role only. Browsing, previewing, installing, updating, removing and selecting themes need only a signed-in account (own data, like preferences). Audit events: `theme_source.create|update|delete|refresh`, `theme.install|update|remove` with source, theme name, version and digest (FR-006).
- **Rationale**: Matches OD-1; the permission key is additive and does not touch spec 039's per-server access rework.

## R8: Official source and repository

- **Decision**: New repository `GameplanePanel/themes` (valgul creates it; sessions cannot). Layout like `GameplanePanel/module`: one folder per theme with `theme.yaml`, optional `theme.css`, `previews/`; a `build.sh` that validates with `gp-module theme validate` and pushes with oras to `ghcr.io/gameplanepanel/gameplane-themes/<name>:<version>`; a release workflow that keyless-signs with cosign (GitHub Actions OIDC). It is not a submodule of Gameplane (nothing in the main repo builds from it).
- **Default source**: the API seeds one `official` theme source on first start (migration-time row), type `oci`, URL `ghcr.io/gameplanepanel/gameplane-themes`, keyless verify pinned to the themes repo's release workflow identity. Admins can disable it (FR-002). Helm value `themes.officialSource.enabled` (default true) lets air-gapped installs skip seeding.
- **Seed content**: the 33 CSS overlays on `feat/example-theme-gallery` (`example/custom-css/*.css`) are run through `themepkg`; those that pass become the first themes, each given tokens (from its CSS variables) and previews captured with a Playwright script against the dev cluster. SC-002 needs at least 10.

## R9: Authoring CLI

- **Decision**: A `theme` command group in the existing `gp-module` CLI: `gp-module theme init <name>`, `theme from-export <export.json>` (spec 016 export to a package folder), `theme validate <dir>`, `theme package <dir>` (writes an OCI layout or pushes with `--push <ref>`). Validation uses `themepkg`, so it gives the same verdict as the panel. Contract: [contracts/gp-module-theme-cli.md](contracts/gp-module-theme-cli.md).
- **Alternatives**: a separate `gp-theme` binary: one more release artifact for little gain.

## R10: Dashboard surfaces (design-first)

- **Decision**: New screens in `design.fig` before any React: Theme catalog (grid, search, tag and source filters, flags), theme detail (previews, changelog, Install/Update/Remove, Preview), preview banner, "Installed themes" section added to Settings → Theme, and admin Settings → Theme sources (list, add/edit dialog mirroring the module source dialog, status). Routes: `/settings/theme/catalog`, `/settings/theme/catalog/$id`, `/settings/theme-sources`. New strings go through the language packs (spec 019).
- The design work is a local-agent prompt in `/mnt/project-files/notes/design-prompts/` (OpenPencil cannot run in cloud threads for new screens without the maintainer's review), written with the tasks.

## R11: Testing (CI only, rule 8)

- `themepkg` unit tests (schema, tokens, CSS rules shared with spec 016 cases, contrast, limits, bad-package fixtures for SC-003).
- `bundlefetch` unit tests moved with the code from the operator; operator envtests for modules must keep passing unchanged (no test edits expected; any edit follows the test-edit rules).
- API envtests: sources CRUD + `themes:manage`, indexing with a fake registry (one source down), install/update/remove snapshot semantics, fallback to Pink, audit rows, preferences `installed` type.
- Web Vitest: catalog, detail, preview banner, installed list, sources admin, safe mode skipping theme CSS.
- `gp-module` tests for the theme commands.
- E2E: new bucket `themes` (`test/e2e/api_themes_e2e_test.go`): push a signed and an unsigned theme to the Kind registry (`ociPushMu`), add a source, catalog lists both with the unsigned one flagged, user A installs and selects, user B unaffected, update, disable source (installed copy keeps working), remove (fallback to Pink). Plus a standalone-bucket case: theme sources work with no operator.
