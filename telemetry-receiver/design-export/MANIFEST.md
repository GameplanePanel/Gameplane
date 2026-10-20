# telemetry-dashboard.pen export manifest

This is a snapshot of `telemetry-receiver/telemetry-dashboard.pen` for review in git. The `.pen` file is accessed only through Pencil MCP. It holds the receiver's private dashboard pages for spec 022 (T066, T075), built on a copy of the HeroUI design-system kit.

## Contents (2026-10-07, first export)

| Node | Frame | Spec |
|---|---|---|
| `HBYBE` | Receiver / Login | T066, FR-022 |
| `a7wUpP` | Receiver / Login — refused (generic "Invalid credentials") | T066, FR-023 |
| `i0xK1E` | Receiver / Overview: basic section (KPIs, reports per day, versions, servers per install) and extended section (coverage, install KPIs, new and lapsed small multiples, environment, games, feature adoption, privacy footnote) | T066, T075, FR-024–FR-027 |
| `Z079vK` | Receiver / Overview — empty (no zero-filled charts) | T066, FR-027 |
| `jy9Rn` | Receiver / Extended — none in range | T075, T076 |
| `y49Ie` | Telemetry components: `sDDfo` Telemetry/Stat tile, `wPbIp` Telemetry/Share row | T066 |

`variables.json` lists every colour and font token, light and dark, for the server-rendered CSS (T068).

## Theme

- **Palette.** This follows the pink theme the main app and website share: the `web/src/styles/globals.css` values under the same HeroUI token names, the same as `website/website-export`. The kit copy in this file is re-themed with it.
- **Typography.** Fonts are Geist (sans) and JetBrains Mono (headings, eyebrows, wordmark).
- **Modes.** The frames are rendered in dark mode, the website's default. Light values are defined for every token.
- **Chart tokens.**
  - `chart/series-1` is pink: `#DB2777` light, `#EC4899` dark. Brand `#FF4FA3` is too light for dark-surface marks.
  - `chart/series-2` is blue: `#2a78d6` light, `#3987e5` dark, used for lapsed installs.
  - Both pass the dataviz validator in both modes against the file's surfaces (`#FFF7FB`, `#1C1A20`).
  - `chart/grid`, `chart/baseline` and `chart/track` are recessive hairline and track tones.
- **Chart rules.** Columns start at zero, there is a single y-axis, and only the latest value is labelled. Small multiples share a scale. A "View as table" link gives a table alternative, because the CSP-locked pages have no JavaScript for hover.

## Method

- **JSON.** `Get(id, {depth: 40, includePathGeometry: true})` was printed from one `execute` call. A script split the output into `json/<id>.json` (2-space indent, UTF-8, trailing newline). The compact re-serialisation length of every file matches `JSON.stringify` in Pencil exactly, and no file contains `"..."` elisions.
- **PNG.** `Export(ids, "png", …, {scale: 2})`. Login, refused and empty are 2880×1800, the overview is 2880×5634, "none in range" is 2880×328 and the components frame is 1624×354.
- **Content checks.** Each string matched only its own file:
  - "Invalid credentials" → `a7wUpP`
  - "No reports yet" → `Z079vK`
  - "Lapsed installs per day" → `i0xK1E`
  - "No reports in this range included extended data" → `jy9Rn`
  - "DASHBOARD_TOKEN value" → `HBYBE`, `a7wUpP`

## Incremental export (2026-10-08): dashboard ends on today (OD-6)

| Node | Change |
|---|---|
| `i0xK1E` | "Data through 2026-10-06 (UTC, today in progress)"; tiles "Reports, today" and "Active installs, today" with "2026-10-06 (UTC, in progress)"; "Median servers per install" sub "Last 30 days"; captions "…, ending today (in progress)", "Reports in range by reported version…", "Reports by game-server count. Median 3." and the lapsed note "Today's bar is final after the UTC day closes." |
| `Z079vK` | Empty-state text: "Figures appear as soon as this receiver accepts a report; today's figures are in progress and update as reports arrive. …" |
| `y49Ie` | `sDDfo` Stat tile sample: "Reports, today", "2026-10-06 (UTC, in progress)" |

Method: 15 `Update` calls in one `execute`. The JSON files were produced by applying the same edits to the previous export, then checked equal to Pencil (`JSON.stringify(Get(id, {depth: 40, includePathGeometry: true}))` length and FNV-1a hash per frame: `i0xK1E` 69071, `Z079vK` 4684, `y49Ie` 1854). PNGs: `Export(ids, "png", …, {scale: 2})`, same sizes as before. Content check: "today in progress" → `i0xK1E`; "accepts a report" → `Z079vK`.

## Incremental export (2026-10-15): version trend (spec 022 T114, FR-042, OD-7 to OD-10)

| Node | Change |
|---|---|
| `i0xK1E` | Basic card `Waady`: title `HlgPb` renamed "Reports by version" (OD-9), caption unchanged. New full-width extended card `b16UiL` "Versions by install" (sample rows v0.3.0, v0.3.0-rc.1, v0.2.0-beta.8, dev), which the template already rendered. New card `K18Ey8` "Version trend" directly under it: Upgraded, Downgraded and Changed (unordered) panels on one shared scale, then a full-width Stale chart on its own scale. The caption names the newest release (v0.3.0), and the card has a "View as table" link. |
| `variables.json` | New tokens `chart/series-3` (green `#008300` light and dark) and `chart/series-4` (amber `#eda100` light, `#c98500` dark). |

Chart choices:

- **Form.** Small multiples. Stale counts installs behind the newest release, often hundreds a day. The other three count installs that changed version that day, usually tens. Stale therefore gets its own chart and scale, and the three change panels share one (user decision, 2026-10-14).
- **Colours.** Upgraded uses `chart/series-1` (pink), Downgraded `chart/series-2` (blue), Changed (unordered) `chart/series-3` (green) and Stale `chart/series-4` (amber).
  - The four pass the dataviz validator all-pairs, as small multiples require, against the file's surfaces in both modes.
  - Light (`#FFF7FB`): worst CVD ΔE 10.7 (green and pink), normal vision 29.0.
  - Dark (`#1C1A20`): worst CVD ΔE 6.9 (green and amber). That is in the 6 to 8 floor band, which is allowed here because each panel has a swatch label and the card has a table view. Normal vision 23.2.
  - Amber is 2.06:1 on the light surface, so it relies on the same labels and table view.
  - Rejected: aqua against pink fails dark CVD (ΔE 5.4), and violet against blue fails normal vision (ΔE 10.5).
- **Rules kept.** Columns start from zero, each chart has one y-axis, only the latest value is labelled, and "View as table" gives the table alternative.

Method:

- **Edits.** `execute` calls on the Gameplane checkout's file, followed by a layout-problem check (`ctx.problems`) and a screenshot review.
- **JSON.** `JSON.stringify(Get("i0xK1E", {depth: 40, includePathGeometry: true}))` was checked equal to the written file: 111,299 UTF-16 code units, FNV-1a 710213898. The file has no `"..."` elisions.
- **PNG.** `Export(["i0xK1E"], "png", …, {scale: 2})` gives 2880×7091. The frame height is 3545.0000000000005, so the export rounds it up by one pixel.
- **Content checks.** Each string matched only `i0xK1E.json`: "Behind v0.3.0, on its own scale", "Installs by their last reported version" and "Reports by version".

## Incremental export (2026-10-17): deployment card (spec 022 T122, FR-045, OD-13 to OD-16)

| Node | Change |
|---|---|
| `i0xK1E` | New row `ISPZo` "Environment row 3" under the Architecture and Cluster nodes row. Its left column holds card `lAzgJ` "Deployment", a copy of the Architecture card with three `wPbIp` Share row instances (cluster 82%, standalone 15%, other 3%). The right column is an empty grid cell `h5Dkd`, so the environment grid stays in pairs. Caption: "Share of install-days by how the panel runs: on a cluster, or standalone. Other is installs on an older version that doesn't report it." The Kubernetes version and Distribution captions now describe sets (OD-15): "Share of install-days reporting each minor version across their managed clusters. Installs with several report each, so shares can add up to more than 100%." Distribution reads the same with "each detected distribution". |

Design notes:

- **Components.** No new component.
  - The card keeps the environment card layout and tokens (`$surface/surface`, `$radius/3xl`, `$border`, padding 20, gap 14), and its rows are `wPbIp` instances.
  - Bar widths follow the file's convention: share × the 432 px track.
  - The "other" label uses `$foreground/muted`, like the Other rows of the neighbouring cards.
- **Audit.** The `pen-componentize` audit found no detached copies, replaced instances or repeated structures. Its hand-built control findings on `fximY`, `y49Ie` and `jy9Rn` predate this change.
- **Modes.** The frame stays in dark mode. Light mode was checked by switching the frame's theme for a preview and back.
- **Narrow layout.** Below 960 px the template's `grid-2` collapses to one column, and the card's SVG scales like its neighbours'.

Method:

- **JSON.** `JSON.stringify(Get("i0xK1E", {depth: 40, includePathGeometry: true}))` was checked equal to the written file: 113,413 UTF-16 code units, FNV-1a 1968389605. The file has no `"..."` elisions.
- **PNG.** `Export(["i0xK1E"], "png", …, {scale: 2})` gives 2880×7553.
- **Content checks.** Each string matched only `i0xK1E.json`: "doesn't report it" and "across their managed clusters".
