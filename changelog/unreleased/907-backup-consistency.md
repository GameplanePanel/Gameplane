# Backup consistency reporting

Backup records distinguish an acknowledged save with writes still enabled, a verified write
freeze and a stopped capture. Historical backups with unverified hooks remain restorable and
report unknown consistency. [PR #907](https://github.com/GameplanePanel/Gameplane/pull/907)
