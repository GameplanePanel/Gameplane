package db

import (
	"context"
	"errors"
)

// ClaimNotification reserves one observed Kubernetes transition per sink.
// The transaction commits before any external HTTP or SMTP delivery begins.
func (s *Store) ClaimNotification(ctx context.Context, cluster, uid, version, eventType, sink string) (bool, error) {
	if uid == "" || version == "" {
		return false, errors.New("notification is missing its Kubernetes identity")
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO notification_claims
		(cluster, object_uid, object_version, event_type, sink_name) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (cluster, object_uid, object_version, event_type, sink_name) DO NOTHING`,
		cluster, uid, version, eventType, sink)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}
