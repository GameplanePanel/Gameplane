# Open Decisions: App Localization & Community Language Packs

The first four decisions were settled by valgul on 2026-10-10 and are recorded in spec.md (Clarifications, Session 2026-10-10). The US6 module-translation rulings (OD-005 to OD-014) and the design placement rulings (OD-015 to OD-017) were settled on 2026-10-12 (Clarifications, Session 2026-10-12).

## OD-001: Which pilot language ships at launch — RESOLVED: French (valgul, 2026-10-10)

At launch, Gameplane must ship English plus one pilot language that is 100% complete for dashboard text, server messages, and the three reference modules (Minecraft Java, Terraria, Valheim). The user confirmed the 2-language strategy on 2026-09-23 and chose French on 2026-10-10.

Options and constraints:

- The pilot language must have a native-speaking reviewer available to approve translations and community contributions.
- Ideally a language with high open-source community activity to enable ongoing contribution.
- Candidates must be evaluated for translation site availability (OD-002) and completeness by launch.

Affects: FR-037, SC-002

## OD-002: Which community translation site — RESOLVED: Hosted Weblate (valgul, 2026-10-10)

The system must support two contribution paths: file-based pull requests and a web-based community translation site where translators work in the browser. The chosen service is Hosted Weblate (libre plan); setup is described in research.md R10.

The site must:

- Be free for open-source projects, owned or managed by the maintainers.
- Support human review and approval workflows; only approved translations ship.
- Send approved work back into the repository automatically as pull requests subject to automated checks (FR-015).
- Show per-text context (where the text appears, placeholder meaning, length hints) to translators.
- Handle languages with multiple plural forms and placeholder reordering.
- Cover both the app repository and module repositories for consistent translation across the project.
- Automatically surface new or changed English text within 24 hours without manual steps.

Affects: FR-020, US3

## OD-003: Minimum completeness threshold for language listing — RESOLVED: 80% (valgul, 2026-10-10)

Languages below a certain completeness threshold must not be offered to end users, though they remain available to translators for continued work. The threshold is 80% completeness of the app text source (see data-model.md, Language).

Options and constraints:

- Threshold must be specified as a completeness percentage.
- Threshold is adjustable by maintainers after launch.
- Incomplete packs below threshold fall back to English for all their text; users are not offered the incomplete language.
- Completeness is computed and reported automatically on pull requests and the translation site.

Affects: FR-017

## OD-004: Notification destination language — RESOLVED: per-destination (valgul, 2026-10-10)

Notifications are sent to multiple destinations (Discord, email, webhooks, and others), each of which could have its own language preference, or all could use a single instance-wide language.

Options:

- Per-destination language setting: each notification destination can be configured in a specific language (default: the instance default language). This allows a team to receive Discord alerts in one language and email notifications in another.
- Instance-wide only: all notification destinations use the instance default language, and no per-destination language configuration is offered.

Decision: per-destination language setting, defaulting to the instance default language.

Affects: FR-028

## US6 module-translation rulings

Ten decisions on the module translations (user story 6), all settled 2026-10-12 by valgul.

## OD-005: Namespace of the translations ConfigMap - RESOLVED: the operator namespace (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. The ConfigMap `<template>-locales` lives in the operator namespace, the namespace Gameplane runs in (`--module-namespace`). The GameTemplate is cluster-scoped, so the owner reference points at a cluster-scoped owner. The API reads the same namespace.

Affects: FR-031, T087

## OD-006: Where module id derivation lives - RESOLVED: `i18n/modlocale` (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. One shared package derives module ids for `gp-module`, `i18n-check --module` and the operator. The `i18n` module may depend on `gopkg.in/yaml.v3`.

Affects: FR-031, T084

## OD-007: Module id rule - RESOLVED: upper case and `~` allowed (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. Module ids get their own documented rule that allows upper case and `~` with reversible `~0`/`~1` escaping. Dashboard and server ids keep the strict lower-case regex.

Settled 2026-10-12 by valgul: gp-module validate reports such names as error locale-id-invalid (scan of all 29 modules: 0 of 808 ids affected).

Affects: FR-031, T084

## OD-008: Meaning of `i18n-check --module` - RESOLVED: en.json must equal the derived ids (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. `en.json` must match the ids derived from the YAML (also when the YAML derives nothing); every translation is validated against the derived set.

Affects: FR-031, T085, T090

## OD-009: LocalesValid reporting - RESOLVED: full reporting (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. `LocalesValid=True` with reason `Valid` when fine; `False` with `LocalesTooLarge` before `InvalidEntries`; a new reason `WriteFailed` when the ConfigMap write fails.

Settled 2026-10-12 by valgul: WriteFailed ranks first.

Affects: FR-034, T087

## OD-010: Oversized locale layers - RESOLVED: recorded by tag (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. An oversized locale file is never dropped silently at pull time. A hard memory cap stays on reading; the language tag is recorded as oversized, reaches the reconciler and shows as `LocalesTooLarge` naming the language in the condition message and the Warning event.

Affects: FR-034, T086, T087

## OD-011: Module sources that carry translations - RESOLVED: all (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. Directory, git, HTTP, upload and `gp-module package --output` tarball sources carry `locales/*.json` as well as OCI layers, with the same file names and validation. Scout note: the upload ConfigMap key is `locales.<tag>.json`.

Affects: FR-031, T086, T087

## OD-012: Locales endpoint edge cases - RESOLVED: `200 {}` and ICU strings (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. `GET /modules/{name}/locales/{tag}` with a malformed tag returns `200 {}`; values are the validated ICU message strings.

Affects: FR-032, T088

## OD-013: sourceHash on module translations - RESOLVED: yes (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. Module translations carry `sourceHash` like app packs; a changed English text shows a needs-review notice. Authoring: `gp-module i18n stamp` writes the hash and `gp-module validate` warns (`locale-needs-review`). i18n-check --module --stamp stays a usage error; gp-module i18n stamp is the only module stamping tool.

Affects: FR-031, T085

## OD-014: Warning event frequency - RESOLVED: only when the problem list changes (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. Warning events fire only when the problem list changes, tracked by a fingerprint annotation `gameplane.local/locales-problems` on the GameTemplate, not on every re-pull.

Affects: FR-034, T087

## Design placement rulings

Three decisions on the design tasks (T043, T072), all settled 2026-10-12 by valgul.

## OD-015: Where a sink's language is edited - RESOLVED: the Add sink form and each sink row (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. The design had no destination form, only the sink rows and an "Add sink" button. The Add sink form gets a Language select that defaults to "Instance default", and every existing sink row gets its own Language select, so the language can be changed without deleting the sink and entering its credentials again.

Affects: FR-028, T072, T073

## OD-016: Position of the Language card in personal settings - RESOLVED: first card (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. The Language card is the first card of the Theme & Appearance page, above Preset theme. The page title and subtitle stay unchanged.

Affects: FR-001, T043, T044

## OD-017: Position of the compact picker on the login and share pages - RESOLVED: top-right corner of the card (valgul, 2026-10-12)

Settled 2026-10-12 by valgul. The compact picker sits inside the card, in its top-right corner, on every state of the login and share pages. It shows only the native names of the languages.

Affects: FR-005, T043, T045

## OD-018: How the en-XA screenshot run drives the specs - RESOLVED: switch at capture (valgul, 2026-10-16)

Settled 2026-10-16 by valgul. The screenshot specs find elements by their English names, so they keep driving the UI in English. In the en-XA run the capture helper switches the dashboard to en-XA just before each screenshot, through a test hook that only builds listing the pseudo-language have, and switches back afterwards. The specs themselves do not change. Screenshots a spec takes with page.screenshot directly stay English.

Affects: FR-010, FR-038, T100

## OD-019: Clipping found by the en-XA run - RESOLVED: fail the run (valgul, 2026-10-16)

Settled 2026-10-16 by valgul. Text that is cut off without a way to show it in full on hover or focus fails the en-XA run through soft assertions, so every screen is still captured and the run lists each clipped element. A fix is designed in design.pen first, then built.

Affects: FR-010, T100

## OD-020: How cut-off data text is revealed - RESOLVED: title attribute (valgul, 2026-10-16)

Settled 2026-10-16 by valgul. The first en-XA run found only data text that is already truncated in English and in design.pen: server names and addresses on the mobile Servers cards, the endpoint hosts on the Overview tab, module summaries on the module cards and file names on the Files tab. Each truncated element gets a title attribute with its full text, so hovering shows it. Nothing changes visually, so design.pen is not updated.

Affects: FR-010, T100
