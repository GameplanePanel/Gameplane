# Open decisions: spec 043 theme marketplace

Only a decision valgul answers is written here as settled. OD-1 to OD-3 were raised on 2026-11-26 and asked as decision cards. OD-1 to OD-3 are settled. Planning raised OD-4 on 2026-11-26; it is open and asked as a decision card.

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

## OD-4: Where do theme sources live, and who fetches themes? (OPEN)

Raised by planning (research.md R1). Themes are per panel user (OD-1), while module sources are cluster resources reconciled by the operator, and a standalone panel (spec 025) has no operator.

- A. In the panel database, managed through the API; the API indexes and pulls themes. The OCI client and cosign verification move from `operator/internal/{oci,verify}` into a new shared Go module `bundlefetch/` that the operator's module sources and the API's theme sources both use. (Recommended: works on standalone and multi-cluster panels, one implementation of signature checks.)
- B. A cluster-scoped `ThemeSource` CRD reconciled by the operator, reusing `operator/internal/modsrc` as is. Smallest change, but standalone panels get no themes, and with several clusters themes depend on one cluster.
- C. In the panel database, with the API using its own small OCI and cosign client. No operator change, but two copies of signature handling to keep in step.
