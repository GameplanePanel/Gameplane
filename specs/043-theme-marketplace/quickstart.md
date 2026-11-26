# Quickstart: validate the theme marketplace (spec 043)

Manual walk for the maintainer after the implementation merges. Automated coverage is the `themes` E2E bucket (research.md R11); CI is the only pass/fail authority.

## Prerequisites

- `make dev-up` cluster with the build from `master`, admin account, and a second plain user account.
- `bin/gp-module` built; `cosign` and `oras` on PATH; the dev registry on `localhost:5001`.

## 1. Author and publish a test theme (US5)

1. In the dashboard as the plain user, set custom colors in Settings → Theme and export the theme (spec 016).
2. `gp-module theme from-export export.json --name qs-theme` → a `qs-theme/` folder. Add two screenshots as `previews/light.png` and `previews/dark.png`.
3. `gp-module theme preview qs-theme` → open the local page and check both modes. `gp-module theme validate qs-theme` → passes. Add `@import url(https://x)` to `theme.css` and validate again → fails with `css.import`, line number. Remove it.
4. `gp-module theme package qs-theme --push localhost:5001/themes/qs-theme:0.1.0`.

## 2. Add the source (US4)

1. As admin, Settings → Theme sources: the `official` source is listed and enabled.
2. Add source `qs`, type OCI, URL `kind-registry:5000/themes` (the in-cluster registry name, as for `MODULE_SOURCE_URL`), themes `qs-theme`, insecure, no verify. After refresh, `themeCount` is 1.
3. Edit `qs` to require a cosign key: the catalog shows `qs-theme` as "Signature not verified" and Install is disabled. Create a key pair with `cosign generate-key-pair` (writes `cosign.key` and `cosign.pub`), paste `cosign.pub` into the source's verify key, sign with `cosign sign --key cosign.key --allow-insecure-registry localhost:5001/themes/qs-theme:0.1.0`, and refresh: it becomes installable.

## 3. Browse, preview, install (US1, US2)

1. As the plain user, Settings → Theme → Browse themes. Search "qs" → only `qs-theme`. Filter by source `qs`.
2. Open it, click Preview: the dashboard switches; the banner offers Keep and Exit preview. Exit → the saved theme is back exactly.
3. Install and select it. Toggle Light/Dark/System → the matching tokens apply. Enable your own custom CSS overlay → it still wins.
4. Sign in as admin in another browser: admin's theme is unchanged and `qs-theme` is not in admin's installed list.
5. Visit `/login` in a private window: Modern Pink, no theme CSS.

## 4. Update, source removal, remove (US3)

1. Bump `version` to 0.2.0, change the accent, push `:0.2.0`, refresh the source. The plain user sees "Update available" with the changelog; their dashboard still shows 0.1.0.
2. Update → 0.2.0 applies. Admin's account still has no copy.
3. Disable source `qs`: the theme leaves the catalog; the plain user's installed copy keeps working and shows "Source removed".
4. Remove the theme → the plain user's dashboard falls back to Modern Pink.

## 5. Safety (edge cases)

1. Install a theme whose CSS hides the sidebar (`nav { display: none }`), select it.
2. Open `/?safe-mode=1` → Modern Pink, no theme CSS, Settings → Theme reachable; switch back to Pink.
3. Stop one source's registry: the catalog still loads within 3 s, the source is flagged with its error (SC-006).

## 6. Standalone panel

On a standalone panel (spec 025 compose profile), repeat step 2 and step 3.3: sources and installs work with no operator (OD-4 A).
