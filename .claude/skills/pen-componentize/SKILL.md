---
name: pen-componentize
description: "Run when a design pass in design.pen (or website/website.pen) is finished, before design-export and commit: finds UI that recreates an existing component or repeats across screens, promotes repeated parts to reusable components and replaces every copy with an instance. Also use when asked to 'make everything a component' or to check that screens reuse components."
---

## Componentize .pen screens

### Goal

- Every repeated UI part is a reusable component on a components board (see the `pen-canvas-layout` skill for where components live). Every screen uses instances (`type: "ref"`) of it.
- No screen contains a detached copy of an existing component, a hand-built button/chip/input where a HeroUI or Gameplane component exists, or an instance whose content is replaced wholesale.

### When

- At the end of every design change, before the `design-export` skill runs and before committing.
- On request ("componentize", "make everything a component", "check component reuse").

### Step 1 — Audit (read-only)

- Pass the full content of `audit.js` (next to this file) as the `input` of `mcp__pencil__execute`, with `filePath` set to the `.pen` file. It reads every screen with `Get(id, {depth: 80})` (the non-visitor form; the visitor form crashes on some subtrees, see `docs/design/pencil-visitor-crash-report.md`) and prints six sections:
  1. **Detached copies of existing components** — a plain frame whose texts cover at least 75% of a component's texts (and at least half of the frame's texts come from that component). Components with fewer than 3 texts are not text-matchable.
  2. **Frames named like a Gameplane component but not instances** — e.g. a frame called `App Sidebar`.
  3. **Instances whose content is replaced wholesale** — a `ref` carrying a literal `children` array although its component is not a slot. Slot components (`slot: []` on the root, e.g. `Table`, `Card/Default`, `Tabs`) and empty containers are filled exactly this way, so refs to them are not reported. The audit still lists `Table Cell` and `Card Content` instances that hold placed content; that is normal use, so triage them as intentional. The visitor form of `Get` crashes on both shapes.
  4. **Missing expected shell components** — app screens without App Sidebar (`kKFX9`) / Top Bar (`gu5WY`); server-detail screens without Server Detail Header (`S4k0x`) / Server Detail Tabs (`I9kvlZ`); server settings without Server Settings Sub Nav (`EV8mp`); admin settings without Admin Settings Nav (`Mp7Ep`); mobile screens without Mobile TopBar (`fGbVF`). Only direct instances are counted, so a shell nested inside another component shows up as missing; confirm before acting.
  5. **Hand-built control-like frames** — pill-shaped frames (corner radius, fill or stroke, padding, 1–3 text/icon children) that are not Button/Chip instances.
  6. **Repeated structures across screens** — identical frame structures (same node types and nesting, texts and names ignored) with at least 6 descendants, found on at least 3 screens; only the outermost repeated structure is listed. These are the candidates for new components.
- Tunables at the top of `audit.js`: `MIN_TEXTS`, `COVER`, `PURITY`, `REPEAT_SCREENS`, `REPEAT_DESC`, `CAP` (lines printed per section).

### Step 2 — Triage

- Screenshot each finding (`TakeScreenshot([id])`) before deciding. The audit is heuristic: legitimate one-off layouts are left alone and listed as intentional in the report.
- Findings in screens outside the current change: list them and ask the user before touching those screens (design changes need human sign-off, CLAUDE.md "System prompt overrides" 1).

### Step 3 — Fix

- **Detached copy of an existing component** → insert an instance of the component in the copy's parent, move it to the copy's index (`Move(newId, parentId, index)`), carry every visible difference as `descendants` overrides (keys are the component's child ids), then delete the copy.
- **Repeated structure** → promote it with `promote.js` (next to this file). Set `PROMO` at the top: `name` (`Gameplane/<Name>`), `source` (id of one clean occurrence) and `dryRun: true`. The dry run prints every occurrence (`OCC <screen> <id> '<name>' :: <first texts>`) and changes nothing.
  - Promote only occurrences that are the same UI element. The match is structural, so a "Recent activity" card can match a "Recent backups" card that has the same layout. Put every occurrence that means something else in `exclude`.
  - Then run with `dryRun: false`. The script copies the source onto the Gameplane board as a reusable component and replaces each occurrence with an instance that carries the differences as overrides. It compares the resolved instance with the original and keeps the original whenever they differ (reported as `kept: … VERIFY …` or `cannot unset …`). Afterwards, move the new component into its board section (`Page blocks` for page-level sections).
- **Hand-built button, chip or input** → replace with the matching HeroUI or Gameplane component (variant and size as close as possible: `Button/<Variant>/<SM|MD|LG>`, `Chip/<Style>/<Tone>/<Size>`, `Gameplane/Button/...`, `Gameplane/Input`, `Gameplane/Select`). Chrome follows the component (radius, padding, background, label size and weight). Content must not change:
  - Same label and icon. An icon that the original hid (`enabled: false`) or did not have gets `enabled: false` on the instance.
  - Monospace stays monospace. A chip or pill that shows a code-like value (group names, status codes, versions, ids) keeps `fontFamily: "$typography/font-mono"` as an override when the component uses the sans font.
  - When the component and the original disagree on content (for example a banner component without the icon the screen shows), check the app code in `web/src/` and fix whichever side is wrong. Fix the component itself when it is the one that differs from the code.
- **Ref with literal children on a non-slot component** → rebuild as a proper instance: property overrides via `descendants`, or a slot replacement (`descendants: {<childId>: {type: ..., ...}}`) when the content really differs.
- **Missing shell** → replace the hand-built sidebar/top bar/header/tabs/nav with the shell instance.
- Prefer making an existing component more generic (overrides) over creating a near-duplicate component.
- Never `Update` descendants of a node you just copied (new ids); use `descendants` in the `Copy` call or read the copy with `Get` first.
- To override one property inside an existing instance, update its path: `Update("<instanceId>/<componentChildId>", {enabled: false})`. The override merges into the instance's `descendants`.
- `Get` on an id that no longer exists aborts the whole `execute` call, and `try/catch` does not catch it. In scripts that look up many ids, read each screen once with `Get(screenId, {depth: 80})`, index its nodes by id and look ids up in that index.

### Step 4 — Keep visuals identical

- Before changing anything, `Export` every screen you will touch to a scratch folder (`{scale: 1}`). `mcp__pencil__execute` always works on the document open in Pencil, so a copy of the committed `.pen` file cannot be rendered as a baseline. If the pre-change renders are lost, compare with `assets/design-export/screenshots/<id>.png` (2x). Those exports can be older than the current shell components (sidebar, top bar, server header), so ignore differences in the shell.
- After the change, export again and compare each pair. They must match, except for intended corrections, which are listed in the report:
  - an outdated copy now shows the component's current items;
  - control chrome now follows the design-system component;
  - a component was corrected to match the app code.
- Check the `(Light)` variant too when the screen has one.
- To list content differences without pictures, compare each replaced control's original (from the pre-change `assets/design-export/json/<screen>.json`, matched by parent id and child index) with the resolved instance (`Get(id, {resolveInstances: true})`). Compare label text, text fill, font family/size/weight, and icon visibility and name.

### Step 5 — Re-audit and finish

- Re-run `audit.js` until every remaining finding is triaged as intentional.
- Check that no instance still shows its component's default content. Read each screen with `Get(id, {depth: 80, resolveInstances: true})` and list visible texts that read exactly `Button`, `Label` or `Placeholder`. Replace each one with the label or placeholder the app uses (`web/src/`).
- Run `check-layout.js` from the `pen-canvas-layout` skill (new components must sit on the board).
- Ask the user to save in the Pencil UI, then run the `design-export` skill for every touched screen and component (JSON + PNG + MANIFEST entry) in the same commit as the `.pen` change.

### Delegation (CLAUDE.md core rules 13, 17, 18)

- One scout runs the audit and writes the change list (screen id, node id, component id, overrides).
- Fix agents start at `haiku` and apply the change list blind, one batch of screens each; batches must not share screens. A `sonnet` reviewer compares before/after screenshots for the whole batch.
