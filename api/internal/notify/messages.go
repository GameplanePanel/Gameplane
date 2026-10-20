package notify

import "github.com/GameplanePanel/gameplane/api/internal/messages"

// Messages returns the notify.* catalog entries. They are the single source for
// i18n/locales/en/app.json (written by `make i18n-sync`) and the English
// fallback when the catalog cannot answer.
func Messages() []messages.Entry {
	return []messages.Entry{
		{
			ID:          "notify.title.named",
			English:     "[{instance}] {event}: {namespace}/{name}",
			Description: "Headline of a notification about a named resource. instance is the Gameplane instance name, event is the event name (for example \"backup failed\"), namespace and name identify the resource.",
		},
		{
			ID:          "notify.title.cluster",
			English:     "[{instance}] {event}: {cluster}/{namespace}/{name}",
			Description: "Headline of a notification about a named resource on a remote cluster. instance is the Gameplane instance name, event is the event name (for example \"backup failed\"), cluster is the cluster name, namespace and name identify the resource.",
		},
		{
			ID:          "notify.title.unnamed",
			English:     "[{instance}] {event}",
			Description: "Headline of a notification without a resource, such as a test send. instance is the Gameplane instance name, event is the event name.",
		},
		{
			ID:          "notify.event.server.unhealthy",
			English:     "server unhealthy",
			Description: "Event name used inside a notification headline when a game server becomes unhealthy.",
		},
		{
			ID:          "notify.event.server.recovered",
			English:     "server recovered",
			Description: "Event name used inside a notification headline when a game server recovers.",
		},
		{
			ID:          "notify.event.backup.failed",
			English:     "backup failed",
			Description: "Event name used inside a notification headline when a backup fails.",
		},
		{
			ID:          "notify.event.backup.succeeded",
			English:     "backup succeeded",
			Description: "Event name used inside a notification headline when a backup succeeds.",
		},
		{
			ID:          "notify.event.restore.failed",
			English:     "restore failed",
			Description: "Event name used inside a notification headline when a restore fails.",
		},
		{
			ID:          "notify.event.restore.succeeded",
			English:     "restore succeeded",
			Description: "Event name used inside a notification headline when a restore succeeds.",
		},
		{
			ID:          "notify.event.test",
			English:     "test notification",
			Description: "Event name used inside the headline of a test notification.",
		},
		{
			ID:          "notify.detail.reason_message",
			English:     "{reason} — {message}",
			Description: "Body line that joins the Kubernetes reason and message of an event. The reason and message are quoted from Kubernetes in English and stay untranslated. The dash is an em dash.",
		},
		{
			ID:          "notify.detail.test",
			English:     "This is a test notification from Gameplane. Your sink is wired up correctly.",
			Description: "Body of a test notification sent from Admin Settings.",
		},
	}
}
