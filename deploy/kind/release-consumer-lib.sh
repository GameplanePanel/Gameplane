#!/usr/bin/env bash
# The config blob is addressed through the verified release index.
verify_release_config() {
  jq -e --arg arch "$1" --arg revision "$2" --arg version "$3" '
    .os == "linux" and .architecture == $arch and
    .config.Labels["org.opencontainers.image.revision"] == $revision and
    .config.Labels["org.opencontainers.image.version"] == $version' >/dev/null
}

# Each consumer cluster has one Docker control-plane node. Capture its
# immutable container ID only after its CA matches the private kubeconfig
# written by this creation. A concurrent name replacement stays untouched.
create_consumer_cluster() {
  local name=$1 existing id node_ca kube_ca
  [[ -z $owned_node ]] || return 1
  existing=$(kind get clusters) || return 1
  if grep -Fxq -- "$name" <<< "$existing"; then
    echo "Kind cluster $name already exists" >&2
    return 1
  fi
  kind create cluster --name "$name" --kubeconfig "$KUBECONFIG" --wait 90s || return 1
  id=$(docker inspect --type container --format '{{.Id}}' "$name-control-plane") || return 1
  [[ $id =~ ^[a-f0-9]{64}$ ]] || return 1
  node_ca=$(docker exec "$id" cat /etc/kubernetes/pki/ca.crt | base64 -w0) || return 1
  kube_ca=$(kubectl config view --kubeconfig "$KUBECONFIG" --context "kind-$name" \
    --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}') || return 1
  if [[ -z $node_ca || $node_ca != "$kube_ca" ]]; then
    echo "Control-plane container for $name does not match its kubeconfig" >&2
    return 1
  fi
  owned_node=$id
}

delete_consumer_cluster() {
  if [[ -n $owned_node ]]; then
    docker rm --force --volumes "$owned_node" >/dev/null || return 1
    owned_node=''
  fi
}
