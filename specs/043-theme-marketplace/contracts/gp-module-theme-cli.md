# Contract: `gp-module theme` commands (spec 043)

Lives in the existing `gp-module` CLI (R9). Exit 0 on success, 1 on validation failure, 2 on usage error. `--json` prints machine-readable results for CI.

| Command | Does |
|---|---|
| `gp-module theme init <name> [--dark] [--light] [--from-preset pink\|legacy]` | scaffolds `<name>/theme.yaml` with tokens copied from a built-in preset and an empty `theme.css` |
| `gp-module theme from-export <export.json> --name <name> [--version 0.1.0]` | turns a spec 016 theme export into a package folder: custom colors expanded to the 14 tokens with the same derivation as the dashboard, custom CSS to `theme.css` |
| `gp-module theme validate <dir>` | runs every rule in theme-package.md; prints each failure with rule id and line; prints `lowContrast` as a warning |
| `gp-module theme package <dir> [--out <oci-layout-dir>] [--push <ref>]` | validates, then writes an OCI layout or pushes with the registry credentials from the Docker config |

Signing stays with `cosign sign` (as for modules); the official repo's workflow does it keyless.
