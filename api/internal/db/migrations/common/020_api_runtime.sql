CREATE TABLE api_rate_buckets (
    budget TEXT NOT NULL,
    subject_hash TEXT NOT NULL,
    next_us BIGINT NOT NULL,
    PRIMARY KEY (budget, subject_hash)
);
CREATE INDEX api_rate_buckets_expiry ON api_rate_buckets(next_us);

CREATE TABLE api_runtime_keys (
    purpose TEXT PRIMARY KEY,
    key_hex TEXT NOT NULL
);
