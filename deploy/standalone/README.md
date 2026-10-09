# Standalone panel deployment

This Compose file runs the Gameplane API and dashboard without Kubernetes on the
panel host. Registered game clusters still need Kubernetes, an operator, and
agents.

Follow the [standalone setup guide](../../docs/standalone-panel.md) for the
matching checkout, build commands, first administrator, HTTPS access, remote
registration, permissions, upgrades, and backups. Run its Compose commands from
the repository root. Preserve the `panel-data` volume with both the database and
`panel.key`.
