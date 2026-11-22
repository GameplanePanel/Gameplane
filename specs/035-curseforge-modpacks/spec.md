# Feature Specification: CurseForge and FTB modpacks for Minecraft

**Feature Branch**: `spec-035/curseforge-modpacks`

**Created**: 2026-11-11

**Status**: Ready for planning (OD-1 to OD-6 settled by valgul on 2026-11-11, see `OPEN-DECISIONS.md`)

**Input**: User description: "CurseForge modpacks don't work today. Only Modrinth modpacks install from the Modpacks tab. CurseForge only works for single mods. Add support for CurseForge modpacks."

## Context (today's behaviour, checked on master `7413822b` and module `33e1095`)

- The Minecraft module declares modpacks only for Modrinth: `modules/minecraft-java/template.yaml` (provider `modrinth`, `modpacks.refEnv: MODRINTH_MODPACK`, `env: TYPE=MODRINTH`). The `curseforge` provider entry has no `modpacks:` block, so the Modpacks tab never offers CurseForge (`web/src/components/registry-browser.tsx:112` lists only providers with `modpacks`).
- The API can already search CurseForge modpacks: `api/internal/registry/curseforge.go` uses class 4471 for modpack searches on game 432. Its `ModpackDeps` is a no-op, which is fine for env-mode installs.
- Env-mode install (`api/internal/handlers/registry.go` `installModpack`) writes one env var (the pack reference) plus the fixed env from the template. There is no way to pin a pack version and no way to see or remove the active pack.
- The CurseForge API key lives in the API only: a labelled Secret in the control namespace (`gameplane-modreg-curseforge`) or the `--curseforge-api-key` flag. The operator refuses any game-server env that reads a Secret the GameServer does not own (`operator/internal/controller/gameserver_controller.go` `validateServerEnvSources`).
- The itzg image installs CurseForge packs with `TYPE=AUTO_CURSEFORGE` plus `CF_SLUG` (or `CF_PAGE_URL`), pins with `CF_FILE_ID`, and needs a CurseForge API key. Per itzg's docs, Java 17 and newer image tags now ship a built-in key; the `java8` tags do not. A key can also be given as a file via `CF_API_KEY_FILE`. Mods whose authors block third-party downloads must be placed by hand in a downloads folder (`CF_DOWNLOADS_REPO`).
- FTB App packs install with `TYPE=FTBA` plus `FTB_MODPACK_ID` and optional `FTB_MODPACK_VERSION_ID`; this needs an Ubuntu Hotspot image tag, which every tag the module uses is. FTB's public modpack API (`api.feed-the-beast.com/v1/modpacks/public/…`) answers search without a key (checked 2026-11-11), and it lists the CurseForge id of packs that are also on CurseForge.
- The module uses `java8-multiarch` for Minecraft ≤1.16.5, `java17`, `java21` and `java25` for newer versions.

## Clarifications

### Session 2026-11-13

- Q: When a modpack is removed, what happens to the mods it installed? → A: Remove empties the server's mods folder (everything in it, including mods added by hand); world data and configs stay.
- Q: What happens to servers that already run an unpinned Modrinth pack? → A: After upgrade, each is pinned to the pack version it already has installed; if that version cannot be read, the Active modpack card shows "Not pinned" until the operator picks one.
- Q: Should the panel back up the server before a modpack install, version change or remove? → A: The confirmation offers "Back up first", ticked by default when the server has a backup destination; the change waits for that backup to finish.
- Q: Can an operator change a pinned pack to an older version? → A: Yes; the confirmation warns that a downgrade can break the world and keeps "Back up first" ticked.
- Q: Which pack versions should the version picker list? → A: Only versions for the server's selected Minecraft version and loader; moving to another Minecraft version means changing the server version first.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Install a CurseForge modpack (Priority: P1)

A server operator opens the Modpacks tab of a modded Minecraft server (Forge, NeoForge, Fabric or Quilt), switches the registry to CurseForge, searches for a pack such as "All the Mods", and installs it. The server restarts and comes up running that pack.

**Why this priority**: This is the feature. CurseForge hosts most of the large Minecraft modpacks, and today none of them can be installed from the panel.

**Independent Test**: With a CurseForge key configured, install a small CurseForge pack on a fresh Fabric server and check that the server reaches Running with the pack's mods loaded.

**Acceptance Scenarios**:

1. **Given** a CurseForge key is configured and a Forge server is selected, **When** the operator opens the Modpacks tab, **Then** CurseForge appears in the registry switch next to Modrinth, and search results are CurseForge modpacks for the server's game version and loader.
2. **Given** search results are shown, **When** the operator installs a pack, **Then** the server is set to that pack, restarts, and the banner says the server is restarting to install it.
3. **Given** no CurseForge key is configured, **When** the operator opens the Modpacks tab, **Then** CurseForge is not offered (same as the Mods tab today).
4. **Given** a server running a Modrinth pack, **When** the operator installs a CurseForge pack, **Then** the Modrinth pack setting is cleared, so only one pack is active.

---

### User Story 2 - Choose and keep a pack version (Priority: P2)

The operator picks which version (file) of the pack to install, and the server stays on that version across restarts until the operator chooses another one. Pinned per OD-2 B, for CurseForge, Modrinth (OD-3 A) and FTB packs.

**Why this priority**: Large packs change mod lists between versions. An unplanned upgrade on restart can break a world, so operators need control.

**Independent Test**: Install an older file of a pack, restart the server twice, and check that the installed version did not change.

**Acceptance Scenarios**:

1. **Given** the operator installs a pack, **When** they choose a specific version, **Then** the server installs that version and keeps it on every restart.
2. **Given** a pinned pack, **When** the operator chooses a newer version, **Then** the server restarts and upgrades to it, keeping world data.
3. **Given** a pinned pack, **When** the operator chooses an older version, **Then** the confirmation warns that a downgrade can break the world, "Back up first" stays ticked (when a backup destination exists), and on confirm the server restarts on the older version.

---

### User Story 3 - See and remove the active pack (Priority: P2)

The Modpacks tab shows which pack (name, registry, version) is active on the server, and lets the operator remove it so the server goes back to the plain loader of its selected version. Settled by OD-4 A.

**Why this priority**: Today nothing in the panel shows that a pack is active, and the only way out is editing env by hand.

**Independent Test**: Install a pack, check the tab shows it, remove it, and check the server restarts as a plain loader server.

**Acceptance Scenarios**:

1. **Given** a server with a pack installed from any registry, **When** the operator opens the Modpacks tab, **Then** the active pack's name, registry and version are shown.
2. **Given** an active pack, **When** the operator removes it, **Then** every pack setting is cleared, the server's mods folder is emptied (including mods added by hand), the server restarts on its selected version's loader, and world data and configs stay.
3. **Given** an active pack, **When** the operator clicks Remove, **Then** the confirmation says that every mod in the mods folder will be deleted, including ones added by hand.

---

### User Story 4 - Packs with mods that block automatic download (Priority: P3)

Some packs include mods whose authors forbid third-party downloads. The operator is told which files are missing and where to put them, uploads them, and restarts. Settled by OD-5 A.

**Why this priority**: It affects a minority of packs, but without it those packs fail with no explanation in the panel.

**Independent Test**: Install a pack known to contain a blocked mod, check that the panel names the missing files, upload them to the stated folder, restart, and check the server starts.

**Acceptance Scenarios**:

1. **Given** a pack install stops because files need a manual download, **When** the operator looks at the server, **Then** the panel shows the list of missing files with a link to each download page and the folder to upload them to.
2. **Given** the files were uploaded to that folder, **When** the server restarts, **Then** the install completes.

---

### User Story 5 - Install an FTB modpack (Priority: P3)

The operator switches the Modpacks registry to FTB, searches FTB's own pack list, picks a pack and version, and installs it. The server restarts running that pack. Pinning, the active pack card and Remove work the same as for the other registries.

**Why this priority**: valgul chose to include FTB (OD-6 B). Many large FTB packs are also on CurseForge, so CurseForge covers most users first.

**Independent Test**: Install a small FTB pack on a fresh Forge server and check that the server reaches Running with that pack and version.

**Acceptance Scenarios**:

1. **Given** a Forge, NeoForge, Fabric or Quilt server, **When** the operator opens the Modpacks tab, **Then** FTB appears in the registry switch with no key needed, and results are FTB packs that have a version for the server's game version and loader.
2. **Given** results are shown, **When** the operator installs a pack and version, **Then** the server is set to that FTB pack pinned to that version, any CurseForge or Modrinth pack setting is cleared, and the server restarts.

---

### Edge Cases

- A pack for an old Minecraft version (≤1.16.5) runs on the `java8` image, which has no built-in CurseForge key. Such packs are offered only when the admin shares the key with game servers (OD-1 C).
- The selected server version's Java image does not match the pack's Minecraft version. Search is already filtered to the server's game version and loader, so a pack is only offered for the version the server runs. A pack whose own manifest disagrees fails at boot with the image's error in the server log.
- The CurseForge key is removed or rotated after a pack is installed. Browsing stops (or uses the new key) at once; per-server key copies follow the admin key when sharing is on (OD-1 C).
- CurseForge is unreachable or rate-limits during boot. The server fails to start with the image's error in the log; the panel does not retry in a loop beyond the normal restart policy.
- A pack is removed from CurseForge after install. A pinned file keeps working while CurseForge still serves it; otherwise the server log shows the error and the operator can pick another pack or remove it.
- The server is a Paper/Spigot/Bukkit/Purpur or vanilla server. The Modpacks tab stays hidden, as today (`serverHasModpacks`).
- A pack is on both FTB and CurseForge. It shows in both registries; whichever registry the operator installs from is the one recorded as active.
- FTB's API is unreachable. FTB is shown with the same error state the registry browser uses for other providers; the rest of the tab keeps working.
- A server installed a Modrinth pack before this feature (no pinned version). After the panel upgrade it is pinned to the version it already has installed; the operator restarts it once to apply the pin as soon as no players are online (OD-8 B), so a later restart does not upgrade the pack. If the installed version cannot be read, the Active modpack card shows "Not pinned" and asks the operator to pick a version.
- The operator ticks "Back up first" and the backup fails. The pack change does not happen; the panel shows the backup error and the server keeps its current pack.
- A pack's newer versions target a newer Minecraft version than the server runs. They are not listed in the picker; the operator changes the server version first, then picks the pack version.
- An operator installs a different pack over an active one. The mods folder is not emptied (OD-7 B), so the old pack's mods stay and may load alongside the new pack; the operator deletes them in the Mods tab or uses Remove first.
- A pack needs more memory than the server has. Out of scope here; the operator raises memory in the server settings (itzg recommends at least 4 GB).

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The Minecraft module MUST offer CurseForge and FTB as modpack registries on Forge, NeoForge, Fabric and Quilt servers, alongside Modrinth.
- **FR-002**: CurseForge MUST appear in the Modpacks registry switch only when a CurseForge key is configured, as in the Mods tab today.
- **FR-003**: Installing a CurseForge pack MUST set the server to install that pack on boot through the game image's own CurseForge modpack mode, and MUST restart the server through the operator like the Modrinth install does today.
- **FR-004**: Installing a pack from one registry MUST clear every setting of a pack from the other registries, so at most one pack is active per server.
- **FR-005**: Game servers MUST use the image's built-in CurseForge key by default (OD-1 C). An admin setting "Share the CurseForge key with game servers" (off by default) MUST make the operator give each server with a CurseForge pack its own server-owned copy of the admin key, exposed to the game container as an env var (`CF_API_KEY`) read via `valueFrom.secretKeyRef`, kept in sync on rotation and removed when the admin key is removed or the setting is turned off. While the setting is off, CurseForge packs for Minecraft versions whose image has no built-in key (java8, ≤1.16.5) MUST NOT be offered. The key MUST NOT be written into GameServer env as plain text, into server logs, or into API responses.
- **FR-006**: Installing a pack MUST open a version picker that lists only the pack's versions for the server's selected Minecraft version and loader, defaulting to the newest of them; the server MUST stay pinned to the chosen file across restarts until the operator picks another (OD-2 B). Older versions MAY be picked; the confirmation MUST warn that a downgrade can break the world (Clarifications 2026-11-13). There is no "always latest" mode. Servers with a Modrinth pack installed before this feature MUST be pinned to their currently installed version on upgrade, without restarting them while players are online; the pin takes effect at the next restart, or through one operator restart as soon as the server has no players online (right away when stopped or asleep) (OD-8 B). If that version cannot be read they show "Not pinned" (Clarifications 2026-11-13). The same picker and pinning apply to Modrinth packs (OD-3 A) and FTB packs (OD-6 B).
- **FR-007**: The Modpacks tab MUST show an "Active modpack" card (name, registry, pinned version) with Change version and Remove actions, for every registry (OD-4 A). Remove MUST ask for confirmation, clear all pack settings, delete everything in the server's mods folder for the active loader (including mods added by hand), and restart the server; world data and configs stay.
- **FR-008**: The server's data volume MUST have a `downloads/` folder that the image reads for manually downloaded files. When a pack install stops because files need a manual download, the Modpacks tab MUST list each missing file with a link to its download page and tell the operator to upload it to `downloads/` in the Files tab and restart (OD-5 A).
- **FR-009**: Install, version change and remove MUST be limited to users who can manage the server (same permission as the Mods and Modpacks tabs today), and each MUST be written to the audit log.
- **FR-010**: Every new user-facing string MUST go through the shared i18n packs.
- **FR-011**: The module change MUST follow the module images rule: keep the official itzg image; no custom image.
- **FR-012**: The panel MUST add an FTB modpack registry (OD-6 B): search FTB's public pack list filtered to the server's game version and loader, list a pack's versions for the picker, and install through the image's FTB mode pinned to the chosen version. FTB needs no key and is always offered on modpack-capable servers. FTB is a modpack-only registry; it does not appear in the Mods tab.
- **FR-013**: The confirmation for install, version change and remove MUST offer "Back up first". It MUST be ticked by default when the server has a backup destination, and hidden or disabled with a short reason when it has none. When ticked, the pack change MUST start only after the backup succeeds; if the backup fails, the change MUST NOT happen and the operator sees why (Clarifications 2026-11-13).

### Key Entities

- **Modpack registry entry**: a registry declared by a game module that can install whole packs (today Modrinth; this spec adds CurseForge and FTB). Says which settings select a pack, which settings pin its version, and which fixed settings switch the image into pack mode.
- **Active modpack**: the pack set on one server: registry, pack reference, optional pinned version. Derived from the server's own settings, so it survives API restarts and needs no new database table.
- **CurseForge key for game servers**: the image's built-in key by default; when the admin opts in, a per-server copy of the admin key the operator owns and keeps in sync (OD-1 C).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can find and install a CurseForge or FTB modpack from the Modpacks tab in under 2 minutes, without editing env or files by hand.
- **SC-002**: A pinned pack's installed version is unchanged after 3 consecutive restarts.
- **SC-003**: The admin's CurseForge key never appears in GameServer objects, API responses, audit entries or server logs.
- **SC-004**: Switching between packs from any two of Modrinth, CurseForge and FTB leaves exactly one pack active, verified by reading the server's settings.
- **SC-005**: For a pack with blocked mods, the operator can see every missing file name in the panel, with no need to read raw logs.

## Assumptions

- The itzg image keeps supporting `AUTO_CURSEFORGE`, `CF_SLUG`, `CF_FILE_ID`, `CF_API_KEY_FILE` and `CF_DOWNLOADS_REPO` as documented today; the plan re-checks this against the pinned image digests.
- The existing CurseForge search (class 4471, game 432) and version listing are reused; no new registry engine is needed for CurseForge. FTB needs a new registry engine; its outbound host goes through the same SSRF guard as the other registry engines.
- World data is never deleted by install, version change or remove. Remove empties only the mods folder (Clarifications 2026-11-13).
- The design for the Modpacks tab changes (registry switch with CurseForge and FTB, version picker, active pack card, missing files notice) is done in `design.pen` through Pencil on valgul's machine before any React code. The copy-paste prompt is written once plan and tasks exist (repo `local-prompt` skill requires a finished spec).
- Module changes go to `GameplanePanel/module` first; the main repo bumps the submodule pointer only after that PR merges, and installs get the module only after the republish-modules workflow runs.
