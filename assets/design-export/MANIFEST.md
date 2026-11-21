# design.fig export manifest

`design.fig` at the repository root is the dashboard design. This folder is its snapshot for review in git: one entry per screen in three forms.

| Folder | Holds |
|---|---|
| `fig/<file>.fig` | The screen on its own, with the components and icons it uses, so it opens by itself in Figma or OpenPencil |
| `json/<file>.json` | Layout, paints, text, and the design variable each layer binds; an instance lists the text it shows instead of its component's layers |
| `screenshots/<file>.png` | The screen rendered at 2x |

`screens.json` lists every screen: its file name, its ID in `design.fig`, its name, and its size.

`parts.json` lists layers below screen level that the browser visual diff (`web/scripts/compare-screenshots.mjs`) compares against, such as one dialog or one badge. Each is exported in the same three forms. To add one, add its file name and exact layer name; the export fills in the rest.

## Which frames are screens

Every top-level frame or instance on the `Dashboard` page, except notes (`Note — …`), the canvas row labels (`Row NN · …`), and the component boards (`HeroUI: Design System Components`, `Gameplane: App Components`, `Shared Components Band`, `Reference frame`).

## File names

A screen or part that existed in the old `design.pen` keeps that file's node ID as its file name (`EZFW0.json`), so the browser visual diff (`web/scripts/compare-screenshots.mjs`) and older references still find it. A new screen is named after its `design.fig` ID with `:` written as `-` (`1-232.json`). `screens.json` maps one to the other.

## Regenerating

`hack/design/fig-export.ts` writes all three forms, `screens.json` and `parts.json` from `design.fig`. It runs on OpenPencil's headless CLI, from a checkout of [ValgulNecron/open-pencil](https://github.com/ValgulNecron/open-pencil) `master`; the script's header gives the commands. Re-run it after every change to `design.fig` and commit the export with the change.

## History

`design.fig` was converted from `design.pen` with OpenPencil on 2026-11-21, after the #1030 design pass. The export made through Pencil MCP before then, and this manifest's earlier log of it, are in git history.
