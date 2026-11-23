# Data model: SFTP access to game servers

One portable migration, `api/internal/db/migrations/common/024_sftp_credentials.sql` (rules in `api/internal/db/migrations/README.md`: ids and timestamps bound from Go, no foreign keys, `DeleteUserTx` deletes a user's rows in Go).

## sftp_ssh_keys

| Column | Type | Notes |
|---|---|---|
| id | BIGINT PK | random positive 53-bit, from Go |
| user_id | BIGINT NOT NULL | indexed |
| name | TEXT NOT NULL | 1–64 chars, unique per user (checked in Go) |
| public_key | TEXT NOT NULL | authorized_keys line, normalized; types ed25519, ecdsa-sha2-nistp256/384/521, rsa ≥ 3072 bits |
| fingerprint | TEXT NOT NULL UNIQUE | SHA256 fingerprint; one key belongs to one user only |
| created_at | TEXT NOT NULL | RFC 3339 UTC |
| last_used_at | TEXT | written at most once a minute |

Limit: 20 keys per user.

## sftp_passwords

| Column | Type | Notes |
|---|---|---|
| user_id | BIGINT PK | at most one password per user (FR-006a) |
| hash | TEXT NOT NULL | argon2id, same parameters as login passwords |
| length | INTEGER NOT NULL | 20–40, for display only |
| created_at | TEXT NOT NULL | |
| last_used_at | TEXT | |

Regenerate replaces the row in one transaction; revoke deletes it. No expiry (OD-2).

## sftp_service_tokens

| Column | Type | Notes |
|---|---|---|
| id | BIGINT PK | |
| cluster | TEXT NOT NULL | cluster the endpoint serves; `default` for the panel's own cluster |
| digest | TEXT NOT NULL UNIQUE | SHA-256 hex of `gpsftps_…` |
| prefix | TEXT NOT NULL | first 12 chars, for display |
| created_at | TEXT NOT NULL | |
| last_used_at | TEXT | |
| revoked_at | TEXT | |

At most 2 active per cluster (rotation).

## sftp_sessions

| Column | Type | Notes |
|---|---|---|
| id | BIGINT PK | |
| user_id | BIGINT NOT NULL | indexed |
| cluster | TEXT NOT NULL | from the service token |
| credential | TEXT NOT NULL | `key:<id>` or `password` |
| digest | TEXT NOT NULL UNIQUE | SHA-256 of `gpsftp_…` session token |
| session_epoch | BIGINT NOT NULL | copied from `users.session_epoch` |
| expires_at | TEXT NOT NULL | now + 15 min, extended on use (sliding, at most once a minute) |
| revoked_at | TEXT | set when the credential is removed or regenerated, or when the SFTP server ends a session it turned away (`POST /sftp/v1/sessions/end`) |

Expired rows are swept with the MCP token sweeper.

## sftp_cluster_endpoints

Migration `025_sftp_cluster_endpoints.sql`. The SFTP endpoint of each registered remote cluster, set by `PUT /clusters/{name}/sftp`. The panel's own cluster has no row: its address comes from chart values and its fingerprint from the host key Secret.

| Column | Type | Notes |
|---|---|---|
| cluster | TEXT PK | cluster name |
| address | TEXT NOT NULL | `host` or `host:port`, shown to users |
| host_key_fingerprint | TEXT NOT NULL | OpenSSH `SHA256:` fingerprint of the endpoint's host key |
| updated_at | TEXT NOT NULL | RFC 3339 UTC |

Removing a cluster registration leaves its row behind; it is harmless and is replaced if the cluster is registered again.

## audit_events (changed)

Add `source TEXT` (NULL for dashboard requests, `sftp` for SFTP session requests).

## GameServer annotations (operator, no CRD change)

| Annotation | Writer | Meaning |
|---|---|---|
| `gameplane.local/files-requested-at` | API | RFC 3339 time of the latest file operation on a stopped/asleep server; the operator keeps the file-only pod while it is younger than the idle timeout |

Operator flag `--files-idle-timeout` (Helm `operator.filesIdleTimeout`, default `10m`).
