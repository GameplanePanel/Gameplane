# Breaking: the chart installs its CRDs and ModuleSources from hooks

The packaged chart no longer bundles the `gameplane-crds` subchart, which cuts
its size by about a third so it fits a k3s `HelmChart`'s `chartContent` under
Kubernetes' 256 KiB annotation limit. The `crds.autoApply` hook now installs
the CRDs on every install as well as every upgrade, so air-gapped clusters
must mirror `crds.autoApply.image`. A second hook applies the default and
upload ModuleSources with `helm.sh/resource-policy: keep`. They are kept
across the upgrade, but `helm uninstall` no longer removes them, and turning
`defaultModuleSource.enabled` off no longer deletes the existing one. If you
set `crds.autoApply.enabled=false`, install the CRDs first from the
`gameplane-crds` chart. See [Helm CRD caveat](../../docs/install.md#helm-crd-caveat).
[PR #1087](https://github.com/GameplanePanel/Gameplane/pull/1087)
