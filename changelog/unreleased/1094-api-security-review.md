# API security fixes

Backup destination URLs no longer show a password embedded in the restic URL to read-only users, transferring a server to a new owner now revokes its share links, mod registry API keys are no longer sent along when a provider redirects to another host, and webhook and telemetry URLs no longer appear in API logs. No action is needed. [PR #1094](https://github.com/GameplanePanel/Gameplane/pull/1094)
