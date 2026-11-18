# The CRDs are published as their own Helm chart

Each release now also publishes the CRDs alone, as the OCI chart
`oci://ghcr.io/gameplanepanel/charts/gameplane-crds`, at the same version as
the `gameplane` chart. An Argo CD install can point its CRD Application at
that chart instead of the `crds` directory in Git; see
[One owner for each resource](../../docs/argocd.md#one-owner-for-each-resource).
Existing installs need no change: the `gameplane` chart still ships the CRDs.
[PR #990](https://github.com/GameplanePanel/Gameplane/pull/990)
