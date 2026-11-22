---
name: design-export
description: "After ANY edit to design.fig, telemetry-receiver/telemetry-dashboard.fig or website/website.fig — regenerates the in-git snapshot of the design. A design edit is not finished until this runs; the export lands in the SAME commit as the design change."
---

## Rule 1 & 2 compliance: design exports

Every design edit is followed by an export in the same commit. Never hand-edit a design file: `.fig` is binary and changes only through OpenPencil (desktop app, `openpencil-mcp`, or the CLI) or Figma.

## Regenerating the export

`hack/design/fig-export.ts` regenerates the whole snapshot from the `.fig`: one standalone `.fig`, one JSON dump and one 2x PNG per screen, plus `screens.json`, and the same three files for each layer listed in `parts.json` (the dialogs and badges the browser visual diff compares against). `assets/design-export/MANIFEST.md` explains the layout and the file names.

It runs from a checkout of [ValgulNecron/open-pencil](https://github.com/ValgulNecron/open-pencil) `master` (`bun install`, then build `packages/pen` and `packages/core` with `bunx tsdown`); the script's header has the exact commands:

```sh
cp hack/design/fig-export.ts <open-pencil>/packages/cli/fig-export.ts
cd <open-pencil>/packages/cli
bun fig-export.ts <gameplane>/design.fig <gameplane>/assets/design-export
bun fig-export.ts <gameplane>/telemetry-receiver/telemetry-dashboard.fig <gameplane>/telemetry-receiver/design-export
bun fig-export.ts <gameplane>/website/website.fig <gameplane>/website/website-export   # commit inside the website submodule
```

- Always re-run the whole export. It is deterministic, so screens you did not touch come out byte-identical and only what changed shows in `git diff --stat`.
- A new screen gets a file named after its `.fig` ID (`1:232` → `1-232`); existing screens keep their file names through `screens.json`.
- When the visual diff needs a new layer below screen level, add `{"file": "<name>", "name": "<exact layer name>"}` to `parts.json`. A top-level layer with that name wins; otherwise the name must be unique outside instances, or the script stops.
- Check: open the changed PNGs, and grep the changed JSON for body text unique to the screen (not its frame name).
- Commit together: `git add design.fig assets/design-export` (or the telemetry or website pair), `git commit -s -m "design: …"`. Do not split the design change and its export into two commits.
