-- Password reset ends every session of the account (security audit
-- 2026-11-10). users.session_epoch is bumped on each reset; a session is
-- valid only while its own session_epoch equals the account's, so a
-- session a concurrent login created after verifying the old password is
-- refused too. Defaults keep every existing session valid. Portable SQL:
-- runs unchanged on SQLite and PostgreSQL.
ALTER TABLE users ADD COLUMN session_epoch INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN session_epoch INTEGER NOT NULL DEFAULT 0;
