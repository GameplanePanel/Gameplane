---
name: design-export
description: "After ANY edit to design.fig, telemetry-receiver/telemetry-dashboard.fig or website/website.fig — regenerates the in-git snapshot of the design. A design edit is not finished until this runs; the export lands in the SAME commit as the design change."
---

## Rule 1 & 2 compliance: design exports

Every design edit is followed by an export in the same commit. Never hand-edit a design file: `.fig` is binary and changes only through OpenPencil (desktop app, `openpencil-mcp`, or the CLI) or Figma.

## Regenerating the export

`hack/design/fig-export.ts` regenerates the snapshot from the `.fig`: one standalone `.fig`, one JSON dump and one 2x PNG per screen, plus `screens.json`, and the same three files for each layer listed in `parts.json` (the dialogs and badges the browser visual diff compares against). `assets/design-export/MANIFEST.md` explains the layout and the file names.

It runs from a checkout of [ValgulNecron/open-pencil](https://github.com/ValgulNecron/open-pencil) `master` (`bun install`, then build `packages/pen` and `packages/core` with `bunx tsdown`); the script's header has the exact commands:

```sh
cp hack/design/fig-export.ts <open-pencil>/packages/cli/fig-export.ts
cd <open-pencil>/packages/cli
bun fig-export.ts <gameplane>/design.fig <gameplane>/assets/design-export
bun fig-export.ts <gameplane>/telemetry-receiver/telemetry-dashboard.fig <gameplane>/telemetry-receiver/design-export
bun fig-export.ts <gameplane>/website/website.fig <gameplane>/website/website-export   # commit inside the website submodule
```

- The export is incremental: each entry in `screens.json` and `parts.json` keeps a hash of everything that renders the screen (its layers, the component layers its instances show, the document's variables), and only new or changed screens are rendered again. A screen whose JSON and PNG come out the same keeps its `.fig`, files only change when their bytes do, and files of deleted screens are removed. Add `--all` after the two paths to re-render every screen, for example after updating the OpenPencil checkout; bump `FORMAT` in the script when a change to it changes what it writes.
- On a pull request from a branch of this repository, the `design export` workflow (`.github/workflows/design-export.yaml`) runs the export for `design.fig` and `telemetry-dashboard.fig` and commits any change to the branch. Running it yourself first is still the way to check your change; the workflow only fills in what is missing. Fork PRs and `website/website.fig` still commit their export by hand.
- A new screen gets a file named after its `.fig` ID (`1:232` → `1-232`); existing screens keep their file names through `screens.json`.
- When the visual diff needs a new layer below screen level, add `{"file": "<name>", "name": "<exact layer name>"}` to `parts.json`. A top-level layer with that name wins; otherwise the name must be unique outside instances, or the script stops.
- Check: open the changed PNGs, and grep the changed JSON for body text unique to the screen (not its frame name).
- Commit together: `git add design.fig assets/design-export` (or the telemetry or website pair), `git commit -s -m "design: …"`. Do not split the design change and its export into two commits.
