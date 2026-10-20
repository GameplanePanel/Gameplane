# Translating Gameplane

Gameplane's dashboard and its server messages are translated into other
languages. Each language is a **language pack**: a folder under
`i18n/locales/` with two JSON files. You do not need to write code to add
one. This guide covers the folder layout, the message rules, how to check
your work, and when a language shows in the picker.

Translations are welcome as a partial pack. A pack merges even when it is
not finished. It stays hidden from users until enough of it is translated.

## Folder layout

```
i18n/locales/
  en/          English reference. Generated. Do not hand-edit app.json.
    app.json
    meta.json
  fr/          your language, tag "fr"
    app.json
    meta.json
```

To start a language, copy the whole `i18n/locales/en/` folder to
`i18n/locales/<tag>/`. Use a BCP 47 tag such as `fr`, `pt-BR`, or `sr-Latn`.
The folder name must equal the `tag` in `meta.json`.

### app.json

`app.json` maps each message id to an entry:

```json
{
  "condition.addressassignment.assigned": {
    "defaultMessage": "{assignedFrom, select, pool {Address {address} assigned from pool ''{pool}''.} other {Address {address} assigned.}}",
    "description": "AddressAssignment condition once an address is assigned. assignedFrom is pool or default."
  }
}
```

- Replace `defaultMessage` with your translation. Keep `description` as it
  is. The description is for translators and is not shown in the app.
- Keys are sorted by id. Keep them sorted when you add or move entries.
- Use 2-space indentation, UTF-8, and end the file with a newline.
- Do not invent ids. An id that English does not have is an error. You may
  leave out any id you have not translated yet.
- `sourceHash` is written by the checker. It records which English text
  your translation was made from. Leave it alone when you edit by hand.

### meta.json

`meta.json` describes the language:

```json
{
  "tag": "fr",
  "nativeName": "Français",
  "englishName": "French",
  "direction": "ltr",
  "credits": [{ "name": "Ada Example", "url": "https://example.org" }]
}
```

- `tag` must equal the folder name.
- `nativeName` is the name of the language in that language. `englishName`
  is the name in English.
- `direction` is `ltr` or `rtl`. A language with `rtl` merges but is not
  listed in the picker yet.
- `credits` is optional. Add yourself here.
- Unknown keys are rejected.

## Message rules

Messages use ICU MessageFormat. Follow these rules:

- Keep every placeholder that English has. Use the same names, such as
  `{name}`. The order can change.
- Do not add a placeholder that English does not have.
- Date and number formats keep their form, such as `{d, date, short}` and
  `{n, number}`.
- A literal apostrophe next to a placeholder is written twice, as in
  `''{pool}''`. Copy the English quoting.
- Plurals follow the rules of your language. Use the `other` category. It
  is required. For example:

  ```
  {n, plural, one {# serveur} other {# serveurs}}
  ```

- `{x, select, ...}` also works, as in the example above. Keep the `other`
  option.
- You may use `<b>…</b>` and `<link>…</link>` tags. Do not use any other
  tag, HTML entity, `javascript:` link, or `<script`. The checker rejects
  them.

## Glossary and style

- Use a neutral, plain tone. Keep sentences short.
- Do not translate these terms. Keep them as written in English:
  Gameplane, GameServer, GameTemplate, Backup, BackupSchedule, Restore,
  Cluster, Module, ModuleSource, NetworkCapture, Kubernetes, LoadBalancer.
  These are resource kinds and product names.
- Do not invent fixed translations for product words. Ask in the pull
  request if a term is unclear.
- Keep button labels short. Match the length of the English text when you
  can, because the layout is tight.

## Check your pack

Run the checker from the `i18n/` folder to see findings on your pack:

```sh
cd i18n
go run ./cmd/i18n-check locales
```

The checker exits with one of these codes:

| Exit | Meaning |
|---|---|
| 0 | No errors found |
| 1 | At least one finding |
| 2 | Usage error, or a required file could not be read |

Findings use these codes:

| Code | Severity | What it means |
|---|---|---|
| `malformed-pack` | error | `app.json` is not valid JSON or has the wrong shape |
| `invalid-meta` | error | `meta.json` is missing a field, has an unknown key, or the tag does not match the folder |
| `icu-syntax` | error | The message does not parse as ICU MessageFormat |
| `missing-placeholder` | error | A placeholder from English is missing |
| `extra-placeholder` | error | A placeholder that English does not have |
| `markup` | error | A tag or entity that is not allowed |
| `unknown-id` | error | The id is not in English. Remove the entry |
| `needs-review` | info | English changed after your translation was made. The entry still shows |

Partial packs are fine. A missing id is not a finding. English shows for it.

The checker has more options:

- `--base <dir>` compares completeness with an older copy of the packs.
- `--summary <file>` writes a markdown summary.
- `--stamp` adds `sourceHash` to translated entries that lack one.
- `--manifest <file>` writes the language list as JSON.

You can run the checker on your machine to see findings early. It is not a
test suite. CI runs it on every pull request. The job summary shows your
pack's completeness before and after your change.

## When a language shows in the picker

A language is listed in the picker only when both of these are true:

- At least **80%** of the English messages have a usable translation.
  Entries marked `needs-review` count as usable.
- The direction is `ltr`.

A pack under 80% still merges. It stays hidden until more of it is done.
Maintainers can change the 80% threshold.

The dashboard reads the list of languages from a generated manifest. A pack
under 80% will not appear in the picker. To rebuild the manifest and the
compiled packs, run these commands in `web/` (they need Go installed), then
start the dev server with `make web-dev`:

```sh
cd web
npm run i18n:manifest
npm run i18n:compile
```

The build keeps only the entries the checker accepts. Any other entry
shows English.

## Review and merge

1. Open a pull request with your pack. Use a `feat:` or `docs:` title, for
   example `feat: add French language pack`.
2. CI runs the checker and posts the completeness in the job summary.
3. A maintainer or a designated language reviewer checks the wording. The
   pull request needs one human approval before it merges.
4. A pull request that changes only `i18n/locales/**` gets `area: shared`
   from the auto-label workflow. If its title has no type prefix, it also
   gets `type: docs`. Add a `docs:` prefix to be sure.

You can also translate in the browser on Hosted Weblate:
<https://hosted.weblate.org/projects/gameplane/>. Only translations that a
human reviewer approves are merged. They arrive as a pull request from the
`weblate/translations` branch and pass the same checks.

## Adding text to the dashboard (developers)

Every user-visible string in the dashboard goes through `intl`. Do not
put English text straight into JSX. Get the helper from `useOptionalIntl`:

```tsx
import { useOptionalIntl } from "@/lib/i18n/format";

const intl = useOptionalIntl();

intl.formatMessage(
  {
    id: "web.servers.deleted",
    defaultMessage: "Server {name} was deleted.",
    description: "Toast after a server is deleted. {name} is the server name.",
  },
  { name: server.name },
);
```

- `id`, `defaultMessage` and `description` must be string literals. The
  extractor reads them at build time.
- Dashboard ids use `web.<path>.<slug>`. Audit labels use `audit.*`.
  The `server.*`, `notify.*` and `condition.*` ids come from the API and
  the operator. Do not invent them in the dashboard.
- The description tells a translator what the text is and where it
  appears. Explain every `{placeholder}`.
- When you migrate a string, keep the rendered English the same.

### Extract and commit

- After you change text, run `cd web && npm run i18n:extract`.
- Commit `i18n/locales/en/app.json` in the same commit as the code.

### Migrate literal text

- `npm run i18n:migrate -- [--dry-run] <paths>` wraps literal JSX text.
  It also wraps `aria-label`, `title`, `placeholder` and `alt` values.
  Run it with `--dry-run` first.
- `node scripts/i18n-migrate.ts --check src` reports what the lint rules
  would flag.

### Lint and CI

- ESLint with `eslint-plugin-formatjs` checks all of `web/src`, except
  tests and `src/test`.
- CI fails on extraction drift. It also fails on server and condition
  drift. It fails on reference ids that no code uses (`i18n-check
  --used-ids`).

### Text from other sources

Some text does not come from a literal in the dashboard. Use a helper
for it:

- API errors: `serverMessageText` and `errorTextIntl`.
- Operator conditions: `conditionMessageText` in
  `src/lib/i18n/conditionMessage.ts`. English keeps the operator's message.
- Module text: `useModuleText` and `useModuleTexts` in
  `src/lib/i18n/moduleText.ts`. English uses the template text and
  fetches nothing.

## Related docs

- [Contributing](contributing.md): the general pull request process.
- [`i18n/specs.md`](../i18n/specs.md): the design of the language packs.
