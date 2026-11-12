# Bounded audit database writes

The API limits the number of audit writes waiting for the database and times out stalled
inserts and retention runs. A slow PostgreSQL connection can no longer build an unbounded
queue of audit writers. [PR #888](https://github.com/GameplanePanel/Gameplane/pull/888)
