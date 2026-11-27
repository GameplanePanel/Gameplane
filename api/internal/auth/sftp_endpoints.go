package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SFTPClusterEndpoint is the SFTP endpoint an admin registered for a remote cluster (spec 040, US4).
type SFTPClusterEndpoint struct {
	Cluster            string    `json:"cluster"`
	Address            string    `json:"address"`
	HostKeyFingerprint string    `json:"hostKeyFingerprint"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// GetSFTPClusterEndpoint returns the registered endpoint of cluster. found is false when none is set.
func (s *SessionStore) GetSFTPClusterEndpoint(ctx context.Context, cluster string) (SFTPClusterEndpoint, bool, error) {
	var (
		ep        = SFTPClusterEndpoint{Cluster: cluster}
		updatedAt string
	)
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT address, host_key_fingerprint, updated_at FROM sftp_cluster_endpoints WHERE cluster = ?`, cluster,
	).Scan(&ep.Address, &ep.HostKeyFingerprint, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SFTPClusterEndpoint{}, false, nil
	}
	if err != nil {
		return SFTPClusterEndpoint{}, false, fmt.Errorf("get sftp cluster endpoint: %w", err)
	}
	ep.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return SFTPClusterEndpoint{}, false, fmt.Errorf("get sftp cluster endpoint: updated_at: %w", err)
	}
	return ep, true, nil
}

// SetSFTPClusterEndpoint stores the endpoint of ep.Cluster, replacing any earlier one, in one transaction.
func (s *SessionStore) SetSFTPClusterEndpoint(ctx context.Context, ep SFTPClusterEndpoint) (SFTPClusterEndpoint, error) {
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return SFTPClusterEndpoint{}, fmt.Errorf("set sftp cluster endpoint: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Truncate(time.Second)
	res, err := tx.ExecContext(ctx,
		`UPDATE sftp_cluster_endpoints SET address = ?, host_key_fingerprint = ?, updated_at = ? WHERE cluster = ?`,
		ep.Address, ep.HostKeyFingerprint, now.Format(time.RFC3339), ep.Cluster)
	if err != nil {
		return SFTPClusterEndpoint{}, fmt.Errorf("set sftp cluster endpoint: update: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return SFTPClusterEndpoint{}, fmt.Errorf("set sftp cluster endpoint: rows: %w", err)
	} else if n == 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sftp_cluster_endpoints(cluster, address, host_key_fingerprint, updated_at) VALUES (?, ?, ?, ?)`,
			ep.Cluster, ep.Address, ep.HostKeyFingerprint, now.Format(time.RFC3339)); err != nil {
			return SFTPClusterEndpoint{}, fmt.Errorf("set sftp cluster endpoint: insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return SFTPClusterEndpoint{}, fmt.Errorf("set sftp cluster endpoint: commit: %w", err)
	}
	ep.UpdatedAt = now
	return ep, nil
}
