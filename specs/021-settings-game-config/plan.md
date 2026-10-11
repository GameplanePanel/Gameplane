# Implementation Plan: Edit game configuration from server Settings

**Branch**: `feat/settings-game-config` (stacked on PR #565 `fix/redact-config-passwords`) | **Date**: 2026-10-05 | **Spec**: `specs/021-settings-game-config/spec.md`

## Summary
Extract the wizard's inline config field loop into `web/src/components/server/ConfigFields.tsx`, extend `validateConfig` with int/bool/length checks, add `web/src/routes/tabs/settings/GameConfig.tsx` registered in `Settings.tsx` after Version, and add a catch-all 404 route inside the app layout. No API, operator or CRD change.

## Technical Context
- TypeScript strict, React 19, HeroUI v3, TanStack Router/Query, Vitest + Testing Library + MSW.
- Save path unchanged: `PUT /servers/{name}` through `mergeDraftOntoLatest` (adopts `draft.spec`).
- Password round trip relies on PR #565: marker in, marker out, stored value kept.

## Constitution Check
| Rule | Status |
|---|---|
| Design-first (rule 1) | Frames `OC804 QFEg9 h5clan ffmEd wxINm e9FnC Ne5TA EV8mp` exported before code |
| Operator authority (rule 10) | No API validation; client validation is advisory |
| Login privacy (rule 3) | 404 sits behind the layout auth guard, shows no data |
| Fix, don't silence (rule 4) | No eslint-disable; no `any` |
| Verification (rule 8) | CI only |

## Project Structure
```text
web/src/components/server/ConfigFields.tsx        NEW shared renderer
web/src/routes/tabs/settings/GameConfig.tsx       NEW section
web/src/routes/tabs/Settings.tsx                  SECTIONS + filter + footer note
web/src/routes/CreateServer.tsx                   uses ConfigFields
web/src/lib/validation.ts                         int/bool/length checks, marker const
web/src/types.ts                                  configSchema min/max/minLength/maxLength
web/src/routes/NotFound.tsx                       NEW 404 page
web/src/router/tree.tsx                           splat route under app layout
web/src/components/AppLayout.tsx                  fixed 404 breadcrumb
web/src/test/{screenshotData,handlers}.ts         screenshot fixtures (single-get configSchema)
web/e2e/screenshots/slice-gameconfig.spec.ts      captures OC804 QFEg9 h5clan ffmEd Ne5TA
web/specs.md, docs/module-authoring.md            docs
```

## Complexity Tracking
| Aspect | Why | Simpler alternative rejected |
|---|---|---|
| `storedRef` of password names seen as marker, plus a `removing` set | Emptying a typed password must return to "unchanged"; clearing a stored optional password is an explicit Remove action (OD-2) whose "will be removed" state lives in `removing` (key absent from the draft, reset on section remount) | Clear on empty: an accidental backspace would delete a stored optional password |
| configSchema only on the screenshot single-get | Keeps wizard captures unchanged | Adding it to the template list would add a "Template configuration" block to `vUqMl` |
