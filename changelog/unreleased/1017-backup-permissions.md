# Stop denied backup requests from repeating

A server's Backups tab checks permissions for its selected cluster and namespace before loading backups, schedules, restores and destinations. Authorization failures stop automatic retries and polling until access changes, and the tab shows an access-denied message. The same destination checks apply to schedule creation and server backup settings; denied access is distinguished from missing configuration. Reopened backup details can recover after access is restored. Existing role restrictions remain unchanged.

[PR #1017](https://github.com/GameplanePanel/Gameplane/pull/1017)
