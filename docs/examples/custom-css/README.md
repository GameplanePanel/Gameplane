# Custom CSS overlay examples

Five example stylesheets for the Gameplane **Custom CSS overlay**
(Settings → Theme → Custom CSS overlay, feature 016), each showing a
different class of customization:

| File | What it demonstrates |
|---|---|
| `top-nav-blue.css` | **Layout change** — moves the desktop sidebar into a horizontal bar above the header, plus a blue accent |
| `phosphor-terminal.css` | **Full visual overhaul** — green-phosphor CRT: green palette, all-monospace type, square corners, scanlines + glow |
| `solarized.css` | **Pure palette re-theme** — Ethan Schoonover's Solarized palette in both modes, tokens only, no layout changes |
| `compact-density.css` | **Density change** — no colors at all; scales the whole UI down ~12.5% via the root font size |
| `high-contrast-focus.css` | **Accessibility layer** — stronger borders and muted text, a 3px global focus ring, underlined content links |

## Apply one

1. Open **Settings → Theme**.
2. Paste the file's contents into the Custom CSS overlay editor.
3. Enable the overlay and **Save**.

Only one overlay is active at a time — to combine ideas from several
examples, copy the sections you want into a single stylesheet.

## Revert / safe mode

- Turn the overlay off in Settings → Theme and Save again, or
- Append `?safe-mode=1` to any app URL — this suspends the overlay
  only; your saved stylesheet text is not deleted, or
- Press `Ctrl+Shift+Alt+T` while the app is open.

## Notes

- Selectors target ARIA landmarks, `data-testid` hooks, and HeroUI's
  stable semantic classes (`.button`, `.chip`, `.card`, ...) rather
  than Tailwind's generated utility classes, since those are an
  implementation detail. If the app's markup changes, these selectors
  may need to be updated to match.
- The overlay's `<style>` element is appended last in the document
  head and is unlayered, while the app's own Tailwind utility classes
  sit inside `@layer utilities` — an unlayered rule beats any
  `@layer` rule regardless of specificity or source order. That means
  the overlay's *layout* rules don't strictly need `!important` to win
  (examples still add it as a safety margin against future layering
  changes). Token blocks (`--accent`, `--link`, ...) are a different
  case: they do need `!important`, because they have to outrank the
  app's own preset token blocks (`.light`/`.dark`), the legacy-preset
  variant (`.light[data-theme-preset="legacy"]` /
  `.dark[data-theme-preset="legacy"]`, which has higher specificity
  than a plain `.light`/`.dark` selector regardless of source order),
  and — when a token is meant to override it — the Custom Colors
  overlay's own token block (see below).
- **Interaction with Custom Colors mode:** when a user has picked
  **Custom Colors** in Settings → Theme, their accent/surface picks
  are written to the `gameplane-custom-theme-vars` style element under
  the selector `html[data-theme-type="custom_colors"][data-theme]`
  (`web/src/lib/theme-derivation.ts`), weighted to (0,2,1) — enough to
  outrank the (0,2,0) legacy-preset token blocks — and *not*
  `!important`. An `!important` declaration always beats a
  non-`!important` one regardless of selector specificity or source
  order — so an example's `!important` token blocks (as in
  `top-nav-blue.css`) win over a user's Custom Colors choice for every
  token they set, while Custom Colors keeps control of every token an
  example leaves alone (e.g. `compact-density.css` and
  `high-contrast-focus.css` leave accent/surface tokens untouched and
  compose with Custom Colors freely). A stylesheet meant to *respect*
  Custom Colors for a token it does set should drop `!important` from
  its token blocks and *exclude* Custom Colors mode instead of
  targeting it, e.g. `html:not([data-theme-type="custom_colors"]).light
  { --accent: …; }` (and the matching `.dark` rule) — at (0,2,1) that
  selector still beats the (0,2,0) legacy/plain preset blocks, but it
  simply doesn't match while Custom Colors is active, so the cascade
  falls through to the user's picks. Do **not** scope such tokens under
  `html[data-theme-type="custom_colors"][data-theme]` itself: the
  overlay's `<style>` element is mounted last in `<head>`, so at equal
  specificity it would win the tie and override the user's picks
  instead of deferring to them.

### top-nav-blue.css specifics

1. Moves the desktop sidebar into a horizontal bar above the header
   (nav groups laid out in a row, group labels compacted, footer/user
   controls kept usable) instead of a fixed left column. The layout
   rules are gated to desktop widths (`min-width: 1024px`) and scoped
   under `[data-testid="app-shell-sidebar"]` — AppShell's desktop
   sidebar wrapper (`web/src/components/ui/AppShell.tsx`) — before
   matching `aside[aria-label="Sidebar"]`. The mobile off-canvas drawer
   reuses the same `aside[aria-label="Sidebar"]` landmark, but its variant
   adds a close button, different width classes, and a
   `nav[aria-label="Mobile navigation"]` label instead of
   `nav[aria-label="Primary"]`. However, `AppLayout.tsx` renders the drawer
   outside the `[data-testid="app-shell-sidebar"]` wrapper, so the scoped
   layout rules never match it — the drawer remains unaffected.
2. Re-themes the accent color to blue (buttons, active nav item, focus
   ring) and the link color to match, for both light and dark mode,
   keeping WCAG AA text contrast on the accent color.

### phosphor-terminal.css specifics

1. Rebinds every semantic token (`--background`, `--surface*`,
   `--accent*`, `--border`, `--link`, ...) to a monochrome
   green-phosphor scale in both light and dark mode — including the
   legacy-preset selector variants and the legacy `--gp-*` HSL
   triplets. The `--success` family shifts to cyan so success states
   stay distinguishable from the green accent.
2. Rebinds `--font-sans` to the JetBrains Mono stack the app already
   loads for `--font-mono`, so the whole UI renders monospace.
3. Flattens corners to 2px on buttons, chips, cards, dialogs,
   popovers, tabs, inputs and tables — the app pins per-family radii
   at the component selector, so this needs its own component-level
   rules, not just `--radius`.
4. Dark mode only: a fixed, `pointer-events: none` scanline overlay
   (`body::after`, faint 1px lines every 3px) and a soft green
   `box-shadow` glow on `.button[data-variant="primary"]`, disabled
   under `prefers-reduced-motion`.

### solarized.css specifics

- Tokens only — no layout, spacing, typography, or shape rules — so it
  is the smallest template to copy for a "just recolor the app" theme.
- Maps the full status palette, not just the accent: cyan accent,
  blue links, green success, yellow warning, red danger, violet
  sleep/stat — each from the canonical Solarized hues.
- Covers both modes and both presets (plain and legacy selectors), so
  it applies no matter which base theme is active underneath.

### compact-density.css specifics

1. Sets `html { font-size: 14px }` (from the 16px default). Tailwind
   v4's spacing and type utilities are rem-based, so padding, gaps and
   font sizes all scale down proportionally from this one lever.
2. Re-pins the few px-based spots that don't follow (modal dialog
   padding/gaps and dialog field heights from
   `web/src/styles/globals.css`).
- Sets no color tokens at all, so it composes with either preset and
  with Custom Colors.

### high-contrast-focus.css specifics

1. Retints only `--border`, `--separator`, `--field-border` and
   `--muted` in both modes (plus the legacy-preset variants) — the
   accent and surfaces stay whatever the base theme says.
2. Adds a 3px `outline` focus ring with a 2px offset on every
   focusable element (using `outline`, not `box-shadow`, so it still
   renders under forced-colors), plus a matching 3px ring on react-aria
   focused table rows and a 2px inset ring on dialog inputs, whose
   outer outline would be clipped by the modal body.
3. Underlines text links inside `<main>` only, excluding button-styled
   anchors and navigation.
