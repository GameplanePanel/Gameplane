# Feature Specification: Theme marketplace

**Feature Branch**: `spec-043/theme-marketplace`

**Created**: 2026-11-26

**Status**: Ready for planning (OD-1 to OD-3 settled, see `OPEN-DECISIONS.md`)

**Input**: User description: "add a "market place" for them something like the game module but for custom theme"

## Background

Spec 016 (`specs/done_016-user-theme-customization/`) gave each user a theme: a preset (Modern Pink or Legacy Orange), custom colors (accent and surface tone), an optional custom CSS overlay (sanitized: no `@import`, no external `url()`, 32 KB cap), safe mode, and JSON export/import. Spec 016 put "theme sharing galleries" out of scope. Today the only way to share a theme is to pass the exported JSON by hand, and the example CSS overlays on the old branch `feat/example-theme-gallery` (`example/custom-css/*.css`) never reached users.

Game modules already have a marketplace shape: an admin adds a source (a `ModuleSource`: OCI registry, git, HTTP or local), the dashboard's `/modules` page lists the source's catalog, and an install pulls a versioned, signed artifact (`docs/module-authoring.md`). This feature gives themes the same shape: themes are published as versioned packages in sources, browsed in a catalog, installed, updated, and then picked by users in Theme settings like the built-in presets.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Browse the theme catalog and try a theme (Priority: P1)

A signed-in user opens the theme catalog and sees every theme published in the configured sources: name, author, short description, light and dark preview images, version, and which source it comes from. They can search by name and filter by tag (for example "dark", "high contrast", "minimal"). Clicking a theme opens its detail view with the full description, previews, changelog and a "Preview" button that applies the theme to their own session only until they leave the preview.

**Why this priority**: Discovery is the core of a marketplace. Without a browsable catalog nothing else in this feature is reachable.

**Independent Test**: With the official source present, open the catalog, search for a theme by name, open its detail view, preview it, and leave the preview; the user's saved theme is unchanged.

**Acceptance Scenarios**:

1. **Given** the official theme source is configured, **When** a user opens the theme catalog, **Then** every theme in that source is listed with name, author, description, preview, version and source.
2. **Given** the catalog is open, **When** the user types part of a theme name or picks a tag, **Then** only matching themes are shown.
3. **Given** a theme detail view, **When** the user clicks "Preview", **Then** the dashboard renders with that theme for this user only, a banner offers "Keep" and "Exit preview", and exiting restores the user's saved theme exactly.
4. **Given** a source that cannot be reached, **When** the catalog loads, **Then** themes from the other sources still show and the unreachable source is flagged with its last error.

---

### User Story 2 - Install a theme and use it (Priority: P1)

A user installs a theme from the catalog for their own account only (OD-1). Once installed, the theme appears in that user's Theme settings next to Modern Pink and Legacy Orange, and selecting it applies it like a built-in preset, including light and dark variants. Other users are not affected and do not see it in their own installed list. The user's installed themes and choice are saved in their profile and follow them across devices.

**Why this priority**: Installing and selecting is what turns the catalog into a working feature.

**Independent Test**: Install one theme from the catalog, open Theme settings as a user, select it, reload on another browser, and see the same theme.

**Acceptance Scenarios**:

1. **Given** a theme in the catalog, **When** a user installs it, **Then** it is listed as installed for that user with its version and becomes selectable in their Theme settings.
5. **Given** user A installed and selected a theme, **When** user B signs in, **Then** user B's dashboard and installed list are unchanged.
2. **Given** an installed theme selected by a user, **When** they switch between Light, Dark and System, **Then** the theme's light or dark variant is used; a theme that ships only one variant says so in the catalog and is used for both modes.
3. **Given** a user who selected an installed theme, **When** they sign in on another device, **Then** the same theme is applied.
4. **Given** a user with their own custom CSS overlay enabled, **When** they select an installed theme, **Then** their overlay stays on top of it, as with built-in presets (spec 016 FR-007).

---

### User Story 3 - Update and remove installed themes (Priority: P2)

When a source publishes a newer version of an installed theme, the catalog shows that an update is available with its changelog. Updating replaces the version the user installed. Removing a theme takes it out of the user's installed list; if it was selected, the user falls back to Modern Pink. If an admin disables or removes the theme's source, users keep their installed copy (FR-013).

**Why this priority**: Themes go stale or get fixed; without update and removal the installed list only grows and breaks.

**Independent Test**: Install version 1 of a theme, publish version 2 in a test source, see "Update available", update, confirm the user gets version 2; then remove it and confirm the user falls back to Modern Pink.

**Acceptance Scenarios**:

1. **Given** an installed theme at version 1 and version 2 in its source, **When** the catalog refreshes, **Then** the theme is marked "Update available" with the new version's changelog.
2. **Given** an update is available, **When** the user updates the theme, **Then** their dashboard uses version 2, and other users who installed the same theme stay on their own version until they update.
3. **Given** a user's selected theme, **When** they remove it, **Then** they fall back to Modern Pink.
4. **Given** an installed theme, **When** a newer version appears, **Then** it is not applied until the user updates it.

---

### User Story 4 - Add and manage theme sources (Priority: P2)

An admin adds a theme source in the same way as a module source: a registry, git repository, HTTP index or local folder, with optional credentials and signature verification. The official Gameplane theme source is present by default and can be disabled. Each source shows its status, last refresh and number of themes.

**Why this priority**: Community and private themes need sources beyond the official one; the default source makes P1 work out of the box.

**Independent Test**: Add a second source holding one theme, see that theme in the catalog with the new source's name, then disable the source and see its themes leave the catalog (installed copies keep working).

**Acceptance Scenarios**:

1. **Given** a fresh install, **When** an admin opens theme sources, **Then** the official source is listed and enabled.
2. **Given** an admin adds a source with signature verification on, **When** a theme in it is unsigned or signed by another key, **Then** it is shown as "Signature not verified" and cannot be installed.
3. **Given** a source is disabled or deleted, **When** the catalog loads, **Then** its themes no longer appear, and already installed themes from it keep working and are marked "Source removed".

---

### User Story 5 - Publish a theme (Priority: P3)

A theme author exports their theme from Theme settings (spec 016 export) or writes one by hand, adds metadata (name, description, author, tags, license, previews, version), validates it offline, and packages it for a source with tooling that mirrors `gp-module` (validate, preview, package).

**Why this priority**: The catalog needs content beyond the official set, but early on the official themes are enough.

**Independent Test**: Take a spec 016 JSON export, turn it into a theme package with the authoring tool, validate it, push it to a test source, and install it from the catalog.

**Acceptance Scenarios**:

1. **Given** a theme configuration exported from Theme settings, **When** the author runs the package step with metadata, **Then** a valid theme package is produced.
2. **Given** a package whose CSS breaks a sanitization rule, **When** it is validated, **Then** validation fails naming the rule and line.
3. **Given** a package that fails validation, **When** it is pushed to a source anyway, **Then** the catalog lists it as invalid and it cannot be installed.

---

### Edge Cases

- **Malicious or broken theme CSS**: A theme's CSS goes through the same sanitization as user custom CSS (spec 016 FR-013) at validation, at catalog listing and again at install; remote resources are refused. Safe mode (spec 016 FR-009) also suspends installed theme CSS, not only the user's overlay, so a broken theme can always be escaped.
- **Theme that hides critical controls**: A theme cannot be applied on Theme settings and the theme catalog in a way the user cannot undo: safe mode renders those pages with the built-in default theme.
- **Unauthenticated pages**: Login and public share links keep the built-in Modern Pink theme (spec 016 FR-011); installed themes never apply there.
- **Same theme name in two sources**: Themes are identified by source plus name; the catalog shows the source next to the name.
- **Source down during install or update**: The install fails with the source error; nothing already installed changes.
- **Theme no longer in any source**: A user's installed copy keeps working and is marked "Source removed"; it can still be removed but not updated.
- **Contrast**: A theme whose text or accent contrast falls below the minimum used for custom colors in spec 016 is flagged "Low contrast" in the catalog; forced-colors and prefers-contrast still override it.
- **Large packages**: A package over the size limit (CSS 32 KB like user CSS, previews and whole package limits set in planning) is refused at validation and at install.
- **Air-gapped installs**: The official source can be mirrored into a private registry or local folder, like modules.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST let admins manage theme sources (add, edit, enable, disable, remove), supporting the same source kinds, credentials and signature verification options as module sources.
- **FR-002**: System MUST ship with the official Gameplane theme source configured and enabled by default; admins MAY disable it.
- **FR-003**: System MUST show a theme catalog to signed-in users listing every valid theme from enabled sources, with name, author, description, tags, license, version, source, light and dark previews, and a "Low contrast" or "Signature not verified" flag when it applies.
- **FR-004**: The catalog MUST support search by name and filter by tag and by source, and MUST mark themes as Installed, Update available, Invalid or Source removed.
- **FR-005**: Users MUST be able to preview a catalog or installed theme on their own session without changing their saved theme, and leave the preview with one action.
- **FR-006**: Every signed-in user MUST be able to install, update and remove catalog themes for their own account only (OD-1); installs never affect other users. Every install, update and removal MUST be written to the audit log with who, which theme, which version and which source.
- **FR-007**: A user's installed themes MUST appear in that user's Theme settings next to the built-in presets and be selectable with the same behaviour as presets, including Light, Dark and System modes and the custom CSS overlay staying on top.
- **FR-008**: A theme package MUST contain only light and dark color tokens, an optional CSS overlay, metadata and preview images (OD-2); fonts, scripts and other bundled files are refused. Every CSS it carries MUST pass the spec 016 FR-013 sanitization at validation, listing and install.
- **FR-009**: Safe mode MUST suspend installed theme CSS as well as the user's own overlay.
- **FR-010**: Installed themes MUST be versioned per user; a newer version MUST only take effect for a user when that user updates.
- **FR-011**: When a user removes their selected theme, they MUST fall back to Modern Pink.
- **FR-012**: Installed theme CSS MUST be isolated to the user who installed it, like custom CSS (spec 016 FR-008), and MUST never apply on unauthenticated pages, which always use Modern Pink.
- **FR-013**: Themes MUST be identified by source plus theme name, and installed themes MUST keep working when their source is disabled or unreachable.
- **FR-014**: System MUST provide authoring tooling to scaffold, validate offline, preview and package a theme, including turning a spec 016 theme export into a package.
- **FR-015**: Official themes MUST be published as signed OCI artifacts from a new repository `GameplanePanel/themes` (OD-3), read through the same source kinds as modules, and the official theme set MUST seed from the existing example overlays where they pass validation.
- **FR-016**: All catalog and theme-source screens MUST be designed in `design.fig` before any dashboard code (repo rule 1), and all new text MUST go through the language packs (spec 019).

### Key Entities

- **Theme Source**: Where themes come from. Kind (registry, git, HTTP, local), location, credentials, verification settings, enabled flag, status (last refresh, last error, theme count).
- **Theme Package**: One published version of a theme. Name, version, author, description, tags, license, changelog, light and dark previews, light and dark color tokens, an optional sanitized CSS overlay (OD-2); optional signature.
- **Catalog Entry**: A Theme Package as seen through a source, with its validation result and flags (Low contrast, Signature not verified, Invalid).
- **Installed Theme**: A Theme Package installed by one user for their own account. User, source, name, pinned version, installed at; selectable in that user's Theme settings.
- **User Theme Configuration** (from spec 016): gains the option to point at an Installed Theme instead of a built-in preset.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a fresh install, a user can find, preview and start using a theme from the official source in under 2 minutes without reading docs.
- **SC-002**: The official source offers at least 10 valid themes at release.
- **SC-003**: 100% of theme CSS that breaks a spec 016 sanitization rule is refused at validation and at install (checked with a fixed set of bad packages).
- **SC-004**: Safe mode restores a usable dashboard in 100% of cases where an installed theme hides navigation or settings.
- **SC-005**: An author can turn a spec 016 theme export into a published theme in under 10 minutes with the authoring tool.
- **SC-006**: The catalog lists themes from reachable sources within 3 seconds even when another source is unreachable.

## Assumptions

- Themes reuse spec 016's visual model: the semantic color tokens of a preset (light and dark) plus an optional CSS overlay. No new theming engine is needed.
- "Something like the game module" means the same distribution model (sources, catalog, versioned and optionally signed packages, install and update), not the same CRD: whether themes use `ModuleSource` itself, a new CRD, or API-side storage is a planning decision.
- Ratings, reviews, download counts, payments and a public web gallery on the website are out of scope for this spec.
- Per-server or per-page themes stay out of scope (as in spec 016).
- Telemetry, if any, counts only theme names from the official source, following the existing telemetry catalog rules.
- **Plugins (spec 044)**: the plugins spec keeps its own sources and install pages. valgul chose two separate catalogs on that spec's card (2026-11-26), so this spec does not depend on it.
