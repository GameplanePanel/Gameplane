# Quickstart: validating spec 024

Local work is limited to compile/build checks (CLAUDE.md rule 8; website: `npm run build` / `npm run check` only). Everything below marked **CI** is verified by the pushed PR's CI run, not locally.

## Prerequisites

- Main repo branch with `FEATURES.md`, refreshed docs and the sync check.
- Website PR merged to `main`, and the main repo `website` pointer bumped to it.

## Scenarios

1. **Catalog is complete and correct (SC-001)**: pick 10 random rows of `FEATURES.md`; for each, confirm the matching record in `feature-evidence.md` names real evidence (bucketed e2e test or `none`, default source) and that the label follows [data-model.md](./data-model.md) validation; follow the Docs link and confirm it documents the capability.
2. **Roadmap matches the specs (SC-002)**: for every folder in `specs/` without `done_`, find its line in the "In progress" section of `docs/roadmap.md`; compare with that spec's `tasks.md` on `master` on the status date.
3. **No stale counts (SC-003)**: `grep -rn "30 ready\|30 game\|30 templates" README.md docs website/src` from the GP root (website submodule initialised) finds nothing; every module count equals the number of game directories in the module repo; every registry count equals the registries in the code. Each fix is listed in `stale-facts.md`.
4. **Repo and website agree (SC-004)** **CI**: `make check-features-sync` passes in the `docs and repository checks` job; the website roadmap page and `docs/roadmap.md` list the same items with the same statuses (manual comparison).
5. **Sync check catches drift (SC-005)** **CI**: `make test-features-sync` passes, meaning every `fail/` fixture in [contracts/features-sync-check.md](./contracts/features-sync-check.md) is rejected with its expected message and every `pass/` fixture is accepted.
6. **Existing doc gates still pass (FR-011)** **CI**: `make check-links` and `make check-doc-versions` pass with `FEATURES.md` added to their audited lists; no rule in either script was relaxed (review the diff).
7. **Website builds** (local allowed): `npm run build` in the website repo succeeds and the Features page appears in the Reference → Releases & Roadmap sidebar group. Website CI (`npm run lint`, `check`, `build`) is green on its PR.
8. **README points to the catalog (US3)**: README Features section is a short summary with a link to `FEATURES.md`; the comparison table's competitor columns are unchanged (`git diff` shows only Gameplane-column edits).
