# Native Kubernetes permissions

Helm preserves the full native dashboard's existing access on upgrade. Use the following values for a single-cluster installation where GitOps owns catalogs, runtime credentials and backup destinations. Add each API-managed game namespace to both lists. The API retains writes in its primary games namespace. Operator-managed namespaces receive additional API writes only when explicitly listed.

```yaml
operator:
  gameNamespaces: [tenant-b]
nativeRBAC:
  catalogAdmin: false
  remoteClusters: false
  runtimeSecretNames: [oidc-production]
  gameSecretManagement: false
  podAttach: false
  operatorStopAttach: true
  snapshotContentReclamation: false
  additionalAPIGameNamespaces: [tenant-b]
```

| Kubernetes identity | Resources and allowed actions | Boundary |
| --- | --- | --- |
| Native API, `<release>-api-read` | Read the catalog, Gameplane resources and cluster inventory. List pods for node usage. Remote mode also permits Cluster discovery. | Read-only. It has no Secret, pod-attach or catalog-write grant. |
| Native API, `<release>-api-catalog-admin` | Create, update, patch and delete cluster-wide templates, modules and module sources when `catalogAdmin` is enabled. The API can also manage module-upload ConfigMaps in its credential namespace. | In GitOps mode the API can read the catalog and ConfigMaps. The operator continues to reconcile administrator-provisioned module resources. |
| Native API, `<release>-api-cluster-registration` and `<release>-api-credentials` | Remote mode grants registration writes plus get/create/update/delete on Secrets in the dedicated credentials namespace. This includes the managed Secret routes for OIDC, notifications and registry credentials. | Local-only mode omits registration, disables the remote watcher and grants get only on explicitly named runtime Secrets. Supply only the OIDC, notification or registry names the API needs. Kubernetes RBAC cannot distinguish kubeconfigs from other payloads in an explicitly allowed Secret. |
| Native API, `<release>-api-games` | Manage game resources and read pod logs in the primary and explicitly authorized extra game namespaces. `gameSecretManagement` adds Secret CRUD for backup destinations and tunnel credentials. `podAttach` enables the dashboard PTY. | These options grant namespace-wide Secret or attach access to the API process. Use dedicated game namespaces. The in-process server ownership checks still apply to user requests. |
| Operator | Owns game workload resources, services, backup jobs and per-server roles in each managed namespace. Reads the configured agent signing Secret and issues agent certificates. `operatorStopAttach` permits the PTY stop sequence independently of dashboard PTY permissions. | Game agents, wakers and the native API cannot read the issuer key. Keep operator attach enabled for modules that use PTY graceful shutdown. The operator drops the remote status watcher and its dedicated kubeconfig namespace from the Secret cache in local-only mode. |
| Operator, `<release>-operator-snapshot-reclaim` | When enabled, deletes cluster-scoped VolumeSnapshotContent on an aborted CSI backup after verifying owner, snapshot reference, deletion policy and UID. | With this grant disabled, the operator can still create and read namespaced snapshots. If content survives, read `SnapshotCleaned`, verify the recorded UIDs and reclaim the orphan as storage administrator. The development operator manifest also leaves cluster-wide content deletion to the administrator. |
| Per-server `<game>-agent` | Get/patch the named GameServer's status. | Cannot patch the peer's status, modify game specs or read Secrets. |
| Per-server `<game>-waker-sa` | Get/patch its named GameServer. Wake admission further constrains modifications. | Cannot patch a peer server or status and cannot read Secrets. |
| Standalone panel | Uses SQL-backed registration and encrypted credentials. The standalone API Pod has ServiceAccount token automount disabled. | Helm creates no native Kubernetes RBAC for this identity. Permissions in registered remote clusters come from the supplied kubeconfig. |

Cluster bootstrap tokens and short-lived Kubernetes user certificates have their own existing `clusterOps.enabled` switch. Leave it disabled unless a trusted panel must perform those actions. Its signer permission is separate from the operator's agent mTLS CA.

## Upgrade and verification

The chart retains the existing `<release>-api-read` ClusterRoleBinding and its immutable `roleRef`. Helm removes write verbs from the existing ClusterRole and creates separately named roles and bindings for enabled administrative capabilities. On disabling a capability, Helm removes its dedicated binding or the verbs on its namespaced Role. Check other bindings installed outside the release, since Kubernetes combines their permissions.

```sh
kubectl auth can-i get gametemplates.gameplane.local --as=system:serviceaccount:gameplane-system:gameplane-api
kubectl auth can-i patch gametemplates.gameplane.local --as=system:serviceaccount:gameplane-system:gameplane-api
kubectl auth can-i get secret/gameplane-agent-ca -n gameplane-system --as=system:serviceaccount:gameplane-system:gameplane-api
kubectl auth can-i delete volumesnapshotcontents.snapshot.storage.k8s.io --as=system:serviceaccount:gameplane-system:gameplane-operator
```

The Kind upgrade job compares the read binding's UID and effective catalog mutation before and after a published upgrade. It then exercises the full and restricted profiles, both named servers, selected namespaces, server-side catalog and CSI content dry runs, and a separate standalone Helm release. The restricted-workloads job exercises issuer denial and the real rotating agent token. The Helm installation suite covers mTLS client reissuance on CA rotation.
