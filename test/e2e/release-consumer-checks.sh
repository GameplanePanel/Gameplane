#!/usr/bin/env bash
# CI checks for the release consumer's artifact and cluster ownership gates.
set -euo pipefail
source deploy/kind/release-consumer-lib.sh

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
