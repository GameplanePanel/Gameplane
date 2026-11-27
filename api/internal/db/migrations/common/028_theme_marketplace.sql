-- Spec 043: theme marketplace. Theme sources are panel data in the API
-- database; the API indexes them and users install themes for themselves.
--   theme_sources          one admin-managed source (oci, git, http or local)
--   theme_catalog_entries  cached latest version of each theme per source,
--                          rebuilt by indexing; never edited by users
--   user_installed_themes  a snapshot of a theme one user installed for
--                          their own account; source_id is NULL once the
--                          source is removed
-- Column notes:
--   credential_ref  name of the labelled Kubernetes Secret (combined mode) or
--                   panel-encrypted secret (standalone) that holds a source's
--                   credential; the credential itself is never stored in this row
--   preview_*       PNG or WebP bytes in BYTEA (BLOB on SQLite, which accepts the
--                   name); the content type is derived from the bytes when served
--   tokens, metadata, problems, versions, spec, verify are JSON in TEXT
-- Ids are generated in Go (random positive 53-bit integers, exact as a JSON
-- number) and timestamps are bound from Go as RFC 3339 UTC, so the same
-- statements run on SQLite and PostgreSQL.
-- No foreign keys (SQLite does not enforce them): the store deletes or
-- nulls dependent rows in Go transactions.
-- Portable SQL: runs unchanged on SQLite and PostgreSQL.
CREATE TABLE theme_sources (
    id BIGINT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL,
    spec TEXT NOT NULL,
    credential_ref TEXT,
    verify TEXT,
    refresh_interval_s INTEGER NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_sync_at TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE theme_catalog_entries (
    id BIGINT PRIMARY KEY,
    source_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    versions TEXT NOT NULL,
    digest TEXT NOT NULL,
    metadata TEXT NOT NULL,
    tokens TEXT NOT NULL,
    css TEXT,
    preview_light BYTEA,
    preview_dark BYTEA,
    status TEXT NOT NULL,
    problems TEXT NOT NULL,
    low_contrast INTEGER NOT NULL DEFAULT 0,
    indexed_at TEXT NOT NULL
);
CREATE UNIQUE INDEX theme_catalog_entries_source_name ON theme_catalog_entries(source_id, name);
CREATE INDEX theme_catalog_entries_source_id ON theme_catalog_entries(source_id);
CREATE TABLE user_installed_themes (
    id BIGINT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    source_id BIGINT,
    source_name TEXT NOT NULL,
    theme_name TEXT NOT NULL,
    version TEXT NOT NULL,
    digest TEXT NOT NULL,
    metadata TEXT NOT NULL,
    tokens TEXT NOT NULL,
    css TEXT,
    installed_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX user_installed_themes_user_id ON user_installed_themes(user_id);
CREATE UNIQUE INDEX user_installed_themes_user_source_name ON user_installed_themes(user_id, source_id, theme_name);
-- The selected installed theme. NULL unless theme_type is installed.
ALTER TABLE user_preferences ADD COLUMN installed_theme_id BIGINT;
