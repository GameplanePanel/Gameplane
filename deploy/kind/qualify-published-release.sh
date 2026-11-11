#!/usr/bin/env bash
# Release CI runs this on a fresh amd64 runner without a registry login.
set -euo pipefail
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
git show FETCH_HEAD:.github/workflows/release.yaml > "$scratch/published-release.yaml"
components="$(yq -r '.jobs.images.strategy.matrix.include[].component' "$scratch/published-release.yaml")"
test -n "$components"
while read -r component; do
  image="$registry/gameplane/$component"
  index="$(oras resolve "$image:$version")"
  ref="$image@$index"
  cosign verify --key signing/cosign.pub --new-bundle-format=false "$ref" >/dev/null
  docker pull --platform linux/amd64 "$ref"
  manifest="$(oras manifest fetch "$ref" | jq -er '[.manifests[] | select(.platform.os == "linux" and .platform.architecture == "amd64") | .digest] | if length == 1 then .[0] else error("expected one amd64 image") end')"
  config="$(oras manifest fetch "$image@$manifest" | jq -er '.config.digest')"
  printf '%s\t%s\t%s\t%s\t%s\n' "$component" "$image:$version" "$index" "$manifest" "$config" >> "$GAMEPLANE_RELEASE_DIGESTS"
  printf '\n- `%s`\n' "$ref" >> "$GITHUB_STEP_SUMMARY"
done <<< "$components"

kind create cluster --name gp-consumer-fresh --wait 90s
helm install gameplane "$GAMEPLANE_PUBLISHED_CHART" --kube-context kind-gp-consumer-fresh \
  --namespace gameplane-system --create-namespace --set ingress.enabled=false \
  --set defaultModuleSource.enabled=false --wait --timeout 6m
bash deploy/kind/verify-release-runtime.sh kind-gp-consumer-fresh api operator web
kind delete cluster --name gp-consumer-fresh

# The regular upgrade fixture seeds the old API database and game PVC,
# exercises the CRD hooks, and checks their contents after the upgrade.
export GAMEPLANE_E2E_CLUSTER=gp-consumer-upgrade GAMEPLANE_E2E_REUSE_CLUSTER=1
deploy/kind/upgrade.sh up "$GAMEPLANE_E2E_CLUSTER"
(cd test/e2e && go test -tags=e2e -timeout 25m -v -run '^TestUpgrade_FromPreviousRelease$' .)
