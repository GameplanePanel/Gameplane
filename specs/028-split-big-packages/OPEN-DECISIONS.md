# Open Decisions: 028-split-big-packages

All decisions for this spec are settled; none is open. A new question gets its own OD entry here and is not treated as decided until a maintainer rules on it.

## Settled elsewhere (binding, not reopened here)

- Spec 027 OD-3 (Go layout), OD-4 (web `pages/` layout, 500-line screens), OD-5 (this spec adds the folder-shape check to `make lint`), OD-2 (`specs.md` stays per module). valgul, 2026-10-20.

## Settled (valgul, 2026-10-20 to 2026-10-22)

- **OD-1 (FR-013): split order.** Web first, then api, then operator; then the small services; tunnel and sentinel wait for their audit fixes (#721–#723, #729) to merge. Chosen on a card.
- **OD-2 (US2, US3, FR-002): sub-package boundaries.** By resource: `api/internal/handlers/` and `operator/internal/controller/` each become sub-packages per resource group, with helpers shared by several groups in one internal helper package per module, one group per PR. (Premise corrected before asking: a by-layer split does not exist in today's code and would need logic rewrites, which this spec forbids.) Chosen on a card.
- **OD-3 (FR-018, SC-001): line ceiling.** 500 lines for every production source file (Go and web), the same as web screens (027 OD-4). Generated files and CRD `*_types.go` are exempt. Today 34 Go and 17 web production files are over 500. Chosen on a card.
- **OD-4 (FR-007, FR-019): enforcement.** `make lint` enforces the 500-line ceiling for production files only. Test files are exempt and are not split. Chosen on a card.
- **OD-5 (FR-020): web types.** No merge; keep both files. The generated `web/src/api/types.ts` stays untouched. The hand-written `web/src/types.ts` (1,132 lines) is over the 500-line ceiling (OD-3), so it is split by domain into files that re-export through `web/src/types.ts`, keeping every `@/types` import working unchanged. Chosen on a card.
- **OD-6 (FR-005): mechanical test edits.** valgul signs off, for the whole spec, on exactly these edits inside moved test files: the `package` clause, import lines, qualified names of identifiers that moved package (`foo` → `pkg.Foo`), relative import and `vi.mock` path strings, and one new `TestMain`/suite file per new sub-package that copies today's envtest setup unchanged. Each PR lists every touched test file, and a script proves no other kind of line changed. Nothing else in a test may change (no assertion, test name, table entry, timeout, skip or fixture). Chosen on a card.
- **OD-7 (US4): what stays in `web/src/routes/`.** One `routes/<Screen>.ts` per page holding that page's route definition (path, guard, component imported from `pages/`), moved out of `router/tree.tsx`, which then only assembles the tree. Chosen on a card (2026-10-21).
- **OD-8 (US4): server-detail tabs.** Keep a `tabs` folder under the screen: `pages/ServerDetail/tabs/<Tab>.tsx` and `pages/ServerDetail/tabs/settings/<Section>.tsx`, same names as today. A tab over 500 lines is split per 027 OD-4 into a folder beside it (for example `pages/ServerDetail/tabs/Mods/<Part>.tsx`). Chosen on a card (2026-10-21).
- **OD-9 (FR-005): relative CRD paths in a copied envtest setup.** Allowed: a copied `suite_envtest_test.go` may add exactly one `".."` per extra folder level to its CRD directory paths (today `operator/internal/controller/suite_envtest_test.go:45,48`, `api/internal/handlers/suite_envtest_test.go:55`); the test-edit check allows only that and the PR lists it. Chosen on a card (2026-10-21).
- **OD-10 (constitution I): new e2e test.** None. Nothing user-facing changes; every move PR runs the full existing e2e suite with `ci: full-run` plus the test-name parity check. Recorded as a constitution I exemption for a behaviour-preserving refactor. Chosen on a card (2026-10-21).

- **OD-11 (FR-018, contracts/layout-check.md rule 1): dev scripts under `web/scripts/`.** No exemption: `web/scripts/i18n-migrate.ts` (1,195 lines, spec 019 codemod) is split like any other file, in place, into files of 500 lines or fewer (T061). Chosen on a card (2026-10-22).

No open decisions remain.
