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
kubectl wait --for=condition=Established crd/applications.argoproj.io crd/appprojects.argoproj.io --timeout=2m
kubectl rollout status -n argocd deployment/argocd-repo-server --timeout=4m
kubectl rollout status -n argocd statefulset/argocd-application-controller --timeout=4m
