# Config fields can export a different env var name

A module's `configSchema` field can set `envName` to export its value under a
different env var than the field name. The stored config key does not change,
so existing servers keep their saved values. The Minecraft module uses it to
make "Enforce whitelist" turn on the whitelist (the itzg image reads
`ENABLE_WHITELIST`, not `WHITELIST_ENABLED`).
