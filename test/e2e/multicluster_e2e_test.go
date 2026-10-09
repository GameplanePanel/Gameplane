//go:build e2e

// Dual-cluster coverage for the multi-cluster `?cluster=` request dispatch
// (api/internal/scope.ResolveCluster, api/internal/kube.Registry) and the
// RBAC cluster dimension (api/internal/rbac, migration
// 004_cluster_rbac.sql). Every other test in this package runs against the
// single kind cluster the harness in env.go/e2e_suite_test.go stands up
// ("cluster A" / envInstance, registered under the API's fixed id "local").
// This file additionally requires a SECOND, independent kind cluster
// ("cluster B") to prove a GameServer created via `?cluster=<B>` actually
// lands on the other cluster's Kubernetes API — not just behind a query
// param that the handler ignores — and that a viewer bound only to "local"
// cannot see it.
//
// # Infra this test assumes
//
//   - A second kind cluster is already up, with the same Gameplane chart
//     installed (CRDs + operator: cluster B needs its own operator to
//     reconcile the GameServer this test creates there, per "the operator
//     is authoritative" — see CLAUDE.md rule 10 and
//     docs/install.md#registering-an-additional-cluster). It must be
//     reachable at kubeconfig context "kind-<name>", where <name> comes
//     from GAMEPLANE_E2E_CLUSTER_B (default "gameplane-e2e-b").
//   - The dedicated CI job "e2e-multicluster" in .github/workflows/ci.yaml
//     brings this up by calling `deploy/kind/e2e.sh up gameplane-e2e-b e2e`
//     — the exact same script and image tag as the primary cluster — right
//     after standing up cluster A the normal way.
//   - `kind` and `docker` CLIs on PATH (already required transitively by
//     deploy/kind/e2e.sh).
//
// # Why a raw docker-network IP, not the kubeconfig `kind` hands out
//
// Registering cluster B (POST /clusters) needs a kubeconfig that is
// reachable from INSIDE cluster A's gameplane-api (and, for the health
// reconciler, gameplane-operator) pods — not from the CI runner's own
// network namespace. `kind get kubeconfig` (no flags) points at
// 127.0.0.1:<host-mapped-port>, which is only valid from the runner itself.
// `kind get kubeconfig --internal` instead names the server by the node
// container's hostname (e.g. https://gameplane-e2e-b-control-plane:6443),
// but that hostname is only resolvable via Docker's embedded DNS — kind
// deliberately strips loopback-pointing nameservers from each node's
// resolv.conf, so cluster A's in-pod CoreDNS forwards upstream and never
// reaches it either.
//
// What DOES work: every kind cluster's nodes attach to the same docker
// bridge network ("kind" by default), so cluster B's control-plane
// container has a real, directly-routable IP address on that bridge that
// cluster A's nodes (and therefore its pods, via the node's own egress
// path) can reach at the network layer. podReachableKubeconfig below takes
// the `--internal` kubeconfig (which already carries the right client
// certificate for cluster-admin auth) and swaps the server host for that
// docker-network IP, skipping server-certificate verification since the
// IP is not guaranteed to be one of the API server certificate's SANs —
// client authentication (the part that actually gates access) is
// unaffected by that flag.
//
// If the second cluster/context isn't present — e.g. iterating locally
// against just the single-cluster harness — the test SKIPS rather than
// faking coverage; see ensureClusterB.
package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
)

// clusterGVR is the Cluster CRD (cluster-scoped) the API's registrations
// are stored as.
var clusterGVR = schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "clusters"}

// clusterBKindName is the kind cluster name the CI job "e2e-multicluster"
// stands up as the second, remote-registered cluster. Overridable via
// GAMEPLANE_E2E_CLUSTER_B for local iteration against a differently-named
// second cluster.
func clusterBKindName() string {
	return getenvOr("GAMEPLANE_E2E_CLUSTER_B", "gameplane-e2e-b")
}

// ensureClusterB connects directly to the second kind cluster's Kubernetes
// API (bypassing the Gameplane API entirely), so the test can assert
// ground truth on each cluster independently in addition to dispatching
// through the control-plane API. It skips the test — not a failure — when
// the second cluster isn't configured or reachable: this dual-cluster
// setup is opt-in infra layered on top of the single-cluster harness, not
// part of the default suite every bucket gets for free.
func ensureClusterB(t *testing.T) (envB *Env, name string) {
	t.Helper()
	name = clusterBKindName()
	envB, err := newEnvForContext("kind-" + name)
	if err != nil {
		t.Skipf("second kind cluster %q not configured (kubeconfig context kind-%s): %v — "+
			"this test needs the e2e-multicluster CI job's dual-cluster setup "+
			"(see the package doc in multicluster_e2e_test.go)", name, name, err)
	}
	if err := envB.ensureCluster(); err != nil {
		t.Skipf("second kind cluster %q not reachable: %v — bring it up with "+
			"`deploy/kind/e2e.sh up %s e2e` before running this test", name, err, name)
	}
	return envB, name
}

// podReachableKubeconfig returns a kubeconfig for the kind cluster named
// clusterName that is dialable from a POD running in a DIFFERENT kind
// cluster — see the package doc above for why neither of `kind`'s own two
// kubeconfig flavors (default, --internal) works as-is for that. It shells
// out to `kind` and `docker`, both already required by deploy/kind/e2e.sh.
func podReachableKubeconfig(t *testing.T, clusterName string) []byte {
	t.Helper()

	raw, err := exec.CommandContext(t.Context(), "kind", "get", "kubeconfig", "--internal", "--name", clusterName).Output()
	if err != nil {
		t.Fatalf("kind get kubeconfig --internal --name %s: %v", clusterName, exitErr(err))
	}

	containerName := clusterName + "-control-plane"
	// Target the "kind" network by name rather than taking the first network
	// in map iteration order: `docker inspect` with a bare {{range .Networks}}
	// returns whichever network Go's (unordered) map iteration visits first,
	// which silently picks the wrong IP on a multi-homed container (e.g. one
	// also attached to the local OCI registry's network from `make dev-up`,
	// or another docker-compose network on a shared runner). Every kind node
	// is guaranteed to be on "kind" (see the package doc above), so name it
	// explicitly.
	ipOut, err := exec.CommandContext(t.Context(), "docker", "inspect", "-f",
		`{{(index .NetworkSettings.Networks "kind").IPAddress}}`, containerName).Output()
	if err != nil {
		t.Fatalf("docker inspect %s: %v", containerName, exitErr(err))
	}
	ip := strings.TrimSpace(string(ipOut))
	if ip == "" {
		t.Fatalf("docker inspect %s: empty IP address on the \"kind\" network", containerName)
	}

	cfg, err := clientcmd.Load(raw)
	if err != nil {
		t.Fatalf("parse internal kubeconfig for %s: %v", clusterName, err)
	}
	for _, c := range cfg.Clusters {
		c.Server = fmt.Sprintf("https://%s:6443", ip)
		// The docker-network IP isn't guaranteed to be a SAN on the API
		// server certificate; skip server-cert verification rather than
		// depend on it. This only weakens verification of the SERVER's
		// identity over a docker-bridge-local hop inside a throwaway CI
		// cluster — the client certificate below (real mTLS auth issued by
		// cluster B's own CA) is what actually gates access, and that is
		// unaffected.
		c.InsecureSkipTLSVerify = true
		c.CertificateAuthority = ""
		c.CertificateAuthorityData = nil
	}
	out, err := clientcmd.Write(*cfg)
	if err != nil {
		t.Fatalf("re-marshal kubeconfig for %s: %v", clusterName, err)
	}
	return out
}

// exitErr enriches an *exec.ExitError with its captured stderr, which
// exec.Command's default Output() error otherwise discards.
func exitErr(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("%w: %s", err, string(ee.Stderr))
	}
	return err
}

// TestMultiCluster_ClusterDispatchAndScopedRBAC registers a second, real
// kind cluster as a remote target (POST /clusters), creates a GameServer on
// it through cluster A's API via `?cluster=<B>`, and proves four things
// that a single-cluster suite can never actually exercise:
//
//  1. The GameServer lands on cluster B's own Kubernetes API — verified by
//     reading it directly from cluster B, bypassing the Gameplane API — and
//     is genuinely absent from cluster A, not merely filtered from a shared
//     list by the query param.
//  2. A viewer whose only role binding is on cluster "local" (the default
//     CreateUser binding — see api/internal/handlers/users.go's
//     SetClusterRoleBinding(..., scope.DefaultCluster, ...)) is forbidden
//     from reading cluster B's servers: the RBAC cluster dimension actually
//     gates namespaced permissions per target cluster, not just per
//     namespace.
//  3. `?cluster=<unknown>` is rejected as a bad request (400) for any
//     caller, before RBAC or the handler ever sees a namespace/name.
//  4. A missing remote server never falls back to the home namesake,
//     including registry, modpack, and capture routes.
func TestMultiCluster_ClusterDispatchAndScopedRBAC(t *testing.T) {
	t.Parallel()

	envB, clusterBName := ensureClusterB(t)

	clusterID := fmt.Sprintf("e2e-mc-b-%d", time.Now().UnixNano())

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	// --- Register cluster B as a remote target ------------------------------
	kubeconfig := podReachableKubeconfig(t, clusterBName)
	resp, body, err := admin.Post("/clusters", map[string]string{
		"name":        clusterID,
		"displayName": "E2E cluster B",
		"kubeconfig":  string(kubeconfig),
	})
	if err != nil {
		t.Fatalf("POST /clusters: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /clusters: status=%d body=%s", resp.StatusCode, string(body))
	}
	resp.Body.Close()
	// Registered first (and so, by t.Cleanup's LIFO order, torn down LAST) —
	// the GameServer/GameTemplate cleanups below dispatch through this
	// cluster registration and must run while it still resolves.
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/clusters/" + clusterID)
		if r != nil {
			r.Body.Close()
		}
	})

	// The API's cluster watch (kube.WatchClusters) loads a client for the new
	// Cluster CR asynchronously off an informer Add event; wait for it to
	// become healthy before dispatching anything to it. Discovery includes
	// persisted registrations before a client loads, so presence alone no
	// longer proves that the registry is ready.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		resp, body, err := admin.Get("/clusters")
		if err != nil {
			return false, err.Error()
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("GET /clusters: status=%d body=%s", resp.StatusCode, string(body))
		}
		var discovery struct {
			Items []struct {
				Name  string `json:"name"`
				Phase string `json:"phase"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &discovery); err != nil {
			return false, err.Error()
		}
		for _, item := range discovery.Items {
			if item.Name == clusterID && item.Phase == "Healthy" {
				return true, ""
			}
		}
		return false, "cluster not yet healthy: " + string(body)
	})

	const ns = "gameplane-games"

	// Give the dedicated user explicit target permissions. Template permissions
	// require a cluster-wide grant; the admin's local wildcard and the user's
	// namespace grant must not authorize remote templates.
	templateRole := fmt.Sprintf("e2e-mc-templates-%d", time.Now().UnixNano())
	resp, body, err = admin.Post("/roles", map[string]any{
		"name":        templateRole,
		"permissions": []string{"templates:read", "templates:write"},
	})
	if err != nil {
		t.Fatalf("create remote template role: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create remote template role: status=%d body=%s", resp.StatusCode, body)
	}
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/roles/" + templateRole)
		if r != nil {
			r.Body.Close()
		}
	})
	opUsername, opPassword, opID := envInstance.CreateUser(t, admin, "operator", "e2e-mc-operator")
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/users/" + opID)
		if r != nil {
			r.Body.Close()
		}
	})
	resp, body, err = admin.Post("/users/"+opID+"/bindings", map[string]any{
		"roleName":  "admin",
		"cluster":   clusterID,
		"namespace": ns,
	})
	if err != nil {
		t.Fatalf("POST /users/%s/bindings: %v", opID, err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /users/%s/bindings: status=%d body=%s", opID, resp.StatusCode, string(body))
	}
	resp.Body.Close()
	resp, body, err = admin.Post("/users/"+opID+"/bindings", map[string]any{
		"roleName":  templateRole,
		"cluster":   clusterID,
		"namespace": "*",
	})
	if err != nil {
		t.Fatalf("grant remote template role: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("grant remote template role: status=%d body=%s", resp.StatusCode, body)
	}
	// addBinding invalidates the target user's existing sessions (see
	// userHandler.invalidateSessions) so a bound-in-flight session wouldn't
	// see the new grant anyway — log in AFTER granting the binding, not
	// before, so the resulting session is minted post-binding instead of
	// being torn down by it.
	operatorClient := envInstance.APIClient(t, opUsername, opPassword)
	defer operatorClient.Close()

	resp, body, err = admin.Post("/templates?cluster="+clusterID, map[string]any{})
	if err != nil {
		t.Fatalf("local admin remote template write: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("local admin remote template write: status=%d want=403 body=%s", resp.StatusCode, body)
	}
	// --- Create a GameTemplate + GameServer directly on cluster B -----------
	tmplName := fmt.Sprintf("e2e-mc-tmpl-%d", time.Now().UnixNano())
	resp, body, err = operatorClient.Post("/templates?cluster="+clusterID, map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": tmplName},
		"spec": map[string]any{
			"displayName": "E2E busybox (cluster B)",
			"game":        "busybox",
			"version":     "1",
			"image":       "busybox:1.36",
			"command":     []any{"sh", "-c", "echo remote-stream-start; export STREAM_SITE=remote; exec sh"},
			"consoleMode": "pty",
			"ports": []any{
				map[string]any{"name": "noop", "containerPort": int64(12345), "advertise": true, "protocol": "TCP"},
			},
		},
	})
	if err != nil {
		t.Fatalf("POST /templates?cluster=%s: %v", clusterID, err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /templates?cluster=%s: status=%d body=%s", clusterID, resp.StatusCode, string(body))
	}
	resp.Body.Close()
	t.Cleanup(func() {
		r, _, _ := operatorClient.Delete("/templates/" + tmplName + "?cluster=" + clusterID)
		if r != nil {
			r.Body.Close()
		}
	})
	gsName := fmt.Sprintf("e2e-mc-gs-%d", time.Now().UnixNano())
	resp, body, err = operatorClient.Post("/servers?cluster="+clusterID, map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gsName, "namespace": ns},
		"spec":       map[string]any{"templateRef": map[string]any{"name": tmplName}},
	})
	if err != nil {
		t.Fatalf("POST /servers?cluster=%s: %v", clusterID, err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers?cluster=%s: status=%d body=%s", clusterID, resp.StatusCode, string(body))
	}
	resp.Body.Close()
	t.Cleanup(func() {
		r, _, _ := operatorClient.Delete("/servers/" + gsName + "?cluster=" + clusterID)
		if r != nil {
			r.Body.Close()
		}
	})

	// --- Ground truth: read each cluster's own Kubernetes API directly,  ----
	// --- entirely bypassing the Gameplane API.                           ----
	if _, err := envB.Dyn.Resource(gameServerGVR).Namespace(ns).
		Get(context.Background(), gsName, metav1.GetOptions{}); err != nil {
		t.Fatalf("gameserver %s/%s not found directly on cluster B: %v", ns, gsName, err)
	}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Get(context.Background(), gsName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("gameserver %s/%s unexpectedly reachable directly on cluster A (err=%v)", ns, gsName, err)
	}

	// --- Through the API: cluster=B lists it, the default cluster doesn't --
	// Uses operatorClient, not admin: this is the same namespaced
	// servers:read check as the POST above, gated the same way.
	resp, body, err = operatorClient.Get("/servers?cluster=" + clusterID)
	if err != nil {
		t.Fatalf("GET /servers?cluster=%s: %v", clusterID, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers?cluster=%s: status=%d body=%s", clusterID, resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), gsName) {
		t.Errorf("GET /servers?cluster=%s: expected %s in listing, got %s", clusterID, gsName, string(body))
	}
	resp.Body.Close()

	resp, body, err = admin.Get("/servers")
	if err != nil {
		t.Fatalf("GET /servers (default cluster): %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers (default cluster): status=%d body=%s", resp.StatusCode, string(body))
	}
	if strings.Contains(string(body), gsName) {
		t.Errorf("GET /servers (default cluster): cluster-B server %s leaked into the local listing: %s",
			gsName, string(body))
	}
	resp.Body.Close()

	// Also create a same-named local server with distinguishable output.
	// Streaming must use B's API URL AND credentials, not the local namesake.
	exerciseMultiClusterStreams(t, envB, operatorClient, clusterID, tmplName, gsName)

	// --- RBAC: a viewer bound only to "local" cannot see cluster B's server -
	viewerName, viewerPW, viewerID := envInstance.CreateUser(t, admin, "viewer", "e2e-mc-viewer")
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/users/" + viewerID)
		if r != nil {
			r.Body.Close()
		}
	})
	viewer := envInstance.APIClient(t, viewerName, viewerPW)
	defer viewer.Close()

	// Sanity check first: the viewer's default-cluster read still works, so
	// a subsequent 403 below is provably about the cluster dimension and not
	// a broken viewer session.
	resp, body, err = viewer.Get("/servers")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer GET /servers (default cluster): status=%v err=%v body=%s", resp, err, string(body))
	}
	resp.Body.Close()

	resp, body, err = viewer.Get("/servers?cluster=" + clusterID)
	if err != nil {
		t.Fatalf("viewer GET /servers?cluster=%s: %v", clusterID, err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("viewer GET /servers?cluster=%s: status=%d want=%d body=%s",
			clusterID, resp.StatusCode, http.StatusForbidden, string(body))
	}
	resp.Body.Close()

	for _, route := range []string{"logs/pod", "console-pty"} {
		resp, body, err := viewer.Get("/ws/servers/" + gsName + "/" + route + "?cluster=" + clusterID)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("local viewer remote %s: status=%d body=%s", route, resp.StatusCode, body)
		}
	}

	// --- An unregistered cluster is a 400 for any caller, admin included ----
	resp, body, err = admin.Get("/servers?cluster=e2e-mc-does-not-exist")
	if err != nil {
		t.Fatalf("admin GET /servers?cluster=<unknown>: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("admin GET /servers?cluster=<unknown>: status=%d want=%d body=%s",
			resp.StatusCode, http.StatusBadRequest, string(body))
	}

	checkMissingRemoteTargetsDoNotReachHome(t, admin, clusterID)

	// Last, because it removes cluster B's registration.
	checkClusterRemoval(t, admin, operatorClient, clusterID, gsName, tmplName)
}

// The registry, modpack, and capture routes now resolve the selected cluster.
// A name present only at home must return 404 remotely, leave the local server
// unchanged, and preserve the capture denial audit. Successful remote operations
// are exercised separately by TestMultiCluster_GatewayParity.
func checkMissingRemoteTargetsDoNotReachHome(t *testing.T, admin *APIClient, clusterID string) {
	t.Helper()
	const (
		ns          = "gameplane-games"
		modpackEnv  = "E2E_MC_MODPACK"
		captureID   = "e2e-mc-capture"
		notFoundWhy = "server_not_found"
	)
	ctx := context.Background()

	// --- A home-cluster template with an env-mode modpack provider, and a --
	// --- home-cluster GameServer that uses it (created directly on A). ------
	homeTmpl := fmt.Sprintf("e2e-mc-home-tmpl-%d", time.Now().UnixNano())
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": homeTmpl},
		"spec": map[string]any{
			"displayName": "E2E busybox (home cluster)",
			"game":        "busybox",
			"version":     "1",
			"image":       "busybox:1.36",
			"command":     []any{"sh", "-c", "sleep 100000"},
			"ports": []any{
				map[string]any{"name": "noop", "containerPort": int64(12345), "advertise": true, "protocol": "TCP"},
			},
			"capabilities": map[string]any{
				"mods": map[string]any{
					"path": "mods",
					"registry": map[string]any{
						"providers": []any{
							map[string]any{
								"provider": "modrinth",
								"modpacks": map[string]any{"refEnv": modpackEnv},
							},
						},
					},
				},
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).Create(ctx, tmpl, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create home template %s: %v", homeTmpl, err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).Delete(context.Background(), homeTmpl, metav1.DeleteOptions{})
	})

	homeGS := fmt.Sprintf("e2e-mc-home-gs-%d", time.Now().UnixNano())
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": homeGS, "namespace": ns},
		"spec":       map[string]any{"templateRef": map[string]any{"name": homeTmpl}},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Create(ctx, gs, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create home gameserver %s/%s: %v", ns, homeGS, err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Delete(context.Background(), homeGS, metav1.DeleteOptions{})
	})

	// --- A user who can read on the home cluster and write only on the ----
	// --- remote cluster (admin role bound to cluster=<remote>, ns). --------
	remoteName, remotePW, remoteID := envInstance.CreateUser(t, admin, "viewer", "e2e-mc-remote-writer")
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/users/" + remoteID)
		if r != nil {
			r.Body.Close()
		}
	})
	resp, body, err := admin.Post("/users/"+remoteID+"/bindings", map[string]any{
		"roleName":  "admin",
		"cluster":   clusterID,
		"namespace": ns,
	})
	if err != nil {
		t.Fatalf("POST /users/%s/bindings: %v", remoteID, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /users/%s/bindings: status=%d body=%s", remoteID, resp.StatusCode, string(body))
	}
	// Log in after the binding is added: adding it ends existing sessions.
	remoteWriter := envInstance.APIClient(t, remoteName, remotePW)
	defer remoteWriter.Close()

	// Sanity: without a selector the request targets the home cluster, where
	// this user can't write, so RBAC refuses it.
	resp, body, err = remoteWriter.Post("/servers/"+homeGS+"/modpack", map[string]any{"ref": "e2e-pack"})
	if err != nil {
		t.Fatalf("POST /servers/%s/modpack (home): %v", homeGS, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /servers/%s/modpack (home): status=%d want=%d body=%s",
			homeGS, resp.StatusCode, http.StatusForbidden, string(body))
	}

	// The remote cluster has no such name: never resolve its home namesake.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/servers/" + homeGS + "/mods/registry/providers?cluster=" + clusterID, nil},
		{http.MethodPost, "/servers/" + homeGS + "/modpack?cluster=" + clusterID, map[string]any{"ref": "e2e-pack"}},
		{http.MethodGet, "/servers/" + homeGS + ":capture-file?id=" + captureID + "&cluster=" + clusterID, nil},
	} {
		resp, body, err := remoteWriter.Do(c.method, c.path, c.body)
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, c.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: status=%d want=%d body=%s", c.method, c.path, resp.StatusCode, http.StatusNotFound, string(body))
		}
	}

	// Ground truth on the home cluster: the GameServer's env is unchanged.
	live, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, homeGS, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get home gameserver %s/%s: %v", ns, homeGS, err)
	}
	env, _, _ := unstructured.NestedSlice(live.Object, "spec", "env")
	for _, e := range env {
		if m, ok := e.(map[string]any); ok && m["name"] == modpackEnv {
			t.Errorf("home gameserver %s/%s spec.env gained %s: %v", ns, homeGS, modpackEnv, env)
		}
	}

	// The capture download outcome is audited with its reason.
	resp, body, err = admin.Get("/admin/audit?limit=500")
	if err != nil {
		t.Fatalf("GET /admin/audit: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/audit: status=%d body=%s", resp.StatusCode, string(body))
	}
	var events []struct {
		Target string `json:"target"`
		Status int    `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &events); err != nil {
		t.Fatalf("decode /admin/audit: %v", err)
	}
	target := homeGS + ":" + captureID
	found := false
	for _, e := range events {
		if e.Target != target {
			continue
		}
		found = true
		if e.Reason != notFoundWhy || e.Status != http.StatusNotFound {
			t.Errorf("audit row for %s: reason=%q status=%d, want reason=%q status=%d",
				target, e.Reason, e.Status, notFoundWhy, http.StatusNotFound)
		}
	}
	if !found {
		t.Errorf("no audit row for capture download target %s", target)
	}
	// Cluster enrollment and credential issuance remain central-only operations.
	for _, path := range []string{"/cluster/nodes:join", "/cluster/kubeconfig"} {
		response, raw, err := admin.Post(path+"?cluster="+clusterID, map[string]any{})
		if err != nil {
			t.Fatalf("central-only %s: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotImplemented {
			t.Errorf("central-only %s: status=%d want=501 body=%s", path, response.StatusCode, raw)
		}
	}
}

// checkClusterRemoval removes cluster B's registration over HTTP and checks
// what cluster removal does and does not touch: the registration stops
// resolving at once, the kubeconfig Secret the API created goes with it, and
// a Secret the API did not create stays in place when a Cluster that names
// it is removed.
func checkClusterRemoval(t *testing.T, admin, operatorClient *APIClient, clusterID, gsName, tmplName string) {
	t.Helper()
	const controlNS = "gameplane-system"
	ctx := context.Background()

	// The GameServer and GameTemplate on cluster B are reached through the
	// registration removed below, so delete them first. Their cleanups,
	// registered earlier, then become no-ops.
	resp, body, err := operatorClient.Delete("/servers/" + gsName + "?cluster=" + clusterID)
	if err != nil {
		t.Fatalf("DELETE /servers/%s?cluster=%s: %v", gsName, clusterID, err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("DELETE /servers/%s?cluster=%s: status=%d body=%s", gsName, clusterID, resp.StatusCode, string(body))
	}
	resp, body, err = operatorClient.Delete("/templates/" + tmplName + "?cluster=" + clusterID)
	if err != nil {
		t.Fatalf("DELETE /templates/%s?cluster=%s: %v", tmplName, clusterID, err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("DELETE /templates/%s?cluster=%s: status=%d body=%s", tmplName, clusterID, resp.StatusCode, string(body))
	}

	// --- Removing the API-created registration ------------------------------
	resp, body, err = admin.Delete("/clusters/" + clusterID)
	if err != nil {
		t.Fatalf("DELETE /clusters/%s: %v", clusterID, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /clusters/%s: status=%d body=%s", clusterID, resp.StatusCode, string(body))
	}

	// The registration stops resolving right away: a removed cluster is
	// an unknown cluster (400), with no wait for the API's cluster watch.
	resp, body, err = admin.Get("/servers?cluster=" + clusterID)
	if err != nil {
		t.Fatalf("GET /servers?cluster=%s after removal: %v", clusterID, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET /servers?cluster=%s after removal: status=%d want=%d body=%s",
			clusterID, resp.StatusCode, http.StatusBadRequest, string(body))
	}

	// The kubeconfig Secret POST /clusters created is removed with it.
	apiSecret := "cluster-" + clusterID + "-kubeconfig"
	if _, err := envInstance.K8s.CoreV1().Secrets(controlNS).Get(ctx, apiSecret, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("secret %s/%s still present after DELETE /clusters/%s (err=%v)", controlNS, apiSecret, clusterID, err)
	}

	// --- Removing a registration made with kubectl --------------------------
	// A Cluster applied directly (docs/install.md Path 1) names a Secret the
	// API did not create. Removing the Cluster over HTTP leaves that Secret
	// in place, labelled as a kubeconfig or not.
	cases := []struct {
		name   string
		labels map[string]string
	}{
		{name: "unlabelled", labels: nil},
		{name: "kubeconfig-label", labels: map[string]string{"gameplane.local/cluster-kubeconfig": "true"}},
	}
	for _, tc := range cases {
		suffix := fmt.Sprintf("%s-%d", tc.name, time.Now().UnixNano())
		secretName := "e2e-mc-keep-" + suffix
		crName := "e2e-mc-ref-" + suffix

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: controlNS, Labels: tc.labels},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{"kubeconfig": []byte("not-a-kubeconfig")},
		}
		if _, err := envInstance.K8s.CoreV1().Secrets(controlNS).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			t.Fatalf("%s: create secret %s/%s: %v", tc.name, controlNS, secretName, err)
		}
		t.Cleanup(func() {
			_ = envInstance.K8s.CoreV1().Secrets(controlNS).Delete(context.Background(), secretName, metav1.DeleteOptions{})
		})

		cr := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "Cluster",
			"metadata":   map[string]any{"name": crName},
			"spec": map[string]any{
				"displayName":      "E2E kubectl-applied cluster",
				"kubeconfigSecret": map[string]any{"name": secretName, "key": "kubeconfig"},
			},
		}}
		if _, err := envInstance.Dyn.Resource(clusterGVR).Create(ctx, cr, metav1.CreateOptions{}); err != nil {
			t.Fatalf("%s: create Cluster %s: %v", tc.name, crName, err)
		}
		t.Cleanup(func() {
			_ = envInstance.Dyn.Resource(clusterGVR).Delete(context.Background(), crName, metav1.DeleteOptions{})
		})

		resp, body, err := admin.Delete("/clusters/" + crName)
		if err != nil {
			t.Fatalf("%s: DELETE /clusters/%s: %v", tc.name, crName, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s: DELETE /clusters/%s: status=%d body=%s", tc.name, crName, resp.StatusCode, string(body))
		}
		if _, err := envInstance.Dyn.Resource(clusterGVR).Get(ctx, crName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Errorf("%s: Cluster %s still present after DELETE /clusters/%s (err=%v)", tc.name, crName, crName, err)
		}
		if _, err := envInstance.K8s.CoreV1().Secrets(controlNS).Get(ctx, secretName, metav1.GetOptions{}); err != nil {
			t.Errorf("%s: secret %s/%s was removed with Cluster %s: %v", tc.name, controlNS, secretName, crName, err)
		}
	}
}

// This runs inside the existing multicluster bucket test to share cluster
// registration and sessions rather than consume extra login-rate budget.
func exerciseMultiClusterStreams(t *testing.T, envB *Env, remote *APIClient, clusterID, templateName, serverName string) {
	t.Helper()
	const ns = "gameplane-games"
	ctx := t.Context()
	tmpl, err := envB.Dyn.Resource(gameTemplateGVR).Get(ctx, templateName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tmpl.SetResourceVersion("")
	tmpl.SetUID("")
	tmpl.SetManagedFields(nil)
	tmpl.SetCreationTimestamp(metav1.Time{})
	tmpl.Object["spec"].(map[string]any)["command"] = []any{"sh", "-c", "echo local-stream-start; export STREAM_SITE=local; exec sh"}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).Create(ctx, tmpl, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).Delete(context.Background(), templateName, metav1.DeleteOptions{})
	})
	applyBusyboxGameServer(t, ns, serverName, templateName)
	for _, env := range []*Env{envInstance, envB} {
		env.Eventually(t, 3*time.Minute, func() (bool, string) {
			pod, err := env.K8s.CoreV1().Pods(ns).Get(ctx, serverName+"-0", metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			for _, c := range pod.Status.ContainerStatuses {
				if c.Name == "game" && c.Ready {
					return true, ""
				}
			}
			return false, "game container not ready"
		})
	}
	for _, route := range []string{"logs/pod?from=end", "logs/pod?from=start", "console-pty?"} {
		conn, stop := dialAuthedWS(t, remote, "/ws/servers/"+serverName+"/"+route+"&cluster="+clusterID)
		func() {
			defer stop()
			readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			want := "remote-stream-start"
			if strings.HasPrefix(route, "console") {
				// The expected remote value is not in the input; seeing terminal echo
				// alone cannot falsely satisfy the assertion.
				command := "printf 'SITE:%s\\n' \"$STREAM_SITE\"\n"
				envelope, err := json.Marshal(ptyEnvelope{Kind: "stdin", Body: base64.StdEncoding.EncodeToString([]byte(command))})
				if err != nil {
					t.Fatal(err)
				}
				if err := conn.Write(readCtx, websocket.MessageText, envelope); err != nil {
					t.Fatal(err)
				}
				want = "SITE:remote"
			}
			var output strings.Builder
			for {
				_, frame, err := conn.Read(readCtx)
				if err != nil {
					t.Fatalf("remote %s without marker: %v; output=%q", route, err, output.String())
				}
				if strings.HasPrefix(route, "console") {
					var envelope ptyEnvelope
					if err := json.Unmarshal(frame, &envelope); err != nil {
						t.Fatal(err)
					}
					if envelope.Kind == "err" {
						t.Fatalf("remote attach error: %s", envelope.Body)
					}
					raw, err := base64.StdEncoding.DecodeString(envelope.Body)
					if err != nil {
						t.Fatal(err)
					}
					output.Write(raw)
				} else {
					output.Write(frame)
				}
				if strings.Contains(output.String(), "local-stream-start") || strings.Contains(output.String(), "SITE:local") {
					t.Fatalf("remote stream reached local namesake: %s", output.String())
				}
				if strings.Contains(output.String(), want) {
					break
				}
			}
		}()
	}
}
