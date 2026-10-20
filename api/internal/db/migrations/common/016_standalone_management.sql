-- Credentials are authenticated ciphertext, never plaintext JSON. Cluster
-- registrations contain public connection references and health metadata.
CREATE TABLE management_objects (
    kind TEXT NOT NULL,
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    uid TEXT NOT NULL,
    version BIGINT NOT NULL,
    payload TEXT NOT NULL,
    PRIMARY KEY (kind, namespace, name)
);
