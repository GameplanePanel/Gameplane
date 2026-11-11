#!/usr/bin/env bash
# CI-only real scrape fixture; never invoke as a local validation shortcut.
set -euo pipefail
cd "$(dirname "$0")/../.."
context="${CTX:-kind-gameplane-e2e}"
kubectl() { command kubectl --context "$context" "$@"; }
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
# Retry transient upstream failures without relaxing the pinned checksum below.
curl --fail --silent --show-error --location \
  --connect-timeout 10 --max-time 30 --retry 5 --retry-max-time 120 \
  https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/v0.94.1/bundle.yaml \
  -o "$scratch/bundle.yaml"
echo "7ab177610b271c70c57c7d6fc550a1e934eff4f707a5d8b61dfaa3845d52deea  $scratch/bundle.yaml" | sha256sum --check
kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -
# The official bundle's namespaced objects and binding subjects use default.
sed 's/namespace: default/namespace: monitoring/g' "$scratch/bundle.yaml" > "$scratch/monitoring.yaml"
kubectl apply --server-side -f "$scratch/monitoring.yaml"
kubectl wait --for=condition=Established --timeout=120s crd/prometheuses.monitoring.coreos.com crd/podmonitors.monitoring.coreos.com crd/servicemonitors.monitoring.coreos.com crd/prometheusrules.monitoring.coreos.com
kubectl rollout status -n monitoring deployment/prometheus-operator --timeout=180s
helm --kube-context "$context" upgrade gameplane charts/gameplane --namespace gameplane-system --reuse-values \
  -f docs/examples/monitoring/gameplane-values.yaml --wait --timeout 5m
kubectl apply -f docs/examples/monitoring/prometheus.yaml
kubectl wait -n monitoring --for=create statefulset/prometheus-gameplane --timeout=120s
kubectl rollout status -n monitoring statefulset/prometheus-gameplane --timeout=180s
