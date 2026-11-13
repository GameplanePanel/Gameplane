#!/usr/bin/env bash
# Install the upstream core controller only on the disposable upgrade cluster.
set -euo pipefail
context="${CTX:?kind upgrade context required}"
[[ "$context" == "kind-${CLUSTER:?kind upgrade cluster required}" ]]
kubectl() { command kubectl --context "$context" "$@"; }
bundle="$(mktemp)"
trap 'rm -f "$bundle"' EXIT
# argoproj/argo-cd v3.6.1
curl --fail --location --silent --show-error --retry 4 --max-time 80 \
    https://raw.githubusercontent.com/argoproj/argo-cd/94037c4d4e1af4bd0651d37a179fbd9e347e668c/manifests/core-install.yaml \
    --output "$bundle"
echo "0b38189d6e1fd76ef5c3db10355026c48a02020d82bf21a1a1b5ab16dffe4f3c  $bundle" | sha256sum --check
kubectl create namespace argocd
kubectl apply --server-side --namespace argocd --filename "$bundle"
kubectl set env -n argocd deployment/argocd-repo-server --containers=argocd-repo-server ARGOCD_GIT_MODULES_ENABLED=false
# Immediately after creation a CRD can report null conditions. Wait for
# the API to publish Established=True on both resources before discovery.
crds=(crd/applications.argoproj.io crd/appprojects.argoproj.io)
deadline=$((SECONDS + 120))
until kubectl get "${crds[@]}" --request-timeout=5s -o json |
    jq -e '.items | length == 2 and all(.[]; any(.status.conditions[]?; .type == "Established" and .status == "True"))' >/dev/null; do
    if ((SECONDS >= deadline)); then
        kubectl get "${crds[@]}" -o json | jq '.items[] | {name: .metadata.name, conditions: .status.conditions}' >&2
        exit 1
    fi
    sleep 1
done
kubectl rollout status -n argocd deployment/argocd-repo-server --timeout=4m
kubectl rollout status -n argocd statefulset/argocd-application-controller --timeout=4m
