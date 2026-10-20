# Open Decisions: 025-standalone-panel

Do not treat an item as decided until a maintainer rules on it here.

## OD-1: One API process with `Recreate` — **Settled (valgul, 2026-10-14)**

Keep one API process and the `Recreate` strategy; no rolling updates or multiple replicas. The audit hash chain and user-management safeguards use process-local locks. Multi-replica HA would be a separate future feature.

## OD-2: Telemetry marker for standalone installs — **Settled (valgul, 2026-10-14)**

A follow-up, not part of this feature. No task here.

## OD-3: Enforce `--skip-crds` for the panel-only Helm profile — **Settled (valgul, PR #630 review)**

Documented requirement only in this feature; valgul agreed to open a follow-up issue. No task here.

## OD-4: Legacy panel-key formats — **Settled (valgul, 2026-10-14): keep**

PR #630 merged with two legacy paths still supported: headerless ciphertext (decrypted and re-encrypted with the `gpk1.` header by rotation) and the legacy default key path `/data/panel.key` (on the database volume). This was still an open review thread on PR #630 when it merged.

- **Keep** (as merged): existing early standalone installs keep working; no change.
- **Drop in a follow-up**: remove headerless decryption and make the key path required, after a release note.

Ruling: keep both legacy paths as merged. No follow-up.

## OD-5: Archive this folder as `done_025-standalone-panel` — **Settled (valgul, 2026-10-14): keep `025-`**

Every task is ticked and PR #630 is merged, so Constitution IV and CLAUDE.md rule 16 call for `git mv specs/025-standalone-panel specs/done_025-standalone-panel` in its own `docs:` commit. Constitution I also says a feature is incomplete without a `test/e2e/` test, and standalone mode has none (spec Known gaps).

- **Keep `025-`**: leave it in flight until a standalone E2E test exists, which would be new work under a new decision.
- **Archive now**: rename to `done_025-`; the missing E2E test stays recorded as a known gap.

Ruling: the folder keeps the bare `025-` name until a standalone E2E test exists. Adding that test is new work and needs its own decision.

## Moot

- Splitting the share-links token fix (`834c3ca`) out of PR #630: merged inside PR #630, so there is nothing left to split.
