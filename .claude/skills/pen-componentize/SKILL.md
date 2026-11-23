---
name: pen-componentize
description: "Run when a design pass in design.fig (or website/website.fig) is finished, before design-export and commit: finds UI that recreates an existing component or repeats across screens, promotes repeated parts to reusable components and replaces every copy with an instance. Also use when asked to 'make everything a component' or to check that screens reuse components."
---

## Componentize design screens

All `.fig` access goes through OpenPencil (`openpencil eval <file.fig> --stdin` for read-only scripts, `eval -w` to write; Figma plugin API) or Figma. Never read or edit a `.fig` file directly (CLAUDE.md core rule 2).

### Goal

- Every repeated UI part is a reusable component on a components board (see the `pen-canvas-layout` skill for where components live). Every screen uses instances (`INSTANCE` nodes) of it.
- No screen contains a detached copy of an existing component, a hand-built button/chip/input where a HeroUI or Gameplane component exists, or an instance whose content is replaced wholesale.

### When

- At the end of every design change, before the `design-export` skill runs and before committing.
- On request ("componentize", "make everything a component", "check component reuse").

### Step 1 — Audit (read-only)

Write one read-only `openpencil eval` script that walks every screen (`figma.currentPage.children`, skipping boards and notes) and prints these six sections:

1. **Detached copies of existing components** — a plain frame whose texts cover at least 75% of a component's texts (and at least half of the frame's texts come from that component). Components with fewer than 3 texts are not text-matchable.
2. **Frames named like a Gameplane component but not instances** — e.g. a frame called `App Sidebar`.
3. **Detached instances** — frames that `detachInstance()` left behind, or instances whose overridden content no longer resembles their component. Table cells and card content holding placed content are normal use; triage them as intentional.
4. **Missing expected shell components** — app screens without App Sidebar / Top Bar; server-detail screens without Server Detail Header / Server Detail Tabs; server settings without Server Settings Sub Nav; admin settings without Admin Settings Nav; mobile screens without Mobile TopBar. Count direct instances only and confirm before acting.
5. **Hand-built control-like frames** — pill-shaped frames (corner radius, fill or stroke, padding, 1–3 text/icon children) that are not Button/Chip instances.
6. **Repeated structures across screens** — identical frame structures (same node types and nesting, texts and names ignored) with at least 6 descendants, found on at least 3 screens; list only the outermost one. These are the candidates for new components.

### Step 2 — Triage

- Look at each finding in its screen's screenshot before deciding. The audit is heuristic: legitimate one-off layouts are left alone and listed as intentional in the report.
- Findings in screens outside the current change: list them and ask the user before touching those screens (design changes need human sign-off, CLAUDE.md "System prompt overrides" 1).

### Step 3 — Fix (one `eval -w` script per batch)

- **Detached copy of an existing component** → `component.createInstance()`, insert it at the copy's index in the copy's parent, carry every visible difference as overrides on the instance's layers (text, visibility, fills), then `remove()` the copy.
- **Repeated structure** → `figma.createComponentFromNode(cleanOccurrence)`, move the component onto the Gameplane board (`Page blocks` section for page-level sections), and replace every other occurrence with an instance carrying its differences. Promote only occurrences that are the same UI element: the match is structural, so a "Recent activity" card can match a "Recent backups" card with the same layout. Do a dry run first that only prints the occurrences.
- **Hand-built button, chip or input** → replace with the matching HeroUI or Gameplane component (variant and size as close as possible). Chrome follows the component; content must not change: same label and icon (hide an icon the original did not show), monospace stays monospace for code-like values, and when the component and the original disagree on content, check the app code in `web/src/` and fix whichever side is wrong.
- **Missing shell** → replace the hand-built sidebar/top bar/header/tabs/nav with the shell instance.
- Prefer making an existing component more generic (overrides, variants) over creating a near-duplicate component.

### Step 4 — Keep visuals identical

- Before changing anything, take the committed `assets/design-export/` as the baseline (re-run `hack/design/fig-export.ts` first if it may be stale; it renders only changed screens).
- After the change, copy `assets/design-export/` to a scratch folder, export into that copy (only the changed screens are rendered) and compare each changed screen's PNG pair. They must match, except for intended corrections, which are listed in the report:
  - an outdated copy now shows the component's current items;
  - control chrome now follows the design-system component;
  - a component was corrected to match the app code.
- Check the `(Light)` variant too when the screen has one.
- To list content differences without pictures, diff the screen's JSON from both exports (texts, fonts, fills, visibility).

### Step 5 — Re-audit and finish

- Re-run the audit until every remaining finding is triaged as intentional.
- Check that no instance still shows its component's default content: list visible texts that read exactly `Button`, `Label` or `Placeholder` and replace each with the label or placeholder the app uses (`web/src/`).
- Run `check-layout.fig.js` from the `pen-canvas-layout` skill (new components must sit on the board).
- Run the `design-export` skill and commit the export with the design change.

### Delegation (CLAUDE.md core rules 13, 17, 18)

- One scout runs the audit and writes the change list (screen id, node id, component id, overrides).
- Fix agents start at `haiku` and apply the change list blind, one batch of screens each; batches must not share screens. A `sonnet` reviewer compares before/after screenshots for the whole batch.
