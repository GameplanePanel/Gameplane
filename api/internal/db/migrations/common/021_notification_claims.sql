CREATE TABLE notification_claims (
    cluster TEXT NOT NULL,
    object_uid TEXT NOT NULL,
    object_version TEXT NOT NULL,
    event_type TEXT NOT NULL,
    sink_name TEXT NOT NULL,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (cluster, object_uid, object_version, event_type, sink_name)
);
CREATE INDEX notification_claims_expiry ON notification_claims(claimed_at);
