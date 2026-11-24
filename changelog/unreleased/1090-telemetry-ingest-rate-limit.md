# Telemetry receiver limits every /ingest request per source

The telemetry receiver now limits how many `/ingest` requests one source can send per minute, counting refused and malformed reports, so failed reports can no longer be sent at any rate. The limit is set with `INGEST_SOURCE_PER_MINUTE` (default `60`, `0` turns it off). Request headers are also capped at 16 KiB, and the `AUTH_TOKEN` check no longer reveals the token's length. No action is needed on upgrade. [PR #1090](https://github.com/GameplanePanel/Gameplane/pull/1090)
