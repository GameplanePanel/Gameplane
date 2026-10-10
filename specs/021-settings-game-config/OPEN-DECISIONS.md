# Open Decisions: 021-settings-game-config

Unsettled items. Do not treat any of the below as decided until a maintainer rules on it here.

## Settled (user, 2026-10-05)
Q1 passwords redacted by the API (PR #565) and write-only in the UI; Q2 always restart; Q3 no API-side schema validation; Q4 separate section after Version; Q5 orphan keys with Remove; Q6 "Written to file" note, selects for bool; Q7 gate on `access.canWrite`.

## OD-1: Move password values out of the CR into a Secret — **Settled (valgul, 2026-10-09)**
Passwords move out of `GameServer.spec.config` into a Secret via a dedicated endpoint (like `:tunnel-credentials`). Deferred to a NEW spec; not built here.

## OD-2: Clearing a stored optional password — **Settled (valgul, 2026-10-09)**
Clearing a stored optional password removes it. Built in this spec (Phase 7, T020+). The API already clears an optional password when the key is absent or `""`; no API change.

## OD-3: Show the template file path for `target: file` fields — **Settled (valgul, 2026-10-09)**
Keep "Written to file"; no schema path. Closed.

## OD-4: Per-field "Reset to default" — **Settled (valgul, 2026-10-09)**
No per-field Reset button. Closed.
