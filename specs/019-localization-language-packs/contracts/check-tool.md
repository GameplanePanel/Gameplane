# Contract: `i18n-check` CI tool

**Feature**: `019-localization-language-packs` | **Binding modules**: `i18n/cmd/i18n-check`, `.github/workflows/ci.yaml`, module repo CI, `gp-module validate` | **Status**: Binding

## Invocation

```text
go run ./i18n/cmd/i18n-check [flags] <locales-dir>
  --reference en            reference tag (default en)
  --base <dir>              base-branch copy for before/after completeness
  --extracted <file>        formatjs extract output to compare with the reference (drift)
  --registry <file>         API message registry JSON (from api/cmd/messages-export) for the two server flags
  --sync-server             write api/internal/messages registry into the reference
  --check-server            fail if the registry and the reference differ
  --used-ids <file>         ids referenced by code (from extract + Go registry) → unused report
  --manifest <out.json>     write languages.json
  --stamp                   write sourceHash for translated entries
  --module <template.yaml>  module mode (see below)
  --summary <file>          GitHub job summary markdown
```

Module mode reads `<template.yaml>` and the `module.yaml` beside it, derives the reference with `i18n/modlocale` (the same ids as `gp-module i18n extract` and the operator, following the module id rule in [module-locales.md](./module-locales.md) §1), requires `<locales-dir>/en.json` to equal the derived set (`extract-drift` for a missing or changed id, `unknown-id` for an extra id, also when the template derives nothing), and validates every other `<tag>.json` against the derived set. File names must be valid language tags (`malformed-pack` otherwise). Without a `module.yaml`, `module.*` ids are not derived, so an `en.json` containing them reports `unknown-id`. The app-only flags, including `--stamp`, remain invalid with `--module`; stamp module translations with `gp-module i18n stamp` (see [module-locales.md](./module-locales.md) §1).

Exit 0 = pass, 1 = findings, 2 = usage/IO error.

## Findings (each names file, id and a plain-language problem — FR-015)

Findings that apply to a whole file rather than one entry use a fixed sentinel id: `<pack>` for `malformed-pack`, `<meta>` for `invalid-meta`, `<reference>` for `extract-drift` and `<registry>` for `server-drift` when no single id is at fault. Every finding therefore has the same shape: `{code, severity, file, id, message}`.

| Code | Severity | Meaning |
|---|---|---|
| `malformed-pack` | error | invalid JSON / wrong shape |
| `invalid-meta` | error | `meta.json` missing field, bad tag, tag ≠ directory, unknown key |
| `icu-syntax` | error | message does not parse |
| `missing-placeholder` / `extra-placeholder` | error | argument set differs from English |
| `markup` | error | tag/entity/script outside `<b>`, `<link>` |
| `unknown-id` | error | translation for an id not in the reference (stale) |
| `missing-description` | error | reference entry without translator note |
| `extract-drift` | error | committed English reference ≠ extracted |
| `server-drift` | error | Go registry ≠ `server.*` entries |
| `unused-id` | error | reference id used nowhere (FR-023) |
| `needs-review` | info | sourceHash stale, placeholders still match |

Summary table: tag, completeness (base → head), listed yes/no.

Seeded fixtures `i18n/testdata/defective/<code>/` MUST each produce exactly their finding (SC-007). Pack-level fixtures hold `en/app.json`, `xx/app.json`, `xx/meta.json`. Fixtures for `extract-drift`, `server-drift` and `unused-id` also hold the auxiliary input (`extracted.json`, `registry.json` or `used-ids.txt`) and an `args` file with the flags the test passes (`--extracted`, `--check-server`, `--used-ids`).
