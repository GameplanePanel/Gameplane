#!/usr/bin/env bash
# Classic keyed cosign verification checks the digest. Also check the
# repository named in the signed payload before trusting a component.
verify_release_signature() {
  local repository=$1 digest=$2
  if ! cosign verify --key signing/cosign.pub --new-bundle-format=false "$repository@$digest" |
    jq -e --arg repo "$repository" --arg digest "$digest" 'any(.[];
      .critical.identity["docker-reference"] == $repo and
      .critical.image["docker-manifest-digest"] == $digest)' >/dev/null; then
    echo "Missing valid release signature for $repository@$digest" >&2
    return 1
  fi
}

# The config blob is addressed through the verified release index.
verify_release_config() {
  jq -e --arg arch "$1" --arg revision "$2" --arg version "$3" '
    .os == "linux" and .architecture == $arch and
    .config.Labels["org.opencontainers.image.revision"] == $revision and
    .config.Labels["org.opencontainers.image.version"] == $version' >/dev/null
}

verify_release_image() {
  local image=$1 version=$2 revision=$3 digest_file=$4
  local index ref index_json arch manifest manifest_json config layers layer
  index=$(oras resolve "$image:$version")
  ref="$image@$index"
  verify_release_signature "$image" "$index"
  docker pull --platform linux/amd64 "$ref"
  printf '\n- `%s`\n' "$ref" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
  index_json=$(oras manifest fetch "$ref")
  for arch in amd64 arm64; do
    manifest=$(jq -er --arg arch "$arch" '
      [.manifests[] | select(.platform.os == "linux" and .platform.architecture == $arch) | .digest]
      | if length == 1 then .[0] else error("expected one linux/" + $arch + " manifest") end' <<< "$index_json")
    manifest_json=$(oras manifest fetch "$image@$manifest")
    config=$(jq -er '.config.digest' <<< "$manifest_json")
    if ! oras blob fetch --output - "$image@$config" | verify_release_config "$arch" "$revision" "$version"; then
      echo "$image@$config must identify linux/$arch, version $version and Git commit $revision" >&2
      return 1
    fi
    printf '  - linux/%s manifest `%s`, config `%s`\n' "$arch" "$manifest" "$config" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
    if [[ $arch == amd64 ]]; then
      printf '%s\t%s\t%s\t%s\t%s\n' "${image##*/}" "$image:$version" "$index" "$manifest" "$config" >> "$digest_file"
    else
      # Docker pulled amd64 above. Read the other platform's blobs without
      # replacing that image in the runner's Docker store.
      layers=$(jq -er '.layers[].digest' <<< "$manifest_json") || return 1
      while IFS= read -r layer; do
        oras blob fetch --output - "$image@$layer" >/dev/null || return 1
      done <<< "$layers"
    fi
  done
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
