-- Spec 040 (US4): the SFTP endpoint address and host key fingerprint of each registered remote
-- cluster, set by an admin with PUT /clusters/{name}/sftp. The panel's own cluster reads its
-- address from chart values and its fingerprint from the host key Secret, so it has no row.
-- Portable SQL: runs unchanged on SQLite and PostgreSQL.
CREATE TABLE sftp_cluster_endpoints (
    cluster TEXT PRIMARY KEY,
    address TEXT NOT NULL,
    host_key_fingerprint TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
