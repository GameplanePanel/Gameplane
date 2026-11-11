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
mkdir -p "$DOCKER_CONFIG"
cosign initialize

chart="$registry/charts/gameplane"
digest="$(oras resolve "$chart:$version")"
cosign verify --key signing/cosign.pub --new-bundle-format=false "$chart@$digest" >/dev/null
helm pull "oci://$chart@$digest" --untar --untardir "$scratch"
test "$(yq -r '.version' "$GAMEPLANE_PUBLISHED_CHART/Chart.yaml")" = "$version"
test "$(yq -r '.appVersion' "$GAMEPLANE_PUBLISHED_CHART/Chart.yaml")" = "$version"
printf 'Verified chart `%s@%s`\n' "$chart" "$digest" >> "$GITHUB_STEP_SUMMARY"

# Read the publisher's complete matrix, including optional images. Keep the
# signed index and its amd64 manifest/config IDs for containerd comparisons.
git fetch --quiet --no-tags --depth=1 origin "refs/tags/$tag"
revision=$(git rev-parse --verify 'FETCH_HEAD^{commit}')
git show "$revision:.github/workflows/release.yaml" > "$scratch/published-release.yaml"
printf 'Release `%s` at Git commit `%s`\n' "$tag" "$revision" >> "$GITHUB_STEP_SUMMARY"
components="$(yq -r '.jobs.images.strategy.matrix.include[].component' "$scratch/published-release.yaml")"
test -n "$components"
while read -r component; do
  image="$registry/gameplane/$component"
  index="$(oras resolve "$image:$version")"
  ref="$image@$index"
  cosign verify --key signing/cosign.pub --new-bundle-format=false "$ref" >/dev/null
  docker pull --platform linux/amd64 "$ref"
  printf '\n- `%s`\n' "$ref" >> "$GITHUB_STEP_SUMMARY"
  index_json=$(oras manifest fetch "$ref")
  for arch in amd64 arm64; do
    manifest=$(jq -er --arg arch "$arch" '
      [.manifests[] | select(.platform.os == "linux" and .platform.architecture == $arch) | .digest]
      | if length == 1 then .[0] else error("expected one linux/" + $arch + " manifest") end' <<< "$index_json")
    config=$(oras manifest fetch "$image@$manifest" | jq -er '.config.digest')
    if ! oras blob fetch --output - "$image@$config" | verify_release_config "$arch" "$revision" "$version"; then
      echo "$image@$config must identify linux/$arch, version $version and Git commit $revision" >&2
      exit 1
    fi
    printf '  - linux/%s manifest `%s`, config `%s`\n' "$arch" "$manifest" "$config" >> "$GITHUB_STEP_SUMMARY"
    if [[ $arch == amd64 ]]; then
      printf '%s\t%s\t%s\t%s\t%s\n' "$component" "$image:$version" "$index" "$manifest" "$config" >> "$GAMEPLANE_RELEASE_DIGESTS"
    fi
  done
done <<< "$components"

run_id=$(cat /proc/sys/kernel/random/uuid)
fresh="gp-fresh-$run_id"
create_consumer_cluster "$fresh"
helm install gameplane "$GAMEPLANE_PUBLISHED_CHART" --kube-context "kind-$fresh" \
  --namespace gameplane-system --create-namespace --set ingress.enabled=false \
  --set defaultModuleSource.enabled=false --wait --timeout 6m
bash deploy/kind/verify-release-runtime.sh "kind-$fresh" api operator web
delete_consumer_cluster

# The regular upgrade fixture seeds the old API database and game PVC,
# exercises the CRD hooks, and checks their contents after the upgrade.
export GAMEPLANE_E2E_CLUSTER="gp-upgrade-$run_id" GAMEPLANE_E2E_REUSE_CLUSTER=1
export GAMEPLANE_E2E_CONTEXT="kind-$GAMEPLANE_E2E_CLUSTER"
create_consumer_cluster "$GAMEPLANE_E2E_CLUSTER"
deploy/kind/upgrade.sh install "$GAMEPLANE_E2E_CLUSTER"
(cd test/e2e && go test -tags=e2e -timeout 25m -v -run '^TestUpgrade_FromPreviousRelease$' .)
