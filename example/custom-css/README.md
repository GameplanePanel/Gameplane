# Custom CSS overlay examples

Example stylesheets for the Gameplane **Custom CSS overlay** (Settings → Theme → Custom CSS overlay, feature 016), each showing a different class of customization:

| File | What it demonstrates |
|---|---|
| `top-nav-blue.css` | **Layout change** — moves the desktop sidebar into a horizontal bar above the header, plus a blue accent |
| `sidebar-right.css` | **Layout change** — moves the desktop sidebar to the right edge, no color changes |
| `dashboard-bento.css` | **Dashboard layout** — asymmetric 12-column layout for metrics and fleet panels |
| `phosphor-terminal.css` | **Full visual overhaul** — green-phosphor CRT: green palette, all-monospace type, square corners, scanlines + glow |
| `cyberpunk-neon.css` | **Full visual overhaul** — neon magenta/cyan on near-black, with restrained dark-mode glow effects |
| `solarized.css` | **Pure palette re-theme** — Solarized palette in both modes, tokens only, no layout changes |
| `nord.css` | **Pure palette re-theme** — Nord (Snow Storm light / Polar Night dark), tokens only |
| `dracula.css` | **Pure palette re-theme** — Dracula dark plus a lightened "paper" light mode, tokens only |
| `catppuccin.css` | **Pure palette re-theme** — Catppuccin Latte (light) / Mocha (dark), tokens only |
| `gruvbox.css` | **Pure palette re-theme** — Gruvbox warm retro in both modes, tokens only |
| `ocean-deep.css` | **Pure palette re-theme** — dark-first abyssal navy with cyan accent, tokens only |
| `pastel-sakura.css` | **Pure palette re-theme** — light-first soft cherry-blossom pastel, tokens only |
| `sepia-paper.css` | **Pure palette re-theme** — e-reader warm paper; pairs well with `elegant-serif.css` |
| `arcade-neon.css` | **Palette re-theme** — electric violet with cyan focus, in both appearance modes |
| `deep-ocean.css` | **Palette re-theme** — midnight blue surfaces and sea-glass teal |
| `forest-camp.css` | **Palette re-theme** — pine, fern, and natural warmth |
| `arctic-blue.css` | **Palette re-theme** — cool slate surfaces and glacial blue |
| `ember-forge.css` | **Palette and shape** — charcoal, kiln orange, and squared corners |
| `lavender-dream.css` | **Palette and shape** — lilac surfaces, violet controls, and generous corners |
| `paper-map.css` | **Palette and shape** — parchment surfaces, ink, and compact corners |
| `terminal-green.css` | **Palette, type, and shape** — phosphor green, monospace, and compact corners |
| `rounded-playful.css` | **Shape change** — maximum roundness (pill buttons, 20px cards), no color changes |
| `brutalist.css` | **Shape and border change** — square corners, 2px borders, hard offset shadows |
| `glass-observatory.css` | **Material treatment** — translucent panels, blur, and atmospheric light |
| `blueprint-board.css` | **Surface treatment** — drafting grid, ruled panels, and dashed focus rings |
| `soft-3d-controls.css` | **Component treatment** — raised cards, inset fields, and tactile button presses |
| `flat-workbench.css` | **Structural treatment** — removes card boxes and organizes content with dividers |
| `elegant-serif.css` | **Typography change** — serif body text via `--font-sans`, no color changes; pairs with `sepia-paper.css` |
| `compact-density.css` | **Density change** — no colors at all; scales the whole UI down via the root font size |
| `large-type.css` | **Density change** — the inverse: scales the whole UI up via the root font size |
| `kinetic-hover.css` | **Interaction treatment** — responsive hover and press feedback with reduced-motion support |
| `no-motion.css` | **Motion control** — near-zeroes all transitions/animations app-wide; the minimal example |
| `high-contrast-focus.css` | **Accessibility layer** — stronger borders and muted text, a global focus ring, underlined content links |

## Apply one

1. Open **Settings → Theme**.
2. Paste one file's contents into the Custom CSS overlay editor.
3. Enable the overlay and **Save**.

Only one overlay is active at a time. To combine ideas from several examples, copy the sections you want into a single stylesheet.

## Revert / safe mode

- Turn the overlay off in Settings → Theme and save again, or
- Append `?safe-mode=1` to any app URL to suspend the overlay for that session, or
- Press `Ctrl+Shift+Alt+T` while the app is open.

## Notes

- Selectors target ARIA landmarks, `data-testid` hooks, and HeroUI's stable semantic classes rather than Tailwind's generated utility classes, since those are an implementation detail. If the app's markup changes, those selectors may need updating.
- The overlay's `<style>` element is appended last in the document head and is unlayered, while the app's own Tailwind utility classes sit inside `@layer utilities`. An unlayered rule beats any `@layer` rule regardless of specificity or source order. Token blocks need enough specificity to override the app's preset token blocks and, when intended, the Custom Colors tokens.
- Palette examples deliberately override the active accent and surface choices. Layout, density, and accessibility examples can be combined with a palette example by copying their relevant rules into one stylesheet.

### Layout examples

`top-nav-blue.css` moves the desktop sidebar into a horizontal bar above the header. Its layout rules are gated to desktop widths (`min-width: 1024px`) and scoped under `[data-testid="app-shell-sidebar"]`, AppShell's desktop sidebar wrapper. The mobile off-canvas drawer is outside that wrapper and remains unaffected. It also re-themes the accent and links to blue in both modes.

### Full visual overhaul

`phosphor-terminal.css` rebinds the semantic palette to green phosphor in both modes, switches the interface to a monospace stack, flattens component corners, and adds a subtle dark-mode scanline treatment and button glow. The success family shifts to cyan to stay distinct from the green accent.

### Pure palette example

`solarized.css` changes semantic tokens only: it leaves layout, spacing, typography, and shape untouched. It covers status colors as well as the accent and supports both modes and both presets.

### Density example

`compact-density.css` lowers the root font size to scale rem-based spacing and type down, then adjusts a few pixel-based modal measurements. It sets no color tokens, so it can compose with another palette and with Custom Colors.

### Accessibility example

`high-contrast-focus.css` strengthens borders and muted text without changing the accent or surfaces. It adds a visible focus ring and underlines content links while excluding button-like anchors and navigation.
