# Audit syslog bridge always requires a token

With `api.audit.webhook.syslogBridge.enabled=true` and no
`api.audit.webhook.authSecretRef`, the chart now generates a token in the
Secret `gameplane-audit-syslog-bridge-token` (kept across upgrades) and gives
it to both the API and the bridge, so other pods can no longer post forged
audit records to the bridge. The bridge image now refuses to start without
`AUTH_HEADER` unless `ALLOW_UNAUTHENTICATED=true` is set. Tailscale tunnels
also cap how many forwarding sessions one tailnet peer can hold (a quarter of
the budget). No action is needed on upgrade.
[PR #1099](https://github.com/GameplanePanel/Gameplane/pull/1099)
