-- Spec 040 review: a session remembers the SFTP service token that minted it, so revoking the
-- token ends its sessions. NULL for sessions minted without a service token.
-- seeded marks the token registered from the deployment's chart secret (SeedSFTPServiceToken),
-- so a later render's seeded token replaces it instead of counting against the active limit.
-- Portable SQL: runs unchanged on SQLite and PostgreSQL.
ALTER TABLE sftp_sessions ADD COLUMN service_token_id BIGINT;
ALTER TABLE sftp_service_tokens ADD COLUMN seeded INTEGER NOT NULL DEFAULT 0;
CREATE INDEX sftp_sessions_service_token_id ON sftp_sessions(service_token_id);
