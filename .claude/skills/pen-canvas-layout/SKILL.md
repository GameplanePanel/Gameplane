---
name: pen-canvas-layout
description: "How design.pen (and the other .pen files) is organised on the canvas: components on boards at the top, one row per page below in app navigation order, variants left to right. Use whenever you add, move or rename a screen, component or note in a .pen file, or when the canvas looks disorganised."
---

## Scope

- Binding for `design.pen` (dashboard). `website/website.pen` and `telemetry-receiver/telemetry-dashboard.pen`
  follow the same two-zone principle (components on top, one row per page, variants to the right), with
  their own page order (site navigation / dashboard views). The canonical row list below is for `design.pen` only.
- All access through the Pencil MCP (`mcp__pencil__execute`). Never Read, grep or edit `.pen` files (CLAUDE.md core rule 2).
- Pencil does not autosave. Ask the user to save in the Pencil UI before committing.

## Two zones

1. **Component band** (top of the canvas, y starts at 0):
   - `HeroUI: Design System Components` board (`LtgNm`) at x=0, y=0. It is the imported HeroUI library and is not reorganised.
   - `Gameplane: App Components` board directly to its right (x = LtgNm width + 400, y = 0).
   - A note `Shared Components Band` sits 400 px above the Gameplane board.
2. **Screen rows** below the band: the first row starts 1200 px below the bottom of the taller board.

## Component boards

- Every reusable node (`reusable: true`) lives on a board. Never leave a component loose at the document root.
- Boards hold only components and their label text nodes. A board's name must end in `Components`
  (`check-layout.js` and the componentize audit find boards by that suffix). The imported HeroUI board also keeps the library's own example frames (`ex1`, `tableEx`, `Modal Example`, …); `check-layout.js` skips that rule for boards whose name starts with `HeroUI`.
- Gameplane board sections, in this order: Shell & navigation; Login & public pages; Buttons & form controls;
  Cards, rows & stats; Status rows, badges & chips; Banners; Menus, popovers & panels; Modals & dialogs;
  Page blocks (larger page sections reused across screens, such as dashboard cards, module cards and wizard panels).
- Section label: text node named `Section · <name>`, Inter 11 / weight 500, fill `$muted`, at x=160.
- Components are packed left to right 20 px below the label, 48 px apart, wrapping at 4400 px of content
  width. The next section label starts 100 px below the section's tallest line. Board padding 160.
- The Gameplane board's fill is transparent (`#00000000`) so components show against the canvas.
- Board title text `Gameplane App Components` (Inter 20/600, `$foreground/foreground`) at (160, 87), with a
  subtitle at (160, 117).
- To add a component: build it, `Move(id, boardId)`, then `Update(id, {x, y})` into its section. If the
  section grows, move the sections below it down and grow the board with `Update(boardId, {height})`.

## Screen rows

- One row per page or surface: a page, a server-detail tab, a server-settings section, an admin-settings
  section, a wizard step, or a multi-step dialog flow.
- Row label: a `note` at x = -1400, same y as the row, 1000 x 240, fontSize 48, named `Row NN · <label>`,
  with content `NN · <label>` (two-digit number). Rows are numbered 01.. top to bottom with no gaps.
- Items start at x = 0, top-aligned to the row's y, with a 200 px horizontal gap. The next row starts 400 px
  below the bottom of the tallest item of the row above, including notes under screens.
- Left-to-right order inside a row:
  1. base screen (dark)
  2. `(Light)` variant
  3. states, errors, empty states
  4. filters-open / dialog-open variants
  5. Standalone and Mobile variants
  6. dialog-state frames (non-reusable instances of a dialog showing one state, e.g. `Gameplane/Dialog/New File (Invalid name)`)
  7. reference / state boards (`State/…`, `States/…`, `… (reference)`)
  8. `Historical/…` frames last
- Annotation notes go directly below the screen they describe, with an 80 px gap. Stack them if there are several.
- Nothing else at the document root: no loose text, icons, buttons or sample instances. Anything without a
  home goes into the last row, `Unsorted — needs a home`, and is reported to the user.

## Canonical row order (design.pen)

Rows follow the dashboard's navigation. Source of truth: `web/src/components/AppLayout.tsx` (sidebar),
`web/src/routes/ServerDetail.tsx` (tabs), `web/src/routes/tabs/Settings.tsx` `SECTIONS` (server settings),
`web/src/components/ui/SettingsNav.tsx` (admin settings). When the app's order changes, the rows follow it.

Current order. Every page, tab, section or step named here is its own row, so the canvas has more rows than this list has lines:

1. Login · App loading
2. Dashboard
3. Servers
4. Create server · Step 1 Template … Step 5 Review (one row per step)
5. Server detail tabs: Overview, Events, Console, Logs, Files, Mods, Modpacks, Players, Backups, Capture
6. Server settings sections: General, Version, Game configuration, Resources, Networking, Environment, Lifecycle, Scheduled backups, Network capture, Placement, RBAC & access, Share links, Danger zone
7. Modules · Modules · Build module dialog
8. Backups · Index, Schedules, Restores
9. Clusters · Cluster settings
10. Users & RBAC · Users, Roles, Service accounts, Identity providers
11. Audit log · System logs
12. Admin settings: General, Theme, Authentication, Backup destinations, Module sources, Mod registries, Notifications, Telemetry, Updates, About
13. Share link (public) · Not found
14. Unsorted — needs a home (should normally be empty)

The live canvas row label notes are the authoritative list of row ids. Do not copy node ids into docs.

## Procedures

Each procedure runs as an `mcp__pencil__execute` snippet. Include the snippets verbatim.

**List root nodes safely.** The visitor form of `Get` crashes inside some subtrees (see
`docs/design/pencil-visitor-crash-report.md`), so always call `c.skipChildren()` at the root:

```js
Get(document,(n,c)=>{if(!c.parentCtx&&n.id!==document){Print(n.id,n.type,n.name,Math.round(c.bounds.x),Math.round(c.bounds.y),Math.round(c.bounds.width),Math.round(c.bounds.height));c.skipChildren()}})
```

**A. Add a variant to an existing row.** Set x = rightmost item's x + width + 200 and y = the row's y. If the new
frame is taller than the row's tallest item, shift everything below down by the difference:

```js
const BELOW_Y=<row y>,DY=<pixels>;
const ids=[];Get(document,(n,c)=>{if(!c.parentCtx&&n.id!==document){if(c.bounds.y>BELOW_Y)ids.push([n.id,c.bounds.x,c.bounds.y]);c.skipChildren()}});
ids.forEach(([id,x,y])=>Update(id,{x,y:y+DY}))
```

**B. Add a new page row.** Pick its position from the navigation order. Shift everything below it down by
(new row height + 400) with the snippet above. Insert the label note, then renumber all row labels:

```js
const L=[];Get(document,(n,c)=>{if(!c.parentCtx&&n.id!==document){if(n.type==="note"&&/^Row \d\d · /.test(n.name||""))L.push({id:n.id,y:c.bounds.y,rest:n.name.replace(/^Row \d\d · /,"")});c.skipChildren()}});
L.sort((a,b)=>a.y-b.y).forEach((l,i)=>{const k=String(i+1).padStart(2,"0")+" · "+l.rest;Update(l.id,{name:"Row "+k,content:k})})
```

**C. Add a component.** See "Component boards" above.

**D. Verify.** Pass the full content of `check-layout.js` (next to this file) as the `input` of
`mcp__pencil__execute`, with `filePath` set to the `.pen` file. It is read-only and must end with
`NO ISSUES`. It checks:
- no loose components at the root
- boards hold only components and labels (HeroUI library examples excepted)
- no overlapping root nodes
- every root node sits in a row
- rows are numbered 01.. without gaps
- items are top-aligned at x = 0 with 200 px gaps
- at least 400 px between rows

**E. Exports.** Moving a node changes `x`/`y` in its `assets/design-export/json/<id>.json`
(`website/website-export/json/` for the site). Patch only those two values, in the same commit as the
`.pen` change (design-export skill). Screenshots do not change when a node only moves. Record the move in
`assets/design-export/MANIFEST.md`.

## When the canvas is disorganised

1. Re-run `check-layout.js`.
2. Fix each reported issue with the procedures above.
3. Re-run until the output is `NO ISSUES`.

Do not rename or restructure screens while reorganising. A move changes only `x`, `y` and (for components) the parent board.
