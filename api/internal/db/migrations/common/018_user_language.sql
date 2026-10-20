-- User language choice (spec 019). language is a BCP 47 tag from the shipped
-- language packs, or NULL when the user has no saved choice and follows the
-- instance default. The column is nullable with no default, so existing rows
-- keep NULL and need no backfill. Portable SQL: runs unchanged on SQLite and
-- PostgreSQL.
ALTER TABLE user_preferences ADD COLUMN language TEXT;
