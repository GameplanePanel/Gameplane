-- Telemetry install state (spec 022). One singleton row holds the consent
-- source, the extended-tier install ID, the report schedule and the signing
-- secret. Timestamps are RFC 3339 UTC text bound from Go, so there is no
-- database-side clock and the file runs unchanged on SQLite and PostgreSQL.
-- The row is seeded by Store.Migrate in Go, because seeding has to read the
-- existing JSON value of config key telemetry, which portable SQL cannot do.
CREATE TABLE telemetry_state (
    id                       TEXT PRIMARY KEY,
    consent_source           TEXT NOT NULL,
    install_id               TEXT,
    notice_shown_at          TEXT,
    next_due_at              TEXT,
    last_attempt_at          TEXT,
    last_success_at          TEXT,
    last_outcome             TEXT NOT NULL DEFAULT 'never',
    consecutive_failures     INTEGER NOT NULL DEFAULT 0,
    ext_unsupported_until    TEXT,
    ext_unsupported_endpoint TEXT,
    signing_secret           TEXT,
    last_id_rotation_at      TEXT
);

-- One row per admin who dismissed the consent notice. user_id is users.id.
-- Rows are deleted in Go when the user is removed, because the shipped
-- SQLite DSN runs with foreign keys off.
CREATE TABLE telemetry_notice_acks (
    user_id  BIGINT PRIMARY KEY,
    acked_at TEXT NOT NULL,
    action   TEXT NOT NULL
);
