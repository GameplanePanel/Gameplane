# T045 e2e chunk: tier-up verification (opus)

Verifier input: `notes.md` in this directory (sonnet reviewer, candidates C-e2e-01..03).
Method: read every cited location plus its callers and history (`git log -S`), the CI wiring
(`.github/workflows/ci.yaml:1104-1117`, `Makefile:185-205`), the per-test doc comments, and the
specs that set the classification (`specs/done_015-top-steam-game-modules/engine-matrix-resolved.md`,
`docs/game-coverage.md`). Nothing was executed; no test or lint suite was run (CLAUDE.md rule 8).
None of the three duplicates an already-tracked item or an entry in `audit/findings.md`, `audit/rounds.md` or `OPEN-DECISIONS.md`.

| Candidate | Verdict (kept/rejected) | Severity | Reason |
|---|---|---|---|
| C-e2e-01 | kept | S4 | Confirmed. The "fast set" is defined in four different ways: `fastGameSet` has 6 games but its comment says "these four". `internal/specs.md` gives 3 games in one section and 4 (with factorio) in another. `buckets.sh` and `docs/game-coverage.md` put factorio, tmodloader and beammp in `bot-heavy` with "exceeds runner disk" reasons. `factorio_bot_e2e_test.go` calls itself a heavy-set test. Spec 015 says fastGameSet and the CI buckets are separate axes, but it also classifies tmodloader and beammp as `bot-fast` in both places, and `buckets.sh` goes against that. CI is not affected because it always passes `-run <bot-fast regex>`. The only effect on behaviour is on a maintainer's hand run that sets neither `-run` nor `GAMEPLANE_E2E_GAMES`; no documented command does that, and setting `GAMEPLANE_E2E_GAMES` avoids it. Downgraded from S3 to S4. The reviewer's "must never happen" claim overstates `buckets.sh`: its header restricts CI, not local runs. |
| C-e2e-02 | kept | S4 | Confirmed, with the counts corrected. The depth table has **16** rows (the reviewer said 15), so "All 16" matches the table but not the repo. `test/e2e/internal/` has **29** per-game probe directories (the reviewer said 28), and `modules/` ships 30 games (`nuclear-option` has no probe). Line 370 ("runs all 16 games") is also out of date. This is internal reference-doc drift only. `docs/game-coverage.md` is current and is the table CI checks through `joincoverage.sh`. |
| C-e2e-03 | kept | S4 | Confirmed that `api-roles` actually spends 7 admin logins, but the reviewer's explanation of the cause is slightly off. `83aefbe9` said "spends 4", which was correct for the bucket before RoleMappings moved in; after the move it spent 5. `df463735` then treated 4 as the current total ("bringing api-roles to 5 (up from the previous 4)"), and `63697d5b` carried the error forward ("to 6"; the real figure is 7). The same wrong number also appears at `api_auth_e2e_test.go:967`. The skill's "api-roles ≤ ~5" ceiling is already exceeded and contradicts `buckets.sh`'s own ~7-per-job ceiling. Nothing fails today: `APIClient` retries 429 responses for about 90 s, and the two serial tests log in before the 5 parallel ones, so refill absorbs the extra login. This is bookkeeping and documentation drift, so S4 rather than S3. |

### C-e2e-01

**Location (corrected):** `test/e2e/gamebot_helpers_e2e_test.go:24-32`; `test/e2e/buckets.sh:187-195` (bot-fast), `:214`, `:226`, `:230`, `:240` (heavy reasons for factorio, tmodloader and beammp); `test/e2e/internal/specs.md:293-298` compared with `:345-354`; `test/e2e/factorio_bot_e2e_test.go:26-29`; `specs/done_015-top-steam-game-modules/engine-matrix-resolved.md:30-31,34,48-50,60`.

Repro (by reading the files):
1. `test/e2e/gamebot_helpers_e2e_test.go:24-32`: the comment says "These four ... boot quickly (minutes at most) and fit within a single kind node". The slice lists 6 games: minecraft-java, terraria, factorio, garrys-mod, tmodloader and beammp.
2. `test/e2e/gamebot_helpers_e2e_test.go:52-55`: when `GAMEPLANE_E2E_GAMES` is empty, `parseGameScope()` returns `fastGameSet`. `skipUnlessGameInScope` (`:79-91`) then lets all 6 games run once `GAMEPLANE_E2E_GAME_BOT=1` is set.
3. `test/e2e/buckets.sh:187-195`: `bot-fast`, which is the set CI runs at `ci.yaml:1112-1117`, has only the Minecraft, Terraria and Garry's Mod bot tests. `:214`, `:226`, `:230` and `:240` put the factorio, tmodloader and beammp tests in `bot_heavy`, and three of those four reasons say "exceeds runner disk" or "Multi-GB ... download". `docs/game-coverage.md:12,29,30` says `bot-heavy` for the same three.
4. `test/e2e/factorio_bot_e2e_test.go:28-29`: "This is a heavy-set test (opt-in via GAMEPLANE_E2E_GAME_BOT=1 and GAMEPLANE_E2E_GAMES=all)". That is false, because factorio is in `fastGameSet`.
5. `test/e2e/internal/specs.md:293-298` lists a 3-game fast set. `:345-354` ("Why the fast set is small") lists 4 games including factorio.
6. `specs/done_015-top-steam-game-modules/engine-matrix-resolved.md:30-31,34,60` classifies tmodloader, beammp and factorio as `bot-fast` and says they are "bucketed in `bucket_bot_fast`". `buckets.sh` does the opposite.
7. Behavioural consequence: `GAMEPLANE_E2E_GAME_BOT=1 make test-e2e-keep` (`Makefile:197-199` has no `-run`) with `GAMEPLANE_E2E_GAMES` unset boots all 6 games one after another (none use `t.Parallel()`). Three of them are games the repo says exceed a CI runner's disk.

**Expected:** there is one "fast set" definition, or `fastGameSet` is explicitly documented as a different axis from `bot-fast`. The code comment, `internal/specs.md`, `factorio_bot_e2e_test.go`, `buckets.sh`, `docs/game-coverage.md` and spec 015 agree on which games are fast.

**Actual:** there are four conflicting definitions: `fastGameSet` has 6 games, `bot-fast`/`game-coverage.md` have 3, `internal/specs.md:347` has 4, and spec 015 has 6. The fastGameSet comment gives the wrong count, and it claims the games boot quickly, which `buckets.sh` contradicts for 3 of them.

### C-e2e-02

**Location (corrected):** `test/e2e/internal/specs.md:305-326` (the depth table is at `:307-324` with 16 rows, and the count claim is at `:326`) and `:370`.

Repro (by reading the files):
1. `test/e2e/internal/specs.md:307-324`: the per-game depth table has 16 rows (minecraft-java through satisfactory).
2. `test/e2e/internal/specs.md:326` says "All 16 game modules now have implemented protocol clients". `:370` says the hand-run command "runs all 16 games".
3. `ls -d test/e2e/internal/*/ | grep -vE 'fakeoidc|probe|protocol' | wc -l` prints 29. The 13 directories missing from the table are arma-reforger, ark-survival-evolved, beammp, euro-truck-simulator-2, farming-simulator-25, fivem, hell-let-loose, left-4-dead-2, mount-and-blade-2-bannerlord, squad, team-fortress-2, the-isle and tmodloader.
4. `ls -d modules/*/ | wc -l` prints 30 (the 29 above plus nuclear-option).
5. `internal/specs.md` never mentions the extra tests `fivem_persistence_e2e_test.go`, `squad_rcon_e2e_test.go`, `teamfortress2_rcon_e2e_test.go`, `tmodloader_mods_e2e_test.go`, `dayz_workshop_e2e_test.go` or `arksurvival_cluster_persistence_e2e_test.go`. All of them are bucketed in `buckets.sh:241-274`.

**Expected:** the reference doc's table and counts match the 29 probe packages, or the doc points to `docs/game-coverage.md` as the current table.

**Actual:** the doc says 16 games and has 16 rows. The repo has 29 probe packages and 30 modules.

### C-e2e-03

**Location (corrected):** `test/e2e/buckets.sh:127-138`; `test/e2e/api_auth_e2e_test.go:965-967`; `.claude/skills/e2e-test-authoring/SKILL.md:17,27`; also `test/e2e/api_owner_collab_e2e_test.go:29-30`, whose comment reads "3 non-admin logins (owner-user, collab-user, plus the admin setup login)" and so contradicts itself.

Repro (by reading the files and history):
1. `test/e2e/env.go:494-560`: every `envInstance.APIClient(...)` call makes one `POST /auth/login` and does not cache the session.
2. Count the `APIClient(t, adminUsername, adminPassword)` calls in the 7 tests of `bucket_api_roles` (`buckets.sh:139-148`). There is one each in `api_roles_e2e_test.go:23`, `:124` and `:150`, `api_owner_collab_e2e_test.go:43`, `api_auth_e2e_test.go:415` and `:971`, and `api_theme_preferences_e2e_test.go:144`. That makes **7** e2e-admin logins, and no test logs in as admin twice.
3. `git show 83aefbe9 -- test/e2e/buckets.sh`: before this commit the bucket held CustomRole, BuiltinRole, PerNamespaceBinding and OwnerCollaboratorAccess, which is 4 logins. The commit adds RoleMappings and says "a bucket that spends 4". That was the spend before the move; after it the bucket spent 5.
4. `git show df463735:test/e2e/buckets.sh` adds OIDCHelmOverride and says "bringing api-roles to 5 (up from the previous 4)". The real total at that point is 6. `api_auth_e2e_test.go:967` repeats "bring the api-roles bucket to 5".
5. `63697d5b` adds ThemePreferences and says "bringing api-roles to 6" (`buckets.sh:136`). The real total is 7.
6. `.claude/skills/e2e-test-authoring/SKILL.md:17` and `:27` say api-roles should stay at "≤ ~5" admin logins. The bucket is already at 7, and `buckets.sh:29-30` and CLAUDE.md both give "~7 admin logins" as the per-job ceiling.

**Expected:** the running tally in `buckets.sh`, the test doc comments and the skill ceiling all match the real e2e-admin login count (7), so the next author knows the bucket is at the ~7 ceiling.

**Actual:** `buckets.sh` says 6, `api_auth_e2e_test.go` says 5 and the skill says the ceiling is ~5. Each number is lower than the real count, so an author would think the bucket has room when it has none. It is not failing today, because the 429 retry loop in `env.go:520-548` (7 attempts, about 90 s) absorbs the overflow.
