#!/usr/bin/env bash
# Ephemeral kind cluster seeded with the LAST PUBLISHED RELEASE, for the
# upgrade e2e test.
#
# Why this exists separately from e2e.sh: that script installs the
# working-tree chart with locally-built gameplane-test/* images, which is the
# right thing for every other bucket and exactly the wrong thing here. The
# upgrade test has to start from the artifacts a real user actually installed
# — the published chart, pulling the published GHCR images — and then upgrade
# to the working tree. So this script only does the "install the old release"
# half; the test itself runs `helm upgrade` to the working-tree chart.
#
# Usage:
#   deploy/kind/upgrade.sh up   [cluster-name] [tag]
#   deploy/kind/upgrade.sh down [cluster-name]

set -euo pipefail

ACTION="${1:-up}"
CLUSTER="${2:-gameplane-upgrade}"
TAG="${3:-${GAMEPLANE_E2E_TAG:-e2e}}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "${HERE}/../.." && pwd)"

# The release to upgrade FROM. Bump this at release time to the previous
# published version.
#
# Deliberately an explicit constant rather than "resolve the newest published
# chart": a regression test that silently changes what it tests is not a
# regression test.
#
# 0.2.0-beta.8 is the last published beta before v0.3.0 — its chart and
# operator/api/agent images exist on GHCR — so this is the genuine upgrade
# path that users take to v0.3.0.
FROM_VERSION="${GAMEPLANE_UPGRADE_FROM:-0.2.0-beta.8}"
# Releases up to v0.3.0 were published before the repo moved to the
# GameplanePanel org, so they live under the original owner on GHCR.
CHART_REF="oci://ghcr.io/valgulnecron/charts/gameplane"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1" >&2; exit 1; }; }

case "${ACTION}" in
up)
    need kind
    need kubectl
    need helm
    need docker

    if kind get clusters | grep -qx "${CLUSTER}"; then
        echo "removing pre-existing cluster ${CLUSTER} for a clean slate"
        kind delete cluster --name "${CLUSTER}" || true
    fi
    echo "creating kind cluster ${CLUSTER} (single-node)"
    kind create cluster --name "${CLUSTER}" --wait 90s --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
EOF

    kubectl cluster-info --context "kind-${CLUSTER}" >/dev/null

    # Load the NEW (working-tree) images now, so the upgrade the test performs
    # later needs no network. The OLD release's images are pulled from GHCR by
    # the install below — that is the point of the test.
    echo "loading working-tree gameplane-test/{operator,api,agent,sentinel}:${TAG} into kind"
    for img in operator api agent sentinel; do
        if ! docker image inspect "gameplane-test/${img}:${TAG}" >/dev/null 2>&1; then
            echo "  missing local image gameplane-test/${img}:${TAG} — building"
            docker build -t "gameplane-test/${img}:${TAG}" -f "${REPO}/${img}/Dockerfile" "${REPO}"
        fi
        kind load docker-image "gameplane-test/${img}:${TAG}" --name "${CLUSTER}"
    done

    echo "installing PUBLISHED chart ${CHART_REF} --version ${FROM_VERSION}"
    # Value overrides mirror e2e.sh's, and are all about the harness rather
    # than the version under test:
    #   - api memory 1Gi: argon2id uses ~64Mi per hash and the 256Mi default
    #     OOM-kills the container under the suite's login rate (see e2e.sh).
    #   - defaultModuleSource off: it points at a public OCI registry the
    #     cluster can't reach, so --wait would block until timeout.
    #   - web off: the suite drives the API directly; beta.5 published no web
    #     image at all, so leaving it on would fail the install outright.
    #   - ingress off: nothing here goes through an ingress controller.
    # Note what is NOT overridden: image.registry/image.tag. The old release
    # must pull its own published GHCR images, or this tests nothing.
    helm install gameplane "${CHART_REF}" \
        --version "${FROM_VERSION}" \
        --namespace gameplane-system --create-namespace \
        --set "ingress.enabled=false" \
        --set "web.enabled=false" \
        --set "api.resources.limits.memory=1Gi" \
        --set "operator.leaderElect=false" \
        --set "defaultModuleSource.enabled=false" \
        --wait --timeout 6m

    echo
    echo "✓ cluster ${CLUSTER} running Gameplane ${FROM_VERSION} (published artifacts)"
    echo "  the upgrade test will helm-upgrade this to the working-tree chart"
    ;;

down)
    if ! kind get clusters | grep -qx "${CLUSTER}"; then
        echo "cluster ${CLUSTER} not found — nothing to do"
        exit 0
    fi
    kind delete cluster --name "${CLUSTER}"
    ;;

*)
    echo "usage: $0 {up|down} [cluster-name] [tag]" >&2
    exit 2
    ;;
esac
