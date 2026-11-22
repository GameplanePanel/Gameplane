# Contract: dashboard MCP tokens (design first in design.fig)

## Personal page `/settings/mcp-tokens` ("MCP tokens")

- Reached from the user menu, next to Theme settings. Visible to every signed-in user allowed to create tokens (OD-5).
- Table: Name, Token (prefix + "…"), Created, Expires, Last used, Revoke. Expired and revoked rows are shown greyed with their state for 30 days. Empty state explains what a token is for and links the MCP server README.
- "Create token" dialog: Name (required), Expires in (7, 30, 90 or 365 days; default 30, OD-4), Create.
- After create: one-time reveal panel with the full token, Copy button, a warning that it will not be shown again, and a copyable MCP client config snippet using the panel URL.
- Revoke asks for confirmation and removes the token from use at once.

## Admin view (Users & RBAC, user row)

- "MCP tokens" item in a user's row menu opens a dialog listing that user's tokens (no secrets) with Revoke (needs `users:manage`).

## Design deliverables

- `design.fig` screens: MCP tokens list (with rows), empty state, create dialog, one-time reveal, revoke confirm, mobile list, admin dialog.
- Export with `hack/design/fig-export.ts` in the same commit.
- All strings through the i18n packs (`i18n/`), English source plus keys for Weblate.
