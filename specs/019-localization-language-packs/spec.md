# Feature Specification: App Localization & Community Language Packs

**Feature Branch**: `019-localization-language-packs`

**Created**: 2026-09-23

**Status**: Draft

**Input**: User description: "Before doing the specs confirm what i ask does not exist yet. i want a modern and easy way to add localised text for the app with lang choice and easy contributable lang \"pack\""

## Context

Gameplane has no localization today. The dashboard's text is written directly in English across roughly 200 screen and component files; the page always declares itself English; dates are always formatted in US English.

Server-side messages (errors, validation failures, notifications to Discord/email/webhooks) are English sentences.

Game modules (about 30 first-party modules) describe themselves in English only: game names, descriptions, configuration field labels and help text.

Users have no language setting anywhere; there is no language picker in the design source.

No prior specification, issue, or pull request covers app translation. Spec 012 (docs refresh) keeps documentation English-only; that decision stands and is not changed by this feature.

Spec 016 (user theme customization, open PR #415) introduces per-user personal preferences stored with the user's account and a personal settings screen. The language preference is expected to live alongside those preferences.

This feature introduces: (1) language choice for every user, (2) translatable dashboard, server messages and game-module text, (3) community language packs contributed either as files through pull requests or through a web-based community translation site, (4) English plus one pilot language at launch.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Choose my display language (Priority: P1)

A signed-in user opens their personal settings, sees the available languages each listed in its own native name (e.g. "Français", "Deutsch") with how complete it is, picks one, and the whole dashboard switches immediately, without a page reload. The choice follows them to other browsers and devices. Dates, times, numbers, durations, relative times ("3 minutes ago") and plurals follow the chosen language's conventions.

**Why this priority**: Without choosing a language nothing else in the feature is visible to users.

**Independent Test**: Switch between English and the pilot language, confirm every visible text and format changes, reload and sign in on a second browser, confirm the choice persists.

**Acceptance Scenarios**:

1. **Given** dashboard in English, **When** user selects pilot language, **Then** all dashboard text (navigation, pages, dialogs, validation messages, empty states, toasts) shows in the pilot language without reload.
2. **Given** user has selected a language, **When** they reload or sign in on another device, **Then** same language applied from first paint after sign-in, no flash of another language.
3. **Given** user has an unsaved form open (e.g. create-server dialog), **When** they switch language, **Then** entered values are kept.
4. **Given** a text has no translation in the selected language, **When** displayed, **Then** that text alone shows in English; no raw identifiers or blank labels ever appear.
5. **Given** first visit with no saved preference, **When** dashboard loads, **Then** the first of the browser's preferred languages that is available is used, otherwise the instance default language, otherwise English.
6. **Given** login page or public share-link page, **When** accessed, **Then** a language picker is offered; the choice is remembered in that browser only and exposes no internal information (no cluster names, versions, counts, or account-existence hints).
7. **Given** a user has selected the pilot language, **When** a screen reader reads any dashboard page, **Then** the page announces itself in the pilot language so text is pronounced correctly, and switching back to English updates this immediately.

---

### User Story 2 - Contribute a language pack as files (Priority: P1)

A community translator who knows nothing about Gameplane's code copies the English reference pack, translates it, and submits it through a pull request. Automated checks validate the pack and report its completeness; after merge it ships in the next release and appears in the language list.

**Why this priority**: "Easy contributable lang pack" is the core ask; without it only maintainers could add languages.

**Independent Test**: Follow only the contributor guide to add a new language with a partial translation, open a pull request, observe the checks pass and the completeness report, build a release candidate and see the language offered.

**Acceptance Scenarios**:

1. **Given** contributor adds a new language pack, **When** no application code is touched, **Then** after merge the language appears in the picker (if above the listing threshold) with its completeness.
2. **Given** pack contains a translation missing a placeholder (e.g. the server name) or adding an unknown one, **When** checked, **Then** automated check fails naming the entry and the problem in plain language.
3. **Given** pack contains markup or script in a translation, **When** checked, **Then** check rejects it; translations are always shown as plain text.
4. **Given** pack contains entries for identifiers that do not exist in the English reference, **When** checked, **Then** reported as stale.
5. **Given** contributor updates an existing pack, **When** checked, **Then** completeness report shows before/after.
6. **Given** contributor wants to see their work in context before submitting, **When** they check the guide, **Then** it explains how to preview a pack in a running dashboard.

---

### User Story 3 - Translate in the browser on the community translation site (Priority: P2)

A non-developer signs up on the project's community translation site, sees untranslated and outdated texts for a language with context (where the text appears, what the placeholders mean, length hints), submits translations; designated language reviewers approve them; approved work flows back into the repository automatically as a reviewable pull request that goes through the same automated checks as US2.

**Why this priority**: Widens the contributor pool beyond people comfortable with pull requests; US2 alone already allows contribution.

**Independent Test**: Submit a translation on the site, approve it as reviewer, observe a pull request containing it and passing checks.

**Acceptance Scenarios**:

1. **Given** new English text merged into the project, **When** 24 hours pass, **Then** it appears as translatable on the site without manual steps.
2. **Given** translator submits a translation, **When** it is not approved, **Then** it does not ship.
3. **Given** machine-suggested translations (if the site offers them), **When** they are suggested, **Then** they are never shipped without human approval.
4. **Given** translations are approved, **When** they are shipped, **Then** they arrive as a pull request, never as a direct change to the protected main branch.

---

### User Story 4 - Developers add translatable text easily (Priority: P2)

A developer adding a new screen writes each text once, in English, with a stable identifier and a short note for translators. Nothing else is needed to make it translatable. Automated checks catch user-visible text that bypasses the translation system and reference entries that are no longer used.

**Why this priority**: Keeps the dashboard fully translatable as it keeps growing; otherwise coverage erodes release after release.

**Independent Test**: Add a hard-coded English label to a dashboard screen and confirm the automated check flags it; add it properly and confirm it appears in every language's missing list and on the translation site.

**Acceptance Scenarios**:

1. **Given** developer adds user-visible dashboard text without going through the translation system, **When** checked, **Then** automated check fails pointing at it.
2. **Given** developer changes the meaning of an existing English text, **When** existing translations are reviewed, **Then** they are flagged "needs review" for translators; they keep being shown unless their placeholders no longer match the new English text, in which case English is shown instead.
3. **Given** an English reference entry is no longer used anywhere, **When** checked, **Then** reported as unused.

---

### User Story 5 - Server messages and notifications in my language (Priority: P2)

Errors and messages that come from the Gameplane server (validation failures, permission denied, conflicts, rate limiting, lifecycle failures, game server status reasons produced by Gameplane) appear in the user's language. Notifications (Discord, email, webhooks and other destinations) are sent in the language configured for each destination.

**Why this priority**: Without it users see a mix of their language and English whenever something goes wrong — exactly when clarity matters most.

**Independent Test**: Trigger a validation error and a permission error with the pilot language selected and see them translated; configure a notification destination in the pilot language and receive a translated test notification.

**Acceptance Scenarios**:

1. **Given** server rejects a request, **When** dashboard displays the message, **Then** it shows in the user's language, with the specific details (names, limits) filled in.
2. **Given** server sends a message the user's language does not translate yet, **When** displayed, **Then** the English text is shown.
3. **Given** an existing script or integration that reads the server's English message text, **When** called, **Then** it keeps working unchanged (backward compatible).
4. **Given** notification destination set to the pilot language, **When** test notification sent, **Then** it arrives in the pilot language; destination with no language set, **When** notification sent, **Then** instance default language used.
5. **Given** audit log entries are stored, **When** displayed, **Then** they are displayed in the viewer's language; entries recorded before this feature still display (in English).
6. **Given** sign-in failure messages, **When** displayed, **Then** they remain generic ("invalid credentials" equivalent) in every language; translation never reveals whether an account exists.

---

### User Story 6 - Game modules in my language (Priority: P3)

Module authors can include translations of everything their module shows to users — game name, summary, description, configuration field labels, help text, option labels, validation hints — inside the module package. The dashboard shows module text in the user's language when the module provides it, otherwise the module's own default text.

**Why this priority**: Module text is a large share of what users read when creating and configuring servers, but the dashboard and server messages deliver value first.

**Independent Test**: Install a reference module that includes pilot-language translations, create a server with the pilot language selected, confirm module labels and help text are translated; switch to a language the module lacks and confirm the module's default text appears.

**Acceptance Scenarios**:

1. **Given** module includes a pilot-language translation, **When** create-server and server-settings screens displayed, **Then** its labels and help text shown in the pilot language.
2. **Given** module lacks the user's language, **When** displayed, **Then** module default text shown, no errors.
3. **Given** module has translations, **When** module signature verified, **Then** they are covered by the module's signature: they cannot be altered without re-signing the module.
4. **Given** module has an invalid translation entry, **When** module installs, **Then** the module still installs and works; the invalid entry is ignored (default text shown) and reported to the administrator.
5. **Given** module translations exist, **When** contributed, **Then** they can be contributed the same two ways as app translations (pull request to the module repository, or the community translation site) and pass the same automated checks.

---

### User Story 7 - Administrator sets the instance default language (Priority: P3)

An administrator chooses the instance's default language, used for users with no saved preference whose browser languages are unavailable, for unauthenticated pages in the same situation, and for notification destinations without their own language.

**Why this priority**: Sensible for non-English teams, but US1's browser detection already covers most users.

**Independent Test**: Set the default to the pilot language, open the login page from a browser whose preferred language is unavailable, confirm pilot language.

**Acceptance Scenarios**:

1. **Given** default set to pilot language, **When** new user whose browser prefers an unavailable language accesses dashboard, **Then** pilot language shown.
2. **Given** user with a saved preference exists, **When** instance default changes, **Then** user is unaffected by the instance default.

---

### Edge Cases

- **Incomplete packs**: Each missing text falls back to English individually; completeness is shown next to the language name; packs below the listing threshold (see assumptions) are not offered to end users but remain available to translators.
- **Saved language later removed from a release**: Fall back per the precedence order; keep the saved preference so it applies again if the language returns.
- **Regional variants**: Separate packs allowed (e.g. Brazilian vs European Portuguese); resolution falls back regional → base language → English. A browser asking for a regional variant that does not exist gets the base language.
- **Text expansion**: Translated text can be much longer than English (up to 40%+); layouts must not overlap or clip essential information; shortened text must reveal the full text on hover/focus.
- **Plurals and grammatical forms**: Languages with several plural forms (e.g. Polish, Russian, Arabic) must be expressible; placeholders can be reordered by translators.
- **Right-to-left languages**: Mirroring the layout is out of scope for v1; language metadata records text direction so it can be added later; right-to-left packs are not offered to end users until mirroring ships.
- **Never translated**: User-entered data (server names, descriptions), game console output, game server logs, file contents, messages produced by Kubernetes or by third-party software, which are shown verbatim.
- **Markup/injection**: Translations are always shown as plain text; any emphasis or links in a translated text only through designated safe placeholders.
- **Public share links**: Shown in the viewer's language (browser/choice), never the sharer's.
- **Outdated translations**: Flagged for translators; shown to users only while their placeholders still match the English text.
- **Notification destination language no longer available**: Instance default, then English.
- **Pseudo-language for testing**: A test-only language that lengthens and accents every text is available to maintainers to spot untranslated or clipped text; it is never offered to end users.

## Requirements *(mandatory)*

### Functional Requirements

**Language selection & display**

- **FR-001**: Users MUST be able to choose their display language from the list of available languages, each shown in its own native name with its completeness.
- **FR-002**: Changing the language MUST update all translatable text immediately, without page reload and without losing unsaved input.
- **FR-003**: A signed-in user's language choice MUST be saved with their account and applied on every browser and device (alongside the personal preferences introduced by spec 016).
- **FR-004**: The language shown MUST be resolved in this order: (1) signed-in user's saved choice, (2) choice made in this browser, (3) the browser's preferred languages that are available, (4) instance default language, (5) English.
- **FR-005**: Login and public share-link pages MUST offer a language choice remembered in that browser only, and MUST NOT expose internal information through it.
- **FR-006**: Dates, times, numbers, durations, relative times and lists MUST be formatted according to the selected language's conventions; the underlying values MUST NOT change.
- **FR-007**: The system MUST support languages with multiple plural forms and let translators reorder placeholders.
- **FR-008**: Any text missing in the selected language MUST fall back to English for that text only; raw identifiers or empty labels MUST never be shown.
- **FR-009**: The page MUST declare the active language so screen readers and browsers pronounce, hyphenate and spell-check correctly.
- **FR-010**: Dashboard layouts MUST remain usable when text is up to 40% longer than English.
- **FR-011**: User-entered data, game console output, game logs, file contents and third-party or Kubernetes messages MUST NOT be translated.

**Language packs & contribution**

- **FR-012**: Translations MUST be organised as self-contained packs: one pack per language per text source (the Gameplane app, and each game module), in a human-readable, diff-friendly plain-text form, with metadata: language, native name, English name, text direction, contributor credits.
- **FR-013**: English MUST be the single reference; every reference entry MUST have a stable identifier, the English text, and a note giving translators context (where it appears, placeholder meaning, length limits where relevant).
- **FR-014**: Adding or updating a language MUST NOT require any change to application code.
- **FR-015**: Every contribution MUST be checked automatically for: well-formed pack; each translation using exactly the placeholders of its English entry; no markup or script beyond designated safe placeholders; no entries for unknown identifiers; valid metadata. Failures MUST name the entry and the problem in plain language.
- **FR-016**: Completeness per language and per text source MUST be computed automatically and shown to contributors (on pull requests and on the translation site) and to users (in the language list).
- **FR-017**: Languages below the listing threshold (80% complete, adjustable by maintainers; OD-003) MUST NOT be offered to end users.
- **FR-018**: When English reference text changes, existing translations MUST be flagged as needing review; a flagged translation whose placeholders no longer match MUST NOT be shown.
- **FR-019**: A contributor guide MUST explain both contribution paths, how to preview a pack in a running dashboard, and translation style guidance (tone, glossary of Gameplane terms that stay untranslated or have fixed translations).
- **FR-020**: The project MUST offer a community web translation site (Hosted Weblate; OD-002) where translators work in the browser with per-text context; only translations approved by a human reviewer MUST ship; approved work MUST reach the repository as pull requests subject to FR-015; new or changed English text MUST become available there within 24 hours without manual steps.
- **FR-021**: Language packs MUST ship as part of each Gameplane release; installing or replacing packs on a running installation is out of scope.

**Developer experience**

- **FR-022**: All user-visible dashboard text MUST go through the translation system; an automated check MUST fail on new user-visible dashboard text that bypasses it.
- **FR-023**: Automated checks MUST report English reference entries that are no longer used and references to identifiers that do not exist.
- **FR-024**: Adding new text MUST require only writing it once in English with an identifier and translator note; it MUST then appear automatically in every language's missing list and on the translation site.

**Server messages & notifications**

- **FR-025**: Every user-facing message produced by the Gameplane server (errors, validation failures, confirmations, and game server status reasons produced by Gameplane components) MUST carry a stable message identifier and its parameters alongside the existing English text.
- **FR-026**: The existing English message text MUST remain in every response so current integrations keep working.
- **FR-027**: The dashboard MUST show server messages in the user's language using the identifier, falling back to the English text when no translation exists.
- **FR-028**: Each notification destination MUST have its own language setting (per-destination, not instance-wide only; OD-004) (default: the instance default language), and notifications MUST be rendered in it.
- **FR-029**: Audit records MUST be stored independent of language and displayed in the viewer's language; records created before this feature MUST remain readable.
- **FR-030**: Sign-in and other unauthenticated messages MUST stay generic in every language and never reveal whether an account exists.

**Game modules**

- **FR-031**: Module packages MUST be able to include translations for all user-visible module text (game name, summary, description, configuration field labels, help text, option labels, validation hints), using the same pack structure as the app.
- **FR-032**: The dashboard MUST show module text in the user's language when the module provides it and the module's default text otherwise.
- **FR-033**: Module translations MUST be covered by the module's existing signature verification.
- **FR-034**: Invalid module translation entries MUST NOT prevent the module from installing or working; they MUST be ignored with fallback and reported to administrators.
- **FR-035**: Module translations MUST be contributable through both paths (module repository pull request and the translation site) and pass the same automated checks.

**Administration**

- **FR-036**: Administrators MUST be able to set the instance default language from the available languages; the initial default is English.

**Launch content**

- **FR-037**: At launch Gameplane MUST ship English plus one pilot language, French (OD-001, decided 2026-10-10), that is 100% complete for dashboard text and server messages, plus complete pilot-language translations for the reference modules Minecraft (Java), Terraria and Valheim.
- **FR-038**: A pseudo-language for testing (lengthened, accented text) MUST be available to maintainers and never offered to end users.

### Key Entities

- **Language**: A language (optionally regional variant) with native name, English name, text direction, and whether it is offered to end users.
- **Text Source**: A body of translatable text: the Gameplane app (dashboard + server messages), or one game module.
- **Reference Entry**: One English text in a text source: stable identifier, English text, translator note, placeholders, plural forms.
- **Language Pack**: Translations of one text source into one language: metadata, credits, and per-entry status (translated / needs review / missing). Completeness is derived, not stored.
- **Language Preference**: A signed-in user's saved language (with their personal preferences), or a browser-local choice for unauthenticated pages.
- **Instance Language Settings**: The instance default language.
- **Notification Destination Language**: Per-destination language setting.
- **Localizable Server Message**: Stable identifier + parameters + English fallback text.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: At launch 100% of user-visible dashboard text, including login and share pages, is translatable; the automated check reports zero bypassing texts.
- **SC-002**: At launch the pilot language is 100% complete for dashboard text and server messages and for the three reference modules.
- **SC-003**: A first-time contributor using only the contributor guide can add a new language pack that passes all automated checks in under 30 minutes, excluding the time spent translating.
- **SC-004**: A new translator on the community translation site can submit their first translation within 5 minutes of signing up, without installing anything.
- **SC-005**: Switching language updates the whole visible dashboard in under 1 second, with no reload and no lost input.
- **SC-006**: New English text becomes translatable on the translation site within 24 hours of being merged, with no manual step.
- **SC-007**: The automated checks catch 100% of a seeded set of defective packs (missing placeholder, extra placeholder, markup injection, unknown identifier, malformed pack, invalid metadata).
- **SC-008**: With a 0%-complete test language selected, zero raw identifiers or blank labels appear anywhere; everything falls back to English.
- **SC-009**: With the pseudo-language selected, every dashboard screen remains usable with no overlapping or clipped essential text.
- **SC-010**: 100% of existing server responses still contain their English message text; existing integrations need no change.
- **SC-011**: Adding a language requires zero changes to application code (verified by the pilot language and by one test contribution).

## Clarifications

### Session 2026-09-23

- **Q1 (Scope of translatable text)**: Dashboard + server messages + game modules. All dashboard text, all user-facing server messages and notifications, and module-provided text are in scope for v1.
  - **Decision**: All dashboard text, all user-facing server messages and notifications, and module-provided text are in scope for v1.

- **Q2 (What a contributable "pack" is)**: Files in the repository contributed through pull requests, plus a community web translation site whose approved work flows back as pull requests. Packs ship with releases; runtime-installable packs are out of scope.
  - **Decision**: Files in the repository contributed through pull requests, plus a community web translation site whose approved work flows back as pull requests. Packs ship with releases; runtime-installable packs are out of scope.

- **Q3 (Languages at launch)**: English plus one pilot language, fully translated. The specific pilot language is not yet chosen (OPEN-DECISIONS.md OD-001).
  - **Decision**: English plus one pilot language, fully translated. The specific pilot language is not yet chosen (OPEN-DECISIONS.md OD-001).

### Session 2026-10-10

Decided by valgul; recorded as settled in OPEN-DECISIONS.md.

- **Q4 (Pilot language, OD-001)**: Which language ships with English at launch?
  - **Decision**: French.
- **Q5 (Community translation site, OD-002)**: Which service hosts browser translation?
  - **Decision**: Hosted Weblate.
- **Q6 (Listing threshold, OD-003)**: How complete must a language be before users can pick it?
  - **Decision**: 80% complete (adjustable by maintainers).
- **Q7 (Notification language, OD-004)**: Per destination or instance-wide only?
  - **Decision**: Per notification destination, defaulting to the instance default language.

### Session 2026-10-12

Decided by valgul; recorded as settled in OPEN-DECISIONS.md (OD-005 to OD-017).

- **Q8 (Module translations, US6)**: Where do they live and how are they reported?
  - **Decision**: ConfigMap in the operator namespace; ids derived in `i18n/modlocale`; module id rule allows upper case and `~0`/`~1` escapes; `i18n-check --module` requires `en.json` to equal the derived ids; `LocalesValid` reports Valid, InvalidEntries, LocalesTooLarge and WriteFailed, with oversized files named by language; every module source (OCI, directory, git, HTTP, upload, tarball) carries `locales/*.json`; the locales endpoint returns `200 {}` for a malformed tag with ICU message strings; module translations carry `sourceHash`; Warning events fire only when the problem list changes.
- **Q9 (Design placement, OD-015 to OD-017)**: Where do the language controls sit?
  - **Decision**: A sink's language is edited in the Add sink form and on each sink row; the Language card is the first card of the personal settings page; the compact picker sits in the top-right corner of the card on the login and share pages.

Existence check 2026-09-23: Confirmed no localization exists in the dashboard, server, modules, website, design source, specifications, issues or pull requests.

## Assumptions

- English is the reference language and the final fallback.
- Language packs are bundled in releases; administrators cannot install packs on a running system (not selected in Q2; possible future feature).
- The community translation site is a hosted service free for open-source projects, owned by the maintainers; the service is Hosted Weblate (OD-002, decided 2026-10-10).
- Listing threshold for end users: 80% completeness, adjustable by maintainers (OD-003, decided 2026-10-10).
- Per-user language storage builds on the personal preferences from spec 016 (PR #415, open at time of writing). If 016 has not merged when this is implemented, this feature provides equivalent storage for the language preference.
- Right-to-left layout mirroring is out of scope for v1 (edge cases).
- Out of scope: the public website and documentation (spec 012 keeps them English), logs and command-line output of Gameplane components, the read-only MCP server's output, Helm chart text, Kubernetes resource messages, and game console/log output.
- Module translations are optional for module authors; only the three reference modules must be translated into the pilot language at launch; other first-party modules can be translated by the community afterwards.
- The language picker and translated screens follow the project's design-first rule (designed in the design source before being built) and its end-to-end testing rule; these are delivery constraints, not scope changes.
