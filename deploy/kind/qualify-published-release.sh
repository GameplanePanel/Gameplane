#!/usr/bin/env bash
# Release CI runs this on a fresh amd64 runner without a registry login.
set -euo pipefail
source deploy/kind/release-consumer-lib.sh
tag=${1:?pass the published release tag}
[[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$ ]] || {
  echo "expected a published vMAJOR.MINOR.PATCH tag, optionally with a prerelease suffix" >&2
  exit 2
}
version=${tag#v}
owner="${GITHUB_REPOSITORY_OWNER,,}"
registry="ghcr.io/$owner"
scratch="$(mktemp -d)"
export DOCKER_CONFIG="$scratch/docker" HELM_REGISTRY_CONFIG="$scratch/helm.json"
export KUBECONFIG="$scratch/kubeconfig" KIND_EXPERIMENTAL_PROVIDER=docker
owned_node=''
cleanup() {
  local status=$?
  delete_consumer_cluster || status=1
  rm -rf -- "$scratch"
  exit "$status"
}
trap cleanup EXIT
export GAMEPLANE_PUBLISHED_CHART="$scratch/gameplane" GAMEPLANE_RELEASE_DIGESTS="$scratch/images.tsv"
export GAMEPLANE_PREVIOUS_RELEASE_DIGESTS="$scratch/previous-images.tsv"
mkdir -p "$DOCKER_CONFIG"
cosign initialize

chart="$registry/charts/gameplane"
digest="$(oras resolve "$chart:$version")"
verify_release_signature "$chart" "$digest"
helm pull "oci://$chart@$digest" --untar --untardir "$scratch"
test "$(yq -r '.version' "$GAMEPLANE_PUBLISHED_CHART/Chart.yaml")" = "$version"
test "$(yq -r '.appVersion' "$GAMEPLANE_PUBLISHED_CHART/Chart.yaml")" = "$version"
printf 'Verified chart `%s@%s`\n' "$chart" "$digest" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"

# Read the publisher's complete matrix, including optional images. Keep the
# signed index and its amd64 manifest/config IDs for containerd comparisons.
git fetch --quiet --no-tags --depth=1 origin "refs/tags/$tag"
revision=$(git rev-parse --verify 'FETCH_HEAD^{commit}')
git show "$revision:.github/workflows/release.yaml" > "$scratch/published-release.yaml"
printf 'Release `%s` at Git commit `%s`\n' "$tag" "$revision" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
components="$(yq -r '.jobs.images.strategy.matrix.include[].component' "$scratch/published-release.yaml")"
test -n "$components"
while read -r component; do
  verify_release_image "$registry/gameplane/$component" "$version" "$revision" "$GAMEPLANE_RELEASE_DIGESTS"
done <<< "$components"

run_id=$(cat /proc/sys/kernel/random/uuid)
fresh="gp-fresh-$run_id"
create_consumer_cluster "$fresh"
helm install gameplane "$GAMEPLANE_PUBLISHED_CHART" --kube-context "kind-$fresh" \
  --namespace gameplane-system --create-namespace --set ingress.enabled=false \
  --set defaultModuleSource.enabled=false --wait --timeout 6m
bash deploy/kind/verify-release-runtime.sh "kind-$fresh" "$GAMEPLANE_RELEASE_DIGESTS" api operator web
delete_consumer_cluster

# The regular upgrade fixture seeds the old API database and game PVC,
# exercises the CRD hooks, and checks their contents after the upgrade.
export GAMEPLANE_E2E_CLUSTER="gp-upgrade-$run_id" GAMEPLANE_E2E_REUSE_CLUSTER=1
export GAMEPLANE_E2E_CONTEXT="kind-$GAMEPLANE_E2E_CLUSTER"
create_consumer_cluster "$GAMEPLANE_E2E_CLUSTER"
deploy/kind/upgrade.sh install "$GAMEPLANE_E2E_CLUSTER"
(cd test/e2e && go test -tags=e2e -timeout 25m -v -run '^TestUpgrade_FromPreviousRelease$' .)
