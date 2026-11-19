---
name: design-export
description: "After ANY edit to design.pen or website/website.pen — re-exports touched Pencil nodes to the in-git snapshot. A design edit is not finished until this runs; the export lands in the SAME commit as the design change."
---

## Rule 1 & 2 compliance: design exports

Every edit to `design.pen` (dashboard) or `website/website.pen` (public site) must be followed by incremental export of only the touched nodes. Never hand-edit `.pen` files; all access goes through Pencil, by either of the two paths below.

### Two ways to reach Pencil

Both paths run the same `execute` language (`Get`, `Insert`, `Update`, `Export`, …) and produce identical exports, so use whichever the session has:

| | Pencil MCP (local) | Pen CLI (cloud or local) |
|---|---|---|
| Available when | `mcp__pencil__*` tools exist (the maintainer's machine with the Pencil app) | `pen` on PATH (`npm install -g @pen.dev/cli`) and `PEN_CLI_KEY` set (cloud project setting). Never print the key. |
| Run code | `mcp__pencil__execute` with `filePath` | `printf '%s\n' 'execute({ input: "…" })' 'save()' 'exit()' \| pen interactive -i <file.pen> -o <file.pen>` (headless; no AI calls) |
| Screenshot | `mcp__pencil__export_nodes` (2x) | `Export(["<id>"], "png", "<dir>")` inside `execute` (2x by default) |
| Saving | Pencil does not autosave: ask the maintainer to save in the Pencil UI | `save()` writes the `-o` file; nothing else touches the file |

The pen.dev free plan caps agent design at 5 active days a month, shared by the local MCP and the CLI on the same account (any number of calls in one day counts once), so batch design work into as few days as you can. With the CLI, edit a copy first when you are unsure (`-o /tmp/out.pen`), check it, then rerun against the real file. `-o` must equal `-i` to change the committed file in place. Check with `pen status` that the key is active.

For exports from the CLI, use the helper next to this file. It writes the JSON and the 2x PNG for each node and never writes the `.pen` file. Its JSON is byte-identical to the MCP export:

```sh
.claude/skills/design-export/pen-cli-export.sh design.pen assets/design-export/json assets/design-export/screenshots <id> [<id>…] [--depth N]
# website: website/website.pen website/website-export/json website/website-export/screenshots
```

### When to run this skill

- After editing any screen or component in `design.pen` or `website/website.pen` via the Pencil editor.
- Before committing the `.pen` change.
- As part of the same commit as the design change (export step is not a separate follow-up commit).

### Pre-export checklist

1. **Make sure the file on disk is saved.** Pencil MCP: Pencil does not autosave, so ask the user to save the design file in the GUI before exporting. Pen CLI: the edit run must have ended with `save()`.
2. **Collect touched node IDs.** Identify which screens and components were added or modified. Node IDs are visible in Pencil's node inspector or in the Get() output.
3. **Verify against MANIFEST.md.** Check `assets/design-export/MANIFEST.md` to see whether any existing node needs its export refreshed (e.g., a re-exported variant of a screen).

### Export procedure

For each touched node ID:

1. **JSON export** — via `mcp__pencil__execute` running `Get("<id>", {depth: N})` (MCP), or `pen-cli-export.sh` (CLI):
   - Choose depth high enough that the node's complete structure comes through with zero `"..."` elision markers.
   - Pipe output to `assets/design-export/json/<id>.json` (website screens → `website/website-export/json/<id>.json`).
   - Validation: `python3 -m json.tool assets/design-export/json/<id>.json > /dev/null` (or `website/website-export/json/<id>.json` for website screens) must pass.

2. **Screenshot export** — via `mcp__pencil__export_nodes` (MCP), or `pen-cli-export.sh` / `Export(["<id>"], "png", dir)` (CLI):
   - Export the node ID at 2x scale.
   - Write to `assets/design-export/screenshots/<id>.png` (website → `website/website-export/screenshots/<id>.png`).
   - Validation: file must be non-empty with real pixel dimensions.

3. **Content validation** — grep for a string unique to that node's body text (never in a frame name):
   - Screen text, component label, or dialog copy — something that only appears in that object's own design content.
   - Example: a new "Capture" screen must grep for `"not enabled"` (its body text), not `"Capture"` (which is in every capture-related frame's *name*).
   - All grep hits must come from the relevant `<id>.json` file, nowhere else in the export.

### Incremental export — touch only what changed

- Export only the nodes you added or edited — do NOT re-run a full export of every screen and component unless renames or structural changes make wider staleness likely.
- If a re-exported node is a variant of an existing screen (e.g., "Admin Settings — Authentication (Save rejected)"), add a row to `assets/design-export/MANIFEST.md` under an "Incremental export" section documenting the date, nodes touched, what changed, and export method/validation used.
- Keep the MANIFEST always in git so the next session can see what was exported when and why.

### Same-commit rule

The export MUST land in the same commit as the `.pen` file change:

```sh
# For dashboard (design.pen) exports:
git add design.pen assets/design-export/json/<id>.json assets/design-export/screenshots/<id>.png assets/design-export/MANIFEST.md

# For website (website/website.pen) exports:
git add website/website.pen website/website-export/json/<id>.json website/website-export/screenshots/<id>.png assets/design-export/MANIFEST.md

git commit -s -m "design: <brief description of change>"
```

Do NOT split into two commits (one for .pen, one for export).

### Lessons from prior passes

- **Keyword checks must match content, not frame names.** A grep for "Capture" passes even on a hollow JSON file if the frame is named `Screen/...Capture...` — grep for the screen's unique *body text* instead.
- **Depth matters.** If a JSON export still contains `"..."` elision markers, the depth was too shallow — re-export at a higher depth until zero elisions appear.
- **Verify after extract.** Subagents may self-report success (100% completion) while still corrupting output — always re-load() exported JSON files and verify dimension/pixel counts on PNGs.
- **`includePathGeometry: true` for sparklines/paths.** If the JSON file contains vector path nodes, use `{depth: N, includePathGeometry: true}` to avoid `"geometry": "..."` elisions.
