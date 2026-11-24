-- Spec 040: an optional SFTP login name each user can choose in Settings, SFTP. NULL means the
-- default: the display name when no other account can claim it, otherwise the panel username.
-- Portable SQL: runs unchanged on SQLite and PostgreSQL (both allow many NULLs in a unique index).
ALTER TABLE users ADD COLUMN sftp_username TEXT;
CREATE UNIQUE INDEX users_sftp_username ON users (sftp_username);
