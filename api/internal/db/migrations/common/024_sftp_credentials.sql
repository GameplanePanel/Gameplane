-- Spec 040: SFTP access. Credentials are separate from dashboard logins.
--   sftp_ssh_keys       a user's SFTP public keys (normalized authorized_keys line); the
--                       fingerprint is unique across all users, so one key belongs to one user
--   sftp_passwords      at most one generated SFTP password per user, argon2id hash only
--   sftp_service_tokens per-cluster bearer credentials of an SFTP endpoint (digest only)
--   sftp_sessions       short-lived session tokens (digest only) minted by /sftp/v1/login
-- Ids are generated in Go (random positive 53-bit integers) and timestamps are bound from Go
-- as RFC 3339 UTC, so the same statements run on SQLite and PostgreSQL.
-- No foreign keys (SQLite does not enforce them): DeleteUserTx deletes a user's rows in Go.
-- Portable SQL: runs unchanged on SQLite and PostgreSQL.
CREATE TABLE sftp_ssh_keys (
    id BIGINT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    public_key TEXT NOT NULL,
    fingerprint TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    last_used_at TEXT
);
CREATE INDEX sftp_ssh_keys_user_id ON sftp_ssh_keys(user_id);
CREATE TABLE sftp_passwords (
    user_id BIGINT PRIMARY KEY,
    hash TEXT NOT NULL,
    length INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    last_used_at TEXT
);
CREATE TABLE sftp_service_tokens (
    id BIGINT PRIMARY KEY,
    cluster TEXT NOT NULL,
    digest TEXT NOT NULL UNIQUE,
    prefix TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at TEXT
);
CREATE INDEX sftp_service_tokens_cluster ON sftp_service_tokens(cluster);
CREATE TABLE sftp_sessions (
    id BIGINT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    cluster TEXT NOT NULL,
    credential TEXT NOT NULL,
    digest TEXT NOT NULL UNIQUE,
    session_epoch BIGINT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT
);
CREATE INDEX sftp_sessions_user_id ON sftp_sessions(user_id);
-- Source of the request that wrote the row: NULL for dashboard requests, sftp for SFTP.
ALTER TABLE audit_events ADD COLUMN source TEXT;
