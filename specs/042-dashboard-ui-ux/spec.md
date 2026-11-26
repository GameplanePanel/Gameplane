# Feature Specification: Dashboard UI and UX polish, screen by screen

**Feature Branch**: `spec-042/dashboard-ui-ux`

**Created**: 2026-11-26

**Status**: Draft, all open decisions settled 2026-11-26 (see `OPEN-DECISIONS.md`)

**Input**: valgul, 2026-11-26, walking the dashboard screen by screen: "let improve ui / ux
[…] let do screen by screen i will write screen name and what i dont like". Dashboard:
"all good for me". Remarks on Servers, Server detail › Logs, Server detail › Backups and
Server detail › Settings › Resources follow below; valgul then said "right now i see
nothing else".

## Context

Facts checked on master `0195f508`:

1. **Servers summary cards share colours.** vCPUs, Storage provisioned and Inventory
   nodes all use the same violet stat colour; Storage switches to the warning colour
   when storage is overcommitted; Players online uses the brand pink
   (`web/src/routes/Servers.tsx:196-244`, `web/src/components/ui/StatCard.tsx:11-17`).
2. **Servers table has a double frame.** An outer bordered box
   (`rounded-lg border border-border bg-card overflow-hidden`) wraps a table that draws
   its own box (`Servers.tsx:366`). The same wrapper exists on `routes/Backups.tsx` and
   `routes/Users.tsx`.
3. **Row actions are faint.** Start, Stop and Restart are 60 % foreground grey
   (`Servers.tsx:663-669`).
4. **The status pill is small.** It is one shared component (`components/ui/PhaseChip.tsx`)
   used on Dashboard, Servers, Server detail, Backups (page and tab), backup row and
   drawer, and network capture.
5. **Toolbar controls have three corner shapes.** Create server is a full pill, the
   search box and the mobile filter button are 12 px, the desktop Filter button is a
   hand-made `div` with 6 px corners that is not a real button (`Servers.tsx:184,300,320,337`).
6. **Logs toolbar.** The source switch (Container output / Game log) and the level
   filter (All / Info / Warn / Error / Debug) look identical side by side. The level
   list has the `w-fit` class, which `web/src/styles/globals.css:565-573` turns into
   "size to text, never wrap"; the source list lacks it, so "Container output" wraps
   onto two lines and its pill highlight becomes a tall oval
   (`web/src/routes/tabs/Logs.tsx:168-206`). Level counts are plain text; Warn and Error
   lines use raw palette colours, Info none, Debug muted (`Logs.tsx:35-40`).
7. **Backup size is the volume capacity.** `Backup.status.size` is the logical size
   (Restic: bytes processed; CSI snapshot: restore size, which equals the volume
   capacity) (`operator/api/v1alpha1/backup_types.go:163-170`). On valgul's server every
   backup shows exactly 200Gi and the card shows "Total logical size 1.6 TB" (8 × 200Gi).
   `status.bytesAdded` (Restic packed data added) exists but is absent for CSI snapshots.
   The agent already measures the allocated bytes of the server's data directory every
   5 minutes (`agent/internal/usage/disk.go`).
8. **Sliders have no fill.** The CPU and memory sliders set `appearance-none`, which
   removes the browser's track and fill, and also `accent-primary`, which only works
   without it (`web/src/components/ui/ResourceInput.tsx:189-199`). The SFTP
   password-length slider is native and looks different
   (`web/src/components/sftp/SftpDialogs.tsx:201-211`). The CPU number box is 64 px wide
   and cuts "8000" off; labels carry a unit ("Memory (GiB)") next to a unit dropdown;
   Storage size is a free-text Kubernetes quantity.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Read the Servers page at a glance (Priority: P1)

An admin opens Servers and, without reading labels, can tell the five summary cards
apart, sees one clean table, and can find the Start, Stop and Restart buttons on each
row.

**Why this priority**: Servers is the most used screen after the Dashboard, and valgul
called the table "broken".

**Independent Test**: Open Servers with at least one running and one stopped server and
compare against the approved design.

**Acceptance Scenarios**:

1. **Given** the Servers page, **When** it loads, **Then** the five cards show five
   different icon colours: Running green, Players online blue, vCPUs violet, Storage
   provisioned teal, Inventory nodes slate.
2. **Given** storage is overcommitted or a card's data is unavailable, **When** the card
   renders, **Then** its icon keeps its own colour and an amber warning badge plus amber
   sub-text explain the problem.
3. **Given** the server table, **When** it renders, **Then** exactly one frame surrounds
   it.
4. **Given** a server row, **When** it renders, **Then** the row action icons are full
   contrast; on hover Start turns green, Stop red, Restart stays neutral.
5. **Given** the toolbar, **When** it renders, **Then** search and Filter have the same
   12 px corners, and Filter is a real button reachable by keyboard.

---

### User Story 2 - One clear status pill everywhere (Priority: P1)

Anyone looking at a server, backup or capture state reads it from a pill as large as
the text around it, with a coloured dot.

**Why this priority**: The pill is shared by eight places; one change fixes all of them.

**Independent Test**: Check the pill on Dashboard, Servers, Server detail, Backups and
network capture.

**Acceptance Scenarios**:

1. **Given** a running server, **When** its pill renders, **Then** it shows a green dot.
2. **Given** a stopped server, **Then** grey; **given** a sleeping server, **then** blue;
   **given** starting, stopping, suspending or resuming, **then** amber; **given** failed,
   **then** red.
3. **Given** any screen that shows a pill, **When** it renders, **Then** the pill text is
   the same size as the row text.

---

### User Story 3 - Tell log source and log level apart (Priority: P2)

A server owner on the Logs tab sees which log they are reading and how it is filtered,
without the two controls blending together.

**Why this priority**: valgul: "the log choice seem wrong to the eye".

**Independent Test**: Open Logs on a server that has a game log file and one that does
not.

**Acceptance Scenarios**:

1. **Given** the Logs tab, **When** it renders, **Then** the source switch stays on one
   line and its selected highlight has the same height as the other tab highlights.
2. **Given** the level filter, **When** it renders, **Then** each level has its colour
   (Info blue, Debug slate, Warn amber, Error red, All neutral) and its count in a badge.
3. **Given** log lines of each level, **When** they render, **Then** each line uses the
   same colour as its level in the filter, in both light and dark theme.
4. **Given** any other horizontal tab list in the app, **When** it renders, **Then** no
   tab label wraps.

---

### User Story 4 - See how much a backup really holds (Priority: P2)

A server owner on the Backups tab sees how much data each backup holds and the total,
instead of the volume capacity repeated for every backup.

**Why this priority**: The current number (1.6 TB for a server with far less data) is
wrong enough to mislead decisions about storage.

**Independent Test**: Take a backup of a server whose volume is much larger than its
data, for both Restic and storage-snapshot backups, and compare the shown size with the
data on the volume.

**Acceptance Scenarios**:

1. **Given** a new backup of a server with 14 GB of data on a 200 GiB volume, **When**
   it succeeds, **Then** its Size shows about 14 GB, not 200 GiB.
2. **Given** a Restic backup, **When** it is listed, **Then** a second line shows the
   space it added to the backup store.
3. **Given** a storage-snapshot backup, **When** the space it uses is not known, **Then**
   no second line is shown and nothing is guessed.
4. **Given** the summary card, **When** it renders, **Then** it is named after what it
   sums (data size) and sums the measured sizes.
5. **Given** a backup taken before this change, **When** it is listed, **Then** it is
   labelled as volume size (not data size) and excluded from the data-size total, which
   says how many backups it covers.

---

### User Story 5 - Set resources with clear sliders and boxes (Priority: P3)

A server owner sets CPU, memory and storage in Create server or in a server's Resources
settings and sees at a glance where each value sits on its range.

**Why this priority**: Cosmetic, but the same component appears in two key flows.

**Independent Test**: Open Create server and Settings › Resources; move each slider.

**Acceptance Scenarios**:

1. **Given** any slider in the app, **When** it renders, **Then** the track is filled in
   the brand colour from its start to the handle, and all sliders share one look.
2. **Given** a CPU value of 8000 mCPU, **When** shown, **Then** the number box shows all
   four digits.
3. **Given** the resource labels, **When** they render, **Then** they carry no unit; the
   unit lives only in the dropdown.
4. **Given** Storage size, **When** edited, **Then** it is a number plus a unit dropdown,
   like Memory, and still produces a valid size.

---

### Edge Cases

- Light theme: every new colour (cards, levels, pill dots, slider fill) must stay
  readable on the light background.
- Mobile: Servers uses cards instead of a table; the status pill, pill rule and action
  colours apply there too; summary cards are hidden on mobile today and stay so.
- Disabled sliders keep the fill but greyed out.
- Storage size values that don't fit a whole unit (e.g. `1536Mi`) must load without
  being rounded or lost.
- A backup taken while the agent cannot report disk use: Size falls back to "unknown",
  never to the volume capacity presented as data.
- A server without a game log file: the source switch is replaced by the plain
  "Container output" label, which follows the new toolbar layout.

## Requirements *(mandatory)*

### Functional Requirements

**Shared rules**

- **FR-001**: Full pills MUST be used only for a page's single main action (e.g. Create
  server) and for small labels (status pills, count badges, chips). All other buttons,
  inputs, tabs, segmented controls and cards MUST use 12 px corners.
- **FR-002**: Every horizontal tab list MUST size each tab to its label and never wrap a
  label, without needing a per-list opt-in.
- **FR-003**: Every table MUST have exactly one frame; the extra outer frame MUST be
  removed on Servers, Backups and Users and any other table found with it.
- **FR-004**: The status pill MUST match the surrounding text size and show a coloured
  dot (green running/succeeded, grey stopped/pending/suspended, blue asleep, amber
  in-transition, red failed) on every screen that uses it, Dashboard included.
- **FR-005**: Every slider MUST show a fill in the brand colour from its start to its
  handle, with one shared look.
- **FR-006**: All new colours MUST come from theme tokens and work in light and dark
  theme.

**Servers**

- **FR-010**: Each summary card MUST have its own fixed icon colour: Running green,
  Players online blue, vCPUs violet, Storage provisioned teal, Inventory nodes slate.
- **FR-011**: Warnings on a card (overcommitted, unavailable) MUST show as an amber
  badge and amber sub-text, never by changing the icon colour.
- **FR-012**: Row actions MUST be full contrast; on hover Start green, Stop red,
  Restart neutral.
- **FR-013**: Search and Filter MUST share 12 px corners; Filter MUST be a real,
  keyboard-reachable button.

**Server detail › Logs**

- **FR-020**: The toolbar MUST separate what is read (source switch, Download) from how
  it is filtered (search, level filter, line count) so the two never look like one
  control.
- **FR-021**: The search box MUST look like the Servers search (icon, sentence-case
  placeholder).
- **FR-022**: Level filter entries MUST be coloured Info blue, Debug slate, Warn amber,
  Error red, All neutral, with counts in badges; log lines MUST use the same colours.

**Server detail › Backups**

- **FR-030**: Each new backup MUST record the size of the server's data at backup time,
  for every backup method.
- **FR-031**: The Size column and the summary card MUST show that data size, not the
  volume capacity.
- **FR-032**: Where the space a backup takes in its store is known (Restic today), it
  MUST be shown as a second line; where unknown, nothing is shown.
- **FR-033**: Backups without a recorded data size (taken before this change) MUST be
  labelled as volume size and kept out of the data-size total, and the total MUST say
  how many backups it covers.

**Resources (Create server, Settings › Resources)**

- **FR-040**: The number box MUST fit at least five digits.
- **FR-041**: Labels MUST NOT carry a unit; the unit stays in the dropdown.
- **FR-042**: Storage size MUST be a number plus a unit dropdown and MUST round-trip any
  existing size value unchanged.

**Process**

- **FR-050**: Every change above MUST be designed in `design.fig` and exported before
  React code (constitution principle II).
- **FR-051**: The Dashboard screen MUST NOT change except through the shared status pill
  (FR-004).

### Key Entities

- **Backup**: gains a recorded data size (bytes of server data at backup time) next to
  its existing logical size and bytes added.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On Servers, all five summary card icons have five distinct colours, in
  both themes.
- **SC-002**: No table in the dashboard shows a double frame.
- **SC-003**: No horizontal tab label in the dashboard wraps at 1280 px width or wider.
- **SC-004**: On a server with 14 GB of data on a 200 GiB volume, a new backup shows a
  size within 5 % of 14 GB.
- **SC-005**: All sliders show a fill up to the handle; an 8000 mCPU value is fully
  visible.
- **SC-006**: The toolbar on Servers and Logs uses exactly one corner shape, apart from
  the main action and small labels.
- **SC-007**: valgul reviews the exported designs for Servers, Logs, Backups and
  Resources and approves them before React work starts.

## Assumptions

- Website and telemetry dashboard are out of scope.
- Teal and slate are added as theme tokens if they don't exist yet; the Players blue may
  match the Info log-level blue (they never appear together).
- Data size is measured from the server's data volume at backup time; the agent's
  existing disk-usage measurement is a candidate source (decided in the plan).
- Space used by storage snapshots is not reported by Kubernetes; no driver-specific
  lookup is in scope.
- Existing backups are not re-measured.
- Other screens may join this spec later if valgul reports them; until then they are
  untouched except through the shared rules (FR-001 to FR-006).
