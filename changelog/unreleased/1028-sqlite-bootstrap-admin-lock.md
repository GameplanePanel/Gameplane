# bootstrap-admin no longer fails with "database is locked" on SQLite

Running `bootstrap-admin` while the API is serving could fail at once with
`database is locked (SQLITE_BUSY)`, even with the busy timeout set. SQLite
read-write transactions now take the write lock when they begin, so they wait
for the lock instead of failing. No action is needed. [PR #1028](https://github.com/GameplanePanel/Gameplane/pull/1028)
