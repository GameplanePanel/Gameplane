# Quickstart: Validating App Localization

**Feature**: `019-localization-language-packs` | **Plan**: [plan.md](./plan.md)

Validation happens on CI (Constitution VI). Locally only compile checks (`go build ./...`, `npx tsc --noEmit`) and `make dev-up` for manual walkthroughs.

## Prerequisites

- `make dev-up` cluster with this branch's images (`make images && make dev-load && make dev-install`), `make web-dev` for the dashboard.
- An admin account (`bootstrap-admin`) and a second browser profile.
- A capture webhook URL for notification checks (the E2E suite's capture sink, or any request bin).

## Scenarios

| # | Proves | Steps | Expected |
|---|---|---|---|
| 1 | US1, FR-002/003/009, SC-005 | Personal settings → Language → `Français`, with the create-server dialog open and a name typed | All visible text French within 1 s, no reload, typed name kept; `<html lang="fr">` |
| 2 | FR-003, US1 AS2 | Reload; sign in from the second profile | French from first paint after sign-in, no English flash |
| 3 | FR-004/005, US1 AS5/6, rule 3 | Sign out; browser language `de-DE`; open `/login` | Instance default shown; picker lists languages; page source / `GET /auth/locale` show only `defaultLanguage` |
| 4 | US7, FR-036 | Admin settings → default language `Français`; repeat 3 with a new profile | Login in French; user from step 1 unaffected by later default change |
| 5 | FR-008, SC-008 | Build with the 0% fixture language selected (`VITE_I18N_FIXTURES=1`) | Everything English, no ids, no blank labels |
| 6 | SC-009, FR-038 | `VITE_I18N_PSEUDO=1 make web-dev`, select `en-XA`, walk every screen (or the screenshot suite) | Accented/expanded text, nothing clipped without hover reveal; `en-XA` absent from release `languages.json` |
| 7 | US5, FR-025..027, SC-010 | `curl -i` a forbidden namespace request; repeat with `-H 'Accept: application/problem+json'`; trigger the same error in the French dashboard | Plain text body byte-identical to `master`; problem body has `detail` = the same English text (no trailing newline) and a separate `messageId` field; dashboard shows French sentence |
| 8 | FR-028, OD-004 | Add a webhook sink with language `fr`, another without; send test to both | First arrives French (`language: "fr"`, French `title`), second in the instance default; existing payload fields unchanged |
| 9 | US6, FR-031..034 | `make modules-push` with `minecraft-java/locales/fr.json`; install; open create-server in French | Module labels French; switch to a language the module lacks → module default text |
| 10 | FR-034 | Push a module whose `fr.json` has a bad placeholder | Module installs; `LocalesValid=False` + Warning event naming the entry; that label falls back |
| 11 | US2, FR-015, SC-003, SC-007 | Follow `docs/translating.md` only: add `i18n/locales/xx/` partially translated, open PR | `i18n-check` passes, job summary shows completeness before/after; defective fixtures each fail with their code |
| 12 | US4, FR-022/023 | Add a JSX literal `"Hello"` in a migrated route; add an unused reference id | ESLint fails pointing at the literal; `i18n-check` reports `unused-id` |
| 13 | US3, FR-020, SC-004/006 | Merge new English text; open Weblate | String visible on Weblate same day; approved translation arrives as a PR from `weblate/translations` passing checks |

## CI checks that must be green

`make lint` (ESLint incl. formatjs rules, `i18n-check`), `make test`, `make test-integration`, the `test/e2e` buckets carrying the new localization tests, Playwright live specs, and the module repo's validation job.

See [contracts/](./contracts/) for exact formats and endpoints and [data-model.md](./data-model.md) for stored fields.
