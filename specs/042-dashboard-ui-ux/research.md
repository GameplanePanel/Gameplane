# Research: 042 dashboard UI and UX polish

Facts checked on master `0195f508`.

## R1. Card and level colours as theme tokens

- **Decision**: add four semantic tokens in `web/src/styles/globals.css`, each with a dark
  and a light value and a `--color-*` mapping: `--stat-players` / `--level-info` (blue,
  one value shared), `--stat-storage` (teal), `--stat-nodes` / `--level-debug` (slate).
  Reuse `--success` (Running), `--chart-stat` (vCPUs violet), `--warning` (Warn) and
  `--danger` (Error). `StatCard` gains matching accents (`info`, `teal`, `slate`) plus a
  `warning` prop that renders the amber badge and sub-text without touching the icon
  colour.
- **Rationale**: FR-006 (tokens, both themes). Today only success, warning, danger,
  sleep, violet and chart-stat exist (`globals.css:40-85, 225-245`).
- **Alternatives**: Tailwind palette classes (`text-sky-400`): rejected, they bypass the
  theme; this is exactly what `Logs.tsx:35-40` does today and why light mode is off.

## R2. Tab lists that never wrap

- **Decision**: move the `.tabs__list.w-fit` rules (`globals.css:565-573`: `min-width:
  fit-content`, tabs `width:auto; flex:0 0 auto; white-space:nowrap`) onto every
  horizontal `.tabs__list`, then drop the now redundant `w-fit` classes (5 uses). Change
  the selected-tab radius from 999px to 12 px (pill rule FR-001), except where a design
  keeps a different radius on purpose (Users & RBAC keeps 6 px today; re-checked in the
  design pass).
- **Rationale**: root cause of the Logs source switch (spec Context 6); 10 files use
  `Tabs.List`, only 5 carry the opt-in.
- **Alternatives**: add `w-fit` to the Logs source list only: rejected, it leaves the
  same trap for every other list.
- **Watch**: the Server detail main tab bar is meant to spread across the width; it must
  keep that (verify in the design and in the browser smoke test).

## R3. One frame per table

- **Decision**: drop the outer bordered wrapper where a HeroUI `Table` already draws its
  box: `Servers.tsx:366`, `Backups.tsx`, `Users.tsx`; audit the other `Table.Root` users
  (`tabs/Backups.tsx`, `tabs/settings/ShareLinks.tsx`, `tabs/settings/EnvVars.tsx`,
  `McpTokens.tsx`, `AuditLog.tsx`, `nodes/NodeInventory.tsx`,
  `modpack-missing-files.tsx`, `CaptureWidget.tsx`).
- **Alternatives**: strip the table's own border instead: rejected, would need a style
  override on every table.

## R4. Status pill

- **Decision**: `PhaseChip` keeps its single phase→colour map (`PhaseChip.tsx:15-30`),
  renders at text size with a leading dot, and adds a sleep colour for Asleep. Call
  sites stop passing size overrides (e.g. `Servers.tsx:627` forces 11 px).
- **Open point**: the existing sleep colour token is **violet** (`--sleep`,
  `globals.css:40,83`) and is already used for sleep elsewhere (`globals.css:263,605`).
  valgul approved "blue for sleeping" before this was known. Raised as OD-9.

## R5. Sliders

- **Decision**: keep the native `input[type=range]` (`ResourceInput.tsx:80-85` explains
  why there is no HeroUI slider) and paint the track with a CSS custom property
  `--fill` (percentage computed from value/min/max) through one shared `.range` class:
  `background: linear-gradient(to right, var(--accent) var(--fill), var(--border)
  var(--fill))`, plus thumb styles for WebKit and Firefox. The SFTP slider
  (`SftpDialogs.tsx:201`) uses the same class.
- **Alternatives**: drop `appearance-none` and rely on `accent-color`: rejected, the fill
  then follows each browser's own look and cannot match the design.

## R6. Storage size as number + unit

- **Decision**: reuse the `ResourceInput` number + unit pattern without the slider
  (units MiB, GiB, TiB); parse an existing quantity into the best whole unit and keep the
  exact original string when it doesn't convert cleanly (edge case `1536Mi` → 1.5 GiB
  shown, `1536Mi` emitted unchanged unless edited). Applies to Settings › Resources
  (`tabs/settings/Resources.tsx:218-236`) and Create server step 3
  (`CreateServer.tsx:69,276`).

## R7. Backup data size

- **Decision**: new `Backup.status.dataSize` (quantity), recorded once per backup:
  - **Restic**: `summary.TotalBytesProcessed` (`backup_controller.go:584-586`); restic
    reads the files, so this is the data size. Same value as today's `size`.
  - **CSI snapshot**: the GameServer's `status.agent.diskUsedBytes`
    (`gameserver_types.go:1081-1086`, measured by the agent every 5 minutes,
    `agent/internal/usage/disk.go`) read when the snapshot is admitted, only if the agent
    sample is fresh (within 10 minutes of the last heartbeat); otherwise left empty.
  - `size` and `bytesAdded` keep their meaning (no breaking change).
- **Rationale**: FR-030/031 for every method with no new agent work; the operator stays
  the authority (rule 10).
- **Alternatives**: a one-shot `du` Job per CSI backup: rejected, extra pod and time per
  backup for a number the agent already has. Driver-specific snapshot usage (ZFS
  `used`): out of scope (spec Assumptions).
- **Limits**: the agent measures the game data directory only; multi-volume servers
  (spec 033) report the main data volume. Older agents report the whole filesystem
  (`gameserver_types.go:1082`); documented in `quickstart.md`.

## R8. Backups taken before the change

- Pending OD-8. Proposed: show `size` labelled "volume size", leave out of the data-size
  total, total reads "14 GB across 3 backups".

## R9. Design pass

- Screens to change in `design.fig` (names from `assets/design-export/screens.json`):
  `Screen/Servers`, `Screen/Servers (Light)`, `Screen/Servers — Cluster filters open`,
  `Screen/Server Detail — Logs`, `Screen/Server Detail — Backups`,
  `Screen/Server Detail — Settings · Resources`, the Create Server step 3 screens, the
  status pill and tab components, and every screen using the pill (Dashboard included)
  through the component instance.
- Runs on valgul's machine or a cloud thread with the OpenPencil `master` build, then
  `hack/design/fig-export.ts`; valgul approves before React (SC-007).
