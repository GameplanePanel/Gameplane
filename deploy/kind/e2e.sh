#!/usr/bin/env bash
# Ephemeral kind cluster for end-to-end tests.
#
# Usage:
#   deploy/kind/e2e.sh up    [cluster-name] [tag]
#   deploy/kind/e2e.sh down  [cluster-name]
#
# Diffs from dev-up (deploy/kind/up.sh):
#   - Single-node cluster (faster boot, sufficient for E2E coverage).
#   - Skips ingress-nginx (the dashboard isn't exercised here).
#   - Loads pre-built gameplane-test/{operator,api,agent,sentinel,capture-sidecar,telemetry-receiver,fakeoidc}:<tag>
#     images (and gameplane-test/gameprobe:<tag> when present).
#   - Helm install with --wait so pods are Ready before tests start.
#
# Image tag defaults to "e2e". Override via the second argument or
# the GAMEPLANE_E2E_TAG env var.

set -euo pipefail

ACTION="${1:-up}"
CLUSTER="${2:-gameplane-e2e}"
TAG="${3:-${GAMEPLANE_E2E_TAG:-e2e}}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "${HERE}/../.." && pwd)"
CHART_DIR="${REPO}/charts/gameplane"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1" >&2; exit 1; }; }

# MetalLB manifest pin. Bumping it is a deliberate edit — never fetch a
# floating ref, or a CI run silently changes its load-balancer implementation.
METALLB_VERSION="v0.14.9"

# kind ships no LoadBalancer implementation, so without MetalLB every
# LoadBalancer Service sits at <pending> forever and the address-pool tests
# would assert against nothing.
install_metallb() {
    if kubectl get namespace metallb-system >/dev/null 2>&1; then
        echo "MetalLB already installed — skipping"
        return 0
    fi
    echo "installing MetalLB ${METALLB_VERSION}"
    kubectl apply -f \
        "https://raw.githubusercontent.com/metallb/metallb/${METALLB_VERSION}/config/manifests/metallb-native.yaml"
    kubectl wait --namespace metallb-system \
        --for=condition=Available deployment/controller --timeout=180s
    # `kubectl wait --selector` exits 1 with "no matching resources found" when
    # the selector matches nothing *at call time* — it does not poll for the
    # first match — which under `set -e` aborts the whole bootstrap. Wait for
    # the DaemonSet to materialise first; rollout status does poll.
    kubectl rollout status --namespace metallb-system \
        daemonset/speaker --timeout=180s
    kubectl wait --namespace metallb-system \
        --for=condition=Ready pod \
        --selector=component=speaker \
        --timeout=180s
}

# The two test pools the Track B e2e coverage assigns from. Their ranges MUST
# be carved from the kind docker bridge subnet: an address outside it is
# unroutable from the nodes, so MetalLB would hand out addresses nothing can
# reach and every pool assertion would pass on a dead endpoint. The prefix is
# read back from docker rather than hardcoded, because the bridge subnet is
# docker's choice (commonly 172.18.0.0/16) and not guaranteed.
apply_metallb_pools() {
    local subnet prefix range_east range_west range_single attempt
    subnet="$(docker network inspect kind \
        -f '{{range .IPAM.Config}}{{.Subnet}} {{end}}' | tr ' ' '\n' | grep -m1 '\.' || true)"
    case "${subnet}" in
    */16) ;;
    *)
        echo "unexpected kind bridge subnet '${subnet}' (want an IPv4 /16) — cannot carve address pools" >&2
        return 1
        ;;
    esac
    prefix="$(echo "${subnet}" | cut -d. -f1,2)"
    range_east="${prefix}.255.100-${prefix}.255.110"
    range_west="${prefix}.255.200-${prefix}.255.210"
    range_single="${prefix}.255.150-${prefix}.255.150"

    echo "defining MetalLB pools pool-us-east (${range_east}), pool-us-west (${range_west}) and pool-e2e-single (${range_single})"
    # IPAddressPool and L2Advertisement are gated by MetalLB's validating
    # webhook, which keeps refusing connections for a few seconds after the
    # controller Deployment reports Available. Retry instead of racing it.
    for attempt in 1 2 3 4 5 6 7 8 9 10; do
        if kubectl apply -f - <<EOF
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: pool-us-east
  namespace: metallb-system
spec:
  addresses:
    - ${range_east}
---
# pool-us-west never auto-assigns: only a Service that explicitly names it
# draws from it, so "the address came from pool-us-west" is proof the
# operator's pool translation ran, not an accident of allocation order.
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: pool-us-west
  namespace: metallb-system
spec:
  autoAssign: false
  addresses:
    - ${range_west}
---
# pool-e2e-single holds exactly one address and never auto-assigns. It exists
# only so TestAddressPool_PoolExhausted can exhaust a pool without starving the
# parallel tests that draw from pool-us-east and pool-us-west. Do not reference
# it from any other test.
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: pool-e2e-single
  namespace: metallb-system
spec:
  autoAssign: false
  addresses:
    - ${range_single}
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: pool-us-east
  namespace: metallb-system
spec:
  ipAddressPools:
    - pool-us-east
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: pool-us-west
  namespace: metallb-system
spec:
  ipAddressPools:
    - pool-us-west
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: pool-e2e-single
  namespace: metallb-system
spec:
  ipAddressPools:
    - pool-e2e-single
EOF
        then
            return 0
        fi
        echo "  pool apply failed (attempt ${attempt}/10) — waiting for the MetalLB webhook"
        sleep 5
    done
    echo "MetalLB pools never applied — webhook did not come up" >&2
    return 1
}

case "${ACTION}" in
up)
    need kind
    need kubectl
    need helm
    need docker

    # Start from a clean cluster every time. The self-hosted CI runner is
    # persistent, so a cancelled run can leave a zombie cluster behind: kind
    # still lists it, but its kubeconfig context is gone (kubectl then falls
    # back to localhost:8080 and everything fails) and a leftover Helm release
    # would break the reinstall. Delete any same-named cluster before creating.
    # Local fast-iteration reuses the cluster via `make test-e2e-keep`, which
    # does not call this `up` path.
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

    install_metallb
    apply_metallb_pools

    echo "loading gameplane-test/{operator,api,agent,sentinel,capture-sidecar,telemetry-receiver}:${TAG} images into kind"
    for img in operator api agent sentinel capture-sidecar telemetry-receiver; do
        if ! docker image inspect "gameplane-test/${img}:${TAG}" >/dev/null 2>&1; then
            echo "  missing local image gameplane-test/${img}:${TAG} — building"
            docker build -t "gameplane-test/${img}:${TAG}" -f "${REPO}/${img}/Dockerfile" "${REPO}"
        fi
        kind load docker-image "gameplane-test/${img}:${TAG}" --name "${CLUSTER}"
    done

    # The game-bot probe runs the protocol bot inside the cluster. Only the
    # game-bot job builds it, so load it when it happens to be present rather
    # than forcing every other bucket to build it.
    if docker image inspect "gameplane-test/gameprobe:${TAG}" >/dev/null 2>&1; then
        echo "loading gameplane-test/gameprobe:${TAG} into kind"
        kind load docker-image "gameplane-test/gameprobe:${TAG}" --name "${CLUSTER}"
    fi

    # The fake OIDC issuer (test/e2e/internal/fakeoidc) backs the
    # Helm-seeded OIDC role-mapping e2e tests (T049). Every bucket's
    # cluster gets it — its Dockerfile lives outside the per-component
    # <name>/Dockerfile convention, so it isn't covered by the loop above.
    echo "loading gameplane-test/fakeoidc:${TAG} into kind"
    if ! docker image inspect "gameplane-test/fakeoidc:${TAG}" >/dev/null 2>&1; then
        echo "  missing local image gameplane-test/fakeoidc:${TAG} — building"
        docker build -t "gameplane-test/fakeoidc:${TAG}" -f "${REPO}/test/e2e/Dockerfile.fakeoidc" "${REPO}"
    fi
    kind load docker-image "gameplane-test/fakeoidc:${TAG}" --name "${CLUSTER}"

    # Create a second StorageClass for e2e install-time-default testing.
    # This must be created before the Helm install so the operator can find
    # it when materializing PVCs.
    echo "creating gameplane-e2e-install-default StorageClass"
    kubectl apply -f - <<'EOF'
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: gameplane-e2e-install-default
provisioner: rancher.io/local-path
# volumeBindingMode must be WaitForFirstConsumer because the rancher.io/local-path
# provisioner cannot bind immediately — it needs to know which node the consuming pod
# landed on before it can provision. Omitting this field causes every PVC to hang at
# Pending indefinitely.
volumeBindingMode: WaitForFirstConsumer
reclaimPolicy: Delete
EOF

    # Stand up the fake OIDC issuer and its Secret *before* the one Helm
    # install below: the API process performs OIDC discovery against
    # --oidc-issuer synchronously at startup (api/cmd/main.go), once,
    # never retried — if the issuer isn't already reachable the "helm"
    # provider silently never comes into existence for the pod's whole
    # lifetime (see api/internal/auth/registry.go's Registry.legacy).
    # gameplane-system must pre-exist since Helm's --create-namespace
    # only runs during the install call further down.
    echo "creating gameplane-system namespace (pre-Helm, for the fake-OIDC fixture)"
    kubectl create namespace gameplane-system --dry-run=client -o yaml | kubectl apply -f -

    echo "creating gameplane-oidc client-secret Secret"
    kubectl create secret generic gameplane-oidc \
        --namespace gameplane-system \
        --from-literal=clientSecret=e2e-test-oidc-client-secret \
        --dry-run=client -o yaml | kubectl apply -f -

    echo "deploying gameplane-test-fakeoidc fixture"
    kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gameplane-test-fakeoidc
  namespace: gameplane-system
  labels:
    app.kubernetes.io/name: gameplane-test-fakeoidc
    app.kubernetes.io/component: e2e-fixture
spec:
  replicas: 1
  selector:
    matchLabels: { app.kubernetes.io/name: gameplane-test-fakeoidc }
  template:
    metadata:
      labels: { app.kubernetes.io/name: gameplane-test-fakeoidc }
    spec:
      securityContext:
        runAsNonRoot: true
        seccompProfile: { type: RuntimeDefault }
      containers:
        - name: fakeoidc
          image: gameplane-test/fakeoidc:${TAG}
          imagePullPolicy: Never
          securityContext:
            allowPrivilegeEscalation: false
            capabilities: { drop: [ALL] }
          resources:
            requests: { cpu: 10m, memory: 16Mi }
            limits: { cpu: 200m, memory: 64Mi }
          ports:
            - { name: http, containerPort: 8080 }
          env:
            - { name: ISSUER, value: "http://gameplane-test-fakeoidc.gameplane-system.svc.cluster.local:8080" }
            - { name: CLIENT_ID, value: "gameplane-e2e-helm-oidc" }
            - { name: REDIRECT_URI, value: "https://gameplane.e2e.invalid/auth/oidc/helm/callback" }
          readinessProbe:
            httpGet: { path: /.well-known/openid-configuration, port: http }
            initialDelaySeconds: 1
---
apiVersion: v1
kind: Service
metadata:
  name: gameplane-test-fakeoidc
  namespace: gameplane-system
  labels:
    app.kubernetes.io/name: gameplane-test-fakeoidc
    app.kubernetes.io/component: e2e-fixture
spec:
  selector: { app.kubernetes.io/name: gameplane-test-fakeoidc }
  ports:
    - { name: http, port: 8080, targetPort: http }
EOF
    kubectl rollout status --namespace gameplane-system \
        deployment/gameplane-test-fakeoidc --timeout=120s

    echo "helm upgrade --install gameplane"
    # Bump the API container's memory limit above the chart default of
    # 256Mi. The bootstrap-admin subcommand and every login endpoint
    # invocation runs argon2id, which uses ~64Mi of working memory per
    # hash; the default limit OOM-kills the container under e2e's
    # frequent BootstrapAdmin/APIClient calls. The suite runs with
    # t.Parallel(), so several logins (64Mi each) can hash at once —
    # 1Gi keeps ~4 concurrent hashes plus baseline comfortably clear.
    # Disable the default upstream ModuleSource: it points at a public OCI
    # registry the e2e cluster can't reach, so `--wait` would block on it
    # (kstatus reports the never-indexed source as InProgress) until timeout.
    # The module e2e tests provision their own in-cluster registry + source.
    # Disable the web front end: the suite drives the API directly via
    # port-forward (never the browser), so building/loading the nginx image
    # would only add minutes, and `--wait` would block on an unloaded image.
    # Deploy the bundled telemetry receiver and point the API at it: never let
    # CI or dev installs reach the project provider (spec 022 R17).
    helm upgrade --install gameplane "${CHART_DIR}" \
        --namespace gameplane-system --create-namespace \
        --set "image.registry=gameplane-test" \
        --set "image.tag=${TAG}" \
        --set "ingress.enabled=false" \
        --set "web.enabled=false" \
        --set "operator.agentImage=gameplane-test/agent:${TAG}" \
        --set "operator.sentinelImage=gameplane-test/sentinel:${TAG}" \
        --set "capture.enabled=true" \
        --set "capture.image=gameplane-test/capture-sidecar:${TAG}" \
        --set "api.resources.limits.memory=1Gi" \
        --set "api.oidc.enabled=true" \
        --set "api.oidc.issuer=http://gameplane-test-fakeoidc.gameplane-system.svc.cluster.local:8080" \
        --set "api.oidc.clientID=gameplane-e2e-helm-oidc" \
        --set "api.oidc.redirectURL=https://gameplane.e2e.invalid/auth/oidc/helm/callback" \
        --set "api.oidc.roleMappings.admin[0]=gameplane-e2e-oidc-admins" \
        --set "operator.leaderElect=false" \
        --set "operator.addressManager=metallb" \
        --set "operator.gameDataStorage.storageClassName=gameplane-e2e-install-default" \
        --set "defaultModuleSource.enabled=false" \
        --set "api.telemetry.receiver.enabled=true" \
        --wait --timeout 5m

    echo
    echo "✓ cluster ${CLUSTER} ready (image tag ${TAG})"
    echo "  GAMEPLANE_E2E_REUSE_CLUSTER=1 reuses this cluster across go test runs"
    ;;

down)
    if ! kind get clusters | grep -qx "${CLUSTER}"; then
        echo "cluster ${CLUSTER} not found — nothing to do"
        exit 0
    fi
    kind delete cluster --name "${CLUSTER}"
    ;;

*)
    echo "usage: $0 up|down [cluster-name] [tag]" >&2
    exit 2
    ;;
esac
