# Open decisions: spec 035 CurseForge modpacks

OD-1 to OD-6 were answered by valgul on 2026-11-11 (decision cards). OD-7 was raised by planning and answered by valgul on 2026-11-16. OD-8 was raised by PR review and answered by valgul on 2026-11-17. OD-9 was raised by T001 and answered by valgul on 2026-11-19. OD-10 was raised during implementation and answered by valgul on 2026-11-19. OD-11 was raised during implementation and answered by valgul on 2026-11-22.

## OD-1: How do game servers get a CurseForge key? (SETTLED: C, valgul 2026-11-11)

**Ruling: option C** (valgul, 2026-11-11). Where an option below says "Recommended", that was the recommendation before the ruling, not the ruling.

The itzg image needs a CurseForge key to install packs. Java 17+ image tags ship a
built-in key; `java8` tags (Minecraft ≤1.16.5) do not.

- A. Use the image's built-in key only. The admin's key stays in the API. Packs for
  Minecraft ≤1.16.5 are not offered on CurseForge.
- B. Always give game servers the admin's key: the operator copies it into a Secret owned
  by each server that uses a CurseForge pack, mounts it as a file (`CF_API_KEY_FILE`), and
  keeps the copy in sync when the admin rotates or removes the key.
- C. Built-in key by default; an admin setting "Share the CurseForge key with game
  servers" switches to B. (Recommended: no key leaves the API unless the admin chooses
  it, and old versions still work once the admin opts in.)

## OD-2: Which pack version does a server run? (SETTLED: B, valgul 2026-11-11)

**Ruling: option B** (valgul, 2026-11-11). Where an option below says "Recommended", that was the recommendation before the ruling, not the ruling.

- A. Always the latest; every restart may upgrade (how Modrinth works today).
- B. Version picker at install, defaulting to the newest file; the server stays pinned to
  the chosen file until the operator picks another. (Recommended: a restart never
  upgrades a pack by surprise.)
- C. Version picker with "Always latest" as an option and as the default.

## OD-3: Does version pinning also apply to Modrinth packs? (SETTLED: A, valgul 2026-11-11)

**Ruling: option A** (valgul, 2026-11-11). Where an option below says "Recommended", that was the recommendation before the ruling, not the ruling.

- A. Yes, same picker and pin rule for both registries (`MODRINTH_VERSION`).
  (Recommended: one behaviour in one tab.)
- B. No, Modrinth stays latest-only; pinning is CurseForge only.

## OD-4: Show the active pack and allow removing it? (SETTLED: A, valgul 2026-11-11)

**Ruling: option A** (valgul, 2026-11-11). Where an option below says "Recommended", that was the recommendation before the ruling, not the ruling.

- A. Yes: an "Active modpack" card at the top of the Modpacks tab (name, registry,
  version, Change version, Remove). Remove clears all pack settings and restarts; world
  data stays. Works for both registries. (Recommended: today nothing shows a pack is
  active and the only way out is editing env.)
- B. No, install only (as today).

## OD-5: What happens with packs that contain mods blocked from automatic download? (SETTLED: A, valgul 2026-11-11)

**Ruling: option A** (valgul, 2026-11-11). Where an option below says "Recommended", that was the recommendation before the ruling, not the ruling.

- A. The server's data volume gets a `downloads/` folder that the image reads. When an
  install stops on blocked files, the Modpacks tab lists them with links to their
  download pages and tells the operator to upload them to `downloads/` in the Files tab,
  then restart. (Recommended: makes those packs usable with tools the panel already has.)
- B. Only show the list of blocked files; no upload path. Such packs stay unusable.
- C. Out of scope: the server log is the only signal.

## OD-6: Support FTB packs (FTB App, `TYPE=FTBA`) in this spec? (SETTLED: B, valgul 2026-11-11)

**Ruling: option B** (valgul, 2026-11-11). Where an option below says "Recommended", that was the recommendation before the ruling, not the ruling.

- A. No, separate spec later. Most large FTB packs are also published on CurseForge, and
  FTB needs a new registry engine for its own API. (Recommended.)
- B. Yes, add an FTB registry to the Modpacks tab in this spec.

## OD-7: Installing a different pack over an active one: empty the mods folder first? (SETTLED: B, valgul 2026-11-16)

**Ruling: option B** (valgul, 2026-11-16). Where an option below says "Recommended", that was the recommendation before the ruling, not the ruling.

Pack mods land in the same mods folder. The image cleans up files from older versions of
the same pack, but not files from a different pack, so leftovers from the old pack would
load alongside the new one.

- A. Yes: switching to another pack (other registry or other project) empties the mods
  folder first, like Remove; changing the version of the same pack does not.
  (Recommended: a clean switch, consistent with your Remove answer.)
- B. No: only Remove empties the folder; the operator cleans up by hand when switching.

## OD-8: How do adopted Modrinth servers get their pin without a restart gap? (SETTLED: B, valgul 2026-11-17)

**Ruling: option B** (valgul, 2026-11-17). Where an option below says "Recommended", that was the recommendation before the ruling, not the ruling.

Servers that already run an unpinned Modrinth pack are pinned on panel upgrade, but the pin
env only reaches the pod at its next start. If the game container crashes and restarts in
place before then, it boots with the old latest-only env and may upgrade the pack (only if
a newer pack version came out in that window).

- A. Accept the window: no restart at upgrade; the pin applies at the next restart,
  stop/start or sleep/wake.
- B. Apply the pin at the next quiet moment: the operator restarts each adopted server once
  when it has no players online (or right away if it is stopped or asleep).
  (Recommended: closes the window within hours without kicking players.)
- C. Restart every adopted server once right after the upgrade.

## OD-9: How does the shared CurseForge key reach java8 servers? (SETTLED: A, valgul 2026-11-19)

**Ruling: option A** (valgul, 2026-11-19). Where an option below says "Recommended", that was the recommendation before the ruling, not the ruling.

Raised by T001 on 2026-11-19.

T001 found that the `java8` / `java8-multiarch` images pin mc-image-helper `1.51.7-java8`, which has
`--api-key` (`CF_API_KEY`) but no `--api-key-file`; `CF_API_KEY_FILE` arrived in a later helper.
Those images are the only ones that need the shared key (Java 17+ images ship one), so the file
mount in OD-1 C / research R2 would never be read.

- A. Keep OD-1 C's server-owned Secret copy, but pass it as `CF_API_KEY` with `valueFrom.secretKeyRef`
  instead of a file. The value stays out of the pod spec and `kubectl describe` (only the Secret
  name shows); the module field becomes `apiKeyEnv: CF_API_KEY`. A rotated key applies at the next
  restart, which is also when the image reads it today. (Recommended: works on every image tag.)
- B. Keep the file mount and also set `LOAD_ENV_FROM_FILE` to a mounted env file holding
  `CF_API_KEY=…`; relies on the image's start scripts supporting that variable in the pinned digest.
- C. Drop key sharing: CurseForge packs only on Java 17+ images (Minecraft 1.17+).

## OD-10: When does a server pick up a CurseForge key-sharing toggle? (SETTLED: A, valgul 2026-11-19)

**Ruling: option A** (valgul, 2026-11-19). Where an option below says "Recommended", that was the recommendation before the ruling, not the ruling.

Raised during implementation on 2026-11-19.

Turning key sharing on or off adds or removes the key env var (OD-9 A) in the pod template, which rolls the
pod of every server with an applied CurseForge pack. Doing that for all servers at once would kick every player.

- A. Wait until 0 players: each server picks up the change at its next quiet moment, like OD-8 B (right away if
  the server is stopped or asleep, and never while the player count is unknown). Key rotation needs no restart and
  is unaffected. (Recommended: no player is kicked and no manual step is needed.)
- B. Restart right away: every affected server rolls within about a minute of the toggle.
- C. Next manual restart: no operator restart; the change applies at the server's next stop/start, sleep/wake or crash restart.

## OD-11: Should the backup taken before a modpack change survive deleting the server? (SETTLED: A, valgul 2026-11-22)

**Ruling: option A** (valgul, 2026-11-22).

Raised during implementation on 2026-11-22.

The operator creates a Backup before a modpack change (`<gs>-modpack-<generation>`). Owning it by the GameServer
would garbage-collect it, and the restic snapshot, when the server is deleted; scheduled backups have no owner.

- A. Keep (no owner): the Backup carries no ownerReference, only the `gameplane.local/modpack-change` label and
  `spec.serverRef`, so it and its restic snapshot survive deleting the server, like scheduled backups.
- B. Delete with the server: the Backup is controller-owned by the GameServer and is garbage-collected with it.

## OD-12: How should modpack browse handle packs for other loaders or versions? (SETTLED: C, valgul 2026-11-26)

**Ruling: option C** (valgul, 2026-11-26).

Raised on 2026-11-26: on a Fabric server the FTB browse lists only the 3 Fabric packs out of FTB's 95 popular packs
(82 Forge, 11 NeoForge), so packs such as FTB Evolution 2 (id 135, NeoForge 26.1.2 only) never show. Modrinth and
CurseForge use the same filter.

- A. List all, switch version on install: browse always shows every pack.
- B. Keep the filter, add a hint naming how many packs need another loader.
- C. Filtered by default, an "All loaders" toggle shows every pack; picking a version for another loader or
  Minecraft version switches the server version on install.

## OD-13: How does an "All loaders" install switch the server's version? (SETTLED: B, valgul 2026-11-26)

**Ruling: option B** (valgul, 2026-11-26). Where an option below says "Recommended", that was the recommendation
before the ruling, not the ruling.

- A. The pack carries its server version (new `spec.modpack.serverVersion`): the pod runs that version while the pack
  is active, the pre-change backup is taken first, and Remove returns the server to its own selected version. Needs a
  CRD field and an operator change. (Recommended.)
- B. The install also rewrites the server's selected version (`spec.version`). Smaller; with "Back up first" the pod
  restarts on the new version as a plain loader before the backup ends, and Remove leaves the server on the new version.
