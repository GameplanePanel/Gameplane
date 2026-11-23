# telemetry-dashboard.fig export manifest

`telemetry-receiver/telemetry-dashboard.fig` holds the receiver's private dashboard pages for spec 022 (T066, T075), built on a copy of the HeroUI design-system kit. This folder is its snapshot for review in git, in the same three forms as the dashboard's (`assets/design-export/MANIFEST.md`): `fig/`, `json/` and `screenshots/`, one file each per frame, listed in `screens.json`.

## Contents

| Node | Frame | Spec |
|---|---|---|
| `HBYBE` | Receiver / Login | T066, FR-022 |
| `a7wUpP` | Receiver / Login — refused (generic "Invalid credentials") | T066, FR-023 |
| `i0xK1E` | Receiver / Overview: basic section (KPIs, reports per day, versions, servers per install) and extended section (coverage, install KPIs, new and lapsed small multiples, environment, games, feature adoption, privacy footnote) | T066, T075, FR-024–FR-027 |
| `Z079vK` | Receiver / Overview — empty (no zero-filled charts) | T066, FR-027 |
| `jy9Rn` | Receiver / Extended — none in range | T075, T076 |
| `y49Ie` | Telemetry components: `sDDfo` Telemetry/Stat tile, `wPbIp` Telemetry/Share row | T066 |

The frames keep the node IDs they had in the old `telemetry-dashboard.pen` as file names. `variables.json` lists every colour and font token, light and dark, for the server-rendered CSS (T068).

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

## Version trend chart (spec 022 T114)

- **Form.** Small multiples. Stale counts installs behind the newest release, often hundreds a day. The other three count installs that changed version that day, usually tens. Stale therefore gets its own chart and scale, and the three change panels share one (user decision, 2026-10-14).
- **Colours.** Upgraded uses `chart/series-1` (pink), Downgraded `chart/series-2` (blue), Changed (unordered) `chart/series-3` (green) and Stale `chart/series-4` (amber).
  - The four pass the dataviz validator all-pairs, as small multiples require, against the file's surfaces in both modes.
  - Light (`#FFF7FB`): worst CVD ΔE 10.7 (green and pink), normal vision 29.0.
  - Dark (`#1C1A20`): worst CVD ΔE 6.9 (green and amber). That is in the 6 to 8 floor band, which is allowed here because each panel has a swatch label and the card has a table view. Normal vision 23.2.
  - Amber is 2.06:1 on the light surface, so it relies on the same labels and table view.
  - Rejected: aqua against pink fails dark CVD (ΔE 5.4), and violet against blue fails normal vision (ΔE 10.5).
- **Rules kept.** Columns start from zero, each chart has one y-axis, only the latest value is labelled, and "View as table" gives the table alternative.

## Deployment card (spec 022 T122)

- **Components.** No new component.
  - The card keeps the environment card layout and tokens (`$surface/surface`, `$radius/3xl`, `$border`, padding 20, gap 14), and its rows are `wPbIp` instances.
  - Bar widths follow the file's convention: share × the 432 px track.
  - The "other" label uses `$foreground/muted`, like the Other rows of the neighbouring cards.
- **Audit.** The `pen-componentize` audit found no detached copies, replaced instances or repeated structures. Its hand-built control findings on `fximY`, `y49Ie` and `jy9Rn` predate this change.
- **Modes.** The frame stays in dark mode. Light mode was checked by switching the frame's theme for a preview and back.
- **Narrow layout.** Below 960 px the template's `grid-2` collapses to one column, and the card's SVG scales like its neighbours'.

## Regenerating

`hack/design/fig-export.ts` writes the export from `telemetry-dashboard.fig`; its header gives the commands. It renders only screens whose content changed since the last export (`--all` renders every screen), and the `design export` workflow commits a missing export on pull requests from branches of this repository. The file was converted from `telemetry-dashboard.pen` with OpenPencil on 2026-11-22. The export made through Pencil MCP before then, and its log, are in git history.
