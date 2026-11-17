# Stop denied backup requests from repeating

A server's Backups tab checks permissions for its selected cluster and namespace before loading backups, schedules, restores and destinations. Authorization failures stop automatic retries and polling until access changes, and the tab shows an access-denied message. Existing role restrictions remain unchanged.
