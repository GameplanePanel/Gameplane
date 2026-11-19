# Contract: operator behaviour for GameServer modpacks

Owner: `operator/internal/controller/` (new file `gameserver_modpack.go`).

1. **Env rendering** (FR-003, FR-004): from `spec.env` only (not template, version or config
   env), remove every env name declared in any provider's `modpacks` block (`refEnv`,
   `versionEnv`, `apiKeyEnv`, each `env[].name`). Then, if `status.modpack.applied` is set,
   append that provider's fixed `env`, `refEnv=project` and, when `version` is set,
   `versionEnv=version` after `spec.env`, so the pack's values win on duplicate names
   (kubelet last-wins). When no pack is applied, the version's own env (for example `TYPE`)
   stays as rendered.
2. **Legacy and adopted packs** (R6): a server is legacy when `spec.env` holds a value for
   any provider's `refEnv` and `spec.modpack` is absent (not yet swept), or adopted when
   annotation `gameplane.local/modpack-adopted` is set. For both, while the pod is running,
   render the env exactly as the pre-035 operator did, so the operator's first reconcile
   after upgrade never rolls the pod, whatever the order of operator and API sweep.
   For an adopted server with a known version, the operator applies the pin (rule 1) with
   one restart as soon as `status.agent.playersOnline` is 0, or right away when the server
   is stopped or asleep (OD-8 B); never while players are online. Remove the
   annotation, and render rule 1, at the next pod start the operator drives.
3. **Change state machine** (R4): see data-model.md. A removal whose backup fails keeps
   `status.modpack.applied` and the remove annotation, so the API can accept a retry. Backup CRs created here are owned by
   the GameServer and named `<gs>-modpack-<generation>`.
4. **Mods wipe** (R5): on a removal only (OD-7 B), add the one-shot
   init step for the active loader's mods mount, guarded by the marker file.
5. **Key sharing** (R2): watch the managed key Secret (control namespace); maintain
   `<gs>-curseforge-key` and the read-only mount + `apiKeyFileEnv` only while sharing is on
   and the applied provider is curseforge. Not part of the config hash.
6. **Conditions**: `ModpackReady` True when `phase=Applied`; False with reason
   `BackingUp|Applying|BackupFailed`.
7. **RBAC**: get/list/watch on the one managed key Secret name in the control namespace
   (resourceNames-scoped where the existing role layout allows); create/update/delete on
   Secrets with the copy label in server namespaces.
