#!/usr/bin/env bash
# CI checks for the release consumer's artifact and cluster ownership gates.
set -euo pipefail
source deploy/kind/release-consumer-lib.sh

# A different component in the same release can have a valid project-key
# signature and identical revision/version labels. Its signed name differs.
cosign() {
  echo '[{"critical":{"identity":{"docker-reference":"ghcr.io/example/gameplane/web"},"image":{"docker-manifest-digest":"sha256:abc"}}}]'
}
verify_release_signature ghcr.io/example/gameplane/web sha256:abc
if verify_release_signature ghcr.io/example/gameplane/api sha256:abc ||
   verify_release_signature ghcr.io/example/gameplane/web sha256:def; then
  echo "Accepted a signature for another repository or digest" >&2
  exit 1
fi
unset -f cosign

revision=040e10ee16253f6ead593bd076ed90835b68e2c1
config='{"os":"linux","architecture":"amd64","config":{"Labels":{"org.opencontainers.image.revision":"040e10ee16253f6ead593bd076ed90835b68e2c1","org.opencontainers.image.version":"0.3.0"}}}'
verify_release_config amd64 "$revision" 0.3.0 <<< "$config"
for args in "amd64 07bee47bf01a7ced0114898605155499065dfb26 0.3.0" "arm64 $revision 0.3.0" "amd64 $revision 0.4.1"; do
  read -r arch commit version <<< "$args"
  if verify_release_config "$arch" "$commit" "$version" <<< "$config"; then
    echo "Accepted mismatched image config for $args" >&2
    exit 1
  fi
done

scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT

# Keep an amd64 image in Docker while checking every arm64 layer by digest.
# Run outside Actions too, where no summary file is configured.
(
  unset GITHUB_STEP_SUMMARY
  image=ghcr.io/example/gameplane/web
  docker() { test "$*" = "pull --platform linux/amd64 $image@sha256:abc"; }
  cosign() {
    echo '[{"critical":{"identity":{"docker-reference":"ghcr.io/example/gameplane/web"},"image":{"docker-manifest-digest":"sha256:abc"}}}]'
  }
  oras() {
    case "$*" in
      "resolve $image:0.3.0") echo 'sha256:abc' ;;
      "manifest fetch $image@sha256:abc")
        echo '{"manifests":[{"platform":{"os":"linux","architecture":"amd64"},"digest":"amd64"},{"platform":{"os":"linux","architecture":"arm64"},"digest":"arm64"}]}' ;;
      "manifest fetch $image@amd64") echo '{"config":{"digest":"config-amd64"}}' ;;
      "manifest fetch $image@arm64") echo '{"config":{"digest":"config-arm64"},"layers":[{"digest":"first"},{"digest":"second"}]}' ;;
      "blob fetch --output - $image@config-amd64") echo "$fixture_config" ;;
      "blob fetch --output - $image@config-arm64") echo "${fixture_config/amd64/arm64}" ;;
      "blob fetch --output - $image@first"|"blob fetch --output - $image@second")
        echo "${*: -1}" >> "$scratch/layers"
        [[ ${*: -1} != "$image@${missing:-}" ]] ;;
      *) return 1 ;;
    esac
  }
  fixture_config=$config
  verify_release_image "$image" 0.3.0 "$revision" "$scratch/digests"
  printf '%s\n' "$image@first" "$image@second" | diff - "$scratch/layers"
  missing=first
  if verify_release_image "$image" 0.3.0 "$revision" "$scratch/digests"; then
    echo "Accepted an arm64 manifest with a missing layer" >&2
    exit 1
  fi
)

export KUBECONFIG="$scratch/kubeconfig"
node_id=$(printf 'a%.0s' {1..64})
owned_node=''
clusters='occupied'
kind() {
  case "$1 $2" in
    'get clusters') echo "$clusters" ;;
    'create cluster') echo create >> "$scratch/actions" ;;
    *) return 1 ;;
  esac
}
docker() {
  case "$1" in
    inspect) echo "$node_id" ;;
    exec) printf 'fixture CA' ;;
    rm) printf '%s\n' "$*" >> "$scratch/actions" ;;
    *) return 1 ;;
  esac
}
kubectl() { printf 'fixture CA' | base64 -w0; }
if create_consumer_cluster occupied; then exit 1; fi
delete_consumer_cluster
test ! -e "$scratch/actions"

kubectl() { printf 'different CA' | base64 -w0; }
if create_consumer_cluster replaced; then exit 1; fi
delete_consumer_cluster
test "$(cat "$scratch/actions")" = create

kubectl() { printf 'fixture CA' | base64 -w0; }
create_consumer_cluster mine
original=$node_id
node_id=$(printf 'b%.0s' {1..64})
delete_consumer_cluster
test "$(tail -1 "$scratch/actions")" = "rm --force --volumes $original"

# Exercise Kind's real CA data and Docker node identity on the same runner
# used for release consumption before the populated upgrade cluster starts.
unset -f kind docker kubectl
export KIND_EXPERIMENTAL_PROVIDER=docker
trap 'delete_consumer_cluster; rm -rf -- "$scratch"' EXIT
name="gp-check-$(cat /proc/sys/kernel/random/uuid)"
create_consumer_cluster "$name"
original=$owned_node
kubectl --kubeconfig "$KUBECONFIG" --context "kind-$name" get node "$name-control-plane"
delete_consumer_cluster
if docker inspect --type container "$original" >/dev/null 2>&1; then
  echo "Owned control plane still exists after cleanup" >&2
  exit 1
fi
