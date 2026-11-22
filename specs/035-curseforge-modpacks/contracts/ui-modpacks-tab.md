# Contract: Modpacks tab and admin setting (design first in design.pen)

Designed in Pencil on valgul's machine before any React (constitution II). The local
prompt lists these surfaces:

1. **Registry switch** in the Modpacks tab: Modrinth, CurseForge, FTB. CurseForge hidden
   without a key or when `modpacksBlockedReason` is set.
2. **Version picker** (install and Change version): versions for the server's Minecraft
   version and loader only, newest first and preselected; release/beta badge; older-than-
   pinned versions marked.
3. **Confirmation dialog**: pack + version; "Back up first" checkbox (ticked when
   `backupAvailable`, otherwise disabled with a reason); downgrade warning when an older
   version is picked; Remove variant states that every mod in the mods folder is deleted,
   including ones added by hand.
4. **Active modpack card** at the top: title, registry, version name or "Not pinned",
   phase (Backing up / Applying / Backup failed + message), Change version, Remove. A pending
   removal shows "Removing"; after a failed removal backup, Remove becomes "Retry remove".
5. **Missing files notice** (CurseForge): table of mod, file name, download link; text
   telling the operator to upload to `downloads/` in the Files tab and restart. When the server is
   stopped (`missingFilesUnknown`), a one-line note says to start it to check for missing files.
6. **Admin → Mod registries → CurseForge**: "Share the CurseForge key with game servers"
   switch with a one-line risk note; disabled with a reason when the key comes only from the
   startup flag.

States: light and dark, mobile width, loading, empty search, provider error, read-only
(no manage permission).
