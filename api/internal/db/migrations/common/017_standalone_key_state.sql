-- The active identity is public metadata, not encryption-key material.
-- Writers hold this singleton's write lock until their credential commit.
CREATE TABLE management_key_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    key_id TEXT NOT NULL,
    previous_key_id TEXT NOT NULL DEFAULT ''
);
