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
#   deploy/kind/upgrade.sh install <existing-kind-cluster> [tag]
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
# 0.3.0 is the last published release — its chart and operator/api/agent
# images exist on GHCR — so this is the genuine upgrade path that users take
# to the next release.
FROM_VERSION="${GAMEPLANE_UPGRADE_FROM:-0.3.0}"
# Releases up to v0.3.0 were published before the repo moved to the
# GameplanePanel org, so they live under the original owner on GHCR.
FROM_REGISTRY="ghcr.io/valgulnecron"
CHART_REF="oci://$FROM_REGISTRY/charts/gameplane"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1" >&2; exit 1; }; }

case "${ACTION}" in
up|install)
    need kind
    need kubectl
    need helm
    need docker

    if [[ "$ACTION" == up ]]; then
        # Kind's default is a single control-plane node. Creation rejects
        # collisions and leaves the existing cluster and its volumes alone.
        kind create cluster --name "${CLUSTER}" --wait 90s
    elif [[ -z ${2:-} ]]; then
        echo "install requires the name of the existing Kind cluster" >&2
        exit 2
    fi

    kubectl cluster-info --context "kind-${CLUSTER}" >/dev/null

    # Load the NEW (working-tree) images now, so the upgrade the test performs
    # later needs no network. The OLD release's images are pulled from GHCR by
    # the install below — that is the point of the test.
    if [[ -z "${GAMEPLANE_PUBLISHED_CHART:-}" ]]; then
        echo "loading working-tree gameplane-test/{operator,api,agent,sentinel}:${TAG} into kind"
        for img in operator api agent sentinel; do
            if ! docker image inspect "gameplane-test/${img}:${TAG}" >/dev/null 2>&1; then
                echo "  missing local image gameplane-test/${img}:${TAG} — building"
                docker build -t "gameplane-test/${img}:${TAG}" -f "${REPO}/${img}/Dockerfile" "${REPO}"
            fi
            kind load docker-image "gameplane-test/${img}:${TAG}" --name "${CLUSTER}"
        done
    fi

    chart_args=(--version "$FROM_VERSION")
    if [[ -n ${GAMEPLANE_PUBLISHED_CHART:-} ]]; then
        # Resolve the documented predecessor in its original registry. Record
        # what should be running before the seeded game and database migrate.
        source "$HERE/release-consumer-lib.sh"
        git -C "$REPO" fetch --quiet --no-tags --depth=1 origin "refs/tags/v$FROM_VERSION"
        previous_commit=$(git -C "$REPO" rev-parse --verify 'FETCH_HEAD^{commit}')
        repository=${CHART_REF#oci://}
        digest=$(oras resolve "$repository:$FROM_VERSION")
        verify_release_signature "$repository" "$digest"
        CHART_REF="$CHART_REF@$digest"
        chart_args=()
        metadata=$(helm show chart "$CHART_REF")
        [[ $(yq -r '.version' <<< "$metadata") == "$FROM_VERSION" &&
           $(yq -r '.appVersion' <<< "$metadata") == "$FROM_VERSION" ]]
        printf '\nPrevious chart `%s@%s`, version `%s`, Git commit `%s`\n' \
            "$repository" "$digest" "$FROM_VERSION" "$previous_commit" >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
        for component in api operator agent; do
            verify_release_image "$FROM_REGISTRY/gameplane/$component" "$FROM_VERSION" "$previous_commit" "$GAMEPLANE_PREVIOUS_RELEASE_DIGESTS"
        done
    fi
    echo "installing PUBLISHED chart ${CHART_REF}, version ${FROM_VERSION}"
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
        --kube-context "kind-${CLUSTER}" \
        "${chart_args[@]}" \
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
    echo "usage: $0 {up|install|down} [cluster-name] [tag]" >&2
    exit 2
    ;;
esac
