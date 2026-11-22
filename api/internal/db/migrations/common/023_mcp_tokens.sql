-- Spec 037: personal MCP tokens. A token is a read-only bearer credential
-- for the MCP server, bound to its owner's permissions at request time.
--   digest         SHA-256 hex of the full token; the token itself is never stored
--   prefix         first 10 characters of the token, shown so users can tell tokens apart
--   session_epoch  copied from users.session_epoch at creation; a password reset
--                  bumps the epoch and invalidates the token
--   expires_at     RFC 3339 UTC; revoked_at is RFC 3339 UTC or NULL
-- id is generated in Go (a random positive 53-bit integer, exact as a JSON number) so the same
-- statement works on SQLite and PostgreSQL. Timestamps are bound from Go.
-- No foreign key: SQLite does not enforce them, so DeleteUserTx deletes a
-- user's tokens in Go (migrations README rule 3).
-- Portable SQL: runs unchanged on SQLite and PostgreSQL.
CREATE TABLE mcp_tokens (
    id BIGINT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    digest TEXT NOT NULL UNIQUE,
    prefix TEXT NOT NULL,
    session_epoch BIGINT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at TEXT,
    UNIQUE (user_id, name)
);
CREATE INDEX mcp_tokens_user_id ON mcp_tokens(user_id);
