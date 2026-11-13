# Argo CD delivery of a native Gameplane installation

Use the chart’s Argo CD values for a combined API and operator installation. The profile uses two externally managed mTLS Secrets and requires discovery of the GA admission policy and binding APIs. It also gives the games Namespace and chart-managed SQLite claim explicit Argo CD retention annotations.

## One owner for each resource

Pin both Applications to the same Gameplane Git commit. Sync the CRD Application first and wait for its definitions to be Established before syncing the workload Application.

| Application | Source | Settings |
| --- | --- | --- |
| CRDs | Plain directory `charts/gameplane/charts/gameplane-crds/crds` | Use `ServerSideApply=true` for the large schemas. Keep automated pruning off and sync this Application without prune. For Application deletion, preserve its resources by using non-cascading deletion. |
| Workload | Helm chart `charts/gameplane` | Set `spec.source.helm.skipCrds: true` and include `examples/argocd-values.yaml` in `spec.source.helm.valueFiles`. The profile disables the CRD apply hook. Set the release name to `gameplane` and the destination namespace to `gameplane-system`. |

Provision the release namespace, CA and client Secret before syncing. Keep ownership of the release namespace and external Secrets outside the workload Application. The example expects `gameplane-external-ca-v1` with `ca.crt` and `ca.key`, and a different Secret named `gameplane-external-client-v1` with `tls.crt` and `tls.key`. Use immutable, versioned Secret names. The operator signs per-game serving certificates with this CA, and the API and operator present the client certificate to the agents.

Carry the installation's `nativeRBAC` settings, additional game namespaces, and credentials namespace into the workload Application. For a single-cluster panel where GitOps owns catalogs, configure the reduced Kubernetes permissions described in the [native RBAC guide](native-rbac.md).

Argo CD ordinarily passes the destination cluster’s API capabilities to Helm. Verify that both `admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy` and `admissionregistration.k8s.io/v1/ValidatingAdmissionPolicyBinding` are served and that admission is enabled. For an offline comparison, pass the discovered capabilities and the destination Kubernetes version. This example assumes both kinds on Kubernetes 1.34.

```sh
for run in 1 2; do
  helm template gameplane charts/gameplane -n gameplane-system \
    -f charts/gameplane/examples/argocd-values.yaml --kube-version 1.34.0 \
    --api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy \
    --api-versions admissionregistration.k8s.io/v1/ValidatingAdmissionPolicyBinding \
    > "gameplane-$run.yaml"
done
cmp gameplane-1.yaml gameplane-2.yaml
```

The chart fails the profile render if either kind is missing. Supplying a capability flag locally does not install that API on the destination. See the [Argo CD Helm source parameters](https://argo-cd.readthedocs.io/en/stable/user-guide/application-specification/) and [Helm hook mapping](https://argo-cd.readthedocs.io/en/stable/user-guide/helm/#helm-hooks).

## Adopting a populated installation

1. Back up the database and game data. Record the API and game claim UIDs, the current CA public certificate fingerprint, and each Gameplane CRD’s UID and `status.storedVersions`. Verify each stored version appears in the target CRD’s `spec.versions`. A removed stored version needs a storage-version migration first.
2. Copy the current CA and client material into the two external Secrets through your secret manager. Compare the CA’s public certificate digest and test the client against a live agent. Keep the existing trust pair for the initial handoff. Disable the prior delivery owner so it cannot concurrently change these resources.
3. Check both Deployments’ live update strategy before handoff. Offline rendering cannot detect an old server-defaulted `spec.strategy.rollingUpdate`. When the desired strategy is `Recreate`, migrate the live Deployment through the existing Helm upgrade path or a controlled merge patch that sets `type: Recreate` and `rollingUpdate: null` together.
4. Sync with pruning disabled. Keep the SQLite claim name and size from the populated release. After the first sync, verify the live games Namespace and the live API PVC both carry `argocd.argoproj.io/sync-options: Prune=false,Delete=false`. If switching to `api.storage.existingClaim`, verify retention on the **old live claim before** the sync that removes its manifest. Point the API at that same populated PVC, or migrate and verify the data separately first.
5. Review the workload prune preview. Compare the UIDs and CA fingerprint from step 1, log in with a pre-upgrade account, read a persisted game marker and make an authenticated call to the running game’s agent. On a wake-enabled test GameServer, impersonate its waker ServiceAccount for server-side dry-run patches. Its wake annotation should succeed and a spec mutation should be denied by the wake policy. Verify the policy and binding names and denial reason.
6. Enable workload pruning after reviewing the retained objects. Argo CD may report retained resources as OutOfSync. `Prune=false` preserves an object removed from Git during sync. `Delete=false` preserves it on Application deletion. Helm’s `helm.sh/resource-policy: keep` maps to deletion protection in Argo CD. Deleting a containing namespace can still remove its claims. See [Argo CD sync options](https://argo-cd.readthedocs.io/en/stable/user-guide/sync-options/).

## Rotating external certificates

For a client renewal under the same CA, stage a new immutable client Secret and change its reference in Git. For a CA rollover, schedule a maintenance window, stage a new CA and matching client Secret, then change both references in one revision. The reference change rolls the API and operator. The operator reissues each GameServer’s agent TLS Secret against the active CA. Kubelet projection and the next TLS handshake determine when each agent starts using the new trust.

Finish the rollout by checking the new CA fingerprint on each `<server>-agent-tls` Secret and making a fresh authenticated agent call for every running game. A stopped game needs that check on its next start. Retain the previous Secret pair until the checks finish. If rolling back the CA, restore both references together and repeat the per-agent checks while certificates are reissued.

## CI qualification

The amd64 upgrade job installs Argo CD Core v3.6.1 on a disposable kind cluster. Two Applications sync the PR commit from a read-only Git endpoint on the kind bridge. After adoption, the workload Application renews the client, switches to the previously populated database claim and prunes a tracked dashboard. A second sync replaces the CA and client together. At each stage the fixture verifies the claim and CRD identities, validates stored versions, authenticates with the original account and reads a saved world marker through the agent. Server-side dry runs check the waker's allowed annotation and denied spec mutation. [Populated upgrade fixture](../test/e2e/argocd_upgrade_e2e_test.go)
