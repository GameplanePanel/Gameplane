# Recovering the panel while games keep running

Panel SQL state, Kubernetes desired state and game files are separate recovery
domains. A game-volume backup cannot reconstruct panel users, password hashes,
roles/grants, sessions, share links, configuration, audit history or standalone
cluster registrations and encrypted credentials. Recover the failed domain;
restoring the panel does not require deleting or recreating live games.

This runbook extends [standalone storage and key recovery](standalone-panel.md#storage-and-recovery).
Standalone SQL storage was introduced in PR [#630](https://github.com/GameplanePanel/Gameplane/pull/630).
PR [#707](https://github.com/GameplanePanel/Gameplane/pull/707) added PostgreSQL-backed
E2E coverage; it does not provide PostgreSQL backup, PITR or failover automation.

## Backup inventory and targets

Record the deployment mode, software/schema version, database location and
backup completion time. Keep the following inventory outside the failed panel:

| Domain | Retain and verify |
| --- | --- |
| SQL, both modes | Consistent SQLite database/WAL snapshot or external PostgreSQL backups and WAL/PITR; users, role bindings, configuration, sessions/shares/API tokens and audit chain. Standalone also stores management objects and encrypted credentials here. |
| Kubernetes | Cluster/etcd recovery procedure, CRDs, GameServer/Module/ModuleSource/Backup/Restore resources, namespaces/RBAC, management/config/agent/registry Secrets, certificates and external provider credentials. Combined mode stores management resources and remote credentials here, rather than in standalone SQL. |
| Workload data | Primary, extra and mod PVCs, PV identities, storage classes/topology, retention policies, Restic repositories/passwords, CSI snapshot classes/content and backend access. Include game-specific save/restore consistency requirements. |
| Key custody | The exact standalone 32-byte management key, separately protected from database archives, plus keys needed by retained older backups after rotation. Preserve relevant certificate/private-key and secret-manager recovery procedures. |
| Authorization evidence | Intended users, roles, per-cluster/namespace grants, owner/collaborator associations, credentials revoked since the recovery point and audit exports. Record Kubernetes UIDs as well as names. |

Set targets for each deployment before a drill. Fill in the maximum acceptable
age of restored management state (**RPO**), maximum time from loss until verified
service (**RTO**), responsible operator, backup schedule and restoration location.
Check measured results against those targets and adjust the backup schedule,
storage, tooling or staffing when they are missed. Chart defaults are not a
universal RPO/RTO promise. Measure game-data recovery separately.

For SQLite, stop every API process and use a SQLite backup tool or copy the
database **and its WAL state** consistently. Never copy only a live `.db` file.
Run `PRAGMA integrity_check` against the restored copy. The CI drill uses SQLite's
backup API with the panel stopped and converts the archive to DELETE journal mode
so it is self-contained. Keep the archive read-only and restore to a separate
working copy. Use the ownership/key permission rules in the standalone guide.

For PostgreSQL, assign backup/PITR/failover to the database platform: schedule and
test base backups plus WAL retention, protect credentials and select an explicit
recovery timestamp/LSN. Restore into an isolated instance, validate schema and
data, and prevent split-brain writes before switching the API's DSN. Stop all API
writers for offline credential invalidation and keep one API process after
recovery. The SQLite CI drill does not certify a PostgreSQL service's PITR/RTO.

## Database outage

Stop or restrict panel traffic and identify whether the database is unreachable,
out of space, locked or corrupt. Preserve logs and the failed storage before
changing it. Restore database connectivity/capacity first when the original
database is intact; do not initialize an empty database or bootstrap new users
as a connectivity repair. Verify readiness, login, scoped access and registered
cluster operations before reopening access.

Game Pods and the Kubernetes operator can continue independently of the panel.
Do not assume this proves game health: check Kubernetes events, game process
state and saves through the operational access available during the outage.

## Database loss and recovery order

1. Restrict external panel access and stop **all** API writers. Fence the old
   database instance so it cannot resume writing. Record incident/recovery start
   time and live GameServer/PVC/Pod UIDs, container IDs and restart counts. Keep
   the operator and healthy game Pods running when only panel SQL was lost.
2. Preserve the failed database and audit evidence. Select a verified backup or
   PostgreSQL recovery point, its software/schema version, and the matching
   standalone key version. Copy the backup into a new working location; keep
   the original immutable. Restore database file ownership/permissions and
   verify integrity before starting the API.
3. With every API stopped, reconcile authorization on the **working copy**.
   An old backup can resurrect users, grants and credentials revoked later.
   Compare the external authorization/revocation inventory, disable/remove stale
   accounts and grants, and replace compromised credentials. Review SSO/provider
   access too. Preserve original audit rows and record recovery actions in a
   separate incident log rather than rewriting the audit chain.
4. Invalidate restored bearer access according to the recovery policy. The drill
   deletes all restored `sessions` and `api_tokens`, and sets `share_links.revoked_at`
   for every active link. Do this offline in one transaction, on the working copy:

   ```sql
   BEGIN;
   DELETE FROM sessions;
   DELETE FROM api_tokens;
   UPDATE share_links SET revoked_at = '<actual recovery time in RFC3339 UTC>'
     WHERE revoked_at IS NULL;
   COMMIT;
   ```

   Replace the timestamp placeholder before execution. Retain the archive as
   evidence, protect access to it, and tell users that they must log in again and
   recreate shares/tokens. Do not run cleanup against the evidence archive.
5. Mount the original standalone management key read-only from separate custody.
   For standalone recovery, start with `--panel-key-provisioned` and set
   `--panel-key-file` to that mounted original key; this prevents startup from
   generating a replacement if the key is missing.
   In combined mode, restore/validate required Kubernetes management objects and
   Secrets through the cluster's recovery process instead. Start one API using
   compatible software and the restored database; inspect migration/startup
   results before exposing it. A wrong/missing key is a hard failure, not a reason
   to replace the key or discard encrypted rows.
6. Reconcile the panel's inventory against each target cluster. Check the actual
   registered cluster endpoint/identity and credential-backed API operations.
   Compare GameServer, PVC and Pod UIDs with the recorded inventory; validate
   owner/collaborator user IDs against intended accounts. Names alone do not
   establish identity or ownership. Resolve deleted/recreated resources, dangling
   references and reused numeric user IDs before granting access. Do not recreate
   live workloads merely to make stale panel records appear consistent.
7. Verify restored administrators and restricted users, role definitions, scoped
   allow/deny behavior, old-cookie/share denial, cluster credentials and audit
   history/integrity. Confirm game Pod/container identities and persistent save
   markers remain intact and games continue progressing. Reopen access only
   after these checks; record completion time and any lost post-backup changes.
8. Record achieved recovery-point age and RTO, compare with the selected targets,
   and retain the drill/incident report without passwords, keys, kubeconfigs,
   session cookies or share tokens. Take a new protected backup after the intended
   authorization and credential state has been restored.

## Missing or wrong management key

Keep the panel stopped. Recover the exact key version paired with the chosen
database backup from separate custody. Existing encrypted credentials cannot be
recovered by generating a new random key. With `--panel-key-provisioned`, startup
rejects a missing file with `provisioned management key is missing` and never
creates a replacement, regardless of database contents. Without provisioned mode,
a database containing management secrets or key-state records requires its key
and a missing file reports `management encryption key is missing`; when neither
is present, startup can generate a new key. Recovery must use provisioned mode
to avoid that fallback. An inactive/wrong key for a recorded key identity reports
`management encryption key is inactive`. Other corruption
may fail authentication of encrypted records. None is permission to serve partial
management state.

If the matching key is irretrievably lost, retain the database/audit archive and
treat the encrypted credentials as lost. Recover them from their original issuers
or reissue/re-register them under an explicitly reviewed rebuild plan. Reconcile
users, grants and Kubernetes identities before exposing a rebuilt panel. A game
backup cannot recover this key or the encrypted management credentials.

## Automated CI drill and measured results

`TestPanel_OfflineRecovery` runs in the amd64 `recovery` E2E bucket. It uses a
unique namespace, its own standalone API from the built image, separate database
and archive PVCs, separate original/wrong key Secrets and a private login budget.
The API has no Kubernetes service-account token: reading the existing GameServer
through a registered cluster proves the restored encrypted kubeconfig works.

The fixture seeds a custom role, a user with a scoped grant, a registration,
sessions, a share and audit events. It stops the API, takes a consistent archive,
mounts the archive read-only, creates a post-backup user, then deletes the working
database. It restores a copy, invalidates restored bearers while preserving audit
rows and the archive digest, and checks wrong-key startup exits without a successful
HTTP response during the startup probe. With the original key it checks login,
allow/deny permissions, registration-backed reads, old-cookie/share denial and
loss of the deliberately post-backup user. A busybox workload retains its Pod UID,
container ID/restart count and persistent marker while its counter advances.

The `panel-recovery-<arch>-<attempt>` CI artifact contains JSON with:

- `recovery_point_age_at_loss_seconds`: age of the completed fixture backup when
  the working database was lost. This measures the fixture's recovery point;
  post-backup changes are deliberately absent after restore.
- `rto_seconds`: time from simulated loss through restoration and acceptance
  checks, **including** the negative wrong-key trial. Cluster/bootstrap setup and
  backup creation precede this clock.
- Check results, archive/audit digests and row counts, and game identity evidence.

The report records actual run values; no benchmark values are hard-coded here.
A failed run writes a partial report with `passed: false`; missing timing fields
mean that stage was not reached. The artifact contains no keys or raw database
rows. Use repeated passing runs and your own production-shaped drills to select
and demonstrate targets. This fixture covers SQLite panel recovery, not storage
cluster loss, PostgreSQL failover, a particular game's save format, or a complete
Kubernetes disaster recovery exercise.
