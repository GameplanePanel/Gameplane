# PostgreSQL availability and recovery

Kubernetes-native installations can run two or more API replicas on separate nodes against one writable PostgreSQL primary. To upgrade, first roll out the HA-capable API with one replica and a Secret-backed DSN. After the singleton is ready, enable `api.postgresHA.enabled` and set `api.replicas` to at least `2`. SQLite and standalone installations continue to use one API replica. [Helm upgrade instructions](../../docs/install.md#api-rollout-and-postgresql-availability)

For point-in-time recovery, keep the physical base backup and archived WAL on storage separate from the database volume. The recovery drill fences the old writer, restores to a named WAL point, and reapplies authorization revocations before the API resumes serving requests. [Panel recovery procedure](../../docs/panel-recovery.md)
